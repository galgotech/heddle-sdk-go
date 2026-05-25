package execute

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"

	"github.com/galgotech/heddle-sdk-go/internal/accessor"
	"github.com/galgotech/heddle-sdk-go/internal/registry"
	internalschema "github.com/galgotech/heddle-sdk-go/internal/schema"
	"github.com/galgotech/heddle-sdk-go/schema"
)

// Executor defines a generic interface for executing a step
type Executor interface {
	Execute(ctx context.Context, input any) (any, error)
}

// LocalInput represents parameters required for direct/local execution.
type LocalInput struct {
	StepName   string
	ConfigJSON any
	Input      any
}

// StepReference encapsulates the data and column handles of a step's output.
type StepReference struct {
	Data    any
	Columns map[string]arrow.Array
}

// PackData inspects the input struct and extracts ColAccessors into a StepReference.
func PackData(input any) *StepReference {
	if input == nil {
		return &StepReference{
			Columns: make(map[string]arrow.Array),
		}
	}
	if ref, ok := input.(*StepReference); ok {
		return ref
	}

	columns := make(map[string]arrow.Array)

	vVal := reflect.ValueOf(input)
	if vVal.Kind() == reflect.Pointer {
		if vVal.IsNil() {
			return &StepReference{
				Columns: columns,
			}
		}
		vVal = vVal.Elem()
	}

	if vVal.Kind() == reflect.Struct {
		t := vVal.Type()
		for i := 0; i < vVal.NumField(); i++ {
			fValue := vVal.Field(i)
			name := t.Field(i).Name

			fType := fValue.Type()
			if fType.Kind() == reflect.Pointer {
				fType = fType.Elem()
			}
			if strings.HasPrefix(fType.Name(), "Col") {
				if colAcc, ok := GetColAccessor(fValue); ok {
					token := accessor.Token{}
					arr := colAcc.GetArrowArray(token)
					if arr != nil && !reflect.ValueOf(arr).IsNil() {
						columns[name] = arr
					}
				}
			}
		}
	}

	return &StepReference{
		Data:    input,
		Columns: columns,
	}
}

func UnpackData(ref any) any {
	if ref == nil {
		return nil
	}
	if r, ok := ref.(*StepReference); ok {
		return r.Data
	}
	return ref
}

// GetColAccessor retrieves the ColAccessor interface from a reflection value.
func GetColAccessor(fValue reflect.Value) (schema.ColAccessor, bool) {
	if !fValue.IsValid() {
		return nil, false
	}
	if fValue.Kind() == reflect.Pointer {
		if fValue.IsNil() {
			return nil, false
		}
		if colAcc, ok := fValue.Interface().(schema.ColAccessor); ok {
			return colAcc, true
		}
	}
	if fValue.CanAddr() {
		if colAcc, ok := fValue.Addr().Interface().(schema.ColAccessor); ok {
			return colAcc, true
		}
	}
	return nil, false
}

func registerArrowArrayInRegistry(r *schema.ColRegistry, stepName string, dir schema.StepDirection, name string, arr arrow.Array) {
	if arr == nil || reflect.ValueOf(arr).IsNil() {
		return
	}
	if structArr, ok := arr.(*array.Struct); ok {
		var children []string
		for idx := 0; idx < structArr.NumField(); idx++ {
			childField := structArr.DataType().(*arrow.StructType).Field(idx)
			childName := strings.ToLower(childField.Name)
			childKey := name + "_" + childName
			children = append(children, childKey)

			registerArrowArrayInRegistry(r, stepName, dir, childKey, structArr.Field(idx))
		}

		offsets := make([]int, structArr.Len()+1)
		for i := 0; i <= structArr.Len(); i++ {
			offsets[i] = i
		}
		r.RegisterStruct(stepName, dir, name, children, structArr.Len(), offsets)
		r.SetArray(stepName, dir, name, structArr)
	} else {
		arrowType := "utf8"
		switch arr.(type) {
		case *array.Int8:
			arrowType = "int8"
		case *array.Int16:
			arrowType = "int16"
		case *array.Int32:
			arrowType = "int32"
		case *array.Int64:
			arrowType = "int64"
		case *array.Uint8:
			arrowType = "uint8"
		case *array.Uint16:
			arrowType = "uint16"
		case *array.Uint32:
			arrowType = "uint32"
		case *array.Uint64:
			arrowType = "uint64"
		case *array.Float32:
			arrowType = "float32"
		case *array.Float64:
			arrowType = "float64"
		case *array.Boolean:
			arrowType = "bool"
		}
		r.RegisterLeaf(stepName, dir, name, arrowType, arr)
	}
}

