package schema_test

import (
	"testing"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/galgotech/heddle-sdk-go/schema"
)

func TestColRegistryBasic(t *testing.T) {
	r := schema.NewColRegistry()
	r.RegisterStep("query")

	// Create a leaf array
	arr := schema.NewColString([]string{"a", "b", "c"}).GetArrowArray(struct{}{})
	r.RegisterLeaf("query", schema.Input, "user_id", "utf8", arr)

	// Verify leaf exists
	storedArr, ok := r.GetArray("query", schema.Input, "user_id")
	assert.True(t, ok)
	assert.Equal(t, arr, storedArr)

	meta, ok := r.GetMetadata("query", schema.Input, "user_id")
	assert.True(t, ok)
	assert.Equal(t, "user_id", meta.Name)
	assert.Equal(t, "utf8", meta.ArrowType)
	assert.False(t, meta.IsStruct)

	// Verify order
	names := r.Names("query", schema.Input)
	assert.Equal(t, []string{"user_id"}, names)

	// Test GetEntry
	entry, ok := r.GetEntry("query", schema.Input, "user_id")
	assert.True(t, ok)
	assert.Equal(t, arr, entry.Array)
}

func TestColRegistryStruct(t *testing.T) {
	r := schema.NewColRegistry()
	r.RegisterStep("query")

	// Register leaf children
	subId2Arr := schema.NewColString([]string{"childA", "childB"}).GetArrowArray(struct{}{})
	subId3Arr := schema.NewColInt64([]int64{10, 20}).GetArrowArray(struct{}{})

	r.RegisterLeaf("query", schema.Input, "sub_id2", "utf8", subId2Arr)
	r.RegisterLeaf("query", schema.Input, "sub_id3", "int64", subId3Arr)

	// Register struct view
	r.RegisterStruct("query", schema.Input, "sub", []string{"sub_id2", "sub_id3"}, 2, []int{0, 1, 2})

	// Get struct array
	subArr, ok := r.GetArray("query", schema.Input, "sub")
	assert.True(t, ok)
	assert.NotNil(t, subArr)

	structArr, ok := subArr.(*array.Struct)
	require.True(t, ok)
	assert.Equal(t, 2, structArr.Len())

	// Verify struct children
	field0Arr := structArr.Field(0).(*array.String)
	assert.Equal(t, "childA", field0Arr.Value(0))

	field1Arr := structArr.Field(1).(*array.Int64)
	assert.Equal(t, int64(10), field1Arr.Value(0))

	entry, ok := r.GetEntry("query", schema.Input, "sub")
	assert.True(t, ok)
	assert.Equal(t, []int{0, 1, 2}, entry.Offsets)
	assert.Equal(t, []string{"sub_id2", "sub_id3"}, entry.From)
}

func TestColRegistryMapOutputToInput(t *testing.T) {
	r := schema.NewColRegistry()
	r.RegisterStep("query")
	r.RegisterStep("transform")

	arr := schema.NewColString([]string{"x", "y"}).GetArrowArray(struct{}{})
	r.RegisterLeaf("query", schema.Output, "result", "utf8", arr)

	r.MapOutputToInput("query", "transform")

	// Verify in destination step input
	mappedArr, ok := r.GetArray("transform", schema.Input, "result")
	assert.True(t, ok)
	assert.Equal(t, arr, mappedArr)

	meta, ok := r.GetMetadata("transform", schema.Input, "result")
	assert.True(t, ok)
	assert.Equal(t, "utf8", meta.ArrowType)
}

func TestColRegistryDecompositionPropagation(t *testing.T) {
	r := schema.NewColRegistry()
	r.RegisterStep("query")

	childA := schema.NewColString([]string{"a", "b"}).GetArrowArray(struct{}{})
	childB := schema.NewColInt64([]int64{1, 2}).GetArrowArray(struct{}{})

	r.RegisterLeaf("query", schema.Input, "sub_id", "utf8", childA)
	r.RegisterLeaf("query", schema.Input, "sub_val", "int64", childB)
	r.RegisterStruct("query", schema.Input, "sub", []string{"sub_id", "sub_val"}, 2, nil)

	// Construct a new struct array
	newChildA := schema.NewColString([]string{"c", "d"}).GetArrowArray(struct{}{})
	newChildB := schema.NewColInt64([]int64{3, 4}).GetArrowArray(struct{}{})
	newStructArr, err := array.NewStructArray([]arrow.Array{newChildA, newChildB}, []string{"id", "val"})
	require.NoError(t, err)

	// Set new struct array
	r.SetArray("query", schema.Input, "sub", newStructArr)

	// Verify child arrays updated automatically
	storedChildA, ok := r.GetArray("query", schema.Input, "sub_id")
	assert.True(t, ok)
	assert.Equal(t, "c", storedChildA.(*array.String).Value(0))

	storedChildB, ok := r.GetArray("query", schema.Input, "sub_val")
	assert.True(t, ok)
	assert.Equal(t, int64(3), storedChildB.(*array.Int64).Value(0))
}
