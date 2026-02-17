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
	IdJobApplication uint   `json:"id_job_application"`
	Url              string `json:"url"`
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

	if err := openWebpage(sessionCtx, workflowId, input.Url); err != nil {
		logger.Error("Failed to open webpage", "error", err)
		updateJobApplicationStatus(ctx, input.IdJobApplication, sqldb.JobApplicationStatusFailed)
		return err
	}

	defer func() {
		workflow.ExecuteActivity(sessionCtx, "ClosePage", browser.ClosePageInput{
			WorkflowID: workflowId,
		}).Get(sessionCtx, nil)
	}()

	userResumeContent := getDummyUserResume()

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
			JobPostingUrl:   input.Url,
			ScreenshotPath:  screenshot.Path,
			TaggedNodes:     screenshot.TaggedNodes,
			ToolCallHistory: toolCallHistory,
			UserResume:      userResumeContent,
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
			err = awaitUserAction(ctx, workflowId, UserAction(plannerResponse.UserAction))
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

type NotifyHumanActivityInput struct {
	Message    string `json:"message"`
	WorkflowID string `json:"workflow_id"`
}

func awaitUserAction(ctx workflow.Context, workflowID string, userAction UserAction) error {
	signalChan := workflow.GetSignalChannel(ctx, string(userAction))

	notificationMessage := ""
	switch userAction {
	case UserActionCaptcha:
		notificationMessage = "Need help with CAPTCHA"
	case UserActionAuthentication:
		notificationMessage = "Need help with authentication"
	}

	// Tell the UI/Human that we are blocked
	// TODO: Implement this function
	err := sendUserNotification(ctx, workflowID, notificationMessage)
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
		return fmt.Errorf("timeout waiting for user action %s after 5 minutes", userAction)
	}
	return nil
}

func getDummyUserResume() string {
	userResumeContent := `JANE DOE
jane.doe@email.com | (555) 123-4567 | San Francisco, CA | linkedin.com/in/janedoe

SUMMARY
Software Engineer with 5+ years of experience building web applications and APIs. Strong in Go, Python, and TypeScript. Passionate about clean architecture and developer experience.

EXPERIENCE

Senior Software Engineer | Acme Corp | 2021 – Present
- Led migration of legacy monolith to microservices; reduced deploy time by 60%
- Built internal tooling in Go and TypeScript used by 50+ engineers
- Mentored 3 junior developers; conducted code reviews and design reviews

Software Engineer | TechStart Inc | 2018 – 2021
- Developed REST and gRPC APIs in Go; improved latency by 40%
- Wrote unit and integration tests; raised coverage from 60% to 85%
- Collaborated with product and design on feature specs and UX

EDUCATION
B.S. Computer Science | State University | 2018

SKILLS
Languages: Go, Python, TypeScript, SQL
Tools: Docker, Kubernetes, PostgreSQL, Redis, Temporal, Git
`
	return userResumeContent
}

func fetchUserResume(ctx workflow.Context) (sqldb.Resume, error) {
	var resume sqldb.Resume
	if err := workflow.ExecuteActivity(ctx, "FetchActiveUserResume", nil).Get(ctx, &resume); err != nil {
		return sqldb.Resume{}, err
	}
	return resume, nil
}
