package executor

import (
	"context"
	"reflect"
	"testing"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
	"github.com/apache/arrow/go/v18/arrow/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/galgotech/heddle-sdk-go/internal/accessor"
	internalarrow "github.com/galgotech/heddle-sdk-go/internal/arrow"
	"github.com/galgotech/heddle-sdk-go/internal/registry"
	pluginschema "github.com/galgotech/heddle-sdk-go/schema"
)

type TestStruct struct {
	A pluginschema.ColFloat64
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

	columns := map[string]arrow.Array{
		"A":    arr,
		"A_id": arrID,
	}

	ts := &TestStruct{}
	val := reflect.ValueOf(ts)

	err := bind(val, []int{0}, columns)
	require.NoError(t, err)

	assert.Equal(t, 3, ts.A.Len())
	assert.Equal(t, 1.1, ts.A.Value(0))
	assert.Equal(t, 2.2, ts.A.Value(1))
	assert.Equal(t, 3.3, ts.A.Value(2))

	assert.Equal(t, int64(10), ts.A.GetIDs(accessor.Token{}).Value(0))
	assert.Equal(t, int64(20), ts.A.GetIDs(accessor.Token{}).Value(1))
	assert.Equal(t, int64(30), ts.A.GetIDs(accessor.Token{}).Value(2))
}

type TestInput struct {
	Col1 pluginschema.ColString
}

type TestOutput struct {
	Col2 pluginschema.ColInt64
}

type TestStepGroup struct{}

func (g *TestStepGroup) SomeStep(ctx context.Context, config struct{}, input *TestInput) (*TestOutput, error) {
	resData := make([]int64, input.Col1.Len())
	for i := 0; i < input.Col1.Len(); i++ {
		resData[i] = int64(len(input.Col1.Value(i)))
	}
	return &TestOutput{
		Col2: *pluginschema.NewColInt64(resData),
	}, nil
}

func (g *TestStepGroup) SomeStepNoError(ctx context.Context, config struct{}, input *TestInput) *TestOutput {
	resData := make([]int64, input.Col1.Len())
	for i := 0; i < input.Col1.Len(); i++ {
		resData[i] = int64(len(input.Col1.Value(i)))
	}
	return &TestOutput{
		Col2: *pluginschema.NewColInt64(resData),
	}
}

func TestExecuteStepDirectly_PreservesIDs(t *testing.T) {
	reg := registry.NewRegistry()
	err := reg.Register(&TestStepGroup{})
	require.NoError(t, err)

	exec := NewExecutor(reg)

	inputData := []string{"hello", "world"}
	inputCol := pluginschema.NewColString(inputData)
	inputIDs := []int64{12345, 67890}

	mem := memory.DefaultAllocator
	bID := array.NewInt64Builder(mem)
	defer bID.Release()
	bID.AppendValues(inputIDs, nil)
	arrID := bID.NewInt64Array()
	defer arrID.Release()

	if colAcc, ok := any(inputCol).(pluginschema.ColAccessor); ok {
		colAcc.SetData(accessor.Token{}, inputCol.GetArrowArray(accessor.Token{}), arrID)
	}

	inputStruct := &TestInput{
		Col1: *inputCol,
	}

	outputAny := exec.ExecuteStepDirectly(t.Context(), "some_step", nil, inputStruct)
	require.NotNil(t, outputAny)

	output, ok := outputAny.(*TestOutput)
	require.True(t, ok)

	assert.Equal(t, inputCol.Len(), output.Col2.Len())
	assert.Equal(t, 2, output.Col2.Len())
	assert.Equal(t, int64(12345), output.Col2.GetIDs(accessor.Token{}).Value(0))
	assert.Equal(t, int64(67890), output.Col2.GetIDs(accessor.Token{}).Value(1))
}

