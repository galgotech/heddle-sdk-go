package schema

import (
	"testing"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
	"github.com/apache/arrow/go/v18/arrow/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type FullBasicTypesStruct struct {
	FInt     int
	FInt8    int8
	FInt16   int16
	FInt32   int32
	FInt64   int64
	FUint    uint
	FUint8   uint8
	FUint16  uint16
	FUint32  uint32
	FUint64  uint64
	FFloat32 float32
	FFloat64 float64
	FBool    bool
	FString  string
}

type StructWithUnexported struct {
	Exported1  int
	unexported string
	Exported2  float64
}

type Embedded struct {
	Val int
}

type StructWithAnonymous struct {
	ExportedField string
	Embedded
}

func TestNewFrame_PointerError(t *testing.T) {
	// Pointers are not allowed as the struct type for a Frame
	frame, err := NewFrame[*FullBasicTypesStruct](nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "is a pointer")
	assert.Empty(t, frame)
}

func TestNewFrame_BasicTypes(t *testing.T) {
	data := []FullBasicTypesStruct{
		{
			FInt:     -1,
			FInt8:    -8,
			FInt16:   -16,
			FInt32:   -32,
			FInt64:   -64,
			FUint:    1,
			FUint8:   8,
			FUint16:  16,
			FUint32:  32,
			FUint64:  64,
			FFloat32: 3.14,
			FFloat64: 6.28,
			FBool:    true,
			FString:  "hello",
		},
		{
			FInt:     100,
			FInt8:    80,
			FInt16:   160,
			FInt32:   320,
			FInt64:   640,
			FUint:    10,
			FUint8:   88,
			FUint16:  168,
			FUint32:  328,
			FUint64:  648,
			FFloat32: 12.34,
			FFloat64: 56.78,
			FBool:    false,
			FString:  "world",
		},
	}

	frame, err := NewFrame(data)
	require.NoError(t, err)
	assert.NotNil(t, frame.state)
	assert.Equal(t, 2, frame.state.length)
	assert.Equal(t, 14, len(frame.state.slices))

	// Verify slices content
	assert.Equal(t, []any{-1, 100}, frame.state.slices["FInt"])
	assert.Equal(t, []any{int8(-8), int8(80)}, frame.state.slices["FInt8"])
	assert.Equal(t, []any{int16(-16), int16(160)}, frame.state.slices["FInt16"])
	assert.Equal(t, []any{int32(-32), int32(320)}, frame.state.slices["FInt32"])
	assert.Equal(t, []any{int64(-64), int64(640)}, frame.state.slices["FInt64"])
	assert.Equal(t, []any{uint(1), uint(10)}, frame.state.slices["FUint"])
	assert.Equal(t, []any{uint8(8), uint8(88)}, frame.state.slices["FUint8"])
	assert.Equal(t, []any{uint16(16), uint16(168)}, frame.state.slices["FUint16"])
	assert.Equal(t, []any{uint32(32), uint32(328)}, frame.state.slices["FUint32"])
	assert.Equal(t, []any{uint64(64), uint64(648)}, frame.state.slices["FUint64"])
	assert.Equal(t, []any{float32(3.14), float32(12.34)}, frame.state.slices["FFloat32"])
	assert.Equal(t, []any{6.28, 56.78}, frame.state.slices["FFloat64"])
	assert.Equal(t, []any{true, false}, frame.state.slices["FBool"])
	assert.Equal(t, []any{"hello", "world"}, frame.state.slices["FString"])

	// Iterate using Each and verify mapping
	var iterated []FullBasicTypesStruct
	err = frame.Each(func(item FullBasicTypesStruct) {
		iterated = append(iterated, item)
	})
	require.NoError(t, err)
	require.Len(t, iterated, 2)
	assert.Equal(t, data[0], iterated[0])
	assert.Equal(t, data[1], iterated[1])

}

func TestFrame_Add(t *testing.T) {
	frame, err := NewFrame([]FullBasicTypesStruct{})
	require.NoError(t, err)
	assert.Equal(t, 0, frame.state.length)

	newItem := FullBasicTypesStruct{
		FInt:     42,
		FInt8:    2,
		FInt16:   3,
		FInt32:   4,
		FInt64:   5,
		FUint:    6,
		FUint8:   7,
		FUint16:  8,
		FUint32:  9,
		FUint64:  10,
		FFloat32: 1.1,
		FFloat64: 2.2,
		FBool:    true,
		FString:  "added",
	}

	frame.Add(newItem)
	assert.Equal(t, 1, frame.state.length)
	assert.Equal(t, []any{42}, frame.state.slices["FInt"])
	assert.Equal(t, []any{"added"}, frame.state.slices["FString"])

	var iterated []FullBasicTypesStruct
	err = frame.Each(func(item FullBasicTypesStruct) {
		iterated = append(iterated, item)
	})
	require.NoError(t, err)
	require.Len(t, iterated, 1)
	assert.Equal(t, newItem, iterated[0])
}

