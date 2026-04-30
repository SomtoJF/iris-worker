package jobapplication

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/SomtoJF/iris-worker/activity/browser"
	"github.com/SomtoJF/iris-worker/activity/realtimeevent"
	"github.com/SomtoJF/iris-worker/activity/sqldb"
	"github.com/SomtoJF/iris-worker/browserfactory"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type JobApplicationWorkflowInput struct {
	IdJobApplication      uint   `json:"id_job_application"`
	ApplicationExternalId string `json:"application_external_id"`
	Url                   string `json:"url"`
	IdUser                uint   `json:"id_user"`
}

const CancelSignalName = "CANCEL_APPLICATION"

type CancelSignalPayload struct {
	Reason string `json:"reason"`
}

func isCancelled(cancelCtx workflow.Context) bool {
	return cancelCtx.Err() != nil
}

func handleCancelOrTimeout(
	ctx workflow.Context,
	cancelCtx workflow.Context,
	timedOut bool,
	input JobApplicationWorkflowInput,
	jobDetails JobDetails,
	cancelPayload CancelSignalPayload,
) (handled bool, err error) {
	if !isCancelled(cancelCtx) {
		return false, nil
	}

	if timedOut {
		handleApplicationError(ctx, input, jobDetails, "Application timed out. Please try again.")
		return true, temporal.NewNonRetryableApplicationError("workflow soft timeout", "WorkflowSoftTimeout", nil)
	}

	handleApplicationCancelled(ctx, input, jobDetails, cancelPayload.Reason)
	return true, nil
}

const SESSION_TIMEOUT = 23*time.Hour + 50*time.Minute

