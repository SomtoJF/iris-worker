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
	workflow.GetLogger(ctx).Info("JobApplicationWorkflow started", "url", input.Url)

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

	isApplicationComplete := false
	for !isApplicationComplete {
		isApplicationComplete = true
	}

	updateJobApplicationStatus(ctx, input.IdJobApplication, sqldb.JobApplicationStatusApplied)

	fmt.Println("JobApplicationWorkflow started", "url", input.Url)

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
