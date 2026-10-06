package browserpool

import (
	"fmt"
	"testing"

	"github.com/SomtoJF/iris-worker/workflow/browserpool/types"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestApplicationQueueFIFO(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(testQueueFIFO)

	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
}

func TestApplicationQueueRemoveAndDuplicate(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(testQueueRemoveAndDuplicate)

	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
}

func TestApplicationQueueConcurrentAccess(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(testQueueConcurrentAccess)

	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptApplicationDeduplicatesUserActionResume(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		queue := NewApplicationQueue(ctx, nil)
		queued := map[string]struct{}{}
		processed := map[uint]struct{}{}
		first := types.ApplicationQueueItem{
			IdJobApplication:      17,
			ApplicationWorkflowId: "resume-1",
			ResumeUserActionID:    23,
		}
		acceptApplication(queue, queued, processed, map[uint]types.ApplicationQueueItem{}, map[uint]types.ApplicationQueueItem{}, first)
		first.ApplicationWorkflowId = "resume-duplicate"
		acceptApplication(queue, queued, processed, map[uint]types.ApplicationQueueItem{}, map[uint]types.ApplicationQueueItem{}, first)

		items := queue.Snapshot()
		if len(items) != 1 || items[0].ApplicationWorkflowId != "resume-1" {
			return fmt.Errorf("queued resume = %+v, want one original resume", items)
		}
		if _, ok := processed[23]; !ok {
			return fmt.Errorf("resume user action ID was not recorded")
		}
		return nil
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptApplicationDefersResumeUntilCurrentWorkflowSettles(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		queue := NewApplicationQueue(ctx, nil)
		queued := map[string]struct{}{"17": {}}
		processed := map[uint]struct{}{}
		active := map[uint]types.ApplicationQueueItem{
			17: {IdJobApplication: 17, IdUser: 10, ApplicationWorkflowId: "paused"},
		}
		activeUsers := map[uint]struct{}{10: {}}
		deferred := map[uint]types.ApplicationQueueItem{}
		resume := types.ApplicationQueueItem{
			IdJobApplication:      17,
			IdUser:                10,
			ApplicationWorkflowId: "resume-1",
			ResumeUserActionID:    23,
		}

		acceptApplication(queue, queued, processed, active, deferred, resume)
		if !queue.IsEmpty() || deferred[17].ApplicationWorkflowId != "resume-1" {
			return fmt.Errorf("resume was not deferred while application remained active")
		}
		sem := workflow.NewSemaphore(ctx, 1)
		if !sem.TryAcquire(ctx, 1) {
			return fmt.Errorf("failed to acquire active application slot")
		}
		settleApplication(queue, sem, queued, active, activeUsers, deferred, 17)
		if items := queue.Snapshot(); len(items) != 1 || items[0].ApplicationWorkflowId != "resume-1" {
			return fmt.Errorf("settled application did not enqueue the durable resume: %+v", items)
		}
		acceptApplication(queue, queued, processed, active, deferred, resume)
		if items := queue.Snapshot(); len(items) != 1 {
			return fmt.Errorf("duplicate resume was enqueued: %+v", items)
		}
		return nil
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
}

func TestDequeueEligibleApplicationSerializesPerUser(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		queue := NewApplicationQueue(ctx, []types.ApplicationQueueItem{
			{IdJobApplication: 1, IdUser: 10},
			{IdJobApplication: 2, IdUser: 20},
			{IdJobApplication: 3, IdUser: 10},
		})
		activeUsers := map[uint]struct{}{10: {}}

		item, ok := dequeueEligibleApplication(queue, activeUsers)
		if !ok || item.IdJobApplication != 2 {
			return fmt.Errorf("eligible item = %+v, %v; want application 2 for user 20", item, ok)
		}
		if _, ok := dequeueEligibleApplication(queue, activeUsers); ok {
			return fmt.Errorf("dequeued an application while all remaining users are active")
		}

		delete(activeUsers, 10)
		item, ok = dequeueEligibleApplication(queue, activeUsers)
		if !ok || item.IdJobApplication != 1 {
			return fmt.Errorf("next eligible item = %+v, %v; want FIFO application 1 for user 10", item, ok)
		}
		return nil
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
}

func testQueueFIFO(ctx workflow.Context) error {
	queue := NewApplicationQueue(ctx, []types.ApplicationQueueItem{
		{IdJobApplication: 1},
		{IdJobApplication: 2},
		{IdJobApplication: 3},
	})

	for wantID := uint(1); wantID <= 3; wantID++ {
		item, err := queue.Dequeue(ctx)
		if err != nil {
			return err
		}
		if item.IdJobApplication != wantID {
			return fmt.Errorf("dequeued ID %d, want %d", item.IdJobApplication, wantID)
		}
	}
	if !queue.IsEmpty() {
		return fmt.Errorf("queue should be empty after dequeue")
	}
	if _, err := queue.Dequeue(ctx); err == nil {
		return fmt.Errorf("dequeue from empty queue should fail")
	}
	return nil
}

func testQueueRemoveAndDuplicate(ctx workflow.Context) error {
	queue := NewApplicationQueue(ctx, []types.ApplicationQueueItem{
		{IdJobApplication: 1},
		{IdJobApplication: 2},
		{IdJobApplication: 3},
	})
	queue.Enqueue(types.ApplicationQueueItem{IdJobApplication: 2})

	removed, ok := queue.Remove(2)
	if !ok || removed.IdJobApplication != 2 {
		return fmt.Errorf("remove returned (%+v, %v), want application 2", removed, ok)
	}
	if _, ok := queue.Remove(2); ok {
		return fmt.Errorf("removing application 2 twice should fail")
	}

	items := queue.Snapshot()
	if len(items) != 2 || items[0].IdJobApplication != 1 || items[1].IdJobApplication != 3 {
		return fmt.Errorf("snapshot after removal = %+v, want [1 3]", items)
	}
	return nil
}

func testQueueConcurrentAccess(ctx workflow.Context) error {
	queue := NewApplicationQueue(ctx, nil)
	done := workflow.NewChannel(ctx)

	for worker := uint(0); worker < 4; worker++ {
		worker := worker
		workflow.Go(ctx, func(gCtx workflow.Context) {
			for offset := uint(0); offset < 25; offset++ {
				queue.Enqueue(types.ApplicationQueueItem{
					IdJobApplication: worker*25 + offset + 1,
				})
			}
			done.Send(gCtx, true)
		})
	}

	for reader := 0; reader < 4; reader++ {
		workflow.Go(ctx, func(gCtx workflow.Context) {
			for i := 0; i < 25; i++ {
				queue.IsEmpty()
				queue.Snapshot()
			}
			done.Send(gCtx, true)
		})
	}

	for i := 0; i < 8; i++ {
		var completed bool
		done.Receive(ctx, &completed)
	}

	items := queue.Snapshot()
	if len(items) != 100 {
		return fmt.Errorf("concurrent snapshot length = %d, want 100", len(items))
	}

	seen := make(map[uint]struct{}, len(items))
	for _, item := range items {
		if _, exists := seen[item.IdJobApplication]; exists {
			return fmt.Errorf("duplicate application ID %d in snapshot", item.IdJobApplication)
		}
		seen[item.IdJobApplication] = struct{}{}
	}
	return nil
}