// Bind maps Arrow Table columns to Go struct fields.
func Bind(reflectValue reflect.Value, fieldIndices []int, columns map[string]arrow.Array) error {
	var numRows int = -1
	for _, arr := range columns {
		if arr != nil && !reflect.ValueOf(arr).IsNil() {
			if numRows == -1 {
				numRows = arr.Len()
			} else if numRows != arr.Len() {
				return fmt.Errorf("inconsistent column lengths")
			}
		}
	}
	if numRows == -1 {
		numRows = 0
	}

	if reflectValue.Kind() == reflect.Pointer {
		reflectValue = reflectValue.Elem()
	}

	r := schema.NewColRegistry()
	r.RegisterStep("_temp")

	// Register all arrays from columns map at registry time!
	// Struct arrays will be recursively decomposed and populated.
	for k, v := range columns {
		registerArrowArrayInRegistry(r, "_temp", schema.Input, strings.ToLower(k), v)
	}

	t := reflectValue.Type()
	_, err := registerInputFields(r, "_temp", "", t)
	if err != nil {
		return err
	}

	for _, i := range fieldIndices {
		fField := t.Field(i)
		fieldName := strings.ToLower(fField.Name)

		fValue := reflectValue.Field(i)
		if fValue.Kind() == reflect.Pointer {
			if fValue.IsNil() {
				newCol := reflect.New(fValue.Type().Elem())
				fValue.Set(newCol)
			}
		}

		if binder, ok := fValue.Interface().(schema.ColRegistryBinder); ok {
			binder.BindRegistry(accessor.Token{}, r, "_temp", schema.Input, fieldName)
		}
	}

	return nil
}

func registerInputFields(r *schema.ColRegistry, stepName string, prefix string, t reflect.Type) ([]string, error) {
	var children []string
	for fField := range t.Fields() {
		if fField.Anonymous || !fField.IsExported() {
			continue
		}
		fieldName := strings.ToLower(fField.Name)
		var colName string
		if prefix == "" {
			colName = fieldName
		} else {
			colName = prefix + "_" + fieldName
		}

		isColStruct := strings.Contains(fField.Type.String(), "ColStruct")
		if isColStruct {
			colStructType := fField.Type.Elem()
			dummyField, ok := colStructType.FieldByName("dummy")
			if !ok {
				return nil, fmt.Errorf("ColStruct has no dummy field")
			}
			subStructType := dummyField.Type.Elem()

			subChildren, err := registerInputFields(r, stepName, colName, subStructType)
			if err != nil {
				return nil, err
			}

			// If this struct column was not registered yet, register it as flat 1-to-1 view
			if _, ok := r.GetEntry(stepName, schema.Input, colName); !ok {
				var totalSize int
				if len(subChildren) > 0 {
					if firstChildArr, ok := r.GetArray(stepName, schema.Input, subChildren[0]); ok {
						totalSize = firstChildArr.Len()
					}
				}

				offsets := make([]int, totalSize+1)
				for i := 0; i <= totalSize; i++ {
					offsets[i] = i
				}

				r.RegisterStruct(stepName, schema.Input, colName, subChildren, totalSize, offsets)
			}
			children = append(children, colName)
		} else {
			// Leaf: verify that it is fully registered in the registry
			if _, ok := r.GetEntry(stepName, schema.Input, colName); !ok {
				return nil, fmt.Errorf("column %q is required but missing", colName)
			}
			children = append(children, colName)
		}
	}
	return children, nil
}

func ExtractOutputArrays(outVal any, step registry.StepRegistration) map[string]arrow.Array {
	if outVal == nil || reflect.ValueOf(outVal).IsNil() {
		return nil
	}

	columns := make(map[string]arrow.Array)

	vVal := reflect.ValueOf(outVal)
	if vVal.Kind() == reflect.Pointer {
		vVal = vVal.Elem()
	}

	if step.OutputType == reflect.TypeFor[*schema.Any]() {
		if df, ok := outVal.(*schema.Any); ok && df != nil {
			for _, name := range df.Columns() {
				if colData, ok := df.Get(name); ok {
					columns[name] = colData
				}
			}
		}
	} else {
		t := vVal.Type()
		for _, i := range step.OutputFieldsIndex {
			fValue := vVal.Field(i)
			name := t.Field(i).Name

			fType := fValue.Type()
			if fType.Kind() == reflect.Pointer {
				fType = fType.Elem()
			}
			if internalschema.IsCol(fType) {
				if colAcc, ok := GetColAccessor(fValue); ok {
					token := accessor.Token{}
					arr := colAcc.GetArrowArray(token)
					if arr != nil && !reflect.ValueOf(arr).IsNil() {
						columns[name] = arr
					}
				}
			}
		}
	}

	return columns
}
