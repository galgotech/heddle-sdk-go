package plugin

import (
	"context"
	"testing"

	baseplugin "github.com/galgotech/heddle-lang/pkg/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type TestPanicInput struct {
	HeddleFrame
}

type TestPanicOutput struct {
	HeddleFrame
}

func PanickingStep(ctx context.Context, config struct{}, input *TestPanicInput, output *TestPanicOutput) error {
	panic("something went horribly wrong")
}

func TestExecuteTask_PanicRecovery(t *testing.T) {
	p := New("test")
	err := p.RegisterStep("panicking_step", PanickingStep)
	require.NoError(t, err)

	req := baseplugin.ExecuteStepRequest{
		TaskID:   "task-123",
		StepName: "panicking_step",
	}

	resp := p.executor.ExecuteTask(context.Background(), req)

	assert.Equal(t, "task-123", resp.TaskID)
	assert.Equal(t, baseplugin.StepResponseError, resp.Status)
	assert.Contains(t, resp.ErrorMessage, "panic: something went horribly wrong")
}

func TestExecuteStepDirectly_PanicRecovery(t *testing.T) {
	p := New("test")
	err := p.RegisterStep("panicking_step", PanickingStep)
	require.NoError(t, err)

	input := &TestPanicInput{}
	output := &TestPanicOutput{}

	err = p.ExecuteStepDirectly(context.Background(), "panicking_step", "", "", input, output)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "panic: something went horribly wrong")
}
