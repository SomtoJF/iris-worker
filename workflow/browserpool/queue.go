package browserpool

import (
	"errors"

	"go.temporal.io/sdk/workflow"
)

type ApplicationQueue struct {
	Items []ApplicationQueueItem `json:"items"`
	Mutex workflow.Mutex
	Ctx   workflow.Context
}

func NewApplicationQueue(ctx workflow.Context, initialItems []ApplicationQueueItem) *ApplicationQueue {
	return &ApplicationQueue{
		Items: initialItems,
		Mutex: workflow.NewMutex(ctx),
		Ctx:   ctx,
	}
}

func (q *ApplicationQueue) Enqueue(item ApplicationQueueItem) {
	q.Mutex.Lock(q.Ctx)
	defer q.Mutex.Unlock()
	q.Items = append(q.Items, item)
}

func (q *ApplicationQueue) IsEmpty() bool {
	q.Mutex.Lock(q.Ctx)
	defer q.Mutex.Unlock()
	return len(q.Items) == 0
}

func (q *ApplicationQueue) Dequeue(ctx workflow.Context) (ApplicationQueueItem, error) {
	q.Mutex.Lock(q.Ctx)
	defer q.Mutex.Unlock()
	if len(q.Items) == 0 {
		return ApplicationQueueItem{}, errors.New("queue is empty")
	}
	item := q.Items[0]
	q.Items = q.Items[1:]
	return item, nil
}

func (q *ApplicationQueue) Snapshot() []ApplicationQueueItem {
	q.Mutex.Lock(q.Ctx)
	defer q.Mutex.Unlock()

	return append([]ApplicationQueueItem(nil), q.Items...)
}
