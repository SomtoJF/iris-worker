package browserpool

import (
	"fmt"
	"sort"
	"time"

	"github.com/SomtoJF/iris-worker/workflow/browserpool/types"
	"github.com/SomtoJF/iris-worker/workflow/jobapplication"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/workflow"
)

type BrowserPoolWorkflowInput struct {
	InitialQueue       []types.ApplicationQueueItem `json:"initial_queue"`
	ActiveApplications []types.ApplicationQueueItem `json:"active_applications"`
}

func BrowserPoolWorkflow(ctx workflow.Context, input BrowserPoolWorkflowInput) error {
	logger := workflow.GetLogger(ctx)

	applicationQueue := NewApplicationQueue(ctx, input.InitialQueue)
	logger.Info("Application queue initialized", "initialQueue", input.InitialQueue)

	sem := workflow.NewSemaphore(ctx, types.MAX_CONCURRENT_APPLICATIONS)
	activeApplications, err := restoreActiveApplications(ctx, sem, input.ActiveApplications)
	if err != nil {
		return err
	}

	queueSignalChan := workflow.GetSignalChannel(ctx, types.QUEUE_APPLICATION_SIGNAL_NAME)
	cancelSignalChan := workflow.GetSignalChannel(ctx, types.CANCEL_APPLICATION_SIGNAL_NAME)
	settledSignalChan := workflow.GetSignalChannel(ctx, types.BROWSER_POOL_APPLICATION_SETTLED_SIGNAL_NAME)
	rolloverTimer := workflow.NewTimer(ctx, types.BROWSER_POOL_ROLLOVER_TIMEOUT)
	childCompleted := workflow.NewChannel(ctx)
	rollingOver := false

	for {
		if !rollingOver && rolloverTimer.IsReady() {
			rollingOver = true
			logger.Info("Browser pool rollover started")
		}

		if rollingOver {
			drainApplicationSignals(queueSignalChan, applicationQueue)
			drainCancellationSignals(ctx, cancelSignalChan, applicationQueue, activeApplications)
			drainSettledApplications(settledSignalChan, sem, activeApplications)
			drainChildCompletions(childCompleted, sem, activeApplications)

			return workflow.NewContinueAsNewError(ctx, BrowserPoolWorkflow, BrowserPoolWorkflowInput{
				InitialQueue:       applicationQueue.Snapshot(),
				ActiveApplications: sortedApplicationItems(activeApplications),
			})
		}

		for !applicationQueue.IsEmpty() && sem.TryAcquire(ctx, 1) {
			item, err := applicationQueue.Dequeue(ctx)
			if err != nil {
				sem.Release(1)
				logger.Error("Failed to dequeue application", "error", err)
				break
			}

			activeApplications[item.IdJobApplication] = item
			workflow.Go(ctx, func(gCtx workflow.Context) {
				defer childCompleted.Send(gCtx, item.IdJobApplication)

				if err := executeJobApplication(gCtx, item); err != nil {
					logger.Error("Job application workflow failed", "idJobApplication", item.IdJobApplication, "error", err)
				}
			})
		}

		selector := workflow.NewSelector(ctx)
		if !rollingOver {
			selector.AddFuture(rolloverTimer, func(workflow.Future) {
				rollingOver = true
				logger.Info("Browser pool rollover started")
			})
		}
		selector.AddReceive(queueSignalChan, func(channel workflow.ReceiveChannel, _ bool) {
			var item types.ApplicationQueueItem
			channel.Receive(ctx, &item)
			applicationQueue.Enqueue(item)
		})
		selector.AddReceive(cancelSignalChan, func(channel workflow.ReceiveChannel, _ bool) {
			var payload types.CancelApplicationPayload
			channel.Receive(ctx, &payload)
			handleApplicationCancellation(ctx, applicationQueue, activeApplications, payload)
		})
		selector.AddReceive(settledSignalChan, func(channel workflow.ReceiveChannel, _ bool) {
			var settled types.BrowserPoolApplicationSettledPayload
			channel.Receive(ctx, &settled)
			settleApplication(sem, activeApplications, settled.IdJobApplication)
		})
		selector.AddReceive(childCompleted, func(channel workflow.ReceiveChannel, _ bool) {
			var idJobApplication uint
			channel.Receive(ctx, &idJobApplication)
			settleApplication(sem, activeApplications, idJobApplication)
		})

		selector.Select(ctx)
	}
}

