package schema

import (
	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
	"github.com/apache/arrow/go/v18/arrow/memory"
	"github.com/bwmarrin/snowflake"
	"github.com/galgotech/heddle-lang/pkg/logger"
	"go.uber.org/zap"

	"github.com/galgotech/heddle-sdk-go/internal/accessor"
)

var colIDNode *snowflake.Node

func init() {
	var err error
	colIDNode, err = snowflake.NewNode(1)
	if err != nil {
		logger.L().Fatal("failed to create snowflake node", zap.Error(err))
	}
}

type hasValue[T goTypes] interface {
	Value(i int) T
}

// FrameSchema defines the structure of a HeddleFrame.
type ColSchema struct {
	Type string
	Name string
}

// ColAccessor provides access to unexported Col[T] fields.
// Requires accessor.Token, preventing external packages from accessing it.
type ColAccessor interface {
	GetArrowArray(accessor.Token) arrow.Array
	GetIDs(accessor.Token) *array.Int64
	SetData(token accessor.Token, arr arrow.Array, ids *array.Int64)
}

type Col[T heddleType, K goTypes] struct {
	arr    arrow.Array
	arrIds *array.Int64
}

func (c *Col[T, K]) GetArrowArray(accessor.Token) arrow.Array {
	return c.arr
}

func (c *Col[T, K]) SetData(token accessor.Token, arr arrow.Array, ids *array.Int64) {
	c.arr = arr
	c.arrIds = ids
}

func (c *Col[T, K]) Len() int {
	if c.arr == nil {
		return 0
	}
	return c.arr.Len()
}

func (c *Col[T, K]) GetIDs(accessor.Token) *array.Int64 {
	return c.arrIds
}

func (c Col[T, K]) Value(i int) K {
	return any(c.arr).(hasValue[K]).Value(i)
}

func newCol[T heddleType, K goTypes](arr arrow.Array) *Col[T, K] {
	return &Col[T, K]{
		arr:    arr,
		arrIds: newIds(arr.Len()),
	}
}

type ColInt8 = Col[Int8, int8]
type ColInt16 = Col[Int16, int16]
type ColInt32 = Col[Int32, int32]
type ColInt64 = Col[Int64, int64]
type ColUint8 = Col[Uint8, uint8]
type ColUint16 = Col[Uint16, uint16]
type ColUint32 = Col[Uint32, uint32]
type ColUint64 = Col[Uint64, uint64]
type ColFloat32 = Col[Float32, float32]
type ColFloat64 = Col[Float64, float64]
type ColBoolean = Col[Boolean, bool]
type ColString = Col[String, string]

func NewColInt8(data []int8) *ColInt8 {
	mem := memory.DefaultAllocator
	b := array.NewInt8Builder(mem)
	defer b.Release()
	b.AppendValues(data, nil)
	arr := b.NewArray()

	return newCol[Int8, int8](arr)
}

func NewColInt16(data []int16) *ColInt16 {
	mem := memory.DefaultAllocator
	b := array.NewInt16Builder(mem)
	defer b.Release()
	b.AppendValues(data, nil)
	arr := b.NewArray()

	return newCol[Int16, int16](arr)
}

func NewColInt32(data []int32) *ColInt32 {
	mem := memory.DefaultAllocator
	b := array.NewInt32Builder(mem)
	defer b.Release()
	b.AppendValues(data, nil)
	arr := b.NewArray()

	return newCol[Int32, int32](arr)
}

func NewColInt64(data []int64) *ColInt64 {
	mem := memory.DefaultAllocator
	b := array.NewInt64Builder(mem)
	defer b.Release()
	b.AppendValues(data, nil)
	arr := b.NewArray()

	return newCol[Int64, int64](arr)
}

func NewColUint8(data []uint8) *ColUint8 {
	mem := memory.DefaultAllocator
	b := array.NewUint8Builder(mem)
	defer b.Release()
	b.AppendValues(data, nil)
	arr := b.NewArray()

	return newCol[Uint8, uint8](arr)
}

func NewColUint16(data []uint16) *ColUint16 {
	mem := memory.DefaultAllocator
	b := array.NewUint16Builder(mem)
	defer b.Release()
	b.AppendValues(data, nil)
	arr := b.NewArray()

	return newCol[Uint16, uint16](arr)
}

func NewColUint32(data []uint32) *ColUint32 {
	mem := memory.DefaultAllocator
	b := array.NewUint32Builder(mem)
	defer b.Release()
	b.AppendValues(data, nil)
	arr := b.NewArray()

	return newCol[Uint32, uint32](arr)
}

func NewColUint64(data []uint64) *ColUint64 {
	mem := memory.DefaultAllocator
	b := array.NewUint64Builder(mem)
	defer b.Release()
	b.AppendValues(data, nil)
	arr := b.NewArray()

	return newCol[Uint64, uint64](arr)
}

func NewColFloat32(data []float32) *ColFloat32 {
	mem := memory.DefaultAllocator
	b := array.NewFloat32Builder(mem)
	defer b.Release()
	b.AppendValues(data, nil)
	arr := b.NewArray()

	return newCol[Float32, float32](arr)
}

func NewColFloat64(data []float64) *ColFloat64 {
	mem := memory.DefaultAllocator
	b := array.NewFloat64Builder(mem)
	defer b.Release()
	b.AppendValues(data, nil)
	arr := b.NewArray()

	return newCol[Float64, float64](arr)
}

func NewColBoolean(data []bool) *ColBoolean {
	mem := memory.DefaultAllocator
	b := array.NewBooleanBuilder(mem)
	defer b.Release()
	b.AppendValues(data, nil)
	arr := b.NewArray()

	return newCol[Boolean, bool](arr)
}

func NewColString(data []string) *ColString {
	mem := memory.DefaultAllocator
	b := array.NewStringBuilder(mem)
	defer b.Release()
	b.AppendValues(data, nil)
	arr := b.NewArray()

	return newCol[String, string](arr)
}

func newIds(size int) *array.Int64 {
	mem := memory.DefaultAllocator

	// ids
	ids := make([]int64, size)
	for i := range ids {
		ids[i] = colIDNode.Generate().Int64()
	}
	bInt64 := array.NewInt64Builder(mem)
	defer bInt64.Release()
	bInt64.AppendValues(ids, nil)
	return bInt64.NewInt64Array()
}
