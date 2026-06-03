package arrow

import (
	"reflect"
	"testing"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type MyInt8 int8
type MyInt16 int16
type MyInt32 int32
type MyInt64 int64
type MyUint8 uint8
type MyUint16 uint16
type MyUint32 uint32
type MyUint64 uint64
type MyFloat32 float32
type MyFloat64 float64
type MyBool bool
type MyString string

type SubStruct struct {
	Val float64
}

type TestStruct struct {
	A int8
	B string
	c int32 // unexported, should be ignored
	D bool
	E SubStruct
}

func TestSliceToArrowArray_DirectSlices(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected []any
	}{
		{
			name:     "int8",
			input:    []int8{1, 2, 3},
			expected: []any{int8(1), int8(2), int8(3)},
		},
		{
			name:     "int16",
			input:    []int16{1, 2, 3},
			expected: []any{int16(1), int16(2), int16(3)},
		},
		{
			name:     "int32",
			input:    []int32{1, 2, 3},
			expected: []any{int32(1), int32(2), int32(3)},
		},
		{
			name:     "int64",
			input:    []int64{1, 2, 3},
			expected: []any{int64(1), int64(2), int64(3)},
		},
		{
			name:     "uint8",
			input:    []uint8{1, 2, 3},
			expected: []any{uint8(1), uint8(2), uint8(3)},
		},
		{
			name:     "uint16",
			input:    []uint16{1, 2, 3},
			expected: []any{uint16(1), uint16(2), uint16(3)},
		},
		{
			name:     "uint32",
			input:    []uint32{1, 2, 3},
			expected: []any{uint32(1), uint32(2), uint32(3)},
		},
		{
			name:     "uint64",
			input:    []uint64{1, 2, 3},
			expected: []any{uint64(1), uint64(2), uint64(3)},
		},
		{
			name:     "float32",
			input:    []float32{1.5, 2.5},
			expected: []any{float32(1.5), float32(2.5)},
		},
		{
			name:     "float64",
			input:    []float64{1.5, 2.5},
			expected: []any{float64(1.5), float64(2.5)},
		},
		{
			name:     "bool",
			input:    []bool{true, false, true},
			expected: []any{true, false, true},
		},
		{
			name:     "string",
			input:    []string{"a", "b", ""},
			expected: []any{"a", "b", ""},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			arr, err := SliceToArrowArray(tc.input)
			require.NoError(t, err)
			defer arr.Release()

			assert.Equal(t, len(tc.expected), arr.Len())
			for i, exp := range tc.expected {
				switch expVal := exp.(type) {
				case int8:
					assert.Equal(t, expVal, arr.(*array.Int8).Value(i))
				case int16:
					assert.Equal(t, expVal, arr.(*array.Int16).Value(i))
				case int32:
					assert.Equal(t, expVal, arr.(*array.Int32).Value(i))
				case int64:
					assert.Equal(t, expVal, arr.(*array.Int64).Value(i))
				case uint8:
					assert.Equal(t, expVal, arr.(*array.Uint8).Value(i))
				case uint16:
					assert.Equal(t, expVal, arr.(*array.Uint16).Value(i))
				case uint32:
					assert.Equal(t, expVal, arr.(*array.Uint32).Value(i))
				case uint64:
					assert.Equal(t, expVal, arr.(*array.Uint64).Value(i))
				case float32:
					assert.Equal(t, expVal, arr.(*array.Float32).Value(i))
				case float64:
					assert.Equal(t, expVal, arr.(*array.Float64).Value(i))
				case bool:
					assert.Equal(t, expVal, arr.(*array.Boolean).Value(i))
				case string:
					assert.Equal(t, expVal, arr.(*array.String).Value(i))
				}
			}
		})
	}
}

