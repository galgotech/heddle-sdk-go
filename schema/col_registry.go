package schema

import (
	"strings"
	"sync"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
)

type MetadataEntry struct {
	Name      string
	ArrowType string
	IsStruct  bool
}

type ArrayEntry struct {
	// Leaf:
	Array arrow.Array

	// Struct view:
	Size    int
	Offsets []int
	From    []string
}

// StepDirection diferencia input de output.
type StepDirection int

const (
	Input StepDirection = iota
	Output
)

// StepColumns agrupa metadata e arrays de um lado (input ou output) de um step.
type StepColumns struct {
	metadata map[string]*MetadataEntry
	arrays   map[string]*ArrayEntry
	order    []string
}

// StepEntry agrupa input e output de um step.
type StepEntry struct {
	Input  *StepColumns
	Output *StepColumns
}

// ColRegistry organiza colunas por step name, dividido entre input e output.
type ColRegistry struct {
	mu    sync.RWMutex
	steps map[string]*StepEntry
}

func NewColRegistry() *ColRegistry {
	return &ColRegistry{
		steps: make(map[string]*StepEntry),
	}
}

// getCols is an internal helper that resolves a StepColumns pointer (unsafe for concurrent access).
func (r *ColRegistry) getCols(stepName string, dir StepDirection) (*StepColumns, bool) {
	step, exists := r.steps[stepName]
	if !exists {
		return nil, false
	}
	if dir == Input {
		return step.Input, true
	}
	return step.Output, true
}

// RegisterStep cria a entrada para um step (chamado na criação/registro do step).
func (r *ColRegistry) RegisterStep(stepName string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.steps[stepName]; !exists {
		r.steps[stepName] = &StepEntry{
			Input: &StepColumns{
				metadata: make(map[string]*MetadataEntry),
				arrays:   make(map[string]*ArrayEntry),
			},
			Output: &StepColumns{
				metadata: make(map[string]*MetadataEntry),
				arrays:   make(map[string]*ArrayEntry),
			},
		}
	}
}

// RegisterLeaf registra uma coluna leaf em um step/direction.
func (r *ColRegistry) RegisterLeaf(stepName string, dir StepDirection, name string, arrowType string, arr arrow.Array) {
	r.mu.Lock()
	defer r.mu.Unlock()

	cols, exists := r.getCols(stepName, dir)
	if !exists {
		return
	}

	cols.metadata[name] = &MetadataEntry{
		Name:      name,
		ArrowType: arrowType,
		IsStruct:  false,
	}
	cols.arrays[name] = &ArrayEntry{
		Array: arr,
	}

	found := false
	for _, o := range cols.order {
		if o == name {
			found = true
			break
		}
	}
	if !found {
		cols.order = append(cols.order, name)
	}
}

// RegisterStruct registra uma coluna struct (view) em um step/direction.
func (r *ColRegistry) RegisterStruct(stepName string, dir StepDirection, name string, children []string, size int, offsets []int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	cols, exists := r.getCols(stepName, dir)
	if !exists {
		return
	}

	cols.metadata[name] = &MetadataEntry{
		Name:      name,
		ArrowType: "struct",
		IsStruct:  true,
	}
	cols.arrays[name] = &ArrayEntry{
		Size:    size,
		Offsets: offsets,
		From:    children,
	}

	// Build the Struct array from children
	var childCols []arrow.Array
	var childNames []string
	for _, childName := range children {
		if childArr, ok := cols.arrays[childName]; ok {
			childCols = append(childCols, childArr.Array)
			simpleName := strings.TrimPrefix(childName, name+"_")
			childNames = append(childNames, simpleName)
		}
	}
	if len(childCols) > 0 {
		structArr, err := array.NewStructArray(childCols, childNames)
		if err == nil {
			cols.arrays[name].Array = structArr
		}
	}

	found := false
	for _, o := range cols.order {
		if o == name {
			found = true
			break
		}
	}
	if !found {
		cols.order = append(cols.order, name)
	}
}

// GetArray busca o arrow.Array de uma coluna.
func (r *ColRegistry) GetArray(stepName string, dir StepDirection, name string) (arrow.Array, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cols, exists := r.getCols(stepName, dir)
	if !exists {
		return nil, false
	}
	entry, exists := cols.arrays[name]
	if !exists {
		return nil, false
	}
	return entry.Array, true
}

// SetArray atualiza o arrow.Array de uma coluna e propaga updates se for um struct.
func (r *ColRegistry) SetArray(stepName string, dir StepDirection, name string, arr arrow.Array) {
	r.mu.Lock()
	defer r.mu.Unlock()

	cols, exists := r.getCols(stepName, dir)
	if !exists {
		return
	}

	if entry, exists := cols.arrays[name]; exists {
		entry.Array = arr
		if len(entry.From) > 0 && arr != nil {
			if structArr, ok := arr.(*array.Struct); ok {
				for idx, childName := range entry.From {
					if idx < structArr.NumField() {
						childFieldArr := structArr.Field(idx)
						if childEntry, exists := cols.arrays[childName]; exists {
							childEntry.Array = childFieldArr
						} else {
							cols.arrays[childName] = &ArrayEntry{
								Array: childFieldArr,
							}
						}
					}
				}
			}
		}
	} else {
		cols.arrays[name] = &ArrayEntry{
			Array: arr,
		}
	}
}

// GetEntry busca o ArrayEntry completo.
func (r *ColRegistry) GetEntry(stepName string, dir StepDirection, name string) (*ArrayEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cols, exists := r.getCols(stepName, dir)
	if !exists {
		return nil, false
	}
	entry, exists := cols.arrays[name]
	return entry, exists
}

// GetMetadata busca metadata de uma coluna.
func (r *ColRegistry) GetMetadata(stepName string, dir StepDirection, name string) (*MetadataEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cols, exists := r.getCols(stepName, dir)
	if !exists {
		return nil, false
	}
	meta, exists := cols.metadata[name]
	return meta, exists
}

// Names retorna nomes de colunas de um step/direction na ordem.
func (r *ColRegistry) Names(stepName string, dir StepDirection) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cols, exists := r.getCols(stepName, dir)
	if !exists {
		return nil
	}
	res := make([]string, len(cols.order))
	copy(res, cols.order)
	return res
}

// MapOutputToInput mapeia output de stepA para input de stepB por nome.
func (r *ColRegistry) MapOutputToInput(fromStep string, toStep string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	fromEntry, existsFrom := r.steps[fromStep]
	toEntry, existsTo := r.steps[toStep]
	if !existsFrom || !existsTo {
		return
	}

	for name, outMeta := range fromEntry.Output.metadata {
		toEntry.Input.metadata[name] = &MetadataEntry{
			Name:      outMeta.Name,
			ArrowType: outMeta.ArrowType,
			IsStruct:  outMeta.IsStruct,
		}

		if outArr, ok := fromEntry.Output.arrays[name]; ok {
			toEntry.Input.arrays[name] = &ArrayEntry{
				Array:   outArr.Array,
				Size:    outArr.Size,
				Offsets: outArr.Offsets,
				From:    outArr.From,
			}
		}

		found := false
		for _, o := range toEntry.Input.order {
			if o == name {
				found = true
				break
			}
		}
		if !found {
			toEntry.Input.order = append(toEntry.Input.order, name)
		}
	}
}
