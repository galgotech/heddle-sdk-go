package schema

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
	"github.com/apache/arrow/go/v18/arrow/memory"
	"github.com/galgotech/heddle-lang/pkg/logger"
	"go.uber.org/zap"

	"github.com/galgotech/heddle-sdk-go/internal/accessor"
)

type ColStruct[T any] struct {
	arr    arrow.Array
	arrIds *array.Int64
	dummy  *T
}

func (c *ColStruct[T]) GetArrowArray(accessor.Token) arrow.Array {
	return c.arr
}

func (c *ColStruct[T]) GetIDs(accessor.Token) *array.Int64 {
	return c.arrIds
}

func (c *ColStruct[T]) SetData(token accessor.Token, arr arrow.Array, arrIds *array.Int64) {
	c.arr = arr
	c.arrIds = arrIds
}

func (c *ColStruct[T]) Len() int {
	return c.arr.Len()
}

func (c *ColStruct[T]) Value(i int) *T {
	if i < 0 || i >= c.Len() {
		panic(fmt.Sprintf("index %d out of bounds (len: %d)", i, c.Len()))
	}

	structArr, ok := c.arr.(*array.Struct)
	if !ok {
		panic("ColStruct arr is not *array.Struct")
	}

	tType := reflect.TypeFor[T]()
	structVal := reflect.New(tType)
	structElem := structVal.Elem()

	fieldIdx := 0
	for j := 0; j < tType.NumField(); j++ {
		fType := tType.Field(j)
		if fType.Anonymous || !fType.IsExported() {
			continue
		}

		arrowFieldArr := structArr.Field(fieldIdx)
		fieldIdx++

		// slice the field array to only contain element i
		slicedArr := array.NewSlice(arrowFieldArr, int64(i), int64(i+1))

		// Create a new column instance for this field
		colPtrVal := reflect.New(fType.Type.Elem())
		colAcc, ok := colPtrVal.Interface().(ColAccessor)
		if !ok {
			panic(fmt.Sprintf("field %s of type %s does not implement ColAccessor", fType.Name, fType.Type))
		}

		// Set the data for the new column instance
		colAcc.SetData(accessor.Token{}, slicedArr, newIds(1))

		// Set the field value in the struct
		structElem.Field(j).Set(colPtrVal)
	}

	return structVal.Interface().(*T)
}

func NewColStruct[T any](data []*T) *ColStruct[T] {
	structType := reflect.TypeFor[*T]()
	indexReference, err := getStructArrowDataType(structType)
	if err != nil {
		panic(fmt.Sprintf("NewColStruct: failed to get Arrow DataType for %s: %v", structType.Name(), err))
	}

	dataVal := reflect.ValueOf(data)
	arr, err := builderListStruct(indexReference, structType, dataVal)
	if err != nil {
		panic(fmt.Sprintf("NewColStruct: failed to build struct array: %v", err))
	}

	structArr, ok := arr.(*array.Struct)
	if !ok {
		panic("NewColStruct: expected *array.Struct from builderListStruct")
	}

	return &ColStruct[T]{
		arr:    structArr,
		arrIds: newIds(structArr.Len()),
	}
}

func builderStruct(indexReference []indexReference, structType reflect.Type, dataVal reflect.Value) (*array.Struct, error) {
	if structType.Kind() == reflect.Pointer {
		structType = structType.Elem()
	}
	if dataVal.Kind() == reflect.Pointer || dataVal.Kind() == reflect.Interface {
		dataVal = dataVal.Elem()
	}

	fieldNames := []string{}
	dataArray := []arrow.Array{}
	for i := range indexReference {
		fieldType := structType.Field(i)
		if fieldType.Anonymous || !fieldType.IsExported() {
			continue
		}
		fieldNames = append(fieldNames, fieldType.Name)

		fieldVal := dataVal.Field(i)
		if fieldVal.IsNil() {
			return nil, fmt.Errorf("field %s is nil", fieldType.Name)
		}

		colAccessor, ok := fieldVal.Interface().(ColAccessor)
		if !ok {
			return nil, fmt.Errorf("field %s of type %s does not implement ColAccessor", fieldType.Name, fieldVal.Type())
		}
		arr := colAccessor.GetArrowArray(accessor.Token{})
		dataArray = append(dataArray, arr)
	}

	structArr, err := array.NewStructArray(dataArray, fieldNames)
	if err != nil {
		logger.L().Error("failed to build struct array", zap.Error(err))
		return nil, err
	}

	return structArr, nil
}

func builderListStruct(indexReference []indexReference, structType reflect.Type, dataVal reflect.Value) (arrow.Array, error) {
	if dataVal.Kind() != reflect.Slice {
		return nil, fmt.Errorf("expected slice, got %s", dataVal.Kind())
	}

	dataArray := make([]arrow.Array, dataVal.Len())
	for i := 0; i < dataVal.Len(); i++ {
		structVal := dataVal.Index(i)
		structArr, err := builderStruct(indexReference, structType, structVal)
		if err != nil {
			return nil, err
		}
		dataArray[i] = structArr
	}

	mem := memory.DefaultAllocator
	listArr, err := array.Concatenate(dataArray, mem)
	if err != nil {
		logger.L().Error("failed to build list array", zap.Error(err))
		return nil, err
	}
	return listArr, err
}

type indexReference struct {
	child []indexReference
}

func getStructArrowDataType(t reflect.Type) ([]indexReference, error) {
	if t.Kind() != reflect.Pointer {
		return nil, fmt.Errorf("expected pointer to struct, got %s", t.Kind())
	}
	if t.Elem().Kind() != reflect.Struct {
		return nil, fmt.Errorf("expected struct type, got %s", t.Elem().Kind())
	}

	t = t.Elem()
	indexMap := make([]indexReference, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.Anonymous || !field.IsExported() {
			continue
		}
		child, err := getArrowDataType(field.Type)
		if err != nil {
			return nil, err
		}
		indexMap[i] = indexReference{child: child}
	}
	return indexMap, nil
}

func getArrowDataType(t reflect.Type) ([]indexReference, error) {
	if t.Kind() != reflect.Pointer {
		return nil, fmt.Errorf("expected pointer to struct, got %s", t.Kind())
	}

	t = t.Elem()
	typeRepresentation := t.String()

	if strings.Contains(typeRepresentation, "ColStruct") {
		dummyField, ok := t.FieldByName("dummy")
		if !ok {
			return nil, fmt.Errorf("ColStruct type %s does not have dummy field", t.Name())
		}
		subT := dummyField.Type
		return getStructArrowDataType(subT)
	}

	return []indexReference{}, nil
}
