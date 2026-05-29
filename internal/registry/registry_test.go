package registry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/galgotech/heddle-lang/pkg/plugin"
	pluginschema "github.com/galgotech/heddle-sdk-go/schema"
)

// MyTestResource is a mock resource for testing.
type MyTestResource struct {
	InitCalled  bool
	CloseCalled bool
	ConfigVal   string `json:"config_val"`
	IntVal      int    `json:"int_val"`
}

func (r *MyTestResource) Init(ctx context.Context) error {
	r.InitCalled = true
	if r.ConfigVal == "trigger-error" {
		return errors.New("mock init error")
	}
	return nil
}

func (r *MyTestResource) Close() error {
	r.CloseCalled = true
	return nil
}

type TestStepConfig struct {
	Foo string `json:"foo"`
}

type TestStepInput struct {
	Bar string `json:"bar"`
}

type TestStepOutput struct {
	Baz string `json:"baz"`
}

type TestComponent struct {
	Res pluginschema.ResourceSchema[*MyTestResource]
}

func (t TestComponent) SimpleStep(ctx context.Context, config TestStepConfig, in pluginschema.Frame[TestStepInput], out pluginschema.Frame[TestStepOutput]) error {
	return nil
}

type DuplicateResComponent struct {
	Res pluginschema.ResourceSchema[*MyTestResource]
}

type DuplicateStepComponent struct{}

func (d DuplicateStepComponent) SimpleStep(ctx context.Context, config TestStepConfig, in pluginschema.Frame[TestStepInput], out pluginschema.Frame[TestStepOutput]) error {
	return nil
}

type ComponentWithResolve struct{}

func (c ComponentWithResolve) ResolveTypeInput(ctx context.Context, config TestStepConfig, stepName string) ([]pluginschema.ColSchema, error) {
	if config.Foo == "trigger-error" {
		return nil, errors.New("mock dynamic input error")
	}
	return []pluginschema.ColSchema{
		{Name: "dynamic_in_col", Type: "string"},
		{Name: "dynamic_in_int", Type: "int32"},
	}, nil
}

func (c ComponentWithResolve) ResolveTypeOutput(ctx context.Context, config TestStepConfig, stepName string) ([]pluginschema.ColSchema, error) {
	if config.Foo == "trigger-error" {
		return nil, errors.New("mock dynamic output error")
	}
	return []pluginschema.ColSchema{
		{Name: "dynamic_out_col", Type: "string"},
	}, nil
}

func (c ComponentWithResolve) DynamicStep(ctx context.Context, config TestStepConfig, in pluginschema.Frame[TestStepInput], out pluginschema.Frame[TestStepOutput]) error {
	return nil
}

func TestNewRegistry(t *testing.T) {
	reg := NewRegistry()
	require.NotNil(t, reg)

	sReg := reg.(*simpleRegistry)
	assert.NotNil(t, sReg.resources)
	assert.NotNil(t, sReg.resourceBinding)
	assert.NotNil(t, sReg.steps)
}

