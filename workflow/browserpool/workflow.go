package browserpool

import (
	"time"

	"github.com/SomtoJF/iris-worker/workflow/jobapplication"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type BrowserPoolWorkflowInput struct {
	InitialQueue []ApplicationQueueItem `json:"initial_queue"`
}

const QUEUE_APPLICATION_SIGNAL_NAME = "queue_application"
const MAX_CONCURRENT_APPLICATIONS = 4
const BROWSER_POOL_ROLLOVER_TIMEOUT = 7 * 24 * time.Hour

func BrowserPoolWorkflow(ctx workflow.Context, input BrowserPoolWorkflowInput) error {
	logger := workflow.GetLogger(ctx)

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

	applicationQueue := NewApplicationQueue(ctx, input.InitialQueue)

	logger.Info("Application queue initialized", "initialQueue", input.InitialQueue)

	sem := workflow.NewSemaphore(ctx, MAX_CONCURRENT_APPLICATIONS)
	signalChan := workflow.GetSignalChannel(ctx, QUEUE_APPLICATION_SIGNAL_NAME)
	rolloverTimer := workflow.NewTimer(ctx, BROWSER_POOL_ROLLOVER_TIMEOUT)
	applicationCompleted := workflow.NewChannel(ctx)
	acceptingApplications := true
	activeApplications := 0

	for {
		if acceptingApplications && rolloverTimer.IsReady() {
			acceptingApplications = false
			logger.Info("Browser pool rollover started")
		}

		for acceptingApplications && !applicationQueue.IsEmpty() && sem.TryAcquire(ctx, 1) {
			item, err := applicationQueue.Dequeue(ctx)
			if err != nil {
				sem.Release(1)
				logger.Error("Failed to dequeue application", "error", err)
				break
			}

			activeApplications++
			workflow.Go(ctx, func(gCtx workflow.Context) {
				defer applicationCompleted.Send(gCtx, nil)
				defer sem.Release(1)

				if err := executeJobApplication(gCtx, item); err != nil {
					logger.Error("Job application workflow failed", "idJobApplication", item.IdJobApplication, "error", err)
				}
			})
		}

		if !acceptingApplications && activeApplications == 0 {
			drainApplicationSignals(signalChan, applicationQueue)
			return workflow.NewContinueAsNewError(ctx, BrowserPoolWorkflow, BrowserPoolWorkflowInput{
				InitialQueue: applicationQueue.Snapshot(),
			})
		}

		selector := workflow.NewSelector(ctx)
		if acceptingApplications {
			selector.AddFuture(rolloverTimer, func(workflow.Future) {
				acceptingApplications = false
				logger.Info("Browser pool rollover started")
			})
		}
		selector.AddReceive(signalChan, func(channel workflow.ReceiveChannel, _ bool) {
			var item ApplicationQueueItem
			channel.Receive(ctx, &item)
			applicationQueue.Enqueue(item)
		})
		if activeApplications > 0 {
			selector.AddReceive(applicationCompleted, func(workflow.ReceiveChannel, bool) {
				activeApplications--
			})
		}

		selector.Select(ctx)
	}
}

func drainApplicationSignals(signalChan workflow.ReceiveChannel, applicationQueue *ApplicationQueue) {
	for {
		var item ApplicationQueueItem
		if !signalChan.ReceiveAsync(&item) {
			return
		}
		applicationQueue.Enqueue(item)
	}
}

func executeJobApplication(ctx workflow.Context, item ApplicationQueueItem) error {
	input := jobapplication.JobApplicationWorkflowInput{
		IdJobApplication: item.IdJobApplication,
		Url:              item.Url,
		IdUser:           item.IdUser,
		IdResume:         item.IdResume,
	}

	return workflow.ExecuteChildWorkflow(ctx, jobapplication.JobApplicationWorkflow, input).Get(ctx, nil)
}
