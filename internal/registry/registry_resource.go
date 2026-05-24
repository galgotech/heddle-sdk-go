package registry

import (
	"reflect"

	"github.com/galgotech/heddle-lang/pkg/schema"
)

type ResourceRegistration struct {
	Name         string
	FieldSchema  schema.FieldSchema
	ResourceType reflect.Type // struct schema.Resource[T schema.ResourceInterface]

	Documentation string
	SourceCode    string
	SourceFile    string
	SourceLine    int
}
