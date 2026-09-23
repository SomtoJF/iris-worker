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
