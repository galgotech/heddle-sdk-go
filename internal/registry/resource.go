package registry

import (
	"reflect"

	"github.com/galgotech/heddle-lang/pkg/schema"
)

type ResourceRegistration struct {
	Name         string
	FieldSchema  schema.FieldSchema
	ResourceType reflect.Type // schema.Resource

	Documentation string
	SourceCode    string
	SourceFile    string
	SourceLine    int
}
