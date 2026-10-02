package jobapplication

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/SomtoJF/iris-worker/activity/browser"
	"github.com/SomtoJF/iris-worker/activity/realtimeevent"
	"github.com/SomtoJF/iris-worker/activity/sqldb"
	"github.com/SomtoJF/iris-worker/browserfactory"
	browserpooltypes "github.com/SomtoJF/iris-worker/workflow/browserpool/types"
	jobapplicationprofile "github.com/SomtoJF/iris-worker/workflow/jobapplication/profile"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type JobApplicationWorkflowInput struct {
	IdJobApplication      uint   `json:"id_job_application"`
	BrowserPoolWorkflowID string `json:"browser_pool_workflow_id,omitempty"`
}

type jobApplicationRuntimeInput struct {
	IdJobApplication      uint
	ApplicationExternalId string
	Url                   string
	IdUser                uint
	IdResume              uint
	BrowserPoolWorkflowID string
}

type JobDetails struct {
	JobTitle          string
	CompanyName       string
	JobDescription    string
	IsValidJobPosting bool
}

const CancelSignalName = "CANCEL_APPLICATION"

type CancelSignalPayload struct {
	Reason string `json:"reason"`
}

type jobAppError struct {
	PublicMessage  string
	LogMessage     string
	Cause          error
	TerminalStatus sqldb.JobApplicationStatus
}

func (e jobAppError) Error() string {
	if e.Cause == nil {
		return e.LogMessage
	}
	if e.LogMessage == "" {
		return e.Cause.Error()
	}
	return fmt.Sprintf("%s: %v", e.LogMessage, e.Cause)
}

func (e jobAppError) Unwrap() error {
	return e.Cause
}

func newJobAppError(err error, logMsg string, publicMsg string) error {
	return jobAppError{
		PublicMessage:  publicMsg,
		LogMessage:     logMsg,
		Cause:          err,
		TerminalStatus: sqldb.JobApplicationStatusFailed,
	}
}

func newHaltedJobAppError(err error, logMsg string, publicMsg string) error {
	return jobAppError{
		PublicMessage:  publicMsg,
		LogMessage:     logMsg,
		Cause:          err,
		TerminalStatus: sqldb.JobApplicationStatusHalted,
	}
}

func isCancelled(cancelCtx workflow.Context) bool {
	return cancelCtx.Err() != nil
}