func TestRegister(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		reg := NewRegistry()
		err := reg.Register(TestComponent{})
		require.NoError(t, err)

		// Verify resource registration
		resources := reg.AllResources()
		assert.Len(t, resources, 1)
		resReg, exists := resources["my_test_resource"]
		assert.True(t, exists)
		assert.Equal(t, "my_test_resource", resReg.Name)

		// Verify step registration
		steps := reg.AllSteps()
		assert.Len(t, steps, 1)
		stepReg, exists := reg.GetStep("simple_step")
		assert.True(t, exists)
		assert.Equal(t, "simple_step", stepReg.Name)
	})

	t.Run("non-struct error", func(t *testing.T) {
		reg := NewRegistry()
		err := reg.Register(42)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Register: expected struct")
	})

	t.Run("duplicate resource error", func(t *testing.T) {
		reg := NewRegistry()
		err := reg.Register(TestComponent{})
		require.NoError(t, err)

		err = reg.Register(DuplicateResComponent{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Resource my_test_resource already registered")
	})

	t.Run("duplicate step error", func(t *testing.T) {
		reg := NewRegistry()
		err := reg.Register(TestComponent{})
		require.NoError(t, err)

		err = reg.Register(DuplicateStepComponent{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "step \"simple_step\" already registered")
	})

	t.Run("skips ResolveTypeInput and ResolveTypeOutput", func(t *testing.T) {
		reg := NewRegistry()
		err := reg.Register(ComponentWithResolve{})
		require.NoError(t, err)

		steps := reg.AllSteps()
		assert.Len(t, steps, 1)
		_, exists := steps["dynamic_step"]
		assert.True(t, exists)

		_, existsInput := steps["resolve_type_input"]
		assert.False(t, existsInput)

		_, existsOutput := steps["resolve_type_output"]
		assert.False(t, existsOutput)
	})
}

func TestResolveSchema(t *testing.T) {
	t.Run("step not found", func(t *testing.T) {
		reg := NewRegistry()
		resp := reg.ResolveSchema(plugin.ResolveSchemaRequest{
			StepName: "non_existent_step",
		})
		assert.NotEmpty(t, resp.Error)
		assert.Contains(t, resp.Error, "step non_existent_step not found")
	})

	t.Run("invalid config json", func(t *testing.T) {
		reg := NewRegistry()
		err := reg.Register(TestComponent{})
		require.NoError(t, err)

		resp := reg.ResolveSchema(plugin.ResolveSchemaRequest{
			StepName:   "simple_step",
			ConfigJSON: "{invalid-json}",
		})
		assert.NotEmpty(t, resp.Error)
		assert.Contains(t, resp.Error, "failed to unmarshal config")
	})

	t.Run("static schema resolution success", func(t *testing.T) {
		reg := NewRegistry()
		err := reg.Register(TestComponent{})
		require.NoError(t, err)

		resp := reg.ResolveSchema(plugin.ResolveSchemaRequest{
			StepName:   "simple_step",
			ConfigJSON: `{"foo": "test"}`,
		})
		assert.Empty(t, resp.Error)
		assert.Len(t, resp.Input.Fields, 1)
		assert.Equal(t, "Bar", resp.Input.Fields[0].Name)
		assert.Equal(t, "utf8", resp.Input.Fields[0].ArrowType)

		assert.Len(t, resp.Output.Fields, 1)
		assert.Equal(t, "Baz", resp.Output.Fields[0].Name)
		assert.Equal(t, "utf8", resp.Output.Fields[0].ArrowType)
	})

	t.Run("dynamic schema resolution success", func(t *testing.T) {
		reg := NewRegistry()
		err := reg.Register(ComponentWithResolve{})
		require.NoError(t, err)

		resp := reg.ResolveSchema(plugin.ResolveSchemaRequest{
			StepName:   "dynamic_step",
			ConfigJSON: `{"foo": "normal"}`,
		})
		assert.Empty(t, resp.Error)

		// Input schema is resolved dynamically
		assert.Len(t, resp.Input.Fields, 2)
		assert.Equal(t, "dynamic_in_col", resp.Input.Fields[0].Name)
		assert.Equal(t, "utf8", resp.Input.Fields[0].ArrowType) // string gets converted to utf8
		assert.Equal(t, "dynamic_in_int", resp.Input.Fields[1].Name)
		assert.Equal(t, "int32", resp.Input.Fields[1].ArrowType)

		// Output schema is resolved dynamically
		assert.Len(t, resp.Output.Fields, 1)
		assert.Equal(t, "dynamic_out_col", resp.Output.Fields[0].Name)
		assert.Equal(t, "utf8", resp.Output.Fields[0].ArrowType)
	})

	t.Run("dynamic schema resolution input error", func(t *testing.T) {
		reg := NewRegistry()
		err := reg.Register(ComponentWithResolve{})
		require.NoError(t, err)

		resp := reg.ResolveSchema(plugin.ResolveSchemaRequest{
			StepName:   "dynamic_step",
			ConfigJSON: `{"foo": "trigger-error"}`,
		})
		assert.NotEmpty(t, resp.Error)
		assert.Contains(t, resp.Error, "mock dynamic input error")
	})
}

func TestInitResource(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		reg := NewRegistry()
		err := reg.Register(TestComponent{})
		require.NoError(t, err)

		config := map[string]any{
			"config_val": "hello",
			"int_val":    123,
		}

		err = reg.InitResource("res-1", "my_test_resource", config)
		require.NoError(t, err)

		res, err := reg.GetResource("res-1")
		require.NoError(t, err)
		testRes, ok := res.(*MyTestResource)
		require.True(t, ok)
		assert.True(t, testRes.InitCalled)
		assert.Equal(t, "hello", testRes.ConfigVal)
		assert.Equal(t, 123, testRes.IntVal)

		// Repeat with the same ID, should return cached instance and not call Init again
		testRes.InitCalled = false
		err = reg.InitResource("res-1", "my_test_resource", config)
		require.NoError(t, err)

		resCached, err := reg.GetResource("res-1")
		require.NoError(t, err)
		testRes2, ok := resCached.(*MyTestResource)
		require.True(t, ok)
		assert.Same(t, testRes, testRes2)
		assert.False(t, testRes.InitCalled)
	})

	t.Run("not found error", func(t *testing.T) {
		reg := NewRegistry()
		err := reg.InitResource("res-1", "unregistered_resource", nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "resource type \"unregistered_resource\" not registered")
	})

	t.Run("config unmarshal error", func(t *testing.T) {
		reg := NewRegistry()
		err := reg.Register(TestComponent{})
		require.NoError(t, err)

		// Passing an invalid type (string instead of int)
		config := map[string]any{
			"int_val": "this-should-fail",
		}
		err = reg.InitResource("res-1", "my_test_resource", config)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal config into resource")
	})

	t.Run("init error", func(t *testing.T) {
		reg := NewRegistry()
		err := reg.Register(TestComponent{})
		require.NoError(t, err)

		config := map[string]any{
			"config_val": "trigger-error",
		}
		err = reg.InitResource("res-1", "my_test_resource", config)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to initialize resource")
	})
}

