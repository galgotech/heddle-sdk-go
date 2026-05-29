package registry

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/galgotech/heddle-lang/pkg/plugin"
)

type mockRegistry struct {
	steps               map[string]StepRegistration
	resources           map[string]ResourceRegistration
	resourceBindings    map[string]any
	initResourceCalled  bool
	initResourceArgs    struct {
		ID           string
		ResourceType string
		Config       map[string]any
	}
	closeCalled         bool
	startMonitorTimeout time.Duration
	resolveSchemaCalled bool
	resolveSchemaReq    plugin.ResolveSchemaRequest
	resolveSchemaResp   plugin.ResolveSchemaResponse
}

func (m *mockRegistry) Register(instance any) error {
	return nil
}

func (m *mockRegistry) ResolveSchema(request plugin.ResolveSchemaRequest) plugin.ResolveSchemaResponse {
	m.resolveSchemaCalled = true
	m.resolveSchemaReq = request
	return m.resolveSchemaResp
}

func (m *mockRegistry) GetStep(name string) (StepRegistration, bool) {
	step, ok := m.steps[name]
	return step, ok
}

func (m *mockRegistry) AllSteps() map[string]StepRegistration {
	return m.steps
}

func (m *mockRegistry) GetResourceRegistration(name string) (ResourceRegistration, bool) {
	res, ok := m.resources[name]
	return res, ok
}

func (m *mockRegistry) AllResources() map[string]ResourceRegistration {
	return m.resources
}

func (m *mockRegistry) InitResource(id string, resourceType string, config map[string]any) error {
	m.initResourceCalled = true
	m.initResourceArgs.ID = id
	m.initResourceArgs.ResourceType = resourceType
	m.initResourceArgs.Config = config
	if resourceType == "trigger-error" {
		return fmt.Errorf("mock init resource error")
	}
	return nil
}

func (m *mockRegistry) GetResource(id string) (any, error) {
	res, ok := m.resourceBindings[id]
	if !ok {
		return nil, fmt.Errorf("resource not found")
	}
	return res, nil
}

func (m *mockRegistry) CloseAllResources() {
	m.closeCalled = true
}

func (m *mockRegistry) StartResourceMonitor(timeout time.Duration) {
	m.startMonitorTimeout = timeout
}

func TestNewCompositeRegistry(t *testing.T) {
	mockReg1 := &mockRegistry{}
	comp := NewCompositeRegistry(map[string]Registry{
		"ns1": mockReg1,
	})
	assert.NotNil(t, comp)
}

