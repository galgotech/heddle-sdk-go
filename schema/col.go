package schema

type Col[T any] struct {
	// Array interno do Arrow oculto (sem exportar)
	// arrowArray arrow.Array

	// Cache de acesso rápido Zero-Copy
	Data []T
}

func NewCol[T any](data []T) Col[T] {
	return Col[T]{Data: data}
}

func (c Col[T]) Len() int {
	return len(c.Data)
}

func (c Col[T]) Value(i int) T {
	return c.Data[i]
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
