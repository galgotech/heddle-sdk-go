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

	inputFieldsIndex  []int
	outputFieldsIndex []int
	GroupInstance     reflect.Value
}

func (s *StepRegistration) newInput() reflect.Value {
	inType := s.InputType
	if inType.Kind() == reflect.Pointer {
		inType = inType.Elem()
	}
	outType := s.OutputType
	if outType.Kind() == reflect.Pointer {
		outType = outType.Elem()
	}

	inputVal := reflect.New(inType)

	s.initFrame(inputVal, s.inputFieldsIndex)

	return inputVal
}

func (s *StepRegistration) initFrame(val reflect.Value, indices []int) {
	v := val.Elem()

	for _, i := range indices {
		f := v.Field(i)
		if f.Type().Kind() == reflect.Pointer {
			f.Set(reflect.New(f.Type().Elem()))
		}
	}
}
