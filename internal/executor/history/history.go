package history

import (
	"time"
)

// WorkerHistoryEntry represents metadata for a task executed via worker.
type WorkerHistoryEntry struct {
	WorkflowID    string            `json:"workflow_id"`
	TaskID        string            `json:"task_id"`
	StepName      string            `json:"step_name"`
	Status        string            `json:"status"`
	ErrorMessage  string            `json:"error_message,omitempty"`
	OutputHandles map[string]string `json:"output_handles,omitempty"`
	Timestamp     time.Time         `json:"timestamp"`
}

// WorkerHistory defines the interface for tracking worker task executions.
type WorkerHistory interface {
	Add(entry WorkerHistoryEntry)
	GetEntries() []WorkerHistoryEntry
	Clear()
}
