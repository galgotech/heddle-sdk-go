package schema

import "context"

// ResourceDefinition represents an external dependency or stateful object
// (e.g., database connection pool, API client) initialized by the Heddle runtime.
type ResourceDefinition interface {
	Init(ctx context.Context) error
	Close() error
}

type Resource[T ResourceDefinition] struct {
	resource T
}

func (r Resource[T]) Get() T {
	return r.resource
}

func (r Resource[T]) IsResource() bool {
	return true
}
