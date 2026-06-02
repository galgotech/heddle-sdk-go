package schema

import (
	"fmt"
	"strings"
	"unsafe"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"

	"github.com/galgotech/heddle-lang/pkg/schema"
)

const UnderLineTypePosition = 1

type Frame[T any] struct {
	state *refState // changing the position of this field breaks the pointers
	_     T
}

func NewFrame[T any](columnsSchema []schema.ColumnSchema, data []T) (Frame[T], error) {
	frame := Frame[T]{}

	var zero T

	tType := getRtype(zero)
	if tType == nil {
		return frame, fmt.Errorf("type is nil")
	}

	if tType.kind&0x1f == KindPointer {
		return frame, fmt.Errorf("type is a pointer")
	}

	st := tType.structType()
	if st == nil {
		return frame, fmt.Errorf("type is not a struct")
	}

	var fields []structField
	for _, field := range st.fields {
		if field.name.IsEmbedded() || !field.name.IsExported() {
			continue
		}

		fields = append(fields, field)
	}

	columns := make([][]any, len(fields))
	for j := range fields {
		columns[j] = make([]any, len(data))
	}

	for i := range data {
		ptr := unsafe.Pointer(&data[i])
		for j, field := range fields {
			valPtr := unsafe.Add(ptr, field.offset)

			var val any

			switch field.typ.kind & 0x1f {
			case KindInt:
				val = *(*int)(valPtr)
			case KindInt8:
				val = *(*int8)(valPtr)
			case KindInt16:
				val = *(*int16)(valPtr)
			case KindInt32:
				val = *(*int32)(valPtr)
			case KindInt64:
				val = *(*int64)(valPtr)
			case KindUint:
				val = *(*uint)(valPtr)
			case KindUint8:
				val = *(*uint8)(valPtr)
			case KindUint16:
				val = *(*uint16)(valPtr)
			case KindUint32:
				val = *(*uint32)(valPtr)
			case KindUint64:
				val = *(*uint64)(valPtr)
			case KindFloat32:
				val = *(*float32)(valPtr)
			case KindFloat64:
				val = *(*float64)(valPtr)
			case KindBool:
				val = *(*bool)(valPtr)
			case KindString:
				val = *(*string)(valPtr)
			}

			columns[j][i] = val
		}
	}

	state, err := newRefState(tType, columns)
	if err != nil {
		return frame, err
	}

	frame.state = state

	return frame, nil
}

func NewFrameArray(frame any, columnsSchema []schema.ColumnSchema, dataArr map[string]arrow.Array) error {
	t := getRtype(frame)
	if t.kind&0x1f != KindPointer {
		return fmt.Errorf("type is not a pointer")
	}

	frameType := (*ptrType)(unsafe.Pointer(t)).elem
	if frameType.kind&0x1f != KindStruct {
		return fmt.Errorf("frame type is not a struct")
	}

	stFrame := frameType.structType()

	var tType *rtype

	for _, field := range stFrame.fields {
		if field.name.Name() == "_" {
			tType = field.typ
			break
		}
	}

	if tType == nil {
		return fmt.Errorf("could not find underlying type T in Frame")
	}

	if tType.kind&0x1f == KindPointer {
		return fmt.Errorf("type is a pointer")
	}

	st := tType.structType()
	if st == nil {
		return fmt.Errorf("type is not a struct")
	}

	var fields []structField
	for _, field := range st.fields {
		if field.name.IsEmbedded() || !field.name.IsExported() {
			continue
		}

		fields = append(fields, field)
	}

	if len(fields) != len(columnsSchema) {
		return fmt.Errorf("columns schema length %d does not match fields length %d", len(columnsSchema), len(fields))
	}

	columnsFrame := make([][]any, len(fields))
	for i, field := range fields {
		var colName string

		for _, col := range columnsSchema {
			if strings.EqualFold(col.Name, field.name.Name()) {
				colName = col.Name
				break
			}
		}

		if colName == "" {
			return fmt.Errorf("missing column schema for field %s", field.name.Name())
		}

		valueArray, ok := dataArr[colName]
		if !ok {
			return fmt.Errorf("missing arrow array for column %s", colName)
		}

		length := valueArray.Len()
		columnsFrame[i] = make([]any, length)

		switch valueArray.DataType().ID() {
		case arrow.INT8:
			arr := valueArray.(*array.Int8)
			for idx := range length {
				columnsFrame[i][idx] = arr.Value(idx)
			}
		case arrow.INT16:
			arr := valueArray.(*array.Int16)
			for idx := range length {
				columnsFrame[i][idx] = arr.Value(idx)
			}
		case arrow.INT32:
			arr := valueArray.(*array.Int32)
			for idx := range length {
				columnsFrame[i][idx] = arr.Value(idx)
			}
		case arrow.INT64:
			arr := valueArray.(*array.Int64)
			for idx := range length {
				columnsFrame[i][idx] = arr.Value(idx)
			}
		case arrow.UINT8:
			arr := valueArray.(*array.Uint8)
			for idx := range length {
				columnsFrame[i][idx] = arr.Value(idx)
			}
		case arrow.UINT16:
			arr := valueArray.(*array.Uint16)
			for idx := range length {
				columnsFrame[i][idx] = arr.Value(idx)
			}
		case arrow.UINT32:
			arr := valueArray.(*array.Uint32)
			for idx := range length {
				columnsFrame[i][idx] = arr.Value(idx)
			}
		case arrow.UINT64:
			arr := valueArray.(*array.Uint64)
			for idx := range length {
				columnsFrame[i][idx] = arr.Value(idx)
			}
		case arrow.FLOAT32:
			arr := valueArray.(*array.Float32)
			for idx := range length {
				columnsFrame[i][idx] = arr.Value(idx)
			}
		case arrow.FLOAT64:
			arr := valueArray.(*array.Float64)
			for idx := range length {
				columnsFrame[i][idx] = arr.Value(idx)
			}
		case arrow.BOOL:
			arr := valueArray.(*array.Boolean)
			for idx := range length {
				columnsFrame[i][idx] = arr.Value(idx)
			}
		case arrow.STRING:
			arr := valueArray.(*array.String)
			for idx := range length {
				columnsFrame[i][idx] = arr.Value(idx)
			}
		default:
			return fmt.Errorf("unsupported data type: %s", valueArray.DataType().ID())
		}
	}

	state, err := newRefState(tType, columnsFrame)
	if err != nil {
		return err
	}

	ptrToFrame := (*eface)(unsafe.Pointer(&frame)).data
	*(**refState)(ptrToFrame) = state

	return nil
}

