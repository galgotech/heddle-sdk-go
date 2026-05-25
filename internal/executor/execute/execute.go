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
	IDs     map[string]arrow.Array
}

// PackData inspects the input struct and extracts ColAccessors into a StepReference.
func PackData(input any) *StepReference {
	if input == nil {
		return &StepReference{
			Columns: make(map[string]arrow.Array),
			IDs:     make(map[string]arrow.Array),
		}
	}
	if ref, ok := input.(*StepReference); ok {
		return ref
	}

	columns := make(map[string]arrow.Array)
	ids := make(map[string]arrow.Array)

	vVal := reflect.ValueOf(input)
	if vVal.Kind() == reflect.Pointer {
		if vVal.IsNil() {
			return &StepReference{
				Columns: columns,
				IDs:     ids,
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
					idArr := colAcc.GetIDs(token)
					if idArr != nil && !reflect.ValueOf(idArr).IsNil() {
						ids[name] = idArr
					}
				}
			}
		}
	}

	return &StepReference{
		Data:    input,
		Columns: columns,
		IDs:     ids,
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

// Bind maps Arrow Table columns to Go struct fields.
func Bind(reflectValue reflect.Value, fieldIndices []int, columns map[string]arrow.Array) error {
	var numRows int = -1
	for _, arr := range columns {
		if numRows == -1 {
			numRows = arr.Len()
		} else if numRows != arr.Len() {
			return fmt.Errorf("inconsistent column lengths")
		}
	}
	if numRows == -1 {
		numRows = 0
	}

	if reflectValue.Kind() == reflect.Pointer {
		reflectValue = reflectValue.Elem()
	}
	reflectType := reflectValue.Type()
	for _, i := range fieldIndices {
		fValue := reflectValue.Field(i)
		name := reflectType.Field(i).Name
		arr := columns[name]
		if arr == nil {
			return fmt.Errorf("column %q is required but missing", name)
		}
		idArr, ok := columns[name+"_id"]
		if !ok {
			return fmt.Errorf("column %q is required but missing", name+"_id")
		}

		var colAcc schema.ColAccessor
		if fValue.Kind() == reflect.Pointer {
			if fValue.IsNil() {
				newCol := reflect.New(fValue.Type().Elem())
				fValue.Set(newCol)
			}
			colAcc, ok = fValue.Interface().(schema.ColAccessor)
		} else {
			colAcc, ok = fValue.Addr().Interface().(schema.ColAccessor)
		}
		if !ok || colAcc == nil {
			return fmt.Errorf("%s column %q is not a ColAccessor", reflectType.Field(i).Name, name)
		}

		arr.Retain()
		idArr.Retain()
		colAcc.SetData(accessor.Token{}, arr, idArr.(*array.Int64))

	}

	return nil
}

func ExtractOutputArrays(outVal any, step registry.StepRegistration) (map[string]arrow.Array, map[string]arrow.Array) {
	if outVal == nil || reflect.ValueOf(outVal).IsNil() {
		return nil, nil
	}

	columns := make(map[string]arrow.Array)
	ids := make(map[string]arrow.Array)

	vVal := reflect.ValueOf(outVal)
	if vVal.Kind() == reflect.Pointer {
		vVal = vVal.Elem()
	}

	if step.OutputType == reflect.TypeFor[*schema.Any]() {
		if df, ok := outVal.(*schema.Any); ok && df != nil {
			for _, name := range df.Columns() {
				if colData, ok := df.Get(name); ok {
					columns[name] = colData

					// Use reflection to extract idsArray from Any
					dfVal := reflect.ValueOf(df).Elem()
					idsArrayField := dfVal.FieldByName("idsArray")
					if idsArrayField.IsValid() && idsArrayField.Kind() == reflect.Map {
						mapVal := idsArrayField.MapIndex(reflect.ValueOf(name))
						if mapVal.IsValid() && !mapVal.IsNil() {
							if idArr, ok := mapVal.Interface().(arrow.Array); ok {
								ids[name] = idArr
							}
						}
					}
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
					idArr := colAcc.GetIDs(token)
					if idArr != nil && !reflect.ValueOf(idArr).IsNil() {
						ids[name] = idArr
					}
				}
			}
		}
	}

	return columns, ids
}
