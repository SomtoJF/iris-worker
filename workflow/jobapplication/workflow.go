package jobapplication

import (
	"fmt"
	"time"

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

	// TODO: Open the job posting url

	isApplicationComplete := false
	toolCallHistory := []ToolCallResult{}
	const maxAgentIterations = 20

	for iteration := 0; !isApplicationComplete && iteration < maxAgentIterations; iteration++ {
		// TODO: Take a screenshot of the current page and feed to the planner

		plannerRequest := PlannerRequest{
			JobPostingUrl:   input.Url,
			ToolCallHistory: toolCallHistory,
		}

		plannerResponse, err := planNextAction(ctx, plannerRequest)
		if err != nil {
			logger.Error("Failed to plan next action", "error", err)
			return err
		}
		isApplicationComplete = plannerResponse.IsApplicationComplete
		toolCalls := plannerResponse.ToolCalls

		results, err := executeToolCalls(ctx, toolCalls)
		if err != nil {
			logger.Error("Failed to execute tool calls", "error", err)
			return err
		}

		toolCallHistory = append(toolCallHistory, results...)
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

func updateJobApplicationStatus(ctx workflow.Context, idJobApplication uint, status sqldb.JobApplicationStatus) error {
	return workflow.ExecuteActivity(ctx, "UpdateJobApplication", sqldb.UpdateJobApplicationInput{
		IdJobApplication: idJobApplication,
		Data: map[string]interface{}{
			"status": status,
		},
	}).Get(ctx, nil)
}
