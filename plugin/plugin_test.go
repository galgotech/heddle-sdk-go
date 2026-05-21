package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/apache/arrow/go/v18/arrow/flight"
	baseplugin "github.com/galgotech/heddle-lang/pkg/plugin"
	"github.com/galgotech/heddle-lang/pkg/runtime"
	"github.com/galgotech/heddle-lang/pkg/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	pluginschema "github.com/galgotech/heddle-sdk-go/schema"
)

type TestResource struct {
	Host string
	Port int
}

func (r *TestResource) Start(ctx context.Context) error { return nil }
func (r *TestResource) Close() error                    { return nil }

func TestRegisterResource(t *testing.T) {
	p := New(t.Context(), "test")
	err := p.RegisterResource("test_res", TestResource{})
	require.NoError(t, err)

	reg, ok := p.registry.GetResource("test_res")
	require.True(t, ok)
	assert.Equal(t, "test_res", reg.Name)
	require.NotNil(t, reg.ResourceSchema)
	assert.Equal(t, 2, len(reg.ResourceSchema.Fields))
	assert.Equal(t, "Host", reg.ResourceSchema.Fields[0].Name)
	assert.Equal(t, "Port", reg.ResourceSchema.Fields[1].Name)
}

func TestPluginRegistrationIncludesResources(t *testing.T) {
	p := New(t.Context(), "test")
	err := p.RegisterResource("my_resource", TestResource{})
	require.NoError(t, err)

	// Since we want to test the registration structure, let's manually build it
	// using the same logic as in plugin.go for verification.

	resources := make(map[string]*schema.ResourceAndConfigSchema)
	for name, res := range p.registry.AllResources() {
		resources[name] = res.ResourceSchema
	}

	reg := baseplugin.PluginRegistration{
		Namespace: p.Namespace,
		Resources: resources,
	}

	assert.Contains(t, reg.Resources, "my_resource")
	assert.NotNil(t, reg.Resources["my_resource"])

	// Test JSON marshaling
	body, err := json.Marshal(reg)
	require.NoError(t, err)
	assert.Contains(t, string(body), "resources")
	assert.Contains(t, string(body), "my_resource")
}

func TestInitializeResource(t *testing.T) {
	p := New(t.Context(), "test")
	err := p.RegisterResource("test_res", TestResource{})
	require.NoError(t, err)

	config := map[string]any{"Host": "127.0.0.1", "Port": 8080}
	err = p.InitializeResource("my_active_res", "test_res", config)
	require.NoError(t, err)
	activeRes, ok := p.resourceManager.Get("my_active_res")
	require.True(t, ok)

	testRes, ok := activeRes.(*TestResource)
	require.True(t, ok)
	assert.Equal(t, "127.0.0.1", testRes.Host)
	assert.Equal(t, 8080, testRes.Port)
}

type mockFlightServer struct {
	flight.BaseFlightServer
	registered bool
}

func (s *mockFlightServer) DoAction(req *flight.Action, stream flight.FlightService_DoActionServer) error {
	if req.Type == baseplugin.ActionRegisterPlugin {
		s.registered = true
		return stream.Send(&flight.Result{Body: []byte("ok")})
	}
	return fmt.Errorf("unknown action: %s", req.Type)
}

func (s *mockFlightServer) DoExchange(stream flight.FlightService_DoExchangeServer) error {
	return nil
}

func TestPluginConnectRetry(t *testing.T) {
	socketPath := runtime.WorkerUDSPath
	if len(socketPath) > 7 && socketPath[:7] == "unix://" {
		socketPath = socketPath[7:]
	}
	_ = os.Remove(socketPath)

	p := New(t.Context(), "test-namespace")

	errChan := make(chan error, 1)
	go func() {
		errChan <- p.Start()
	}()

	// Wait a bit to ensure it failed at least once (internally)
	time.Sleep(1 * time.Second)

	// Now start the server
	lis, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer lis.Close()
	defer os.Remove(socketPath)

	server := grpc.NewServer()
	mock := &mockFlightServer{}
	flight.RegisterFlightServiceServer(server, mock)

	go server.Serve(lis)
	defer server.Stop()

	// The plugin should now connect and signal readiness
	select {
	case <-p.Ready:
		assert.True(t, mock.registered)
	case <-time.After(10 * time.Second):
		t.Fatal("Plugin failed to signal readiness within timeout")
	}

	// --- TEST RECONNECTION ---

	// Reset mock registration state
	mock.registered = false

	// Stop the server (causing exchange stream error)
	server.Stop()
	lis.Close()

	// Wait a bit for the plugin to detect failure and start retrying
	time.Sleep(2 * time.Second)

	// Restart the server
	lis, err = net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer lis.Close()

	server = grpc.NewServer()
	flight.RegisterFlightServiceServer(server, mock)
	go server.Serve(lis)
	defer server.Stop()

	// The plugin should eventually reconnect and register again
	// We wait for mock.registered to become true (polling is easiest here for mock)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if mock.registered {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	assert.True(t, mock.registered, "Plugin failed to reconnect and re-register")

	// --- STOP PLUGIN VIA SIGNAL ---
	syscall.Kill(syscall.Getpid(), syscall.SIGINT)

	select {
	case err := <-errChan:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("Plugin failed to exit after signal")
	}
}

type TestRegistrationInput struct {
	pluginschema.HeddleFrame
	Data *pluginschema.Int64
}

type TestRegistrationOutput struct {
	pluginschema.HeddleFrame
	Result *pluginschema.String
}

// MyDocComment is a test doc comment.
func MyTestStep(ctx context.Context, config struct{}, input *TestRegistrationInput, output *TestRegistrationOutput) error {
	return nil
}

func TestRegisterStepMetadata(t *testing.T) {
	p := New(t.Context(), "test")
	err := p.RegisterStep("my_step", MyTestStep)
	require.NoError(t, err)

	reg, ok := p.registry.GetStep("my_step")
	require.True(t, ok)
	assert.Equal(t, "MyDocComment is a test doc comment.\n", reg.Documentation)
	assert.Contains(t, reg.SourceCode, "func MyTestStep")
	assert.Contains(t, reg.SourceCode, "return nil")
	assert.Contains(t, reg.SourceFile, "plugin_test.go")
}

type TestBindInput struct {
	pluginschema.HeddleFrame
	A *pluginschema.Float64
	B *pluginschema.Float64
}

type TestBindOutput struct {
	pluginschema.HeddleFrame
	A *pluginschema.Float64
	B *pluginschema.Float64
}

type TestFrame struct {
	pluginschema.HeddleFrame
	A *pluginschema.Float64
	B *pluginschema.Float64
}
