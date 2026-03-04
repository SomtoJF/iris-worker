package jobapplication

import (
	"fmt"
	"time"

	"github.com/SomtoJF/iris-worker/activity/browser"
	"github.com/SomtoJF/iris-worker/activity/sqldb"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// UserAction is the action the user needs to take when the workflow is blocked.
type UserAction string

const (
	UserActionCaptcha        = "USER_ACTION_CAPTCHA"
	UserActionAuthentication = "USER_ACTION_AUTHENTICATION"
)

type JobApplicationWorkflowInput struct {
	IdJobApplication      uint   `json:"id_job_application"`
	ApplicationExternalId string `json:"application_external_id"`
	Url                   string `json:"url"`
	IdUser                uint   `json:"id_user"`
}

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

	workflowId := workflow.GetInfo(ctx).WorkflowExecution.ID

	sessionCtx, err := workflow.CreateSession(ctx, &workflow.SessionOptions{
		ExecutionTimeout: 30 * time.Minute,
		CreationTimeout:  time.Minute,
	})
	if err != nil {
		logger.Error("Failed to create session", "error", err)
		updateJobApplicationStatus(ctx, input.IdJobApplication, sqldb.JobApplicationStatusFailed)
		return err
	}
	defer workflow.CompleteSession(sessionCtx)

	userResume, err := fetchUserResume(ctx, input.IdUser)
	if err != nil {
		logger.Error("Failed to fetch user resume", "error", err)
		updateJobApplicationStatus(ctx, input.IdJobApplication, sqldb.JobApplicationStatusFailed)
		return err
	}

	userProfile, err := fetchJobApplicationProfile(ctx, input.IdUser)
	if err != nil {
		logger.Error("Failed to fetch user profile", "error", err)
		updateJobApplicationStatus(ctx, input.IdJobApplication, sqldb.JobApplicationStatusFailed)
		return err
	}

	resumePath, err := loadResumeIntoMemory(ctx, userResume.FileKey)
	if err != nil {
		logger.Error("Failed to download and load resume into memory", "error", err)
		updateJobApplicationStatus(ctx, input.IdJobApplication, sqldb.JobApplicationStatusFailed)
		return err
	}

	if err := openWebpage(sessionCtx, workflowId, input.Url); err != nil {
		logger.Error("Failed to open webpage", "error", err)
		updateJobApplicationStatus(ctx, input.IdJobApplication, sqldb.JobApplicationStatusFailed)
		return err
	}

	jobDetails, err := retrieveJobDetails(sessionCtx, input.Url)
	if err != nil {
		logger.Error("Failed to retrieve job details", "error", err)
		updateJobApplicationStatus(ctx, input.IdJobApplication, sqldb.JobApplicationStatusFailed)
		return err
	}

	if err := updateJobApplication(ctx, input.IdJobApplication, map[string]interface{}{
		"job_title":       jobDetails.JobTitle,
		"company_name":    jobDetails.CompanyName,
		"job_description": jobDetails.JobDescription,
	}); err != nil {
		logger.Error("Failed to update job application", "error", err)
		updateJobApplicationStatus(ctx, input.IdJobApplication, sqldb.JobApplicationStatusFailed)
		return err
	}

	defer func() {
		newCtx, _ := workflow.NewDisconnectedContext(sessionCtx)
		closeOpts := workflow.ActivityOptions{
			StartToCloseTimeout: 30 * time.Second,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
		}
		newCtx = workflow.WithActivityOptions(newCtx, closeOpts)
		workflow.ExecuteActivity(newCtx, "ClosePage", browser.ClosePageInput{
			WorkflowID: workflowId,
		})
	}()

	isApplicationComplete := false
	toolCallHistory := []ToolCallResult{}
	const maxAgentIterations = 20

	for iteration := 0; !isApplicationComplete && iteration < maxAgentIterations; iteration++ {
		var screenshot browser.TakeScreenshotOutput
		err = workflow.ExecuteActivity(sessionCtx, "TakeScreenshot", browser.TakeScreenshotInput{
			WorkflowID: workflowId,
			FileName:   fmt.Sprintf("screenshot_%d.png", iteration),
		}).Get(sessionCtx, &screenshot)
		if err != nil {
			logger.Error("Failed to take screenshot", "error", err)
			updateJobApplicationStatus(ctx, input.IdJobApplication, sqldb.JobApplicationStatusFailed)
			return err
		}

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
			UserProfile:             userProfile,
		}

		plannerResponse, err := planNextAction(ctx, plannerRequest)
		if err != nil {
			logger.Error("Failed to plan next action", "error", err)
			updateJobApplicationStatus(ctx, input.IdJobApplication, sqldb.JobApplicationStatusFailed)
			return err
		}

		isApplicationComplete = plannerResponse.IsApplicationComplete
		if isApplicationComplete {
			break
		}

		if plannerResponse.RequiresUserAction {
			if err := updateJobApplicationStatus(ctx, input.IdJobApplication, sqldb.JobApplicationStatusBlocked); err != nil {
				logger.Error("Failed to update job application status", "error", err)
			}
			err = awaitUserAction(ctx, AwaitUserActionInput{
				WorkflowId:            workflowId,
				UserAction:            UserAction(plannerResponse.UserAction),
				CompanyName:           jobDetails.CompanyName,
				JobTitle:              jobDetails.JobTitle,
				IdUser:                input.IdUser,
				ApplicationExternalId: input.ApplicationExternalId,
			})
			if err != nil {
				logger.Error("Failed to await user action", "error", err)
				updateJobApplicationStatus(ctx, input.IdJobApplication, sqldb.JobApplicationStatusFailed)
				return err
			}
			continue
		}

		if plannerResponse.ToolCall != nil {
			result := executeToolCall(sessionCtx, workflowId, *plannerResponse.ToolCall)
			toolCallHistory = append(toolCallHistory, result)
		}
	}

	if !isApplicationComplete {
		if err := updateJobApplicationStatus(ctx, input.IdJobApplication, sqldb.JobApplicationStatusFailed); err != nil {
			logger.Error("Failed to update job application status", "error", err)
			return err
		}
		logger.Warn("Job application not complete after %d iterations", maxAgentIterations)
		return fmt.Errorf("job application not complete after %d iterations", maxAgentIterations)
	}

	if err := updateJobApplicationStatus(ctx, input.IdJobApplication, sqldb.JobApplicationStatusApplied); err != nil {
		logger.Error("Failed to update job application status", "error", err)
	}

	return nil
}

