package internal

import (
	"reflect"

	"github.com/galgotech/heddle-lang/pkg/schema"
)

type StepRegistration struct {
	Name         string
	ConfigSchema *schema.ResourceAndConfigSchema
	ConfigType   reflect.Type
	InputType    reflect.Type
	OutputType   reflect.Type
	Func         reflect.Value
	InputSchema  *schema.FrameSchema
	OutputSchema *schema.FrameSchema

	Documentation string
	SourceCode    string
	SourceFile    string
	SourceLine    int

	inputHeddleFrameIndex  int
	outputHeddleFrameIndex int
	inputFieldsIndex       []int
	outputFieldsIndex      []int
}

func (s *StepRegistration) newInputOutput() (reflect.Value, reflect.Value) {
	inputVal := reflect.New(s.InputType.Elem())
	outputVal := reflect.New(s.OutputType.Elem())

	s.initFrame(inputVal, s.inputFieldsIndex)
	s.initFrame(outputVal, s.outputFieldsIndex)

	return inputVal, outputVal
}

func (s *StepRegistration) initFrame(val reflect.Value, indices []int) {
	v := val.Elem()

	for _, i := range indices {
		f := v.Field(i)
		f.Set(reflect.New(f.Type().Elem()))
	}
}