func JobApplicationWorkflow(ctx workflow.Context, input JobApplicationWorkflowInput) error {
	logger := workflow.GetLogger(ctx)

	logger.Info("JobApplicationWorkflow started", "url", input.Url)

	activityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, activityOptions)

	// Set up cancellation signal listener
	cancelCtx, cancelFunc := workflow.WithCancel(ctx)
	var cancelPayload CancelSignalPayload
	timedOut := false

	workflow.Go(ctx, func(gCtx workflow.Context) {
		if err := workflow.NewTimer(gCtx, SESSION_TIMEOUT).Get(gCtx, nil); err != nil {
			return
		}
		timedOut = true
		cancelFunc()
	})

	workflow.Go(cancelCtx, func(gCtx workflow.Context) {
		signalChan := workflow.GetSignalChannel(gCtx, CancelSignalName)
		signalChan.Receive(gCtx, &cancelPayload)
		cancelFunc()
	})

	workflowId := workflow.GetInfo(ctx).WorkflowExecution.ID

	var jobDetails JobDetails

	sessionCtx, err := workflow.CreateSession(cancelCtx, &workflow.SessionOptions{
		ExecutionTimeout: SESSION_TIMEOUT,
		CreationTimeout:  time.Minute,
	})
	if err != nil {
		if handled, cerr := handleCancelOrTimeout(ctx, cancelCtx, timedOut, input, jobDetails, cancelPayload); handled {
			return cerr
		}
		logger.Error("Failed to create session", "error", err)
		handleApplicationError(ctx, input, jobDetails, "An error occurred while starting your application session")
		return err
	}
	defer workflow.CompleteSession(sessionCtx)

	userResume, err := fetchUserResume(cancelCtx, input.IdUser)
	if err != nil {
		if handled, cerr := handleCancelOrTimeout(ctx, cancelCtx, timedOut, input, jobDetails, cancelPayload); handled {
			return cerr
		}
		logger.Error("Failed to fetch user resume", "error", err)
		handleApplicationError(ctx, input, jobDetails, "An error occurred while fetching your resume")
		return err
	}

	userProfile, err := fetchJobApplicationProfile(cancelCtx, input.IdUser)
	if err != nil {
		if handled, cerr := handleCancelOrTimeout(ctx, cancelCtx, timedOut, input, jobDetails, cancelPayload); handled {
			return cerr
		}
		logger.Error("Failed to fetch user profile", "error", err)
		handleApplicationError(ctx, input, jobDetails, "An error occurred while fetching your profile")
		return err
	}

	resumePath, err := loadResumeIntoMemory(cancelCtx, userResume.FileName, userResume.FileKey)
	if err != nil {
		if handled, cerr := handleCancelOrTimeout(ctx, cancelCtx, timedOut, input, jobDetails, cancelPayload); handled {
			return cerr
		}
		logger.Error("Failed to download and load resume into memory", "error", err)
		handleApplicationError(ctx, input, jobDetails, "An error occurred while loading your resume into memory")
		return err
	}

	if err := openWebpage(sessionCtx, workflowId, input.Url); err != nil {
		if handled, cerr := handleCancelOrTimeout(ctx, cancelCtx, timedOut, input, jobDetails, cancelPayload); handled {
			return cerr
		}
		logger.Error("Failed to open webpage", "error", err)
		handleApplicationError(ctx, input, jobDetails, "We couldn't open the job posting page")
		return err
	}

	// Ensure browser resources are released even if the session is canceled/times out.
	// Use a disconnected context derived from the base workflow context, not the session context.
	defer func() {
		newCtx, _ := workflow.NewDisconnectedContext(ctx)
		closeOpts := workflow.ActivityOptions{
			StartToCloseTimeout: 30 * time.Second,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
		}
		newCtx = workflow.WithActivityOptions(newCtx, closeOpts)
		workflow.ExecuteActivity(newCtx, "ClosePage", browser.ClosePageInput{
			WorkflowID: workflowId,
		})
	}()

	jobDetails, err = retrieveJobDetails(sessionCtx, input.Url, input.IdUser, input.IdJobApplication)
	if err != nil {
		if handled, cerr := handleCancelOrTimeout(ctx, cancelCtx, timedOut, input, jobDetails, cancelPayload); handled {
			return cerr
		}
		logger.Error("Failed to retrieve job details", "error", err)
		handleApplicationError(ctx, input, jobDetails, "We couldn't retrieve the job details")
		return err
	} else if !jobDetails.IsValidJobPosting {
		logger.Info("Invalid job posting", "error", "The job posting is invalid")
		handleApplicationError(ctx, input, jobDetails, "The job posting is invalid. The link doesn't contain the job description")
		return err
	}

	if err := updateJobApplication(cancelCtx, input.IdJobApplication, map[string]interface{}{
		"job_title":       jobDetails.JobTitle,
		"company_name":    jobDetails.CompanyName,
		"job_description": jobDetails.JobDescription,
	}); err != nil {
		if handled, cerr := handleCancelOrTimeout(ctx, cancelCtx, timedOut, input, jobDetails, cancelPayload); handled {
			return cerr
		}
		logger.Error("Failed to update job application", "error", err)
		handleApplicationError(ctx, input, jobDetails, "We couldn't update the job application")
		return err
	}

	if handled, cerr := handleCancelOrTimeout(ctx, cancelCtx, timedOut, input, jobDetails, cancelPayload); handled {
		return cerr
	}

	userProfileBytes, err := json.Marshal(userProfile)
	if err != nil {
		logger.Error("Failed to marshal user profile", "error", err)
		handleApplicationError(ctx, input, jobDetails, "We couldn't marshal the user profile")
		return err
	}
	userProfileJSON := string(userProfileBytes)

	isApplicationComplete := false
	toolCallHistory := []ToolCallResult{}
	qaMap := make(map[string]string)
	var coverLetter *string
	const maxAgentIterations = 50

	for iteration := 0; !isApplicationComplete && iteration < maxAgentIterations; iteration++ {
		var screenshot browser.TakeScreenshotOutput
		err = workflow.ExecuteActivity(sessionCtx, "TakeScreenshot", browser.TakeScreenshotInput{
			WorkflowID: workflowId,
			FileName:   fmt.Sprintf("screenshot_%d.png", iteration),
		}).Get(sessionCtx, &screenshot)
		if err != nil {
			if handled, cerr := handleCancelOrTimeout(ctx, cancelCtx, timedOut, input, jobDetails, cancelPayload); handled {
				return cerr
			}
			logger.Error("Failed to take screenshot", "error", err)
			handleApplicationError(ctx, input, jobDetails, "We couldn't continue the application because we failed to capture the page state")
			return err
		}

		requiredFields := extractRequiredFields(screenshot.TaggedNodes)

		plannerRequest := PlannerRequest{
			IdUser:                  input.IdUser,
			IdJobApplication:        input.IdJobApplication,
			JobPostingUrl:           input.Url,
			ScreenshotPath:          screenshot.Path,
			TaggedNodes:             screenshot.TaggedNodes,
			TaggedFileInputElements: screenshot.TaggedFileInputNodes,
			ToolCallHistory:         toolCallHistory,
			UserResume:              userResume.Content,
			JobDescription:          jobDetails.JobDescription,
			UserResumePath:          resumePath,
			UserProfileJSON:         userProfileJSON,
			RequiredFields:          requiredFields,
		}

		plannerResponse, err := planNextAction(cancelCtx, plannerRequest)
		if err != nil {
			if handled, cerr := handleCancelOrTimeout(ctx, cancelCtx, timedOut, input, jobDetails, cancelPayload); handled {
				return cerr
			}
			logger.Error("Failed to plan next action", "error", err)
			handleApplicationError(ctx, input, jobDetails, "We couldn't continue the application because we failed to plan the next step")
			return err
		}

		if plannerResponse.IsApplicationFailed {
			failureReason := "We couldn't complete your application. Please try again."
			if plannerResponse.FailureReason != nil && *plannerResponse.FailureReason != "" {
				failureReason = *plannerResponse.FailureReason
			}
			handleApplicationError(ctx, input, jobDetails, failureReason)
			logger.Warn("Job application failed by planner", "reason", failureReason)
			return fmt.Errorf("%s", failureReason)
		}

		for _, qa := range plannerResponse.QuestionsAnswered {
			if qa.Question != "" && qa.Answer != "" {
				qaMap[qa.Question] = qa.Answer
			}
		}

		isApplicationComplete = plannerResponse.IsApplicationComplete
		if isApplicationComplete {
			break
		}

		if plannerResponse.ToolCall != nil {
			result := executeToolCall(sessionCtx, workflowId, input.IdUser, input.IdJobApplication, *plannerResponse.ToolCall)
			toolCallHistory = append(toolCallHistory, result)

			if result.Error != nil && isCancelled(cancelCtx) {
				if handled, cerr := handleCancelOrTimeout(ctx, cancelCtx, timedOut, input, jobDetails, cancelPayload); handled {
					return cerr
				}
			}

			if plannerResponse.ToolCall.Name == "write_cover_letter" {
				if cl, ok := result.Result["cover_letter"].(string); ok {
					coverLetter = &cl
				}
			}
		}
	}

	if !isApplicationComplete {
		failureReason := "We couldn't complete your application. Please try again."
		handleApplicationError(ctx, input, jobDetails, failureReason)
		logger.Warn("Job application incomplete", "reason", "max_iterations", "maxAgentIterations", maxAgentIterations)
		return fmt.Errorf("%s", failureReason)
	}

	handleApplicationSuccess(ctx, input, jobDetails)

	questions := mapToQuestions(qaMap)
	if len(questions) > 0 {
		deduped, err := deduplicateQA(ctx, input.IdUser, input.IdJobApplication, questions)
		if err != nil {
			logger.Warn("Failed to deduplicate Q&A, saving raw", "error", err)
		} else {
			questions = deduped
		}
	}

	if err := saveApplicationData(ctx, input.IdUser, input.IdJobApplication, userResume.IdResume, coverLetter, questions); err != nil {
		logger.Error("Failed to save application data", "error", err)
	}

	return nil
}