func handleCancelOrTimeout(
	ctx workflow.Context,
	cancelCtx workflow.Context,
	timedOut bool,
	input jobApplicationRuntimeInput,
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

	logger.Info("JobApplicationWorkflow started", "id_job_application", input.IdJobApplication)

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

	var application sqldb.JobApplication
	if err := workflow.ExecuteActivity(ctx, "GetJobApplication", sqldb.GetJobApplicationInput{
		IdJobApplication: input.IdJobApplication,
	}).Get(ctx, &application); err != nil {
		logger.Error("Failed to get job application", "error", err)
		return err
	}
	jobDetails := JobDetails{
		JobTitle:          application.JobTitle,
		CompanyName:       application.CompanyName,
		JobDescription:    application.JobDescription,
		IsValidJobPosting: true,
	}
	runtimeInput := jobApplicationRuntimeInput{
		IdJobApplication:      application.IdJobApplication,
		ApplicationExternalId: application.IdExternal.String(),
		Url:                   application.Url,
		IdUser:                application.UserId,
		IdResume:              application.ResumeId,
		BrowserPoolWorkflowID: input.BrowserPoolWorkflowID,
	}
	defer notifyBrowserPoolApplicationSettled(ctx, runtimeInput)

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

	var execResult executeJobApplicationResult

	sessionCtx, err := workflow.CreateSession(cancelCtx, &workflow.SessionOptions{
		ExecutionTimeout: SESSION_TIMEOUT,
		CreationTimeout:  time.Minute,
	})
	if err != nil {
		if handled, cerr := handleCancelOrTimeout(ctx, cancelCtx, timedOut, runtimeInput, jobDetails, cancelPayload); handled {
			return cerr
		}
		logger.Error("Failed to create session", "error", err)
		handleApplicationError(ctx, runtimeInput, jobDetails, "An error occurred while starting your application session")
		return err
	}
	defer workflow.CompleteSession(sessionCtx)

	err = executeJobApplication(ctx, cancelCtx, sessionCtx, workflowId, runtimeInput, &jobDetails, &execResult)
	if err != nil {
		if handled, cerr := handleCancelOrTimeout(ctx, cancelCtx, timedOut, runtimeInput, jobDetails, cancelPayload); handled {
			return cerr
		}

		publicMessage := "We couldn't complete your application. Please try again."
		terminalStatus := sqldb.JobApplicationStatusFailed
		var jobErr jobAppError
		if errors.As(err, &jobErr) {
			if jobErr.PublicMessage != "" {
				publicMessage = jobErr.PublicMessage
			}
			if jobErr.TerminalStatus != "" {
				terminalStatus = jobErr.TerminalStatus
			}
			if jobErr.LogMessage != "" {
				logger.Error(jobErr.LogMessage, "error", err)
			} else {
				logger.Error("Job application workflow failed", "error", err)
			}
		} else {
			logger.Error("Job application workflow failed", "error", err)
		}

		if terminalStatus == sqldb.JobApplicationStatusHalted {
			handleApplicationHalted(ctx, runtimeInput, jobDetails, publicMessage)
		} else {
			handleApplicationError(ctx, runtimeInput, jobDetails, publicMessage)
		}
		return err
	}

	handleApplicationSuccess(ctx, runtimeInput, jobDetails)

	questions := mapToQuestions(execResult.QAMap)
	if len(questions) > 0 {
		deduped, err := deduplicateQA(ctx, runtimeInput.IdUser, runtimeInput.IdJobApplication, questions)
		if err != nil {
			logger.Warn("Failed to deduplicate Q&A, saving raw", "error", err)
		} else {
			questions = deduped
		}
	}

	if err := saveApplicationData(ctx, runtimeInput.IdUser, runtimeInput.IdJobApplication, questions); err != nil {
		logger.Error("Failed to save application data", "error", err)
	}

	if execResult.CoverLetter != nil && *execResult.CoverLetter != "" {
		if err := upsertCoverLetter(ctx, runtimeInput, jobDetails, *execResult.CoverLetter); err != nil {
			logger.Error("Failed to save cover letter", "error", err)
		}
	}

	return nil
}

func notifyBrowserPoolApplicationSettled(ctx workflow.Context, input jobApplicationRuntimeInput) {
	if input.BrowserPoolWorkflowID == "" {
		return
	}

	err := workflow.SignalExternalWorkflow(
		ctx,
		input.BrowserPoolWorkflowID,
		"",
		browserpooltypes.BROWSER_POOL_APPLICATION_SETTLED_SIGNAL_NAME,
		browserpooltypes.BrowserPoolApplicationSettledPayload{IdJobApplication: input.IdJobApplication},
	).Get(ctx, nil)
	if err != nil {
		workflow.GetLogger(ctx).Error("Failed to signal browser pool application completion", "error", err)
	}
}

type executeJobApplicationResult struct {
	CoverLetter *string
	QAMap       map[string]string
}

