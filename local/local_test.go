package local_test

import (
	"context"
	"testing"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
	"github.com/apache/arrow/go/v18/arrow/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/galgotech/heddle-sdk-go/internal/registry"
	"github.com/galgotech/heddle-sdk-go/local"
	"github.com/galgotech/heddle-sdk-go/plugin"
	"github.com/galgotech/heddle-sdk-go/schema"
)

type ConfigA struct {
	Param string `json:"param"`
}

type InputA struct {
	InVal string
}

type OutputA struct {
	OutVal string
}

type PluginASteps struct {
	Param string `json:"param"`
}

func (s PluginASteps) StepA(ctx context.Context, in schema.FrameInput[InputA], out schema.FrameOutput[OutputA]) error {
	in.Each(func(item InputA) error {
		out.Add(OutputA{
			OutVal: item.InVal + "_pA",
		})
		return nil
	})

	return nil
}

type PluginBSteps struct {
	Param string `json:"param"`
}

func (s PluginBSteps) StepB(ctx context.Context, in schema.FrameInput[OutputA], out schema.FrameOutput[OutputA]) error {
	in.Each(func(item OutputA) error {
		out.Add(OutputA{
			OutVal: item.OutVal + "_pB",
		})
		return nil
	})

	return nil
}

func TestLocalRunnerMultiplePlugins(t *testing.T) {
	pA := plugin.New("pluginA")
	regA := pA.Registry()
	err := regA.RegisterStep(registry.StepRegistration{
		Name: "step_a",
		Invoke: func(ctx context.Context, configJSON string, inColumns map[string]arrow.Array) (map[string]arrow.Array, error) {
			in_Val := inColumns["InVal"].(*array.String)
			outBuilder_OutVal := array.NewStringBuilder(memory.DefaultAllocator)
			defer outBuilder_OutVal.Release()

			for i := 0; i < in_Val.Len(); i++ {
				outBuilder_OutVal.Append(in_Val.Value(i) + "_pA")
			}

			return map[string]arrow.Array{
				"OutVal": outBuilder_OutVal.NewArray(),
			}, nil
		},
	})
	require.NoError(t, err)

	pB := plugin.New("pluginB")
	regB := pB.Registry()
	err = regB.RegisterStep(registry.StepRegistration{
		Name: "step_b",
		Invoke: func(ctx context.Context, configJSON string, inColumns map[string]arrow.Array) (map[string]arrow.Array, error) {
			in_OutVal := inColumns["OutVal"].(*array.String)
			outBuilder_OutVal := array.NewStringBuilder(memory.DefaultAllocator)
			defer outBuilder_OutVal.Release()

			for i := 0; i < in_OutVal.Len(); i++ {
				outBuilder_OutVal.Append(in_OutVal.Value(i) + "_pB")
			}

			return map[string]arrow.Array{
				"OutVal": outBuilder_OutVal.NewArray(),
			}, nil
		},
	})
	require.NoError(t, err)

	runner := local.NewLocalRunner(pA, pB)
	ctx := context.Background()

	// 1. Run StepA on PluginA using fully-qualified namespaced step name
	inBuilder := array.NewStringBuilder(memory.DefaultAllocator)
	inBuilder.Append("test")
	defer inBuilder.Release()

	inA := map[string]arrow.Array{
		"InVal": inBuilder.NewArray(),
	}

	resA := runner.Execute(ctx, "pluginA.step_a", `{"param":"xyz"}`, inA)
	require.NotNil(t, resA)

	outAArr, ok := resA["OutVal"].(*array.String)
	require.True(t, ok)
	assert.Equal(t, "test_pA", outAArr.Value(0))

	// 2. Run StepB on PluginB using fully-qualified namespaced step name (auto-chaining via history / simulated SHM)
	resB := runner.Execute(ctx, "pluginB.step_b", `{"param":"xyz"}`, nil)
	require.NotNil(t, resB)

	outBArr, ok := resB["OutVal"].(*array.String)
	require.True(t, ok)
	assert.Equal(t, "test_pA_pB", outBArr.Value(0))
}