func openWebpage(ctx workflow.Context, workflowID string, url string) error {
	return workflow.ExecuteActivity(ctx, "OpenWebpage", browser.OpenWebpageInput{
		Url:        url,
		WorkflowID: workflowID,
	}).Get(ctx, nil)
}

func updateJobApplicationStatus(ctx workflow.Context, idJobApplication uint, status sqldb.JobApplicationStatus) error {
	return workflow.ExecuteActivity(ctx, "UpdateJobApplication", sqldb.UpdateJobApplicationInput{
		IdJobApplication: idJobApplication,
		Data: map[string]interface{}{
			"status": status,
		},
	}).Get(ctx, nil)
}

func updateJobApplication(ctx workflow.Context, idJobApplication uint, data map[string]interface{}) error {
	return workflow.ExecuteActivity(ctx, "UpdateJobApplication", sqldb.UpdateJobApplicationInput{
		IdJobApplication: idJobApplication,
		Data:             data,
	}).Get(ctx, nil)
}

type NotifyHumanActivityInput struct {
	Message    string `json:"message"`
	WorkflowID string `json:"workflow_id"`
}

type AwaitUserActionInput struct {
	WorkflowId            string
	UserAction            UserAction
	CompanyName           string
	JobTitle              string
	IdUser                uint
	ApplicationExternalId string
}

