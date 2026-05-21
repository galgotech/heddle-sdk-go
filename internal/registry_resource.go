package internal

import (
	"reflect"

	"github.com/galgotech/heddle-lang/pkg/schema"
)

type resourceRegistration struct {
	Name           string
	ResourceSchema *schema.ResourceAndConfigSchema
	ResourceType   reflect.Type

	Documentation string
	SourceCode    string
	SourceFile    string
	SourceLine    int
}
