package registry

import (
	"context"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/galgotech/heddle-lang/pkg/schema"
)

type StepRegistration struct {
	Name         string
	ConfigSchema schema.FieldSchema
	InputSchema  schema.FrameSchema
	OutputSchema schema.FrameSchema

	Invoke func(ctx context.Context, configJSON string, inColumns map[string]arrow.Array) (map[string]arrow.Array, error)

	Documentation string
	SourceCode    string
	SourceFile    string
	SourceLine    int
}