func TestCompositeRegistry_Register(t *testing.T) {
	comp := NewCompositeRegistry(nil)
	err := comp.Register(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Register not supported on composite registry")
}

func TestCompositeRegistry_ResolveSchema(t *testing.T) {
	mockReg1 := &mockRegistry{
		steps: map[string]StepRegistration{
			"stepA": {Name: "stepA"},
		},
		resolveSchemaResp: plugin.ResolveSchemaResponse{
			Error: "no_error_mock1",
		},
	}
	mockReg2 := &mockRegistry{
		steps: map[string]StepRegistration{
			"stepB": {Name: "stepB"},
		},
		resolveSchemaResp: plugin.ResolveSchemaResponse{
			Error: "no_error_mock2",
		},
	}

	comp := NewCompositeRegistry(map[string]Registry{
		"ns1": mockReg1,
		"ns2": mockReg2,
	})

	t.Run("dotted namespace match", func(t *testing.T) {
		req := plugin.ResolveSchemaRequest{
			StepName: "ns1.stepA",
		}
		resp := comp.ResolveSchema(req)
		assert.Equal(t, "no_error_mock1", resp.Error)
		assert.True(t, mockReg1.resolveSchemaCalled)
		assert.Equal(t, "stepA", mockReg1.resolveSchemaReq.StepName)
	})

	t.Run("dotted unknown namespace, but exists in a registry", func(t *testing.T) {
		// Reset mock
		mockReg2.resolveSchemaCalled = false
		mockReg2.steps["ns_unknown.stepB"] = StepRegistration{Name: "ns_unknown.stepB"}

		req := plugin.ResolveSchemaRequest{
			StepName: "ns_unknown.stepB",
		}
		resp := comp.ResolveSchema(req)
		assert.Equal(t, "no_error_mock2", resp.Error)
		assert.True(t, mockReg2.resolveSchemaCalled)
		assert.Equal(t, "ns_unknown.stepB", mockReg2.resolveSchemaReq.StepName)
	})

	t.Run("undotted name matches registry via GetStep", func(t *testing.T) {
		// Reset mock
		mockReg2.resolveSchemaCalled = false

		req := plugin.ResolveSchemaRequest{
			StepName: "stepB",
		}
		resp := comp.ResolveSchema(req)
		assert.Equal(t, "no_error_mock2", resp.Error)
		assert.True(t, mockReg2.resolveSchemaCalled)
		assert.Equal(t, "stepB", mockReg2.resolveSchemaReq.StepName)
	})

	t.Run("step not found in any registry", func(t *testing.T) {
		req := plugin.ResolveSchemaRequest{
			StepName: "missing_step",
		}
		resp := comp.ResolveSchema(req)
		assert.Contains(t, resp.Error, "step missing_step not found")
	})
}

func TestCompositeRegistry_GetStep(t *testing.T) {
	mockReg1 := &mockRegistry{
		steps: map[string]StepRegistration{
			"stepA": {Name: "stepA"},
		},
	}
	mockReg2 := &mockRegistry{
		steps: map[string]StepRegistration{
			"stepB": {Name: "stepB"},
		},
	}

	comp := NewCompositeRegistry(map[string]Registry{
		"ns1": mockReg1,
		"ns2": mockReg2,
	})

	t.Run("dotted namespace match", func(t *testing.T) {
		step, ok := comp.GetStep("ns1.stepA")
		assert.True(t, ok)
		assert.Equal(t, "stepA", step.Name)
	})

	t.Run("undotted name matches registry", func(t *testing.T) {
		step, ok := comp.GetStep("stepB")
		assert.True(t, ok)
		assert.Equal(t, "stepB", step.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, ok := comp.GetStep("missing_step")
		assert.False(t, ok)
	})
}

func TestCompositeRegistry_AllSteps(t *testing.T) {
	mockReg1 := &mockRegistry{
		steps: map[string]StepRegistration{
			"stepA": {Name: "stepA"},
		},
	}
	mockReg2 := &mockRegistry{
		steps: map[string]StepRegistration{
			"stepB": {Name: "stepB"},
		},
	}

	comp := NewCompositeRegistry(map[string]Registry{
		"ns1": mockReg1,
		"ns2": mockReg2,
	})

	all := comp.AllSteps()
	assert.Len(t, all, 2)
	assert.Equal(t, "stepA", all["ns1.stepA"].Name)
	assert.Equal(t, "stepB", all["ns2.stepB"].Name)
}

func TestCompositeRegistry_GetResourceRegistration(t *testing.T) {
	mockReg1 := &mockRegistry{
		resources: map[string]ResourceRegistration{
			"resA": {Name: "resA"},
		},
	}
	mockReg2 := &mockRegistry{
		resources: map[string]ResourceRegistration{
			"resB": {Name: "resB"},
		},
	}

	comp := NewCompositeRegistry(map[string]Registry{
		"ns1": mockReg1,
		"ns2": mockReg2,
	})

	t.Run("dotted namespace match", func(t *testing.T) {
		res, ok := comp.GetResourceRegistration("ns1.resA")
		assert.True(t, ok)
		assert.Equal(t, "resA", res.Name)
	})

	t.Run("undotted name matches registry", func(t *testing.T) {
		res, ok := comp.GetResourceRegistration("resB")
		assert.True(t, ok)
		assert.Equal(t, "resB", res.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, ok := comp.GetResourceRegistration("missing_res")
		assert.False(t, ok)
	})
}

func TestCompositeRegistry_AllResources(t *testing.T) {
	mockReg1 := &mockRegistry{
		resources: map[string]ResourceRegistration{
			"resA": {Name: "resA"},
		},
	}
	mockReg2 := &mockRegistry{
		resources: map[string]ResourceRegistration{
			"resB": {Name: "resB"},
		},
	}

	comp := NewCompositeRegistry(map[string]Registry{
		"ns1": mockReg1,
		"ns2": mockReg2,
	})

	all := comp.AllResources()
	assert.Len(t, all, 2)
	assert.Equal(t, "resA", all["ns1.resA"].Name)
	assert.Equal(t, "resB", all["ns2.resB"].Name)
}

func TestCompositeRegistry_InitResource(t *testing.T) {
	mockReg1 := &mockRegistry{
		resources: map[string]ResourceRegistration{
			"resA": {Name: "resA"},
		},
	}
	mockReg2 := &mockRegistry{
		resources: map[string]ResourceRegistration{
			"resB": {Name: "resB"},
		},
	}

	comp := NewCompositeRegistry(map[string]Registry{
		"ns1": mockReg1,
		"ns2": mockReg2,
	})

	config := map[string]any{"key": "val"}

	t.Run("dotted namespace match", func(t *testing.T) {
		err := comp.InitResource("id1", "ns1.resA", config)
		require.NoError(t, err)
		assert.True(t, mockReg1.initResourceCalled)
		assert.Equal(t, "id1", mockReg1.initResourceArgs.ID)
		assert.Equal(t, "resA", mockReg1.initResourceArgs.ResourceType)
		assert.Equal(t, config, mockReg1.initResourceArgs.Config)
	})

	t.Run("undotted name matches registry", func(t *testing.T) {
		mockReg2.initResourceCalled = false
		err := comp.InitResource("id2", "resB", config)
		require.NoError(t, err)
		assert.True(t, mockReg2.initResourceCalled)
		assert.Equal(t, "id2", mockReg2.initResourceArgs.ID)
		assert.Equal(t, "resB", mockReg2.initResourceArgs.ResourceType)
		assert.Equal(t, config, mockReg2.initResourceArgs.Config)
	})

	t.Run("not found", func(t *testing.T) {
		err := comp.InitResource("id3", "resC", config)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "resource type \"resC\" not found in registries")
	})
}

func TestCompositeRegistry_GetResource(t *testing.T) {
	mockReg1 := &mockRegistry{
		resourceBindings: map[string]any{},
	}
	mockReg2 := &mockRegistry{
		resourceBindings: map[string]any{
			"bind1": "my-resource-instance",
		},
	}

	comp := NewCompositeRegistry(map[string]Registry{
		"ns1": mockReg1,
		"ns2": mockReg2,
	})

	t.Run("found in registry", func(t *testing.T) {
		inst, err := comp.GetResource("bind1")
		require.NoError(t, err)
		assert.Equal(t, "my-resource-instance", inst)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := comp.GetResource("bind2")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "resource binding bind2 not found in registries")
	})
}

func TestCompositeRegistry_CloseAllResources(t *testing.T) {
	mockReg1 := &mockRegistry{}
	mockReg2 := &mockRegistry{}

	comp := NewCompositeRegistry(map[string]Registry{
		"ns1": mockReg1,
		"ns2": mockReg2,
	})

	comp.CloseAllResources()
	assert.True(t, mockReg1.closeCalled)
	assert.True(t, mockReg2.closeCalled)
}

func TestCompositeRegistry_StartResourceMonitor(t *testing.T) {
	mockReg1 := &mockRegistry{}
	mockReg2 := &mockRegistry{}

	comp := NewCompositeRegistry(map[string]Registry{
		"ns1": mockReg1,
		"ns2": mockReg2,
	})

	comp.StartResourceMonitor(42 * time.Second)
	assert.Equal(t, 42*time.Second, mockReg1.startMonitorTimeout)
	assert.Equal(t, 42*time.Second, mockReg2.startMonitorTimeout)
}
