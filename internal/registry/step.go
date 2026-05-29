package registry

import (
	"reflect"

	"github.com/galgotech/heddle-lang/pkg/schema"
)

type StepRegistration struct {
	Name         string
	StructType   reflect.Type // pointer to struct
	ConfigSchema schema.FieldSchema
	ConfigType   reflect.Type  // struct
	InputType    reflect.Type  // pointer to struct
	OutputType   reflect.Type  // pointer to struct
	Func         reflect.Value // method
	InputSchema  schema.FrameSchema
	OutputSchema schema.FrameSchema

	Documentation string
	SourceCode    string
	SourceFile    string
	SourceLine    int

	InputFieldsIndex  []int
	OutputFieldsIndex []int
}
