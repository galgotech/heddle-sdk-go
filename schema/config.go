package schema

import "context"

// TypeResolver is an optional interface that configurations can implement
// to provide dynamic input and output schemas based on their values.
type TypeResolver interface {
	ResolveTypeInput(ctx context.Context, config any, stepName string) ([]ColSchema, error)
	ResolveTypeOutput(ctx context.Context, config any, stepName string) ([]ColSchema, error)
}
