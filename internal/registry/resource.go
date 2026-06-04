package registry

import (
	"context"

	"github.com/galgotech/heddle-lang/pkg/schema"
	pluginschema "github.com/galgotech/heddle-sdk-go/schema"
)

type ResourceRegistration struct {
	Name        string
	FieldSchema schema.FieldSchema

	Init func(ctx context.Context, configJSON string) (pluginschema.Resource, error)

	Documentation string
	SourceCode    string
	SourceFile    string
	SourceLine    int
}
