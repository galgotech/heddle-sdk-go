package local_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/galgotech/heddle-sdk-go/local"
	"github.com/galgotech/heddle-sdk-go/plugin"
	"github.com/galgotech/heddle-sdk-go/schema"
)

type ConfigA struct {
	Param string `json:"param"`
}

type InputA struct {
	InVal *schema.ColString
}

type OutputA struct {
	OutVal *schema.ColString
}

type PluginASteps struct{}

func (s *PluginASteps) StepA(ctx context.Context, cfg ConfigA, in *InputA) *OutputA {
	numRows := in.InVal.Len()
	res := make([]string, numRows)
	for i := 0; i < numRows; i++ {
		res[i] = in.InVal.Value(i) + "_pA"
	}
	return &OutputA{
		OutVal: schema.NewColString(res),
	}
}

type PluginBSteps struct{}

func (s *PluginBSteps) StepB(ctx context.Context, cfg ConfigA, in *OutputA) *OutputA {
	numRows := in.OutVal.Len()
	res := make([]string, numRows)
	for i := 0; i < numRows; i++ {
		res[i] = in.OutVal.Value(i) + "_pB"
	}
	return &OutputA{
		OutVal: schema.NewColString(res),
	}
}

func TestLocalRunnerMultiplePlugins(t *testing.T) {
	pA := plugin.New("pluginA")
	err := pA.Register(&PluginASteps{})
	require.NoError(t, err)

	pB := plugin.New("pluginB")
	err = pB.Register(&PluginBSteps{})
	require.NoError(t, err)

	runner := local.NewLocalRunner(pA, pB)
	ctx := context.Background()

	// 1. Run StepA on PluginA using fully-qualified namespaced step name
	inA := &InputA{
		InVal: schema.NewColString([]string{"test"}),
	}
	resA := runner.Execute(ctx, "pluginA.step_a", ConfigA{Param: "xyz"}, inA)
	require.NotNil(t, resA)
	outA, ok := resA.(*OutputA)
	require.True(t, ok)
	assert.Equal(t, "test_pA", outA.OutVal.Value(0))

	// 2. Run StepB on PluginB using fully-qualified namespaced step name (auto-chaining via history / simulated SHM)
	resB := runner.Execute(ctx, "pluginB.step_b", ConfigA{Param: "xyz"}, nil)
	require.NotNil(t, resB)
	outB, ok := resB.(*OutputA)
	require.True(t, ok)
	assert.Equal(t, "test_pA_pB", outB.OutVal.Value(0))
}