func extractRequiredFields(taggedNodes []browserfactory.SerializableTaggedNode) []browserfactory.SerializableTaggedNode {
	required := make([]browserfactory.SerializableTaggedNode, 0)
	for _, node := range taggedNodes {
		if node.Required == nil || !*node.Required {
			continue
		}

		// Copy to avoid any accidental mutation of shared pointers downstream.
		copied := node
		if node.Value != nil {
			v := *node.Value
			copied.Value = &v
		}
		if node.Required != nil {
			r := *node.Required
			copied.Required = &r
		}
		if node.Checked != nil {
			c := *node.Checked
			copied.Checked = &c
		}

		required = append(required, copied)
	}
	return required
}

func handleApplicationCancelled(ctx workflow.Context, input JobApplicationWorkflowInput, jobDetails JobDetails, reason string) {
	newCtx, _ := workflow.NewDisconnectedContext(ctx)
	cleanupOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	}
	newCtx = workflow.WithActivityOptions(newCtx, cleanupOpts)

	var reasonPtr *string
	if reason != "" {
		reasonPtr = &reason
	}

	updateJobApplication(newCtx, input.IdJobApplication, map[string]interface{}{
		"status":              sqldb.JobApplicationStatusCancelled,
		"cancellation_reason": reasonPtr,
	})

	workflow.ExecuteActivity(newCtx, "PublishRedisEvent", input.IdUser, string(realtimeevent.EventApplicationCancelled), map[string]interface{}{
		"id":          input.ApplicationExternalId,
		"jobTitle":    jobDetails.JobTitle,
		"companyName": jobDetails.CompanyName,
		"reason":      reason,
	}).Get(newCtx, nil)
}

