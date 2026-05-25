package schema

import (
	"iter"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
	"github.com/apache/arrow/go/v18/arrow/memory"

	"github.com/galgotech/heddle-sdk-go/internal/accessor"
)

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
	SetData(token accessor.Token, arr arrow.Array)
}

type ColRegistryBinder interface {
	BindRegistry(token accessor.Token, registry *ColRegistry, stepName string, dir StepDirection, colName string)
}

type Col[T heddleType, K goTypes] struct {
	registry  *ColRegistry
	stepName  string
	direction StepDirection
	colName   string
}

func (c *Col[T, K]) BindRegistry(token accessor.Token, registry *ColRegistry, stepName string, dir StepDirection, colName string) {
	c.registry = registry
	c.stepName = stepName
	c.direction = dir
	c.colName = colName
}

func (c *Col[T, K]) GetArrowArray(accessor.Token) arrow.Array {
	if c.registry == nil {
		return nil
	}
	arr, _ := c.registry.GetArray(c.stepName, c.direction, c.colName)
	return arr
}

func (c *Col[T, K]) SetData(token accessor.Token, arr arrow.Array) {
	if c.registry == nil {
		c.registry = NewColRegistry()
		c.registry.RegisterStep("_temp")
		c.stepName = "_temp"
		c.direction = Input
		c.colName = "col"
	}
	c.registry.SetArray(c.stepName, c.direction, c.colName, arr)
}

func (c *Col[T, K]) Len() int {
	if c.registry == nil {
		return 0
	}
	arr, ok := c.registry.GetArray(c.stepName, c.direction, c.colName)
	if !ok || arr == nil {
		return 0
	}
	return arr.Len()
}

func (c Col[T, K]) Value(i int) K {
	if c.registry == nil {
		panic("column registry is nil")
	}
	arr, ok := c.registry.GetArray(c.stepName, c.direction, c.colName)
	if !ok || arr == nil {
		panic("column array is nil")
	}
	return any(arr).(hasValue[K]).Value(i)
}

// All returns an iterator to be used with standard 'for i, e := range' loops.
func (c Col[T, K]) All() iter.Seq2[int, K] {
	return func(yield func(int, K) bool) {
		for i := 0; i < c.Len(); i++ {
			if !yield(i, c.Value(i)) {
				return
			}
		}
	}
}

func newCol[T heddleType, K goTypes](registry *ColRegistry, stepName string, dir StepDirection, name string) *Col[T, K] {
	return &Col[T, K]{
		registry:  registry,
		stepName:  stepName,
		direction: dir,
		colName:   name,
	}
}

func newColWithData[T heddleType, K goTypes](arrowType string, arr arrow.Array) *Col[T, K] {
	r := NewColRegistry()
	r.RegisterStep("_temp")
	r.RegisterLeaf("_temp", Input, "col", arrowType, arr)
	return newCol[T, K](r, "_temp", Input, "col")
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

	return newColWithData[Int8, int8]("int8", arr)
}

func NewColInt16(data []int16) *ColInt16 {
	mem := memory.DefaultAllocator
	b := array.NewInt16Builder(mem)
	defer b.Release()
	b.AppendValues(data, nil)
	arr := b.NewArray()

	return newColWithData[Int16, int16]("int16", arr)
}

func NewColInt32(data []int32) *ColInt32 {
	mem := memory.DefaultAllocator
	b := array.NewInt32Builder(mem)
	defer b.Release()
	b.AppendValues(data, nil)
	arr := b.NewArray()

	return newColWithData[Int32, int32]("int32", arr)
}

func NewColInt64(data []int64) *ColInt64 {
	mem := memory.DefaultAllocator
	b := array.NewInt64Builder(mem)
	defer b.Release()
	b.AppendValues(data, nil)
	arr := b.NewArray()

	return newColWithData[Int64, int64]("int64", arr)
}

func NewColUint8(data []uint8) *ColUint8 {
	mem := memory.DefaultAllocator
	b := array.NewUint8Builder(mem)
	defer b.Release()
	b.AppendValues(data, nil)
	arr := b.NewArray()

	return newColWithData[Uint8, uint8]("uint8", arr)
}

func NewColUint16(data []uint16) *ColUint16 {
	mem := memory.DefaultAllocator
	b := array.NewUint16Builder(mem)
	defer b.Release()
	b.AppendValues(data, nil)
	arr := b.NewArray()

	return newColWithData[Uint16, uint16]("uint16", arr)
}

func NewColUint32(data []uint32) *ColUint32 {
	mem := memory.DefaultAllocator
	b := array.NewUint32Builder(mem)
	defer b.Release()
	b.AppendValues(data, nil)
	arr := b.NewArray()

	return newColWithData[Uint32, uint32]("uint32", arr)
}

func NewColUint64(data []uint64) *ColUint64 {
	mem := memory.DefaultAllocator
	b := array.NewUint64Builder(mem)
	defer b.Release()
	b.AppendValues(data, nil)
	arr := b.NewArray()

	return newColWithData[Uint64, uint64]("uint64", arr)
}

func NewColFloat32(data []float32) *ColFloat32 {
	mem := memory.DefaultAllocator
	b := array.NewFloat32Builder(mem)
	defer b.Release()
	b.AppendValues(data, nil)
	arr := b.NewArray()

	return newColWithData[Float32, float32]("float32", arr)
}

func NewColFloat64(data []float64) *ColFloat64 {
	mem := memory.DefaultAllocator
	b := array.NewFloat64Builder(mem)
	defer b.Release()
	b.AppendValues(data, nil)
	arr := b.NewArray()

	return newColWithData[Float64, float64]("float64", arr)
}

func NewColBoolean(data []bool) *ColBoolean {
	mem := memory.DefaultAllocator
	b := array.NewBooleanBuilder(mem)
	defer b.Release()
	b.AppendValues(data, nil)
	arr := b.NewArray()

	return newColWithData[Boolean, bool]("bool", arr)
}

func NewColString(data []string) *ColString {
	mem := memory.DefaultAllocator
	b := array.NewStringBuilder(mem)
	defer b.Release()
	b.AppendValues(data, nil)
	arr := b.NewArray()

	return newColWithData[String, string]("utf8", arr)
}
