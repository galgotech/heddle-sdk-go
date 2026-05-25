package schema

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
	"github.com/apache/arrow/go/v18/arrow/memory"

	"github.com/galgotech/heddle-sdk-go/internal/accessor"
)

type ColStruct[T any] struct {
	registry  *ColRegistry
	stepName  string
	direction StepDirection
	colName   string
	dummy     *T
}

func (c *ColStruct[T]) BindRegistry(token accessor.Token, registry *ColRegistry, stepName string, dir StepDirection, colName string) {
	c.registry = registry
	c.stepName = stepName
	c.direction = dir
	c.colName = colName
}

func (c *ColStruct[T]) GetArrowArray(accessor.Token) arrow.Array {
	if c.registry == nil {
		return nil
	}
	arr, _ := c.registry.GetArray(c.stepName, c.direction, c.colName)
	return arr
}

func (c *ColStruct[T]) SetData(token accessor.Token, arr arrow.Array) {
	if c.registry == nil {
		c.registry = NewColRegistry()
		c.registry.RegisterStep("_temp")
		c.stepName = "_temp"
		c.direction = Input
		c.colName = "col"
	}
	c.registry.SetArray(c.stepName, c.direction, c.colName, arr)
}

func (c *ColStruct[T]) Len() int {
	if c.registry == nil {
		return 0
	}
	arr, ok := c.registry.GetArray(c.stepName, c.direction, c.colName)
	if !ok || arr == nil {
		return 0
	}
	return arr.Len()
}

func (c *ColStruct[T]) Value(i int) *T {
	if i < 0 || i >= c.Len() {
		panic(fmt.Sprintf("index %d out of bounds (len: %d)", i, c.Len()))
	}

	entry, ok := c.registry.GetEntry(c.stepName, c.direction, c.colName)
	if !ok {
		panic(fmt.Sprintf("ColStruct column %s not found in registry", c.colName))
	}

	var start, end int
	if len(entry.Offsets) > 0 {
		start = entry.Offsets[i]
		end = entry.Offsets[i+1]
	} else {
		start = i
		end = i + 1
	}

	localReg := NewColRegistry()
	localReg.RegisterStep("_temp")

	c.sliceChildrenToLocal(localReg, "_temp", Input, c.colName, c.colName, start, end)

	tType := reflect.TypeFor[T]()
	structVal := reflect.New(tType)
	structElem := structVal.Elem()

	for j := 0; j < tType.NumField(); j++ {
		fType := tType.Field(j)
		if fType.Anonymous || !fType.IsExported() {
			continue
		}

		fieldName := strings.ToLower(fType.Name)
		childColName := fieldName

		colPtrVal := reflect.New(fType.Type.Elem())

		if binder, ok := colPtrVal.Interface().(ColRegistryBinder); ok {
			binder.BindRegistry(accessor.Token{}, localReg, "_temp", Input, childColName)
		}

		structElem.Field(j).Set(colPtrVal)
	}

	return structVal.Interface().(*T)
}

func (c *ColStruct[T]) sliceChildrenToLocal(localReg *ColRegistry, targetStep string, targetDir StepDirection, rootName string, parentName string, start, end int) {
	entry, ok := c.registry.GetEntry(c.stepName, c.direction, parentName)
	if !ok {
		return
	}

	for _, childName := range entry.From {
		childEntry, ok := c.registry.GetEntry(c.stepName, c.direction, childName)
		if !ok {
			continue
		}

		localChildName := strings.TrimPrefix(childName, rootName+"_")

		if len(childEntry.From) > 0 {
			var childStart, childEnd int
			if len(childEntry.Offsets) > 0 {
				childStart = childEntry.Offsets[start]
				childEnd = childEntry.Offsets[end]
			} else {
				childStart = start
				childEnd = end
			}

			c.sliceChildrenToLocal(localReg, targetStep, targetDir, rootName, childName, childStart, childEnd)

			localOffsets := make([]int, end-start+1)
			localOffsets[0] = 0
			if len(childEntry.Offsets) > 0 {
				for i := 0; i < end-start; i++ {
					localOffsets[i+1] = localOffsets[i] + (childEntry.Offsets[start+i+1] - childEntry.Offsets[start+i])
				}
			} else {
				for i := 0; i < end-start; i++ {
					localOffsets[i+1] = localOffsets[i] + 1
				}
			}

			var localChildren []string
			for _, subChild := range childEntry.From {
				localChildren = append(localChildren, strings.TrimPrefix(subChild, rootName+"_"))
			}

			localReg.RegisterStruct(targetStep, targetDir, localChildName, localChildren, childEnd-childStart, localOffsets)
		} else {
			slicedArr := array.NewSlice(childEntry.Array, int64(start), int64(end))
			childMeta, _ := c.registry.GetMetadata(c.stepName, c.direction, childName)
			localReg.RegisterLeaf(targetStep, targetDir, localChildName, childMeta.ArrowType, slicedArr)
		}
	}
}

