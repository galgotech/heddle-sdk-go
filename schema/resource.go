package schema

import "context"

// Resource represents an external dependency or stateful object
// (e.g., database connection pool, API client) initialized by the Heddle runtime.
type Resource interface {
	Init(ctx context.Context) error
	Close() error
}

type ResourceSchema[T Resource] struct {
	resource T
}

func (r ResourceSchema[T]) Get() T {
	return r.resource
}

func (r ResourceSchema[T]) IsResource() bool {
	return true
}

func (r *ResourceSchema[T]) SetResource(val any) {
	r.resource = val.(T)
}