func TestSliceToArrowArray_ReflectionSlices(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected []any
	}{
		{
			name:     "MyInt8",
			input:    []MyInt8{1, 2},
			expected: []any{int8(1), int8(2)},
		},
		{
			name:     "MyInt16",
			input:    []MyInt16{1, 2},
			expected: []any{int16(1), int16(2)},
		},
		{
			name:     "MyInt32",
			input:    []MyInt32{1, 2},
			expected: []any{int32(1), int32(2)},
		},
		{
			name:     "MyInt64",
			input:    []MyInt64{1, 2},
			expected: []any{int64(1), int64(2)},
		},
		{
			name:     "MyUint8",
			input:    []MyUint8{1, 2},
			expected: []any{uint8(1), uint8(2)},
		},
		{
			name:     "MyUint16",
			input:    []MyUint16{1, 2},
			expected: []any{uint16(1), uint16(2)},
		},
		{
			name:     "MyUint32",
			input:    []MyUint32{1, 2},
			expected: []any{uint32(1), uint32(2)},
		},
		{
			name:     "MyUint64",
			input:    []MyUint64{1, 2},
			expected: []any{uint64(1), uint64(2)},
		},
		{
			name:     "MyFloat32",
			input:    []MyFloat32{1.2, 3.4},
			expected: []any{float32(1.2), float32(3.4)},
		},
		{
			name:     "MyFloat64",
			input:    []MyFloat64{1.2, 3.4},
			expected: []any{float64(1.2), float64(3.4)},
		},
		{
			name:     "MyBool",
			input:    []MyBool{true, false},
			expected: []any{true, false},
		},
		{
			name:     "MyString",
			input:    []MyString{"abc", "def"},
			expected: []any{"abc", "def"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			arr, err := SliceToArrowArray(tc.input)
			require.NoError(t, err)
			defer arr.Release()

			assert.Equal(t, len(tc.expected), arr.Len())
			for i, exp := range tc.expected {
				switch expVal := exp.(type) {
				case int8:
					assert.Equal(t, expVal, arr.(*array.Int8).Value(i))
				case int16:
					assert.Equal(t, expVal, arr.(*array.Int16).Value(i))
				case int32:
					assert.Equal(t, expVal, arr.(*array.Int32).Value(i))
				case int64:
					assert.Equal(t, expVal, arr.(*array.Int64).Value(i))
				case uint8:
					assert.Equal(t, expVal, arr.(*array.Uint8).Value(i))
				case uint16:
					assert.Equal(t, expVal, arr.(*array.Uint16).Value(i))
				case uint32:
					assert.Equal(t, expVal, arr.(*array.Uint32).Value(i))
				case uint64:
					assert.Equal(t, expVal, arr.(*array.Uint64).Value(i))
				case float32:
					assert.Equal(t, expVal, arr.(*array.Float32).Value(i))
				case float64:
					assert.Equal(t, expVal, arr.(*array.Float64).Value(i))
				case bool:
					assert.Equal(t, expVal, arr.(*array.Boolean).Value(i))
				case string:
					assert.Equal(t, expVal, arr.(*array.String).Value(i))
				}
			}
		})
	}
}

func TestSliceToArrowArray_StructSlice(t *testing.T) {
	input := []TestStruct{
		{A: 10, B: "first", c: 100, D: true, E: SubStruct{Val: 1.1}},
		{A: 20, B: "second", c: 200, D: false, E: SubStruct{Val: 2.2}},
	}

	arr, err := SliceToArrowArray(input)
	require.NoError(t, err)
	defer arr.Release()

	structArr, ok := arr.(*array.Struct)
	require.True(t, ok)
	require.Equal(t, 2, structArr.Len())

	// We expect 4 exported fields in the Arrow Struct: A, B, D, E (c is unexported)
	require.Equal(t, 4, structArr.NumField())

	arrA := structArr.Field(0).(*array.Int8)
	arrB := structArr.Field(1).(*array.String)
	arrD := structArr.Field(2).(*array.Boolean)
	arrE := structArr.Field(3).(*array.Struct)

	assert.Equal(t, int8(10), arrA.Value(0))
	assert.Equal(t, int8(20), arrA.Value(1))

	assert.Equal(t, "first", arrB.Value(0))
	assert.Equal(t, "second", arrB.Value(1))

	assert.True(t, arrD.Value(0))
	assert.False(t, arrD.Value(1))

	require.Equal(t, 1, arrE.NumField())
	arrEVal := arrE.Field(0).(*array.Float64)
	assert.Equal(t, 1.1, arrEVal.Value(0))
	assert.Equal(t, 2.2, arrEVal.Value(1))
}

