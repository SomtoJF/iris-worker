package handleuseraction

import (
	"github.com/SomtoJF/iris-worker/activity/sqldb"
	"github.com/SomtoJF/iris-worker/shared"
	"go.temporal.io/sdk/workflow"
)

type HandleUserActionWorkflowInput struct {
	IdUser           uint              `json:"id_user"`
	IdJobApplication uint              `json:"id_job_application"`
	UserAction       shared.UserAction `json:"user_action"`
	ActionDetails    string            `json:"action_details"`
}

func HandleUserActionWorkflow(ctx workflow.Context, input HandleUserActionWorkflowInput) (map[string]interface{}, error) {
	logger := workflow.GetLogger(ctx)

	logger.Info("HandleUserActionWorkflow started", "input", input)

	// mark job application as blocked
	if err := workflow.ExecuteActivity(ctx, "UpdateJobApplicationStatus", input.IdJobApplication, sqldb.JobApplicationStatusBlocked).Get(ctx, nil); err != nil {
		logger.Error("Failed to mark job application as blocked", "error", err)
		return nil, err
	}

	// take screenshot of page

	// Call LLM with the screenshot, user action, and action details to build the user action format

	// Create a pending user action record in the database

	// Send user notification for the user to take action

	// Block workflow for 10 minutes and wait for user action

	// If user action is taken, update the user action record in the database to is_pending false.

	// Update job application status to pending

	// Return the user action result (job application workflow will fill out any provided data)

	// If user action is not taken, do nothing. Parent workflow will timeout eventually.

	return map[string]interface{}{}, nil
}