func handleApplicationError(ctx workflow.Context, input JobApplicationWorkflowInput, jobDetails JobDetails, failureReason string) {
	var failureReasonPtf *string
	if failureReason != "" {
		failureReasonPtf = &failureReason
	}
	updateJobApplicationStatus(ctx, input.IdJobApplication, sqldb.JobApplicationStatusFailed, failureReasonPtf)

	workflow.ExecuteActivity(ctx, "PublishRedisEvent", input.IdUser, string(realtimeevent.EventApplicationFailed), map[string]interface{}{
		"id":          input.ApplicationExternalId,
		"jobTitle":    jobDetails.JobTitle,
		"companyName": jobDetails.CompanyName,
	}).Get(ctx, nil)
}

func handleApplicationSuccess(ctx workflow.Context, input JobApplicationWorkflowInput, jobDetails JobDetails) {
	updateJobApplicationStatus(ctx, input.IdJobApplication, sqldb.JobApplicationStatusApplied, nil)
	workflow.ExecuteActivity(ctx, "PublishRedisEvent", input.IdUser, string(realtimeevent.EventApplicationSuccessful), map[string]interface{}{
		"id":          input.ApplicationExternalId,
		"jobTitle":    jobDetails.JobTitle,
		"companyName": jobDetails.CompanyName,
	}).Get(ctx, nil)
}

func openWebpage(ctx workflow.Context, workflowID string, url string) error {
	return workflow.ExecuteActivity(ctx, "OpenWebpage", browser.OpenWebpageInput{
		Url:        url,
		WorkflowID: workflowID,
	}).Get(ctx, nil)
}

func updateJobApplicationStatus(ctx workflow.Context, idJobApplication uint, status sqldb.JobApplicationStatus, failureReason *string) error {
	return workflow.ExecuteActivity(ctx, "UpdateJobApplication", sqldb.UpdateJobApplicationInput{
		IdJobApplication: idJobApplication,
		Data: map[string]interface{}{
			"status":         status,
			"failure_reason": failureReason,
		},
	}).Get(ctx, nil)
}

func mapToQuestions(qaMap map[string]string) []sqldb.JobApplicationQuestion {
	questions := make([]sqldb.JobApplicationQuestion, 0, len(qaMap))
	for q, a := range qaMap {
		questions = append(questions, sqldb.JobApplicationQuestion{Question: q, Answer: a})
	}
	return questions
}

func saveApplicationData(ctx workflow.Context, idUser, idJobApplication, idResume uint, coverLetter *string, questions []sqldb.JobApplicationQuestion) error {
	return workflow.ExecuteActivity(ctx, "CreateJobApplicationData", sqldb.CreateJobApplicationDataInput{
		IdUser:           idUser,
		IdJobApplication: idJobApplication,
		IdResume:         idResume,
		CoverLetter:      coverLetter,
		Questions:        questions,
	}).Get(ctx, nil)
}

type deduplicateQAResponse struct {
	Questions []sqldb.JobApplicationQuestion `json:"questions"`
}

func updateJobApplication(ctx workflow.Context, idJobApplication uint, data map[string]interface{}) error {
	return workflow.ExecuteActivity(ctx, "UpdateJobApplication", sqldb.UpdateJobApplicationInput{
		IdJobApplication: idJobApplication,
		Data:             data,
	}).Get(ctx, nil)
}