func restoreActiveApplications(ctx workflow.Context, sem workflow.Semaphore, items []types.ApplicationQueueItem) (map[uint]types.ApplicationQueueItem, error) {
	activeApplications := make(map[uint]types.ApplicationQueueItem, len(items))
	for _, item := range items {
		if _, exists := activeApplications[item.IdJobApplication]; exists {
			continue
		}
		if !sem.TryAcquire(ctx, 1) {
			return nil, fmt.Errorf("active application count exceeds pool capacity")
		}
		activeApplications[item.IdJobApplication] = item
	}
	return activeApplications, nil
}

func settleApplication(sem workflow.Semaphore, activeApplications map[uint]types.ApplicationQueueItem, idJobApplication uint) {
	if _, exists := activeApplications[idJobApplication]; !exists {
		return
	}
	delete(activeApplications, idJobApplication)
	sem.Release(1)
}

func drainApplicationSignals(signalChan workflow.ReceiveChannel, applicationQueue *ApplicationQueue) {
	for {
		var item types.ApplicationQueueItem
		if !signalChan.ReceiveAsync(&item) {
			return
		}
		applicationQueue.Enqueue(item)
	}
}

func drainCancellationSignals(ctx workflow.Context, signalChan workflow.ReceiveChannel, applicationQueue *ApplicationQueue, activeApplications map[uint]types.ApplicationQueueItem) {
	for {
		var payload types.CancelApplicationPayload
		if !signalChan.ReceiveAsync(&payload) {
			return
		}
		handleApplicationCancellation(ctx, applicationQueue, activeApplications, payload)
	}
}

func handleApplicationCancellation(ctx workflow.Context, applicationQueue *ApplicationQueue, activeApplications map[uint]types.ApplicationQueueItem, payload types.CancelApplicationPayload) {
	if applicationQueue.Remove(payload.IdJobApplication) {
		return
	}

	item, active := activeApplications[payload.IdJobApplication]
	if !active {
		return
	}
	workflow.SignalExternalWorkflow(ctx, item.ApplicationWorkflowId, "", jobapplication.CancelSignalName, jobapplication.CancelSignalPayload{Reason: payload.Reason})
}

func drainSettledApplications(signalChan workflow.ReceiveChannel, sem workflow.Semaphore, activeApplications map[uint]types.ApplicationQueueItem) {
	for {
		var settled types.BrowserPoolApplicationSettledPayload
		if !signalChan.ReceiveAsync(&settled) {
			return
		}
		settleApplication(sem, activeApplications, settled.IdJobApplication)
	}
}

func drainChildCompletions(channel workflow.ReceiveChannel, sem workflow.Semaphore, activeApplications map[uint]types.ApplicationQueueItem) {
	for {
		var idJobApplication uint
		if !channel.ReceiveAsync(&idJobApplication) {
			return
		}
		settleApplication(sem, activeApplications, idJobApplication)
	}
}

func sortedApplicationItems(activeApplications map[uint]types.ApplicationQueueItem) []types.ApplicationQueueItem {
	items := make([]types.ApplicationQueueItem, 0, len(activeApplications))
	for _, item := range activeApplications {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].IdJobApplication < items[j].IdJobApplication })
	return items
}

func executeJobApplication(ctx workflow.Context, item types.ApplicationQueueItem) error {
	input := jobapplication.JobApplicationWorkflowInput{
		IdJobApplication:      item.IdJobApplication,
		Url:                   item.Url,
		IdUser:                item.IdUser,
		IdResume:              item.IdResume,
		BrowserPoolWorkflowID: workflow.GetInfo(ctx).WorkflowExecution.ID,
	}

	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:               item.ApplicationWorkflowId,
		ParentClosePolicy:        enumspb.PARENT_CLOSE_POLICY_ABANDON,
		WorkflowTaskTimeout:      1 * time.Minute,
		WorkflowExecutionTimeout: 30 * time.Minute,
	})
	return workflow.ExecuteChildWorkflow(childCtx, jobapplication.JobApplicationWorkflow, input).Get(ctx, nil)
}
