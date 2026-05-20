package plugin

import (
	"context"
	"testing"

	baseplugin "github.com/galgotech/heddle-lang/pkg/plugin"
	"github.com/galgotech/heddle-lang/pkg/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
