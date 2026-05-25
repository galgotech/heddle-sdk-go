package executor_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/galgotech/heddle-sdk-go/internal/executor"
	"github.com/galgotech/heddle-sdk-go/internal/executor/execute"
	"github.com/galgotech/heddle-sdk-go/local"
	"github.com/galgotech/heddle-sdk-go/plugin"
	"github.com/galgotech/heddle-sdk-go/schema"
)

type DummyConfig struct {
	Val string `json:"val"`
}

type StepAInput struct {
	InVal *schema.ColString
}

type StepAOutput struct {
	OutVal *schema.ColString
}

type StepBInput struct {
	OutVal *schema.ColString
}

type StepBOutput struct {
	FinalVal *schema.ColString
}

type DummySteps struct{}

func (s *DummySteps) StepA(ctx context.Context, cfg DummyConfig, in *StepAInput) *StepAOutput {
	numRows := in.InVal.Len()
	res := make([]string, numRows)
	for i := 0; i < numRows; i++ {
		res[i] = in.InVal.Value(i) + "_processed_a"
	}
	return &StepAOutput{
		OutVal: schema.NewColString(res),
	}
}

func (s *DummySteps) StepB(ctx context.Context, cfg DummyConfig, in *StepBInput) *StepBOutput {
	numRows := in.OutVal.Len()
	res := make([]string, numRows)
	for i := 0; i < numRows; i++ {
		res[i] = in.OutVal.Value(i) + "_processed_b"
	}
	return &StepBOutput{
		FinalVal: schema.NewColString(res),
	}
}

func (s *DummySteps) StepC(ctx context.Context, cfg DummyConfig, in *StepBInput) *StepBOutput {
	numRows := in.OutVal.Len()
	res := make([]string, numRows)
	for i := 0; i < numRows; i++ {
		res[i] = in.OutVal.Value(i) + "_processed_c"
	}
	return &StepBOutput{
		FinalVal: schema.NewColString(res),
	}
}

func TestExecutorSimulationAndTimeTravel(t *testing.T) {
	p := plugin.New("ns")
	steps := &DummySteps{}
	err := p.Register(steps)
	require.NoError(t, err)

	exec := local.NewLocalRunner(p)

	ctx := context.Background()

	// 1. Run Step A with manual input
	inA := &StepAInput{
		InVal: schema.NewColString([]string{"hello", "world"}),
	}
	resA := exec.Execute(ctx, "step_a", DummyConfig{Val: "test"}, inA)
	require.NotNil(t, resA)

	outA, ok := resA.(*StepAOutput)
	require.True(t, ok)
	assert.Equal(t, 2, outA.OutVal.Len())
	assert.Equal(t, "hello_processed_a", outA.OutVal.Value(0))
	assert.Equal(t, "world_processed_a", outA.OutVal.Value(1))

	// Verify history updated
	history := exec.GetHistory()
	assert.Equal(t, []string{"step_a"}, history)

	// Verify simulated SHM has Step A's output
	shm := exec.GetSimulatedSHM()
	require.NotNil(t, shm)
	assert.Contains(t, shm, "OutVal")
	assert.Contains(t, shm, "OutVal_id")

	// 2. Run Step B with nil input -> should auto-chain from Step A
	resB := exec.Execute(ctx, "step_b", DummyConfig{Val: "test"}, nil)
	require.NotNil(t, resB)

	outB, ok := resB.(*StepBOutput)
	require.True(t, ok)
	assert.Equal(t, 2, outB.FinalVal.Len())
	assert.Equal(t, "hello_processed_a_processed_b", outB.FinalVal.Value(0))
	assert.Equal(t, "world_processed_a_processed_b", outB.FinalVal.Value(1))

	history = exec.GetHistory()
	assert.Equal(t, []string{"step_a", "step_b"}, history)

	// 3. Time travel: Go back to step_a and run step_c (which also takes OutVal from step_a)
	err = exec.SetHistoryCursor(0) // Step A
	require.NoError(t, err)

	resC := exec.Execute(ctx, "step_c", DummyConfig{Val: "test"}, nil)
	require.NotNil(t, resC)

	outC, ok := resC.(*StepBOutput)
	require.True(t, ok)
	assert.Equal(t, 2, outC.FinalVal.Len())
	assert.Equal(t, "hello_processed_a_processed_c", outC.FinalVal.Value(0))
	assert.Equal(t, "world_processed_a_processed_c", outC.FinalVal.Value(1))

	// History should truncate step_b and append step_c
	history = exec.GetHistory()
	assert.Equal(t, []string{"step_a", "step_c"}, history)

	// 4. Test ClearHistory
	exec.ClearHistory()
	assert.Empty(t, exec.GetHistory())
	assert.Nil(t, exec.GetSimulatedSHM())
}

func TestExecutorChainingWithStepReference(t *testing.T) {
	p := plugin.New("ns")
	steps := &DummySteps{}
	err := p.Register(steps)
	require.NoError(t, err)

	execA := local.NewLocalRunner(p)
	execB := local.NewLocalRunner(p)

	ctx := context.Background()

	inA := &StepAInput{
		InVal: schema.NewColString([]string{"apple", "banana"}),
	}

	// Pack input data
	ref := executor.PackData(inA)
	require.NotNil(t, ref)
	assert.Contains(t, ref.Columns, "InVal")

	// Execute StepA with reference on execA
	ref = execA.Execute(ctx, "step_a", DummyConfig{Val: "test"}, ref).(*execute.StepReference)
	require.NotNil(t, ref)
	assert.Contains(t, ref.Columns, "OutVal")

	// Execute StepB with reference on a different executor (execB)
	ref = execB.Execute(ctx, "step_b", DummyConfig{Val: "test"}, ref).(*execute.StepReference)
	require.NotNil(t, ref)
	assert.Contains(t, ref.Columns, "FinalVal")

	// Unpack final result
	finalRaw := executor.UnpackData(ref)
	outB, ok := finalRaw.(*StepBOutput)
	require.True(t, ok)

	assert.Equal(t, 2, outB.FinalVal.Len())
	assert.Equal(t, "apple_processed_a_processed_b", outB.FinalVal.Value(0))
	assert.Equal(t, "banana_processed_a_processed_b", outB.FinalVal.Value(1))

	// Test packing nil input
	nilRef := executor.PackData(nil)
	require.NotNil(t, nilRef)
	assert.Empty(t, nilRef.Columns)
	assert.Nil(t, nilRef.Data)
}
