package schema

import (
	"testing"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
	"github.com/apache/arrow/go/v18/arrow/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	langschema "github.com/galgotech/heddle-lang/pkg/schema"
)

func TestNewFrameAnyArrayAndEach(t *testing.T) {
	mem := memory.DefaultAllocator

	bAge := array.NewInt32Builder(mem)
	defer bAge.Release()

	bAge.AppendValues([]int32{30, 25}, nil)

	arrAge := bAge.NewInt32Array()
	defer arrAge.Release()

	bName := array.NewStringBuilder(mem)
	defer bName.Release()

	bName.AppendValues([]string{"Alice", "Bob"}, nil)

	arrName := bName.NewStringArray()
	defer arrName.Release()

	dataArr := map[string]arrow.Array{
		"Age":  arrAge,
		"Name": arrName,
	}

	fieldsSchema := langschema.FieldSchema{
		Fields: []langschema.Field{
			{Name: "Age", Type: "int32"},
			{Name: "Name", Type: "utf8"},
		},
	}

	var frame Any

	err := NewFrameAnyArray(&frame, fieldsSchema, dataArr)
	require.NoError(t, err)

	var iterated []map[string]any

	err = frame.Each(func(item map[string]any) {
		iterated = append(iterated, item)
	})
	require.NoError(t, err)
	require.Len(t, iterated, 2)
	assert.Equal(t, int32(30), iterated[0]["Age"])
	assert.Equal(t, "Alice", iterated[0]["Name"])
	assert.Equal(t, int32(25), iterated[1]["Age"])
	assert.Equal(t, "Bob", iterated[1]["Name"])
}