func TestGetAndSetResource(t *testing.T) {
	reg := NewRegistry()
	err := reg.Register(TestComponent{})
	require.NoError(t, err)

	_, err = reg.GetResource("r1")
	assert.Error(t, err)

	err = reg.InitResource("binding-1", "my_test_resource", nil)
	require.NoError(t, err)

	res, err := reg.GetResource("binding-1")
	require.NoError(t, err)
	assert.NotNil(t, res)

	_, err = reg.GetResource("non-existent-binding")
	assert.Error(t, err)
}

func TestCloseAllResources(t *testing.T) {
	reg := NewRegistry()
	err := reg.Register(TestComponent{})
	require.NoError(t, err)

	err = reg.InitResource("res-1", "my_test_resource", nil)
	require.NoError(t, err)

	err = reg.InitResource("res-2", "my_test_resource", nil)
	require.NoError(t, err)

	res1, err := reg.GetResource("res-1")
	require.NoError(t, err)
	testRes1 := res1.(*MyTestResource)

	res2, err := reg.GetResource("res-2")
	require.NoError(t, err)
	testRes2 := res2.(*MyTestResource)

	assert.False(t, testRes1.CloseCalled)
	assert.False(t, testRes2.CloseCalled)

	reg.CloseAllResources()

	assert.True(t, testRes1.CloseCalled)
	assert.True(t, testRes2.CloseCalled)
}

func TestResourceMonitor(t *testing.T) {
	reg := NewRegistry()
	err := reg.Register(TestComponent{})
	require.NoError(t, err)

	err = reg.InitResource("res-1", "my_test_resource", nil)
	require.NoError(t, err)

	res1, err := reg.GetResource("res-1")
	require.NoError(t, err)
	testRes1 := res1.(*MyTestResource)

	reg.StartResourceMonitor(10 * time.Millisecond)

	// Wait for the monitor to run and evict the resource
	time.Sleep(30 * time.Millisecond)

	// The resource should have been closed and evicted
	assert.True(t, testRes1.CloseCalled)

	_, err = reg.GetResource("res-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "resource binding res-1 not initialized")

	reg.CloseAllResources()
}
