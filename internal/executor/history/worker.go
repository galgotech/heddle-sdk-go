package history

import (
	"sync"
)

type workerHistory struct {
	mu      sync.Mutex
	entries []WorkerHistoryEntry
}

func NewWorkerHistory() WorkerHistory {
	return &workerHistory{}
}

func (wh *workerHistory) Add(entry WorkerHistoryEntry) {
	wh.mu.Lock()
	defer wh.mu.Unlock()
	wh.entries = append(wh.entries, entry)
}

func (wh *workerHistory) GetEntries() []WorkerHistoryEntry {
	wh.mu.Lock()
	defer wh.mu.Unlock()

	copied := make([]WorkerHistoryEntry, len(wh.entries))
	copy(copied, wh.entries)
	return copied
}

func (wh *workerHistory) Clear() {
	wh.mu.Lock()
	defer wh.mu.Unlock()
	wh.entries = nil
}