func TestSliceToArrowArray_Errors(t *testing.T) {
	// Not a slice
	_, err := SliceToArrowArray(123)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected slice, got int")

	// Unsupported element type in direct/reflection path
	_, err = SliceToArrowArray([]chan int{nil})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported slice element type: chan")
}

func TestGoTypeToArrowDataType(t *testing.T) {
	tests := []struct {
		name    string
		typ     reflect.Type
		want    arrow.DataType
		wantErr bool
	}{
		{"int8", reflect.TypeOf(int8(0)), arrow.PrimitiveTypes.Int8, false},
		{"int16", reflect.TypeOf(int16(0)), arrow.PrimitiveTypes.Int16, false},
		{"int32", reflect.TypeOf(int32(0)), arrow.PrimitiveTypes.Int32, false},
		{"int64", reflect.TypeOf(int64(0)), arrow.PrimitiveTypes.Int64, false},
		{"int", reflect.TypeOf(int(0)), arrow.PrimitiveTypes.Int64, false},
		{"uint8", reflect.TypeOf(uint8(0)), arrow.PrimitiveTypes.Uint8, false},
		{"uint16", reflect.TypeOf(uint16(0)), arrow.PrimitiveTypes.Uint16, false},
		{"uint32", reflect.TypeOf(uint32(0)), arrow.PrimitiveTypes.Uint32, false},
		{"uint64", reflect.TypeOf(uint64(0)), arrow.PrimitiveTypes.Uint64, false},
		{"uint", reflect.TypeOf(uint(0)), arrow.PrimitiveTypes.Uint64, false},
		{"float32", reflect.TypeOf(float32(0)), arrow.PrimitiveTypes.Float32, false},
		{"float64", reflect.TypeOf(float64(0)), arrow.PrimitiveTypes.Float64, false},
		{"bool", reflect.TypeOf(false), arrow.FixedWidthTypes.Boolean, false},
		{"string", reflect.TypeOf(""), arrow.BinaryTypes.String, false},
		{"chan", reflect.TypeOf(make(chan int)), nil, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dt, err := GoTypeToArrowDataType(tc.typ)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, dt)
			}
		})
	}

	// Test struct mapping containing unexported fields and unsupported fields
	t.Run("struct with unsupported field", func(t *testing.T) {
		type BadStruct struct {
			Ch chan int
		}
		_, err := GoTypeToArrowDataType(reflect.TypeOf(BadStruct{}))
		assert.Error(t, err)
	})
}

func TestAppendGoValueToBuilder_UnsupportedBuilder(t *testing.T) {
	// Custom mock builder that doesn't match any supported type in AppendGoValueToBuilder switch
	type DummyBuilder struct {
		array.Builder
	}
	err := AppendGoValueToBuilder(DummyBuilder{}, reflect.ValueOf(123))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Arrow builder type")
}

func TestArrowStructToGoSlice(t *testing.T) {
	input := []TestStruct{
		{A: 5, B: "hello", c: 999, D: true, E: SubStruct{Val: 3.14}},
		{A: -5, B: "world", c: 888, D: false, E: SubStruct{Val: 2.71}},
	}

	arr, err := SliceToArrowArray(input)
	require.NoError(t, err)
	defer arr.Release()

	structArr := arr.(*array.Struct)
	val := ArrowStructToGoSlice(structArr, reflect.TypeOf(TestStruct{}))
	require.True(t, val.IsValid())

	resSlice, ok := val.Interface().([]TestStruct)
	require.True(t, ok)
	require.Len(t, resSlice, 2)

	// Since 'c' is unexported, it won't be marshaled or unmarshaled, so it should be the zero value (0)
	assert.Equal(t, int8(5), resSlice[0].A)
	assert.Equal(t, "hello", resSlice[0].B)
	assert.Equal(t, int32(0), resSlice[0].c)
	assert.True(t, resSlice[0].D)
	assert.Equal(t, 3.14, resSlice[0].E.Val)

	assert.Equal(t, int8(-5), resSlice[1].A)
	assert.Equal(t, "world", resSlice[1].B)
	assert.Equal(t, int32(0), resSlice[1].c)
	assert.False(t, resSlice[1].D)
	assert.Equal(t, 2.71, resSlice[1].E.Val)
}