func (r Frame[T]) Add(value T) {
	ptr := unsafe.Pointer(&value)
	for i, offset := range r.state.offsets {
		valPtr := unsafe.Add(ptr, offset)

		var val any

		switch r.state.kinds[i] {
		case KindInt:
			val = *(*int)(valPtr)
		case KindInt8:
			val = *(*int8)(valPtr)
		case KindInt16:
			val = *(*int16)(valPtr)
		case KindInt32:
			val = *(*int32)(valPtr)
		case KindInt64:
			val = *(*int64)(valPtr)
		case KindUint:
			val = *(*uint)(valPtr)
		case KindUint8:
			val = *(*uint8)(valPtr)
		case KindUint16:
			val = *(*uint16)(valPtr)
		case KindUint32:
			val = *(*uint32)(valPtr)
		case KindUint64:
			val = *(*uint64)(valPtr)
		case KindFloat32:
			val = *(*float32)(valPtr)
		case KindFloat64:
			val = *(*float64)(valPtr)
		case KindBool:
			val = *(*bool)(valPtr)
		case KindString:
			val = *(*string)(valPtr)
		}

		fieldName := r.state.names[i]
		r.state.slices[fieldName] = append(r.state.slices[fieldName], val)
	}

	r.state.length++
}

func (r Frame[T]) Each(yield func(item T)) error {
	offsets := r.state.offsets
	kinds := r.state.kinds
	slices := r.state.slices
	names := r.state.names

	for i := range r.state.length {
		itemPtr := new(T)
		ptr := unsafe.Pointer(itemPtr)

		for j, offset := range offsets {
			kind := kinds[j]
			fieldName := names[j]
			val := slices[fieldName][i]

			switch kind {
			case KindInt:
				*(*int)(unsafe.Add(ptr, offset)) = val.(int)
			case KindInt8:
				*(*int8)(unsafe.Add(ptr, offset)) = val.(int8)
			case KindInt16:
				*(*int16)(unsafe.Add(ptr, offset)) = val.(int16)
			case KindInt32:
				*(*int32)(unsafe.Add(ptr, offset)) = val.(int32)
			case KindInt64:
				*(*int64)(unsafe.Add(ptr, offset)) = val.(int64)
			case KindUint:
				*(*uint)(unsafe.Add(ptr, offset)) = val.(uint)
			case KindUint8:
				*(*uint8)(unsafe.Add(ptr, offset)) = val.(uint8)
			case KindUint16:
				*(*uint16)(unsafe.Add(ptr, offset)) = val.(uint16)
			case KindUint32:
				*(*uint32)(unsafe.Add(ptr, offset)) = val.(uint32)
			case KindUint64:
				*(*uint64)(unsafe.Add(ptr, offset)) = val.(uint64)
			case KindFloat32:
				*(*float32)(unsafe.Add(ptr, offset)) = val.(float32)
			case KindFloat64:
				*(*float64)(unsafe.Add(ptr, offset)) = val.(float64)
			case KindBool:
				*(*bool)(unsafe.Add(ptr, offset)) = val.(bool)
			case KindString:
				*(*string)(unsafe.Add(ptr, offset)) = val.(string)
			}
		}

		yield(*itemPtr)
	}

	return nil
}

func (r Frame[T]) Slices() map[string][]any {
	return r.state.slices
}
