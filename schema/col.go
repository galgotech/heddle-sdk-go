package schema

import (
	"context"

	"github.com/bwmarrin/snowflake"
)

var colIDNode *snowflake.Node

// TypeResolver is an optional interface that configurations can implement
// to provide dynamic input and output schemas based on their values.
type TypeResolver interface {
	ResolveTypeInput(ctx context.Context, config any, stepName string) ([]ColSchema, error)
	ResolveTypeOutput(ctx context.Context, config any, stepName string) ([]ColSchema, error)
}

type Col[T any] struct {
	// Cache de acesso rápido Zero-Copy
	data  []T
	ids   []int64
	dirty []bool
}

func (c Col[T]) Len() int {
	return len(c.data)
}

func (c Col[T]) Value(i int) T {
	return c.data[i]
}

func (c *Col[T]) Delete(i int) {
	if i >= 0 && i < len(c.dirty) {
		c.dirty[i] = true
	}
}

func (c Col[T]) IsDeleted(i int) bool {
	if i >= 0 && i < len(c.dirty) {
		return c.dirty[i]
	}
	return false
}

func (c Col[T]) ID(i int) int64 {
	if i >= 0 && i < len(c.ids) {
		return c.ids[i]
	}
	return 0
}

func NewCol[T any](data []T) Col[T] {
	ids := make([]int64, len(data))
	for i := range ids {
		ids[i] = colIDNode.Generate().Int64()
	}
	return Col[T]{
		data:  data,
		ids:   ids,
		dirty: make([]bool, len(data)),
	}
}

// FrameSchema defines the structure of a HeddleFrame.
type ColSchema struct {
	Type string
	Name string
}

type Any struct {
	columns map[string]any
}

func (a *Any) Set(name string, value any) {
	if a.columns == nil {
		a.columns = make(map[string]any)
	}
	a.columns[name] = value
}

func (a *Any) Get(name string) (any, bool) {
	value, ok := a.columns[name]
	return value, ok
}

func (a *Any) Columns() map[string]any {
	return a.columns
}

type Void struct{}

func init() {
	var err error
	colIDNode, err = snowflake.NewNode(1)
	if err != nil {
		panic(err)
	}
}