func NewColStruct[T any](data []*T) *ColStruct[T] {
	tType := reflect.TypeFor[T]()
	if tType.Kind() != reflect.Struct {
		panic("expected struct type")
	}

	for _, elem := range data {
		if elem == nil {
			panic("struct in slice is nil")
		}
		elemVal := reflect.ValueOf(elem).Elem()
		for j := 0; j < elemVal.NumField(); j++ {
			fField := elemVal.Type().Field(j)
			if fField.Anonymous || !fField.IsExported() {
				continue
			}
			if elemVal.Field(j).IsNil() {
				panic(fmt.Sprintf("field %s is nil", fField.Name))
			}
		}
	}

	r := NewColRegistry()
	r.RegisterStep("_temp")

	children, totalSize, _ := registerFieldsReflect(r, "_temp", Input, "col", tType, reflect.ValueOf(data))

	r.RegisterStruct("_temp", Input, "col", children, totalSize, nil)

	return &ColStruct[T]{
		registry:  r,
		stepName:  "_temp",
		direction: Input,
		colName:   "col",
	}
}

func getElementLen(elemVal reflect.Value) int {
	if elemVal.Kind() == reflect.Pointer {
		elemVal = elemVal.Elem()
	}
	for j := 0; j < elemVal.NumField(); j++ {
		fField := elemVal.Type().Field(j)
		if fField.Anonymous || !fField.IsExported() {
			continue
		}
		fieldVal := elemVal.Field(j)
		if fieldVal.IsNil() {
			continue
		}
		if colAcc, ok := fieldVal.Interface().(ColAccessor); ok {
			arr := colAcc.GetArrowArray(accessor.Token{})
			if arr != nil {
				return arr.Len()
			}
		}
	}
	return 1
}

func getArrowTypeString(t reflect.Type) string {
	s := t.String()
	if strings.Contains(s, "Int8") {
		return "int8"
	} else if strings.Contains(s, "Int16") {
		return "int16"
	} else if strings.Contains(s, "Int32") {
		return "int32"
	} else if strings.Contains(s, "Int64") {
		return "int64"
	} else if strings.Contains(s, "Uint8") {
		return "uint8"
	} else if strings.Contains(s, "Uint16") {
		return "uint16"
	} else if strings.Contains(s, "Uint32") {
		return "uint32"
	} else if strings.Contains(s, "Uint64") {
		return "uint64"
	} else if strings.Contains(s, "Float32") {
		return "float32"
	} else if strings.Contains(s, "Float64") {
		return "float64"
	} else if strings.Contains(s, "Boolean") {
		return "bool"
	} else if strings.Contains(s, "String") {
		return "utf8"
	}
	return "utf8"
}

func registerFieldsReflect(r *ColRegistry, stepName string, dir StepDirection, prefix string, structType reflect.Type, dataSlice reflect.Value) ([]string, int, []int) {
	N := dataSlice.Len()
	offsets := make([]int, N+1)
	offsets[0] = 0

	if N == 0 {
		return nil, 0, offsets
	}

	for i := 0; i < N; i++ {
		offsets[i+1] = offsets[i] + getElementLen(dataSlice.Index(i))
	}

	var children []string

	for j := 0; j < structType.NumField(); j++ {
		fField := structType.Field(j)
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
				panic("ColStruct must have dummy field")
			}
			subStructType := dummyField.Type.Elem()
			ptrToSubStructType := reflect.PointerTo(subStructType)

			subSlice := reflect.MakeSlice(reflect.SliceOf(ptrToSubStructType), 0, 0)
			subOffsets := make([]int, N+1)
			subOffsets[0] = 0

			for i := 0; i < N; i++ {
				elemVal := dataSlice.Index(i)
				fieldVal := elemVal.Elem().Field(j)

				lenMethod := fieldVal.MethodByName("Len")
				valMethod := fieldVal.MethodByName("Value")
				subLen := lenMethod.Call(nil)[0].Interface().(int)

				for k := 0; k < subLen; k++ {
					subPtrVal := valMethod.Call([]reflect.Value{reflect.ValueOf(k)})[0]
					subSlice = reflect.Append(subSlice, subPtrVal)
				}
				subOffsets[i+1] = subOffsets[i] + subLen
			}

			subChildren, subTotalSize, _ := registerFieldsReflect(r, stepName, dir, colName, subStructType, subSlice)

			r.RegisterStruct(stepName, dir, colName, subChildren, subTotalSize, nil)
			children = append(children, colName)
		} else {
			arraysToConcat := make([]arrow.Array, N)
			for i := 0; i < N; i++ {
				elemVal := dataSlice.Index(i)
				fieldVal := elemVal.Elem().Field(j)

				colAcc, ok := fieldVal.Interface().(ColAccessor)
				if !ok {
					panic(fmt.Sprintf("field %s of type %s does not implement ColAccessor", fField.Name, fieldVal.Type()))
				}

				arr := colAcc.GetArrowArray(accessor.Token{})
				arraysToConcat[i] = arr
			}

			mem := memory.DefaultAllocator
			concatenatedArr, err := array.Concatenate(arraysToConcat, mem)
			if err != nil {
				panic(fmt.Sprintf("failed to concatenate arrays: %v", err))
			}

			arrowType := getArrowTypeString(fField.Type)
			r.RegisterLeaf(stepName, dir, colName, arrowType, concatenatedArr)
			children = append(children, colName)
		}
	}

	var totalSize int
	if len(children) > 0 {
		firstLeafName := children[0]
		if arr, ok := r.GetArray(stepName, dir, firstLeafName); ok {
			totalSize = arr.Len()
		}
	}

	return children, totalSize, offsets
}
