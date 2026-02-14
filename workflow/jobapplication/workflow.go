package jobapplication

import (
	"fmt"
	"time"

	"github.com/SomtoJF/iris-worker/activity/browser"
	"github.com/SomtoJF/iris-worker/activity/sqldb"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
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
			MaximumAttempts:    2,
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
		return err
	}
	defer workflow.CompleteSession(sessionCtx)

	if err := openWebpage(sessionCtx, workflowId, input.Url); err != nil {
		logger.Error("Failed to open webpage", "error", err)
		return err
	}

	defer func() {
		workflow.ExecuteActivity(sessionCtx, "ClosePage", browser.ClosePageInput{
			WorkflowID: workflowId,
		}).Get(sessionCtx, nil)
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
			return err
		}

		plannerRequest := PlannerRequest{
			JobPostingUrl:   input.Url,
			ScreenshotPath:  screenshot.Path,
			TaggedNodes:     screenshot.TaggedNodes,
			ToolCallHistory: toolCallHistory,
		}

		plannerResponse, err := planNextAction(ctx, plannerRequest)
		if err != nil {
			logger.Error("Failed to plan next action", "error", err)
			return err
		}
		isApplicationComplete = plannerResponse.IsApplicationComplete

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