func TestFrame_UnexportedFields(t *testing.T) {
	data := []StructWithUnexported{
		{Exported1: 10, unexported: "secret1", Exported2: 20.5},
		{Exported1: 30, unexported: "secret2", Exported2: 40.5},
	}

	frame, err := NewFrame(data)
	require.NoError(t, err)
	assert.Equal(t, 2, frame.state.length)
	// We expect 2 columns, since the unexported field is ignored
	assert.Equal(t, 2, len(frame.state.slices))

	// Verify slices are mapped correctly
	assert.Equal(t, []any{10, 30}, frame.state.slices["Exported1"])
	assert.Equal(t, []any{20.5, 40.5}, frame.state.slices["Exported2"])

	// Try adding an item
	newItem := StructWithUnexported{Exported1: 50, unexported: "secret3", Exported2: 60.5}
	assert.NotPanics(t, func() {
		frame.Add(newItem)
	})
	assert.Equal(t, 3, frame.state.length)

	// Iterate and make sure unexported fields are not populated (stay as zero value "")
	var iterated []StructWithUnexported
	err = frame.Each(func(item StructWithUnexported) {
		iterated = append(iterated, item)
	})
	require.NoError(t, err)
	require.Len(t, iterated, 3)

	assert.Equal(t, 10, iterated[0].Exported1)
	assert.Equal(t, "", iterated[0].unexported) // stays default/zero value since it was ignored
	assert.Equal(t, 20.5, iterated[0].Exported2)

	assert.Equal(t, 50, iterated[2].Exported1)
	assert.Equal(t, "", iterated[2].unexported)
	assert.Equal(t, 60.5, iterated[2].Exported2)
}

func TestFrame_AnonymousFields(t *testing.T) {
	data := []StructWithAnonymous{
		{
			ExportedField: "foo",
			Embedded:      Embedded{Val: 42},
		},
	}

	frame, err := NewFrame(data)
	require.NoError(t, err)
	// We expect 1 column, since the embedded/anonymous field is ignored
	assert.Equal(t, 1, len(frame.state.slices))
	assert.Equal(t, []any{"foo"}, frame.state.slices["ExportedField"])

	newItem := StructWithAnonymous{
		ExportedField: "bar",
		Embedded:      Embedded{Val: 100},
	}
	frame.Add(newItem)

	var iterated []StructWithAnonymous
	err = frame.Each(func(item StructWithAnonymous) {
		iterated = append(iterated, item)
	})
	require.NoError(t, err)
	require.Len(t, iterated, 2)
	assert.Equal(t, "foo", iterated[0].ExportedField)
	assert.Equal(t, 0, iterated[0].Embedded.Val) // should be zero value since it was ignored
}

type FrameArrayTestStruct struct {
	Age    int32
	Name   string
	Active bool
}

func TestNewFrameArray(t *testing.T) {
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

	bActive := array.NewBooleanBuilder(mem)
	defer bActive.Release()
	bActive.AppendValues([]bool{true, false}, nil)
	arrActive := bActive.NewBooleanArray()
	defer arrActive.Release()

	dataArr := map[string]arrow.Array{
		"Age":    arrAge,
		"Name":   arrName,
		"Active": arrActive,
	}

	var frame Frame[FrameArrayTestStruct]
	err := NewFrameArray(&frame, dataArr)
	require.NoError(t, err)

	assert.Equal(t, 2, frame.state.length)
	assert.Equal(t, 3, len(frame.state.slices))

	assert.Equal(t, []any{int32(30), int32(25)}, frame.state.slices["Age"])
	assert.Equal(t, []any{"Alice", "Bob"}, frame.state.slices["Name"])
	assert.Equal(t, []any{true, false}, frame.state.slices["Active"])

	var iterated []FrameArrayTestStruct
	err = frame.Each(func(item FrameArrayTestStruct) {
		iterated = append(iterated, item)
	})
	require.NoError(t, err)
	require.Len(t, iterated, 2)
	assert.Equal(t, FrameArrayTestStruct{Age: 30, Name: "Alice", Active: true}, iterated[0])
	assert.Equal(t, FrameArrayTestStruct{Age: 25, Name: "Bob", Active: false}, iterated[1])
}

func BenchmarkFrame_Each(b *testing.B) {
	// Create dummy data
	const size = 1000
	data := make([]FullBasicTypesStruct, size)
	for i := 0; i < size; i++ {
		data[i] = FullBasicTypesStruct{
			FInt:     i,
			FInt8:    int8(i),
			FInt16:   int16(i),
			FInt32:   int32(i),
			FInt64:   int64(i),
			FUint:    uint(i),
			FUint8:   uint8(i),
			FUint16:  uint16(i),
			FUint32:  uint32(i),
			FUint64:  uint64(i),
			FFloat32: float32(i),
			FFloat64: float64(i),
			FBool:    i%2 == 0,
			FString:  "benchmark",
		}
	}

	frame, err := NewFrame(data)
	if err != nil {
		b.Fatalf("failed to create frame: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		err = frame.Each(func(item FullBasicTypesStruct) {
			if item.FInt < 0 {
				b.Fail()
			}
		})
		if err != nil {
			b.Fatalf("Each failed: %v", err)
		}
	}
}

