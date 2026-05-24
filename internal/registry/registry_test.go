package registry

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/galgotech/heddle-sdk-go/schema"
	pluginschema "github.com/galgotech/heddle-sdk-go/schema"
)

type TestInput struct {
	Name pluginschema.ColString
}

type TestOutput struct {
	Age pluginschema.ColInt64
}

type TestResource struct{}

func (r *TestResource) Init(ctx context.Context) error {
	return nil
}

func (r *TestResource) Close() error {
	return nil
}

type TestStep struct {
	TestResource schema.Resource[*TestResource]
}

// MyStep is a test step.
func (g *TestStep) MyStep(ctx context.Context, config struct{}, input *TestInput) *TestOutput {
	return &TestOutput{}
}

func TestRegister(t *testing.T) {
	reg := NewRegistry()

	// Test successful registration
	err := reg.Register(&TestStep{})
	require.NoError(t, err)

	// Verify step exists and all registered fields are correct
	step, ok := reg.GetStep("my_step")
	require.True(t, ok)
	assert.Equal(t, "my_step", step.Name)
	assert.True(t, step.Func.IsValid())
	assert.NotNil(t, step.ConfigSchema)
	assert.Equal(t, reflect.TypeFor[struct{}](), step.ConfigType)
	assert.NotNil(t, step.InputSchema)
	assert.Equal(t, reflect.TypeFor[*TestInput](), step.InputType)
	assert.NotNil(t, step.OutputSchema)
	assert.Equal(t, reflect.TypeFor[*TestOutput](), step.OutputType)
	assert.Equal(t, "MyStep is a test step.\n", step.Documentation)
	assert.Contains(t, step.SourceCode, "func (g *TestStep) MyStep")
	assert.Contains(t, step.SourceFile, "registry_test.go")
	assert.True(t, step.SourceLine > 0)
	assert.Equal(t, []int{0}, step.InputFieldsIndex)
	assert.Equal(t, []int{0}, step.OutputFieldsIndex)
	assert.Equal(t, reflect.TypeFor[TestStep](), step.StructVal.Type())

	allSteps := reg.AllSteps()
	assert.Contains(t, allSteps, "my_step")

	// Test invalid registration (non-pointer)
	err = reg.Register(TestStep{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected pointer to struct")

	// Test invalid registration (pointer to non-struct)
	nonStructVal := 42
	err = reg.Register(&nonStructVal)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected pointer to struct")
}
