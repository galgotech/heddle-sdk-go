package schema

import (
	"fmt"
	"unsafe"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
	"github.com/galgotech/heddle-lang/pkg/schema"
)

type refAnyState struct {
	slices map[string][]any
}

type Any struct {
	state *refAnyState // changing the position of this field breaks the pointers
}

func (r Any) Each(yield func(item map[string]any)) error {
	if r.state == nil || len(r.state.slices) == 0 {
		return nil
	}

	var length int
	for _, slice := range r.state.slices {
		length = len(slice)
		break
	}

	for i := 0; i < length; i++ {
		item := make(map[string]any)
		for colName, slice := range r.state.slices {
			item[colName] = slice[i]
		}

		yield(item)
	}

	return nil
}

func (r Any) Add(value map[string]any) {
	for colName, slice := range r.state.slices {
		slice = append(slice, value[colName])
	}
}

func (r Any) Slices() map[string][]any {
	return r.state.slices
}

func NewFrameAnyArray(frame Any, fieldsSchema schema.FieldSchema, dataArr map[string]arrow.Array) error {
	t := getRtype(frame)
	if t.kind&0x1f != KindPointer {
		return fmt.Errorf("type is not a pointer")
	}

	frameType := (*ptrType)(unsafe.Pointer(t)).elem
	if frameType.kind&0x1f != KindStruct {
		return fmt.Errorf("frame type is not a struct")
	}

	columnsFrame := make([][]any, len(fieldsSchema.Fields))
	for i, field := range fieldsSchema.Fields {
		valueArray, ok := dataArr[field.Name]
		if !ok {
			return fmt.Errorf("missing arrow array for field %s", field.Name)
		}

		length := valueArray.Len()
		columnsFrame[i] = make([]any, length)

		switch valueArray.DataType().ID() {
		case arrow.INT8:
			arr := valueArray.(*array.Int8)
			for idx := 0; idx < length; idx++ {
				columnsFrame[i][idx] = arr.Value(idx)
			}
		case arrow.INT16:
			arr := valueArray.(*array.Int16)
			for idx := 0; idx < length; idx++ {
				columnsFrame[i][idx] = arr.Value(idx)
			}
		case arrow.INT32:
			arr := valueArray.(*array.Int32)
			for idx := 0; idx < length; idx++ {
				columnsFrame[i][idx] = arr.Value(idx)
			}
		case arrow.INT64:
			arr := valueArray.(*array.Int64)
			for idx := 0; idx < length; idx++ {
				columnsFrame[i][idx] = arr.Value(idx)
			}
		case arrow.UINT8:
			arr := valueArray.(*array.Uint8)
			for idx := 0; idx < length; idx++ {
				columnsFrame[i][idx] = arr.Value(idx)
			}
		case arrow.UINT16:
			arr := valueArray.(*array.Uint16)
			for idx := 0; idx < length; idx++ {
				columnsFrame[i][idx] = arr.Value(idx)
			}
		case arrow.UINT32:
			arr := valueArray.(*array.Uint32)
			for idx := 0; idx < length; idx++ {
				columnsFrame[i][idx] = arr.Value(idx)
			}
		case arrow.UINT64:
			arr := valueArray.(*array.Uint64)
			for idx := 0; idx < length; idx++ {
				columnsFrame[i][idx] = arr.Value(idx)
			}
		case arrow.FLOAT32:
			arr := valueArray.(*array.Float32)
			for idx := 0; idx < length; idx++ {
				columnsFrame[i][idx] = arr.Value(idx)
			}
		case arrow.FLOAT64:
			arr := valueArray.(*array.Float64)
			for idx := 0; idx < length; idx++ {
				columnsFrame[i][idx] = arr.Value(idx)
			}
		case arrow.BOOL:
			arr := valueArray.(*array.Boolean)
			for idx := 0; idx < length; idx++ {
				columnsFrame[i][idx] = arr.Value(idx)
			}
		case arrow.STRING:
			arr := valueArray.(*array.String)
			for idx := 0; idx < length; idx++ {
				columnsFrame[i][idx] = arr.Value(idx)
			}
		default:
			return fmt.Errorf("unsupported data type: %s", valueArray.DataType().ID())
		}
	}

	slices := make(map[string][]any)
	for i, field := range fieldsSchema.Fields {
		slices[field.Name] = columnsFrame[i]
	}

	state := &refAnyState{
		slices: slices,
	}

	ptrToFrame := (*eface)(unsafe.Pointer(&frame)).data
	*(**refAnyState)(ptrToFrame) = state

	return nil
}
