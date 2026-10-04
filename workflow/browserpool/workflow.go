package browserpool

import (
	"fmt"
	"sort"
	"strconv"
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

const (
	// 10,000 events limit
	browserPoolHistoryLengthLimit = 10000
	// 10MB workflow history size limit
	browserPoolHistorySizeLimit = 10485760
)

func BrowserPoolWorkflow(ctx workflow.Context, input BrowserPoolWorkflowInput) error {
	logger := workflow.GetLogger(ctx)

	applicationQueue := NewApplicationQueue(ctx, input.InitialQueue)
	logger.Info("Application queue initialized", "initialQueue", input.InitialQueue)
	queuedApplications := make(map[string]struct{}, len(input.InitialQueue))
	for _, item := range input.InitialQueue {
		queuedApplications[applicationQueueKey(item)] = struct{}{}
	}
	for _, item := range input.ActiveApplications {
		queuedApplications[applicationQueueKey(item)] = struct{}{}
	}

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
		if !rollingOver && browserPoolHistoryLimitReached(ctx) {
			rollingOver = true
			logger.Info("Browser pool history rollover started",
				"historyLength", workflow.GetInfo(ctx).GetCurrentHistoryLength(),
				"historySize", workflow.GetInfo(ctx).GetCurrentHistorySize(),
			)
		}

		if rollingOver {
			drainApplicationSignals(queueSignalChan, applicationQueue, queuedApplications)
			drainCancellationSignals(ctx, cancelSignalChan, applicationQueue, queuedApplications, activeApplications)
			drainSettledApplications(settledSignalChan, sem, queuedApplications, activeApplications)
			drainChildCompletions(childCompleted, sem, queuedApplications, activeApplications)

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
			enqueueApplication(applicationQueue, queuedApplications, item)
		})
		selector.AddReceive(cancelSignalChan, func(channel workflow.ReceiveChannel, _ bool) {
			var payload types.CancelApplicationPayload
			channel.Receive(ctx, &payload)
			handleApplicationCancellation(ctx, applicationQueue, queuedApplications, activeApplications, payload)
		})
		selector.AddReceive(settledSignalChan, func(channel workflow.ReceiveChannel, _ bool) {
			var settled types.BrowserPoolApplicationSettledPayload
			channel.Receive(ctx, &settled)
			settleApplication(sem, queuedApplications, activeApplications, settled.IdJobApplication)
		})
		selector.AddReceive(childCompleted, func(channel workflow.ReceiveChannel, _ bool) {
			var idJobApplication uint
			channel.Receive(ctx, &idJobApplication)
			settleApplication(sem, queuedApplications, activeApplications, idJobApplication)
		})

		selector.Select(ctx)
	}
}

func browserPoolHistoryLimitReached(ctx workflow.Context) bool {
	info := workflow.GetInfo(ctx)
	return info.GetCurrentHistoryLength() > browserPoolHistoryLengthLimit ||
		info.GetCurrentHistorySize() > browserPoolHistorySizeLimit
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

func settleApplication(sem workflow.Semaphore, queuedApplications map[string]struct{}, activeApplications map[uint]types.ApplicationQueueItem, idJobApplication uint) {
	item, exists := activeApplications[idJobApplication]
	if !exists {
		return
	}
	delete(activeApplications, idJobApplication)
	delete(queuedApplications, applicationQueueKey(item))
	sem.Release(1)
}

func enqueueApplication(applicationQueue *ApplicationQueue, queuedApplications map[string]struct{}, item types.ApplicationQueueItem) {
	key := applicationQueueKey(item)
	if _, exists := queuedApplications[key]; exists {
		return
	}
	queuedApplications[key] = struct{}{}
	applicationQueue.Enqueue(item)
}

func applicationQueueKey(item types.ApplicationQueueItem) string {
	return strconv.FormatUint(uint64(item.IdJobApplication), 10)
}

func drainApplicationSignals(signalChan workflow.ReceiveChannel, applicationQueue *ApplicationQueue, queuedApplications map[string]struct{}) {
	for {
		var item types.ApplicationQueueItem
		if !signalChan.ReceiveAsync(&item) {
			return
		}
		enqueueApplication(applicationQueue, queuedApplications, item)
	}
}

func drainCancellationSignals(ctx workflow.Context, signalChan workflow.ReceiveChannel, applicationQueue *ApplicationQueue, queuedApplications map[string]struct{}, activeApplications map[uint]types.ApplicationQueueItem) {
	for {
		var payload types.CancelApplicationPayload
		if !signalChan.ReceiveAsync(&payload) {
			return
		}
		handleApplicationCancellation(ctx, applicationQueue, queuedApplications, activeApplications, payload)
	}
}

func handleApplicationCancellation(ctx workflow.Context, applicationQueue *ApplicationQueue, queuedApplications map[string]struct{}, activeApplications map[uint]types.ApplicationQueueItem, payload types.CancelApplicationPayload) {
	if item, removed := applicationQueue.Remove(payload.IdJobApplication); removed {
		delete(queuedApplications, applicationQueueKey(item))
		return
	}

	item, active := activeApplications[payload.IdJobApplication]
	if !active {
		return
	}
	workflow.SignalExternalWorkflow(ctx, item.ApplicationWorkflowId, "", jobapplication.CancelSignalName, jobapplication.CancelSignalPayload{Reason: payload.Reason})
}

func drainSettledApplications(signalChan workflow.ReceiveChannel, sem workflow.Semaphore, queuedApplications map[string]struct{}, activeApplications map[uint]types.ApplicationQueueItem) {
	for {
		var settled types.BrowserPoolApplicationSettledPayload
		if !signalChan.ReceiveAsync(&settled) {
			return
		}
		settleApplication(sem, queuedApplications, activeApplications, settled.IdJobApplication)
	}
}

func drainChildCompletions(channel workflow.ReceiveChannel, sem workflow.Semaphore, queuedApplications map[string]struct{}, activeApplications map[uint]types.ApplicationQueueItem) {
	for {
		var idJobApplication uint
		if !channel.ReceiveAsync(&idJobApplication) {
			return
		}
		settleApplication(sem, queuedApplications, activeApplications, idJobApplication)
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
		BrowserPoolWorkflowID: workflow.GetInfo(ctx).WorkflowExecution.ID,
	}

	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:               item.ApplicationWorkflowId,
		ParentClosePolicy:        enumspb.PARENT_CLOSE_POLICY_ABANDON,
		WorkflowTaskTimeout:      1 * time.Minute,
		WorkflowExecutionTimeout: 35 * time.Minute,
	})
	return workflow.ExecuteChildWorkflow(childCtx, jobapplication.JobApplicationWorkflow, input).Get(ctx, nil)
}