func awaitUserAction(ctx workflow.Context, input AwaitUserActionInput) error {
	signalChan := workflow.GetSignalChannel(ctx, string(input.UserAction))

	notificationMessage := ""
	switch input.UserAction {
	case UserActionCaptcha:
		notificationMessage = fmt.Sprintf("Your application for %s at %s is blocked on a CAPTCHA. Please help with the CAPTCHA.", input.JobTitle, input.CompanyName)
	case UserActionAuthentication:
		notificationMessage = fmt.Sprintf("Your application for %s at %s is blocked on a login screen. Please help with the authentication.", input.JobTitle, input.CompanyName)
	}

	// Tell the UI/Human that we are blocked
	err := sendUserNotification(ctx, input.IdUser, input.WorkflowId, input.ApplicationExternalId, notificationMessage)
	if err != nil {
		return err
	}

	// Create a 5-minute timeout timer
	timerChan := workflow.NewTimer(ctx, 5*time.Minute)

	// Wait for either the signal or the timeout
	selector := workflow.NewSelector(ctx)
	signalReceived := false
	selector.AddReceive(signalChan, func(c workflow.ReceiveChannel, more bool) {
		c.Receive(ctx, nil)
		signalReceived = true
	})
	selector.AddFuture(timerChan, func(f workflow.Future) {
		// Timer fired - timeout occurred
	})
	selector.Select(ctx)

	if !signalReceived {
		return fmt.Errorf("timeout waiting for user action %s after 5 minutes", input.UserAction)
	}
	return nil
}

func fetchUserResume(ctx workflow.Context, idUser uint) (sqldb.Resume, error) {
	var resume sqldb.Resume
	if err := workflow.ExecuteActivity(ctx, "FetchActiveUserResume", idUser).Get(ctx, &resume); err != nil {
		return sqldb.Resume{}, err
	}
	return resume, nil
}

type UserProfile struct {
	FirstName              string   `json:"first_name"`
	LastName               string   `json:"last_name"`
	Email                  string   `json:"email"`
	Phone                  string   `json:"phone"`
	Address                string   `json:"address"`
	City                   string   `json:"city"`
	State                  string   `json:"state"`
	Zip                    string   `json:"zip"`
	CountryOfResidence     string   `json:"country_of_residence"`
	IsVeteran              bool     `json:"is_veteran"`
	CountriesOfCitizenship []string `json:"countries_of_citizenship"`
	Gender                 string   `json:"gender"`
	DateOfBirth            string   `json:"date_of_birth"`
	Age                    int      `json:"age"`
}

func fetchJobApplicationProfile(ctx workflow.Context, idUser uint) (UserProfile, error) {
	var jobApplicationProfile sqldb.JobApplicationProfile
	if err := workflow.ExecuteActivity(ctx, "FetchJobApplicationProfile", idUser).Get(ctx, &jobApplicationProfile); err != nil {
		return UserProfile{}, err
	}
	age := time.Now().Year() - jobApplicationProfile.DateOfBirth.Year()
	return UserProfile{
		FirstName:              jobApplicationProfile.FirstName,
		LastName:               jobApplicationProfile.LastName,
		Email:                  jobApplicationProfile.Email,
		Phone:                  jobApplicationProfile.Phone,
		Address:                jobApplicationProfile.Address,
		City:                   jobApplicationProfile.City,
		State:                  jobApplicationProfile.State,
		Zip:                    jobApplicationProfile.Zip,
		CountryOfResidence:     jobApplicationProfile.CountryOfResidence,
		IsVeteran:              jobApplicationProfile.IsVeteran,
		CountriesOfCitizenship: jobApplicationProfile.CountriesOfCitizenship,
		Gender:                 jobApplicationProfile.Gender,
		DateOfBirth:            jobApplicationProfile.DateOfBirth.Format("2006-01-02"),
		Age:                    age,
	}, nil
}
