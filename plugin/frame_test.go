package plugin

import (
	"context"
	"testing"

	baseplugin "github.com/galgotech/heddle-lang/pkg/plugin"
	"github.com/galgotech/heddle-lang/pkg/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFieldValue(t *testing.T) {

	t.Run("Int8", func(t *testing.T) {
		data := NewInt8([]int8{1, 2, 3})
		assert.Equal(t, int8(1), data.Value(0))
		assert.Equal(t, int8(2), data.Value(1))
		assert.Equal(t, int8(3), data.Value(2))
	})

	t.Run("Int16", func(t *testing.T) {
		data := NewInt16([]int16{1, 2, 3})
		assert.Equal(t, int16(1), data.Value(0))
		assert.Equal(t, int16(2), data.Value(1))
		assert.Equal(t, int16(3), data.Value(2))
	})

	t.Run("Int32", func(t *testing.T) {
		data := NewInt32([]int32{1, 2, 3})
		assert.Equal(t, int32(1), data.Value(0))
		assert.Equal(t, int32(2), data.Value(1))
		assert.Equal(t, int32(3), data.Value(2))
	})

	t.Run("Int64", func(t *testing.T) {
		data := NewInt64([]int64{10, 20, 30})
		assert.Equal(t, int64(10), data.Value(0))
		assert.Equal(t, int64(20), data.Value(1))
		assert.Equal(t, int64(30), data.Value(2))
	})

	t.Run("Int", func(t *testing.T) {
		data := NewInt64([]int64{10, 20, 30})
		assert.Equal(t, int64(10), data.Value(0))
		assert.Equal(t, int64(20), data.Value(1))
		assert.Equal(t, int64(30), data.Value(2))

	})

	t.Run("Uint8", func(t *testing.T) {
		data := NewUint8([]uint8{1, 2, 3})
		assert.Equal(t, uint8(1), data.Value(0))
		assert.Equal(t, uint8(2), data.Value(1))
		assert.Equal(t, uint8(3), data.Value(2))
	})

	t.Run("Uint16", func(t *testing.T) {
		data := NewUint16([]uint16{1, 2, 3})
		assert.Equal(t, uint16(1), data.Value(0))
		assert.Equal(t, uint16(2), data.Value(1))
		assert.Equal(t, uint16(3), data.Value(2))
	})

	t.Run("Uint32", func(t *testing.T) {
		data := NewUint32([]uint32{1, 2, 3})
		assert.Equal(t, uint32(1), data.Value(0))
		assert.Equal(t, uint32(2), data.Value(1))
		assert.Equal(t, uint32(3), data.Value(2))
	})

	t.Run("Uint64", func(t *testing.T) {
		data := NewUint64([]uint64{10, 20, 30})
		assert.Equal(t, uint64(10), data.Value(0))
		assert.Equal(t, uint64(20), data.Value(1))
		assert.Equal(t, uint64(30), data.Value(2))
	})

	t.Run("Float32", func(t *testing.T) {
		data := NewFloat32([]float32{1.1, 2.2, 3.3})
		assert.InDelta(t, float32(1.1), data.Value(0), 1e-6)
		assert.InDelta(t, float32(2.2), data.Value(1), 1e-6)
		assert.InDelta(t, float32(3.3), data.Value(2), 1e-6)
	})

	t.Run("String", func(t *testing.T) {
		data := NewString([]string{"a", "b", "c"})
		assert.Equal(t, "a", data.Value(0))
		assert.Equal(t, "b", data.Value(1))
		assert.Equal(t, "c", data.Value(2))
	})

	t.Run("Float64", func(t *testing.T) {
		data := NewFloat64([]float64{1.1, 2.2, 3.3})
		assert.Equal(t, 1.1, data.Value(0))
		assert.Equal(t, 2.2, data.Value(1))
		assert.Equal(t, 3.3, data.Value(2))
	})

	t.Run("Bool", func(t *testing.T) {
		data := NewBool([]bool{true, false, true})
		assert.Equal(t, true, data.Value(0))
		assert.Equal(t, false, data.Value(1))
		assert.Equal(t, true, data.Value(2))
	})
}

func TestFieldDelete(t *testing.T) {
	// Test with nil array
	f := NewInt8([]int8{1, 2, 3})
	f.Delete(1)

	bitmap := f.dirt
	assert.NotNil(t, bitmap)
	assert.Equal(t, uint64(2), bitmap[0]) // 1 << 1
}

type TestConfig struct {
	Config
}

type TestInput struct {
	HeddleFrame
	A *Int64
}

type TestOutput struct {
	HeddleFrame
	B *Int64
}

func StepNewSignature(ctx context.Context, cfg TestConfig, input *TestInput, output *TestOutput) error {
	output.B = NewInt64([]int64{1, 2, 3})
	return nil
}

func TestRegisterStep_NewSignature(t *testing.T) {
	p := New("test")
	err := p.RegisterStep("test_step", StepNewSignature)
	require.NoError(t, err)
}

type DynamicQueryConfig struct {
	Config
	Query string `json:"query"`
}

func (c *DynamicQueryConfig) ResolveTypes() (*schema.FrameSchema, *schema.FrameSchema, error) {
	if c.Query == "SELECT name, age" {
		return &schema.FrameSchema{IsVoid: true}, &schema.FrameSchema{
			Fields: []schema.FrameSchemaField{
				{Name: "name", ArrowType: "utf8"},
				{Name: "age", ArrowType: "int64"},
			},
		}, nil
	}
	return nil, nil, nil
}

func DynamicStep(ctx context.Context, cfg DynamicQueryConfig, input *DynamicFrame, output *DynamicFrame) error {
	output.AddColumn("name", NewString([]string{"Alice", "Bob"}))
	output.AddColumn("age", NewInt64([]int64{30, 25}))
	return nil
}

func TestDynamicSchemaExtraction(t *testing.T) {
	p := New("test")
	err := p.RegisterStep("dynamic_step", func(ctx context.Context, cfg DynamicQueryConfig, input *DynamicFrame, output *DynamicFrame) error {
		return nil
	})
	require.NoError(t, err)

	reg, ok := p.steps["dynamic_step"]
	require.True(t, ok)

	assert.True(t, reg.InputSchema.IsDynamic)
	assert.True(t, reg.OutputSchema.IsDynamic)
}

func TestResolveSchema(t *testing.T) {
	p := New("test")
	err := p.RegisterStep("dynamic_step", func(ctx context.Context, cfg DynamicQueryConfig, input *DynamicFrame, output *DynamicFrame) error {
		return nil
	})
	require.NoError(t, err)

	req := baseplugin.ResolveSchemaRequest{
		StepName:   "dynamic_step",
		ConfigJSON: `{"query": "SELECT name, age"}`,
	}

	resp := p.ResolveSchema(req)
	require.Empty(t, resp.Error)
	require.NotNil(t, resp.Output)
	assert.Equal(t, 2, len(resp.Output.Fields))
	assert.Equal(t, "name", resp.Output.Fields[0].Name)
	assert.Equal(t, "age", resp.Output.Fields[1].Name)
}

func TestCompatibleWithDynamic(t *testing.T) {
	dynamic := &schema.FrameSchema{IsDynamic: true}
	typed := &schema.FrameSchema{
		Fields: []schema.FrameSchemaField{
			{Name: "id", ArrowType: "int64"},
		},
	}

	assert.NoError(t, schema.Compatible(dynamic, typed))
	assert.NoError(t, schema.Compatible(typed, dynamic))
	assert.NoError(t, schema.Compatible(dynamic, dynamic))
}
