package browserpool

import (
	"errors"

	"github.com/SomtoJF/iris-worker/workflow/browserpool/types"
	"go.temporal.io/sdk/workflow"
)

type ApplicationQueue struct {
	Items []types.ApplicationQueueItem `json:"items"`
	Mutex workflow.Mutex
	Ctx   workflow.Context
}

func NewApplicationQueue(ctx workflow.Context, initialItems []types.ApplicationQueueItem) *ApplicationQueue {
	return &ApplicationQueue{
		Items: initialItems,
		Mutex: workflow.NewMutex(ctx),
		Ctx:   ctx,
	}
}

func (q *ApplicationQueue) Enqueue(item types.ApplicationQueueItem) {
	q.Mutex.Lock(q.Ctx)
	defer q.Mutex.Unlock()
	q.Items = append(q.Items, item)
}

func (q *ApplicationQueue) IsEmpty() bool {
	q.Mutex.Lock(q.Ctx)
	defer q.Mutex.Unlock()
	return len(q.Items) == 0
}

func (q *ApplicationQueue) Dequeue(ctx workflow.Context) (types.ApplicationQueueItem, error) {
	q.Mutex.Lock(q.Ctx)
	defer q.Mutex.Unlock()
	if len(q.Items) == 0 {
		return types.ApplicationQueueItem{}, errors.New("queue is empty")
	}
	item := q.Items[0]
	q.Items = q.Items[1:]
	return item, nil
}

func (q *ApplicationQueue) Remove(idJobApplication uint) (types.ApplicationQueueItem, bool) {
	q.Mutex.Lock(q.Ctx)
	defer q.Mutex.Unlock()

	for i, item := range q.Items {
		if item.IdJobApplication != idJobApplication {
			continue
		}
		q.Items = append(q.Items[:i], q.Items[i+1:]...)
		return item, true
	}
	return types.ApplicationQueueItem{}, false
}

func (q *ApplicationQueue) Snapshot() []types.ApplicationQueueItem {
	q.Mutex.Lock(q.Ctx)
	defer q.Mutex.Unlock()

	return append([]types.ApplicationQueueItem(nil), q.Items...)
}
