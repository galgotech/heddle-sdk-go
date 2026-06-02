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
	InVal string
}

type OutputA struct {
	OutVal string
}

type PluginASteps struct{}

func (s *PluginASteps) StepA(ctx context.Context, cfg ConfigA, in schema.Frame[InputA], out schema.Frame[OutputA]) error {
	in.Each(func(item InputA) {
		out.Add(OutputA{
			OutVal: item.InVal + "_pA",
		})
	})

	return nil
}

type PluginBSteps struct{}

func (s *PluginBSteps) StepB(ctx context.Context, cfg ConfigA, in schema.Frame[OutputA], out schema.Frame[OutputA]) error {
	in.Each(func(item OutputA) {
		out.Add(OutputA{
			OutVal: item.OutVal + "_pB",
		})
	})

	return nil
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
	inA, _ := schema.NewFrame(nil, []InputA{{InVal: "test"}})
	resA := runner.Execute(ctx, "pluginA.step_a", ConfigA{Param: "xyz"}, inA)
	require.NotNil(t, resA)

	outA, ok := resA.(schema.Frame[OutputA])
	require.True(t, ok)

	var valA string

	outA.Each(func(item OutputA) {
		valA = item.OutVal
	})
	assert.Equal(t, "test_pA", valA)

	// 2. Run StepB on PluginB using fully-qualified namespaced step name (auto-chaining via history / simulated SHM)
	resB := runner.Execute(ctx, "pluginB.step_b", ConfigA{Param: "xyz"}, nil)
	require.NotNil(t, resB)

	outB, ok := resB.(schema.Frame[OutputA])
	require.True(t, ok)

	var valB string

	outB.Each(func(item OutputA) {
		valB = item.OutVal
	})
	assert.Equal(t, "test_pA_pB", valB)
}
