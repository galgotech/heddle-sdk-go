package schema_test

import (
	"testing"

	"github.com/apache/arrow/go/v18/arrow/array"
	"github.com/stretchr/testify/assert"

	"github.com/galgotech/heddle-sdk-go/internal/accessor"
	"github.com/galgotech/heddle-sdk-go/schema"
)

type DummyStruct struct {
	Field1 *schema.ColString
	Field2 *schema.ColInt64
}

func TestNewColStruct(t *testing.T) {
	data := []*DummyStruct{
		{
			Field1: schema.NewColString([]string{"a", "b"}),
			Field2: schema.NewColInt64([]int64{1, 2}),
		},
		{
			Field1: schema.NewColString([]string{"c", "d"}),
			Field2: schema.NewColInt64([]int64{3, 4}),
		},
	}

	var col *schema.ColStruct[DummyStruct]
	assert.NotPanics(t, func() {
		col = schema.NewColStruct(data)
	})

	assert.NotNil(t, col)
	arr := col.GetArrowArray(accessor.Token{})
	assert.NotNil(t, arr)

	// Verify type and structure of the Arrow Array
	structArr, ok := arr.(*array.Struct)
	assert.True(t, ok)
	assert.Equal(t, 4, structArr.Len())

	// Field 0: Field1 (ColString -> *array.String)
	field1Arr, ok := structArr.Field(0).(*array.String)
	assert.True(t, ok)
	assert.Equal(t, "a", field1Arr.Value(0))
	assert.Equal(t, "b", field1Arr.Value(1))
	assert.Equal(t, "c", field1Arr.Value(2))
	assert.Equal(t, "d", field1Arr.Value(3))

	// Field 1: Field2 (ColInt64 -> *array.Int64)
	field2Arr, ok := structArr.Field(1).(*array.Int64)
	assert.True(t, ok)
	assert.Equal(t, int64(1), field2Arr.Value(0))
	assert.Equal(t, int64(2), field2Arr.Value(1))
	assert.Equal(t, int64(3), field2Arr.Value(2))
	assert.Equal(t, int64(4), field2Arr.Value(3))

	// Verify IDs
	ids := col.GetIDs(accessor.Token{})
	assert.NotNil(t, ids)
	assert.Equal(t, 4, ids.Len())

	// Verify Value(i)
	val0 := col.Value(0)
	assert.Equal(t, 1, val0.Field1.Len())
	assert.Equal(t, 1, val0.Field2.Len())
	assert.Equal(t, "a", val0.Field1.Value(0))
	assert.Equal(t, int64(1), val0.Field2.Value(0))

	val1 := col.Value(1)
	assert.Equal(t, "b", val1.Field1.Value(0))
	assert.Equal(t, int64(2), val1.Field2.Value(0))

	val2 := col.Value(2)
	assert.Equal(t, "c", val2.Field1.Value(0))
	assert.Equal(t, int64(3), val2.Field2.Value(0))

	val3 := col.Value(3)
	assert.Equal(t, "d", val3.Field1.Value(0))
	assert.Equal(t, int64(4), val3.Field2.Value(0))

	// Test SetData
	col.SetData(accessor.Token{}, structArr, ids)
	assert.Equal(t, structArr, col.GetArrowArray(accessor.Token{}))
	assert.Equal(t, ids, col.GetIDs(accessor.Token{}))
}

type SubStruct struct {
	Name *schema.ColString
}

type NestedStruct struct {
	ID    *schema.ColInt64
	Child *schema.ColStruct[SubStruct]
}

func TestNestedColStruct(t *testing.T) {
	child1 := schema.NewColStruct([]*SubStruct{
		{Name: schema.NewColString([]string{"childA", "childB"})},
	})
	child2 := schema.NewColStruct([]*SubStruct{
		{Name: schema.NewColString([]string{"childC", "childD"})},
	})

	data := []*NestedStruct{
		{
			ID:    schema.NewColInt64([]int64{10, 20}),
			Child: child1,
		},
		{
			ID:    schema.NewColInt64([]int64{30, 40}),
			Child: child2,
		},
	}

	var col *schema.ColStruct[NestedStruct]
	assert.NotPanics(t, func() {
		col = schema.NewColStruct(data)
	})

	assert.NotNil(t, col)
	arr := col.GetArrowArray(accessor.Token{})
	assert.NotNil(t, arr)

	structArr, ok := arr.(*array.Struct)
	assert.True(t, ok)
	assert.Equal(t, 4, structArr.Len())

	// Field 0: ID (ColInt64 -> *array.Int64)
	idArr, ok := structArr.Field(0).(*array.Int64)
	assert.True(t, ok)
	assert.Equal(t, int64(10), idArr.Value(0))
	assert.Equal(t, int64(20), idArr.Value(1))
	assert.Equal(t, int64(30), idArr.Value(2))
	assert.Equal(t, int64(40), idArr.Value(3))

	// Field 1: Child (ColStruct -> *array.Struct)
	childStructArr, ok := structArr.Field(1).(*array.Struct)
	assert.True(t, ok)
	assert.Equal(t, 4, childStructArr.Len())

	childNameArr, ok := childStructArr.Field(0).(*array.String)
	assert.True(t, ok)
	assert.Equal(t, "childA", childNameArr.Value(0))
	assert.Equal(t, "childB", childNameArr.Value(1))
	assert.Equal(t, "childC", childNameArr.Value(2))
	assert.Equal(t, "childD", childNameArr.Value(3))

	// Verify Value(i) for nested struct
	val0 := col.Value(0)
	assert.Equal(t, int64(10), val0.ID.Value(0))
	assert.Equal(t, 1, val0.Child.Len())
	assert.Equal(t, "childA", val0.Child.Value(0).Name.Value(0))

	val1 := col.Value(1)
	assert.Equal(t, int64(20), val1.ID.Value(0))
	assert.Equal(t, 1, val1.Child.Len())
	assert.Equal(t, "childB", val1.Child.Value(0).Name.Value(0))

	val2 := col.Value(2)
	assert.Equal(t, int64(30), val2.ID.Value(0))
	assert.Equal(t, 1, val2.Child.Len())
	assert.Equal(t, "childC", val2.Child.Value(0).Name.Value(0))

	val3 := col.Value(3)
	assert.Equal(t, int64(40), val3.ID.Value(0))
	assert.Equal(t, 1, val3.Child.Len())
	assert.Equal(t, "childD", val3.Child.Value(0).Name.Value(0))
}

func TestColStructEdgeCases(t *testing.T) {
	t.Run("nil field in struct", func(t *testing.T) {
		dataWithNil := []*DummyStruct{
			{
				Field1: nil,
				Field2: schema.NewColInt64([]int64{1}),
			},
		}
		assert.Panics(t, func() {
			schema.NewColStruct(dataWithNil)
		})
	})

	t.Run("non-struct type", func(t *testing.T) {
		assert.Panics(t, func() {
			schema.NewColStruct([]*int{})
		})
	})
}
