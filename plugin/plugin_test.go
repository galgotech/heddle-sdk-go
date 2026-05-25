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

	"github.com/galgotech/heddle-sdk-go/network"
	pluginschema "github.com/galgotech/heddle-sdk-go/schema"
)

type TestResource struct {
	Host string
	Port int
}

func (r *TestResource) Init(ctx context.Context) error {
	return nil
}
func (r *TestResource) Close() error {
	return nil
}

type TestResourceGroup struct {
	DB pluginschema.Resource[*TestResource]
}

func TestRegisterResource(t *testing.T) {
	p := New("test")
	err := p.Register(&TestResourceGroup{})
	require.NoError(t, err)

	reg, ok := p.Registry().GetResource("testresource")
	require.True(t, ok)
	assert.Equal(t, "testresource", reg.Name)
	require.NotNil(t, reg.FieldSchema)
	assert.Equal(t, 2, len(reg.FieldSchema.Fields))
	assert.Equal(t, "Host", reg.FieldSchema.Fields[0].Name)
	assert.Equal(t, "Port", reg.FieldSchema.Fields[1].Name)
}

func TestPluginRegistrationIncludesResources(t *testing.T) {
	p := New("test")
	err := p.Register(&TestResourceGroup{})
	require.NoError(t, err)

	resources := make(map[string]schema.FieldSchema)
	for name, res := range p.Registry().AllResources() {
		resources[name] = res.FieldSchema
	}

	reg := baseplugin.PluginRegistration{
		Resources: resources,
	}

	assert.Contains(t, reg.Resources, "testresource")
	assert.NotNil(t, reg.Resources["testresource"])

	// Test JSON marshaling
	body, err := json.Marshal(reg)
	require.NoError(t, err)
	assert.Contains(t, string(body), "resources")
	assert.Contains(t, string(body), "testresource")
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

	p := New("test-namespace")

	errChan := make(chan error, 1)
	go func() {
		errChan <- network.Run(context.Background(), p)
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
	Data pluginschema.ColInt64
}

type TestRegistrationOutput struct {
	Result pluginschema.ColString
}

type MyTestGroup struct{}

// MyDocComment is a test doc comment.
func (s *MyTestGroup) MyTestStep(ctx context.Context, config struct{}, input *TestRegistrationInput) *TestRegistrationOutput {
	return &TestRegistrationOutput{}
}

func TestRegisterStepMetadata(t *testing.T) {
	p := New("test")
	err := p.Register(&MyTestGroup{})
	require.NoError(t, err)

	reg, ok := p.Registry().GetStep("my_test_step")
	require.True(t, ok)
	assert.Equal(t, "MyDocComment is a test doc comment.\n", reg.Documentation)
	assert.Contains(t, reg.SourceCode, "func (s *MyTestGroup) MyTestStep")
	assert.Contains(t, reg.SourceCode, "return &TestRegistrationOutput{}")
	assert.Contains(t, reg.SourceFile, "plugin_test.go")
}

type AnotherGroup struct{}

// AnotherGroup also defines MyTestStep — should conflict with MyTestGroup.
func (s *AnotherGroup) MyTestStep(ctx context.Context, config struct{}, input *TestRegistrationInput) *TestRegistrationOutput {
	return &TestRegistrationOutput{}
}

func TestRegisterDuplicateStepName(t *testing.T) {
	p := New("test")
	err := p.Register(&MyTestGroup{})
	require.NoError(t, err)

	err = p.Register(&AnotherGroup{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "my_test_step")
	assert.Contains(t, err.Error(), "already registered")
}

type TestBindInput struct {
	A pluginschema.ColFloat64
	B pluginschema.ColFloat64
}

type TestBindOutput struct {
	A pluginschema.ColFloat64
	B pluginschema.ColFloat64
}

type TestFrame struct {
	A pluginschema.ColFloat64
	B pluginschema.ColFloat64
}
