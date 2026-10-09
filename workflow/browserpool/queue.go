package browserpool

import (
	"errors"

	"github.com/SomtoJF/iris-worker/workflow/browserpool/types"
	"go.temporal.io/sdk/workflow"
)

type queueEntry struct {
	item types.ApplicationQueueItem
	prev *queueEntry
	next *queueEntry
}

type ApplicationQueue struct {
	entries map[uint]*queueEntry
	head    *queueEntry
	tail    *queueEntry
	Mutex   workflow.Mutex
	Ctx     workflow.Context
}

func NewApplicationQueue(ctx workflow.Context, initialItems []types.ApplicationQueueItem) *ApplicationQueue {
	queue := &ApplicationQueue{
		entries: make(map[uint]*queueEntry, len(initialItems)),
		Mutex:   workflow.NewMutex(ctx),
		Ctx:     ctx,
	}
	for _, item := range initialItems {
		queue.enqueue(item)
	}
	return queue
}

func (q *ApplicationQueue) Enqueue(item types.ApplicationQueueItem) {
	q.Mutex.Lock(q.Ctx)
	defer q.Mutex.Unlock()
	q.enqueue(item)
}

func (q *ApplicationQueue) IsEmpty() bool {
	q.Mutex.Lock(q.Ctx)
	defer q.Mutex.Unlock()
	return q.head == nil
}

func (q *ApplicationQueue) Dequeue(ctx workflow.Context) (types.ApplicationQueueItem, error) {
	q.Mutex.Lock(q.Ctx)
	defer q.Mutex.Unlock()
	if q.head == nil {
		return types.ApplicationQueueItem{}, errors.New("queue is empty")
	}
	item := q.head.item
	q.removeEntry(q.head)
	return item, nil
}

func (q *ApplicationQueue) Remove(idJobApplication uint) (types.ApplicationQueueItem, bool) {
	q.Mutex.Lock(q.Ctx)
	defer q.Mutex.Unlock()

	entry, exists := q.entries[idJobApplication]
	if !exists {
		return types.ApplicationQueueItem{}, false
	}
	item := entry.item
	q.removeEntry(entry)
	return item, true
}

func (q *ApplicationQueue) Snapshot() []types.ApplicationQueueItem {
	q.Mutex.Lock(q.Ctx)
	defer q.Mutex.Unlock()

	items := make([]types.ApplicationQueueItem, 0, len(q.entries))
	for entry := q.head; entry != nil; entry = entry.next {
		items = append(items, entry.item)
	}
	return items
}

func (q *ApplicationQueue) enqueue(item types.ApplicationQueueItem) {
	if _, exists := q.entries[item.IdJobApplication]; exists {
		return
	}
	entry := &queueEntry{item: item}
	q.entries[item.IdJobApplication] = entry
	if q.tail == nil {
		q.head = entry
		q.tail = entry
		return
	}
	entry.prev = q.tail
	q.tail.next = entry
	q.tail = entry
}

func (q *ApplicationQueue) removeEntry(entry *queueEntry) {
	if entry.prev == nil {
		q.head = entry.next
	} else {
		entry.prev.next = entry.next
	}
	if entry.next == nil {
		q.tail = entry.prev
	} else {
		entry.next.prev = entry.prev
	}
	delete(q.entries, entry.item.IdJobApplication)
}
