package plugin

import (
	"reflect"

	"github.com/galgotech/heddle-lang/pkg/schema"
)

// StepRegistration stores the execution contract for a Heddle Step.
// It captures the function signature, inferred JSON schemas for configuration,
// and the mapping between Arrow schemas and Go struct types.
type StepRegistration struct {
	Name         string
	ConfigSchema *schema.ResourceAndConfigSchema // JSON schema inferred from the configuration struct for DSL-side validation
	ConfigType   reflect.Type
	InputType    reflect.Type
	OutputType   reflect.Type
	Func         reflect.Value
	InputSchema  *schema.FrameSchema
	OutputSchema *schema.FrameSchema

	// DEV: lsp
	Documentation string
	SourceCode    string
	SourceFile    string
	SourceLine    int

	// Pre-calculated indices for optimized initialization
	inputHeddleFrameIndex  int
	outputHeddleFrameIndex int
	inputFieldsIndex       []int
	outputFieldsIndex      []int
}

// newInputOutput initializes both input and output frames using pre-calculated indices.
// It pre-populates the HeddleFrame schema and initializes any nil field pointers.
func (s *StepRegistration) newInputOutput() (reflect.Value, reflect.Value) {
	inputVal := reflect.New(s.InputType.Elem())
	outputVal := reflect.New(s.OutputType.Elem())

	s.initFrame(inputVal, s.inputFieldsIndex)
	s.initFrame(outputVal, s.outputFieldsIndex)

	return inputVal, outputVal
}

func (s *StepRegistration) initFrame(val reflect.Value, indices []int) {
	v := val.Elem()

	// Initialize all pointer fields
	for _, i := range indices {
		f := v.Field(i)
		f.Set(reflect.New(f.Type().Elem()))
	}
}
