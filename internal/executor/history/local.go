package history

import (
	"fmt"
	"maps"
	"reflect"
	"sync"

	"github.com/apache/arrow/go/v18/arrow"
)

type localHistory struct {
	mu      sync.Mutex
	history []*HistoryState
	cursor  int // -1 when empty
}

func NewLocalHistory() LocalHistory {
	return &localHistory{
		cursor: -1,
	}
}

func (lh *localHistory) Add(stepName string, columns map[string]arrow.Array) {
	lh.mu.Lock()
	defer lh.mu.Unlock()

	retainedCols := make(map[string]arrow.Array)

	for k, arr := range columns {
		if arr != nil && !reflect.ValueOf(arr).IsNil() {
			arr.Retain()
			retainedCols[k] = arr
		}
	}

	// Truncate history if cursor is in the past
	if lh.cursor >= 0 && lh.cursor < len(lh.history)-1 {
		for i := lh.cursor + 1; i < len(lh.history); i++ {
			lh.releaseState(lh.history[i])
		}

		lh.history = lh.history[:lh.cursor+1]
	}

	state := &HistoryState{
		StepName: stepName,
		Columns:  retainedCols,
	}
	lh.history = append(lh.history, state)
	lh.cursor = len(lh.history) - 1
}

func (lh *localHistory) Get() []string {
	lh.mu.Lock()
	defer lh.mu.Unlock()

	names := make([]string, len(lh.history))
	for i, h := range lh.history {
		names[i] = h.StepName
	}

	return names
}

func (lh *localHistory) SetCursor(index int) error {
	lh.mu.Lock()
	defer lh.mu.Unlock()

	if index < -1 || index >= len(lh.history) {
		return fmt.Errorf("index out of bounds: %d (history len: %d)", index, len(lh.history))
	}

	lh.cursor = index

	return nil
}

func (lh *localHistory) GetSimulatedSHM() map[string]arrow.Array {
	lh.mu.Lock()
	defer lh.mu.Unlock()

	if lh.cursor < 0 || lh.cursor >= len(lh.history) {
		return nil
	}

	state := lh.history[lh.cursor]
	shm := make(map[string]arrow.Array)
	maps.Copy(shm, state.Columns)

	return shm
}

func (lh *localHistory) Clear() {
	lh.mu.Lock()
	defer lh.mu.Unlock()

	for _, state := range lh.history {
		lh.releaseState(state)
	}

	lh.history = nil
	lh.cursor = -1
}

func (lh *localHistory) releaseState(state *HistoryState) {
	if state == nil {
		return
	}

	for _, arr := range state.Columns {
		if arr != nil && !reflect.ValueOf(arr).IsNil() {
			arr.Release()
		}
	}
}
