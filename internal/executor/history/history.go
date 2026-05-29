package history

import (
	"time"

	"github.com/apache/arrow/go/v18/arrow"
)

// HistoryState represents a single step execution state in the local history.
type HistoryState struct {
	StepName string
	Columns  map[string]arrow.Array
}

// LocalHistory defines the interface for local execution history and time travel simulation.
type LocalHistory interface {
	Add(stepName string, columns map[string]arrow.Array)
	Get() []string
	SetCursor(index int) error
	GetSimulatedSHM() map[string]arrow.Array
	Clear()
}

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
