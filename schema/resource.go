package schema

import "context"

// Resource represents an external dependency or stateful object
// (e.g., database connection pool, API client) initialized by the Heddle runtime.
type Resource interface {
	Start(ctx context.Context) error
	Close() error
}