func executeJobApplication(
	ctx workflow.Context,
	cancelCtx workflow.Context,
	sessionCtx workflow.Context,
	workflowID string,
	input jobApplicationRuntimeInput,
	jobDetails *JobDetails,
	result *executeJobApplicationResult,
) error {
	userResume, err := fetchUserResume(cancelCtx, input.IdResume)
	if err != nil {
		return newJobAppError(err, "Failed to fetch user resume", "An error occurred while fetching your resume")
	}

	userProfile, err := jobapplicationprofile.Fetch(cancelCtx, input.IdUser)
	if err != nil {
		return newJobAppError(err, "Failed to fetch user profile", "An error occurred while fetching your profile")
	}

	resumePath, err := loadResumeIntoMemory(cancelCtx, userResume.FileName, userResume.FileKey)
	if err != nil {
		return newJobAppError(err, "Failed to download and load resume into memory", "An error occurred while loading your resume into memory")
	}

	if err := openWebpage(sessionCtx, workflowID, input.Url); err != nil {
		return newJobAppError(err, "Failed to open webpage", "We couldn't open the job posting page")
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
			WorkflowID: workflowID,
		}).Get(newCtx, nil)
	}()

	userProfileBytes, err := json.Marshal(userProfile)
	if err != nil {
		return newJobAppError(err, "Failed to marshal user profile", "We couldn't marshal the user profile")
	}
	userProfileJSON := string(userProfileBytes)

	isApplicationComplete := false
	toolCallHistory := []ToolCallResult{}
	qaMap := make(map[string]string)
	const maxAgentIterations = 50

	for iteration := 0; !isApplicationComplete && iteration < maxAgentIterations; iteration++ {
		var screenshot browser.TakeScreenshotOutput
		err = workflow.ExecuteActivity(sessionCtx, "TakeScreenshot", browser.TakeScreenshotInput{
			WorkflowID: workflowID,
			FileName:   fmt.Sprintf("screenshot_%d.png", iteration),
		}).Get(sessionCtx, &screenshot)
		if err != nil {
			return newJobAppError(err, "Failed to take screenshot", "We couldn't continue the application because we failed to capture the page state")
		}

		// Deterministically detect + solve any captcha before the planner sees the page,
		// so the planner never has to reason about captchas. Re-screenshot afterwards if
		// a captcha was solved so tagged nodes reflect the cleared page.
		solvedCaptcha, err := maybeSolveCaptcha(sessionCtx, workflowID, input.IdUser, input.IdJobApplication)
		if err != nil {
			return newJobAppError(err, "Failed to handle captcha", "We couldn't get past a security check on the page")
		}
		if solvedCaptcha {
			err = workflow.ExecuteActivity(sessionCtx, "TakeScreenshot", browser.TakeScreenshotInput{
				WorkflowID: workflowID,
				FileName:   fmt.Sprintf("screenshot_%d_postcaptcha.png", iteration),
			}).Get(sessionCtx, &screenshot)
			if err != nil {
				return newJobAppError(err, "Failed to take screenshot after captcha", "We couldn't continue the application because we failed to capture the page state")
			}
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
			CurrentDate:             workflow.Now(cancelCtx).Format("2006-01-02"),
		}

		plannerResponse, err := planNextAction(cancelCtx, plannerRequest)
		if err != nil {
			return newJobAppError(err, "Failed to plan next action", "We couldn't continue the application because we failed to plan the next step")
		}

		if plannerResponse.IsApplicationFailed {
			failureReason := "We couldn't complete your application. Please try again."
			if plannerResponse.FailureReason != nil && *plannerResponse.FailureReason != "" {
				failureReason = *plannerResponse.FailureReason
			}
			if plannerResponse.FailureStatus != nil && *plannerResponse.FailureStatus == PlannerFailureStatusTruthfulness {
				return newHaltedJobAppError(fmt.Errorf("%s", failureReason), "Job application halted by truthfulness clause", failureReason)
			}
			return newJobAppError(fmt.Errorf("%s", failureReason), "Job application failed by planner", failureReason)
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
			toolResult := executeToolCall(sessionCtx, workflowID, input.IdUser, input.IdJobApplication, *plannerResponse.ToolCall)
			toolCallHistory = append(toolCallHistory, toolResult)

			if toolResult.Error != nil && isCancelled(cancelCtx) {
				return toolResult.Error
			}

			if plannerResponse.ToolCall.Name == "write_cover_letter" {
				if cl, ok := toolResult.Result["cover_letter"].(string); ok {
					result.CoverLetter = &cl
				}
			}
		}
	}

	if !isApplicationComplete {
		failureReason := "We couldn't complete your application. Please try again."
		return newJobAppError(fmt.Errorf("%s", failureReason), "Job application incomplete", failureReason)
	}

	result.QAMap = qaMap

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

func handleApplicationCancelled(ctx workflow.Context, input jobApplicationRuntimeInput, jobDetails JobDetails, reason string) {
	newCtx, _ := workflow.NewDisconnectedContext(ctx)
	cleanupOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	}
	newCtx = workflow.WithActivityOptions(newCtx, cleanupOpts)

	workflow.ExecuteActivity(newCtx, "PublishRedisEvent", input.IdUser, string(realtimeevent.EventApplicationCancelled), map[string]interface{}{
		"id":          input.ApplicationExternalId,
		"jobTitle":    jobDetails.JobTitle,
		"companyName": jobDetails.CompanyName,
		"reason":      reason,
	}).Get(newCtx, nil)
}