func fetchUserResume(ctx workflow.Context, idUser uint) (sqldb.Resume, error) {
	var resume sqldb.Resume
	if err := workflow.ExecuteActivity(ctx, "FetchActiveUserResume", idUser).Get(ctx, &resume); err != nil {
		return sqldb.Resume{}, err
	}
	return resume, nil
}

type UserProfile struct {
	FirstName                   string                      `json:"first_name"`
	LastName                    string                      `json:"last_name"`
	Email                       string                      `json:"email"`
	Phone                       string                      `json:"phone"`
	Address                     string                      `json:"address"`
	City                        string                      `json:"city"`
	State                       string                      `json:"state"`
	Zip                         string                      `json:"zip"`
	CountryOfResidence          string                      `json:"country_of_residence"`
	IsVeteran                   bool                        `json:"is_veteran"`
	CountriesOfCitizenship      []string                    `json:"countries_of_citizenship"`
	Gender                      string                      `json:"gender"`
	DateOfBirth                 string                      `json:"date_of_birth"`
	Age                         int                         `json:"age"`
	SalaryMin                   *float64                    `json:"salary_min,omitempty"`
	SalaryMax                   *float64                    `json:"salary_max,omitempty"`
	SalaryCurrency              string                      `json:"salary_currency,omitempty"`
	Ethnicity                   string                      `json:"ethnicity,omitempty"`
	IsOpenToRelocating          *bool                       `json:"is_open_to_relocating,omitempty"`
	NoticePeriodDays            *int                        `json:"notice_period_days,omitempty"`
	LinkedInUrl                 *string                     `json:"linkedin_url,omitempty"`
	PreferredWorkingArrangement []string                    `json:"preferred_working_arrangement,omitempty"`
	LanguageProficiencies       []sqldb.LanguageProficiency `json:"language_proficiencies,omitempty"`
	PortfolioLink               *string                     `json:"portfolio_link,omitempty"`
}

func fetchJobApplicationProfile(ctx workflow.Context, idUser uint) (UserProfile, error) {
	var jobApplicationProfile sqldb.JobApplicationProfile
	if err := workflow.ExecuteActivity(ctx, "FetchJobApplicationProfile", idUser).Get(ctx, &jobApplicationProfile); err != nil {
		return UserProfile{}, err
	}
	age := time.Now().Year() - jobApplicationProfile.DateOfBirth.Year()
	return UserProfile{
		FirstName:                   jobApplicationProfile.FirstName,
		LastName:                    jobApplicationProfile.LastName,
		Email:                       jobApplicationProfile.Email,
		Phone:                       jobApplicationProfile.Phone,
		Address:                     jobApplicationProfile.Address,
		City:                        jobApplicationProfile.City,
		State:                       jobApplicationProfile.State,
		Zip:                         jobApplicationProfile.Zip,
		CountryOfResidence:          jobApplicationProfile.CountryOfResidence,
		IsVeteran:                   jobApplicationProfile.IsVeteran,
		CountriesOfCitizenship:      jobApplicationProfile.CountriesOfCitizenship,
		Gender:                      jobApplicationProfile.Gender,
		DateOfBirth:                 jobApplicationProfile.DateOfBirth.Format("2006-01-02"),
		Age:                         age,
		SalaryMin:                   jobApplicationProfile.SalaryMin,
		SalaryMax:                   jobApplicationProfile.SalaryMax,
		SalaryCurrency:              jobApplicationProfile.SalaryCurrency,
		Ethnicity:                   jobApplicationProfile.Ethnicity,
		IsOpenToRelocating:          jobApplicationProfile.IsOpenToRelocating,
		NoticePeriodDays:            jobApplicationProfile.NoticePeriodDays,
		LinkedInUrl:                 jobApplicationProfile.LinkedInUrl,
		PreferredWorkingArrangement: jobApplicationProfile.PreferredWorkingArrangement,
		LanguageProficiencies:       jobApplicationProfile.LanguageProficiencies,
		PortfolioLink:               jobApplicationProfile.PortfolioLink,
	}, nil
}
