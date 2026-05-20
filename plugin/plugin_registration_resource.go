package plugin

import (
	"reflect"

	"github.com/galgotech/heddle-lang/pkg/schema"
)

// resourceRegistration maintains metadata and the execution handle for a Heddle Resource.
// It allows the plugin to expose custom infrastructure or stateful components to the Heddle DSL.
type resourceRegistration struct {
	Name           string
	ResourceSchema *schema.ResourceAndConfigSchema
	ResourceType   reflect.Type

	// DEV: lsp
	Documentation string
	SourceCode    string
	SourceFile    string
	SourceLine    int
}