func handleApplicationError(ctx workflow.Context, input jobApplicationRuntimeInput, jobDetails JobDetails, failureReason string) {
	newCtx, _ := workflow.NewDisconnectedContext(ctx)
	cleanupOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	}
	newCtx = workflow.WithActivityOptions(newCtx, cleanupOpts)

	var failureReasonPtf *string
	if failureReason != "" {
		failureReasonPtf = &failureReason
	}
	updateJobApplicationStatus(newCtx, input.IdJobApplication, sqldb.JobApplicationStatusFailed, failureReasonPtf)

	workflow.ExecuteActivity(newCtx, "PublishRedisEvent", input.IdUser, string(realtimeevent.EventApplicationFailed), map[string]interface{}{
		"id":          input.ApplicationExternalId,
		"jobTitle":    jobDetails.JobTitle,
		"companyName": jobDetails.CompanyName,
	}).Get(newCtx, nil)
}

func handleApplicationHalted(ctx workflow.Context, input jobApplicationRuntimeInput, jobDetails JobDetails, haltReason string) {
	newCtx, _ := workflow.NewDisconnectedContext(ctx)
	cleanupOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	}
	newCtx = workflow.WithActivityOptions(newCtx, cleanupOpts)

	var haltReasonPtr *string
	if haltReason != "" {
		haltReasonPtr = &haltReason
	}
	updateJobApplicationStatus(newCtx, input.IdJobApplication, sqldb.JobApplicationStatusHalted, haltReasonPtr)

	workflow.ExecuteActivity(newCtx, "PublishRedisEvent", input.IdUser, string(realtimeevent.EventApplicationHalted), map[string]interface{}{
		"id":          input.ApplicationExternalId,
		"jobTitle":    jobDetails.JobTitle,
		"companyName": jobDetails.CompanyName,
	}).Get(newCtx, nil)
}

func handleApplicationSuccess(ctx workflow.Context, input jobApplicationRuntimeInput, jobDetails JobDetails) {
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

func updateJobApplicationStatus(ctx workflow.Context, idJobApplication uint, status sqldb.JobApplicationStatus, reason *string) error {
	data := map[string]interface{}{
		"status": status,
	}

	if status == sqldb.JobApplicationStatusApplied {
		data["applied_at"] = workflow.Now(ctx).Format("2006-01-02")
	}

	if status == sqldb.JobApplicationStatusHalted {
		data["halt_reason"] = reason
	} else {
		data["failure_reason"] = reason
	}
	return workflow.ExecuteActivity(ctx, "UpdateJobApplication", sqldb.UpdateJobApplicationInput{
		IdJobApplication: idJobApplication,
		Data:             data,
	}).Get(ctx, nil)
}

func mapToQuestions(qaMap map[string]string) []sqldb.JobApplicationQuestion {
	questions := make([]sqldb.JobApplicationQuestion, 0, len(qaMap))
	for q, a := range qaMap {
		questions = append(questions, sqldb.JobApplicationQuestion{Question: q, Answer: a})
	}
	return questions
}

func saveApplicationData(ctx workflow.Context, idUser, idJobApplication uint, questions []sqldb.JobApplicationQuestion) error {
	return workflow.ExecuteActivity(ctx, "CreateJobApplicationData", sqldb.CreateJobApplicationDataInput{
		IdUser:           idUser,
		IdJobApplication: idJobApplication,
		Questions:        questions,
	}).Get(ctx, nil)
}

func upsertCoverLetter(ctx workflow.Context, input jobApplicationRuntimeInput, jobDetails JobDetails, body string) error {
	return workflow.ExecuteActivity(ctx, "UpsertCoverLetter", sqldb.UpsertCoverLetterInput{
		IdUser:           input.IdUser,
		IdJobApplication: input.IdJobApplication,
		IdResume:         input.IdResume,
		JobTitle:         jobDetails.JobTitle,
		CompanyName:      jobDetails.CompanyName,
		JobDescription:   jobDetails.JobDescription,
		Url:              input.Url,
		Body:             body,
		Status:           sqldb.CoverLetterStatusReady,
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

func fetchUserResume(ctx workflow.Context, idResume uint) (sqldb.Resume, error) {
	var resume sqldb.Resume
	if err := workflow.ExecuteActivity(ctx, "GetResumeByID", idResume).Get(ctx, &resume); err != nil {
		return sqldb.Resume{}, err
	}
	return resume, nil
}