func TestExecuteStepNoError(t *testing.T) {
	reg := registry.NewRegistry()
	err := reg.Register(&TestStepGroup{})
	require.NoError(t, err)

	exec := NewExecutor(reg)

	inputData := []string{"apple", "banana", "cherry"}
	inputCol := pluginschema.NewColString(inputData)
	inputStruct := &TestInput{
		Col1: *inputCol,
	}

	outputAny := exec.ExecuteStepDirectly(t.Context(), "some_step_no_error", nil, inputStruct)
	require.NotNil(t, outputAny)

	output, ok := outputAny.(*TestOutput)
	require.True(t, ok)

	assert.Equal(t, inputCol.Len(), output.Col2.Len())
	assert.Equal(t, int64(5), output.Col2.Value(0))
	assert.Equal(t, int64(6), output.Col2.Value(1))
	assert.Equal(t, int64(6), output.Col2.Value(2))
}

type MyTestRow struct {
	Name string
	Age  int
}

func TestSliceToArrowArray_Struct(t *testing.T) {
	data := []MyTestRow{
		{Name: "Alice", Age: 30},
		{Name: "Bob", Age: 25},
	}
	arrVal, err := internalarrow.SliceToArrowArray(data)
	require.NoError(t, err)
	require.NotNil(t, arrVal)
	defer arrVal.Release()

	structArr, ok := arrVal.(*array.Struct)
	require.True(t, ok)
	require.Equal(t, 2, structArr.Len())

	// Field 0: Name (string)
	nameArr, ok := structArr.Field(0).(*array.String)
	require.True(t, ok)
	assert.Equal(t, "Alice", nameArr.Value(0))
	assert.Equal(t, "Bob", nameArr.Value(1))

	// Field 1: Age (int64 due to int conversion)
	ageArr, ok := structArr.Field(1).(*array.Int64)
	require.True(t, ok)
	assert.Equal(t, int64(30), ageArr.Value(0))
	assert.Equal(t, int64(25), ageArr.Value(1))

	// Convert back to Go slice
	sliceVal := internalarrow.ArrowStructToGoSlice(structArr, reflect.TypeFor[MyTestRow]())
	require.Equal(t, 2, sliceVal.Len())

	row0 := sliceVal.Index(0).Interface().(MyTestRow)
	assert.Equal(t, "Alice", row0.Name)
	assert.Equal(t, 30, row0.Age)

	row1 := sliceVal.Index(1).Interface().(MyTestRow)
	assert.Equal(t, "Bob", row1.Name)
	assert.Equal(t, 25, row1.Age)
}

type TestStructWithStructCol struct {
	Rows pluginschema.ColStruct[MyTestRow]
}

type SubField1 struct {
	Test string
}
type TestInput1 struct {
	Legal pluginschema.ColStruct[SubField1]
}

type SubField2 struct {
	Test string
}
type TestInput2 struct {
	Legal pluginschema.ColStruct[SubField2]
}

type ConversionStepGroup struct{}

func (g *ConversionStepGroup) ProcessInput(ctx context.Context, config struct{}, input *TestInput2) (*TestOutput, error) {
	resData := make([]int64, input.Legal.Len())
	for i := 0; i < input.Legal.Len(); i++ {
		resData[i] = int64(len(input.Legal.Value(i).Test))
	}
	return &TestOutput{
		Col2: *pluginschema.NewColInt64(resData),
	}, nil
}

func TestExecuteStepDirectly_ZeroCopyConversion(t *testing.T) {
	reg := registry.NewRegistry()
	err := reg.Register(&ConversionStepGroup{})
	require.NoError(t, err)

	exec := NewExecutor(reg)

	inputData := []*SubField1{
		{Test: "hello"},
		{Test: "world!!!"},
	}
	inputCol := pluginschema.NewColStruct(inputData)
	inputStruct := &TestInput1{
		Legal: *inputCol,
	}

	outputAny := exec.ExecuteStepDirectly(t.Context(), "process_input", nil, inputStruct)
	require.NotNil(t, outputAny)

	output, ok := outputAny.(*TestOutput)
	require.True(t, ok)

	assert.Equal(t, 2, output.Col2.Len())
	assert.Equal(t, int64(5), output.Col2.Value(0))
	assert.Equal(t, int64(8), output.Col2.Value(1))
}
