package internal

import (
	"context"
	"reflect"
	"testing"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
	"github.com/apache/arrow/go/v18/arrow/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pluginschema "github.com/galgotech/heddle-sdk-go/schema"
)

type TestStruct struct {
	A pluginschema.Col[float64]
}

func TestBind(t *testing.T) {
	mem := memory.DefaultAllocator
	b := array.NewFloat64Builder(mem)
	defer b.Release()
	b.AppendValues([]float64{1.1, 2.2, 3.3}, nil)
	arr := b.NewArray()
	defer arr.Release()

	bID := array.NewInt64Builder(mem)
	defer bID.Release()
	bID.AppendValues([]int64{10, 20, 30}, nil)
	arrID := bID.NewArray()
	defer arrID.Release()

	bDirty := array.NewBooleanBuilder(mem)
	defer bDirty.Release()
	bDirty.AppendValues([]bool{false, true, false}, nil)
	arrDirty := bDirty.NewArray()
	defer arrDirty.Release()

	columns := map[string]arrow.Array{
		"A":       arr,
		"A_id":    arrID,
		"A_dirty": arrDirty,
	}

	ts := &TestStruct{}
	val := reflect.ValueOf(ts)

	err := bind(val, []int{0}, columns)
	require.NoError(t, err)

	assert.Equal(t, 3, ts.A.Len())
	assert.Equal(t, 1.1, ts.A.Value(0))
	assert.Equal(t, 2.2, ts.A.Value(1))
	assert.Equal(t, 3.3, ts.A.Value(2))

	assert.Equal(t, int64(10), ts.A.ID(0))
	assert.Equal(t, int64(20), ts.A.ID(1))
	assert.Equal(t, int64(30), ts.A.ID(2))

	assert.False(t, ts.A.IsDeleted(0))
	assert.True(t, ts.A.IsDeleted(1))
	assert.False(t, ts.A.IsDeleted(2))
}

type TestInput struct {
	Col1 pluginschema.Col[string]
}

type TestOutput struct {
	Col2 pluginschema.Col[int64]
}

type TestStepGroup struct{}

func (g *TestStepGroup) SomeStep(ctx context.Context, config struct{}, input *TestInput) (*TestOutput, error) {
	resData := make([]int64, input.Col1.Len())
	for i := 0; i < input.Col1.Len(); i++ {
		resData[i] = int64(len(input.Col1.Value(i)))
	}
	return &TestOutput{
		Col2: pluginschema.NewCol(resData),
	}, nil
}

func TestExecuteStepDirectly_PreservesIDs(t *testing.T) {
	reg := NewRegistry("testns")
	err := reg.RegisterGroup(&TestStepGroup{})
	require.NoError(t, err)

	exec := NewExecutor(reg)

	inputData := []string{"hello", "world"}
	inputCol := pluginschema.NewCol(inputData)
	inputIDs := []int64{12345, 67890}
	getUnexportedField(reflect.ValueOf(&inputCol).Elem(), "ids").Set(reflect.ValueOf(inputIDs))

	inputStruct := &TestInput{
		Col1: inputCol,
	}

	outputAny, err := exec.ExecuteStepDirectly(t.Context(), "teststepgroup.somestep", nil, inputStruct)
	require.NoError(t, err)
	require.NotNil(t, outputAny)

	output, ok := outputAny.(*TestOutput)
	require.True(t, ok)

	assert.Equal(t, 2, output.Col2.Len())
	assert.Equal(t, int64(12345), output.Col2.ID(0))
	assert.Equal(t, int64(67890), output.Col2.ID(1))
}
