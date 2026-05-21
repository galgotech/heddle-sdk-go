package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
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

	"github.com/galgotech/heddle-sdk-go/internal/resourcelink"
	pluginschema "github.com/galgotech/heddle-sdk-go/schema"
)

type TestResource struct {
	Host string
	Port int
}

func (r *TestResource) Start(ctx context.Context) error { return nil }
func (r *TestResource) Close() error                    { return nil }

type TestResourceGroup struct {
	DB pluginschema.Resource[*TestResource]
}

func TestRegisterResource(t *testing.T) {
	p := New(t.Context(), "test")
	err := p.Register(&TestResourceGroup{})
	require.NoError(t, err)

	reg, ok := p.registry.GetResource("testresource")
	require.True(t, ok)
	assert.Equal(t, "testresource", reg.Name)
	require.NotNil(t, reg.ResourceSchema)
	assert.Equal(t, 2, len(reg.ResourceSchema.Fields))
	assert.Equal(t, "Host", reg.ResourceSchema.Fields[0].Name)
	assert.Equal(t, "Port", reg.ResourceSchema.Fields[1].Name)
}

func TestPluginRegistrationIncludesResources(t *testing.T) {
	p := New(t.Context(), "test")
	err := p.Register(&TestResourceGroup{})
	require.NoError(t, err)

	resources := make(map[string]*schema.ResourceAndConfigSchema)
	for name, res := range p.registry.AllResources() {
		resources[name] = res.ResourceSchema
	}

	reg := baseplugin.PluginRegistration{
		Namespace: p.namespace,
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

func TestLazyResourceAndConcurrency(t *testing.T) {
	ctx := t.Context()
	p := New(ctx, "test")
	group := &TestResourceGroup{}
	resourcelink.Configure(&group.DB, map[string]any{"Host": "127.0.0.1", "Port": 8080})

	err := p.Register(group)
	require.NoError(t, err)

	// Since we shallow-cloned the prototype on registration, the active state is shared.
	// Let's call Get() from multiple goroutines concurrently to verify thread safety!
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := group.DB.Get()
			require.NotNil(t, res)
			assert.Equal(t, "127.0.0.1", res.Host)
			assert.Equal(t, 8080, res.Port)
		}()
	}
	wg.Wait()
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
	Data pluginschema.Col[int64]
}

type TestRegistrationOutput struct {
	Result pluginschema.Col[string]
}

type MyTestGroup struct{}

// MyDocComment is a test doc comment.
func (s *MyTestGroup) MyTestStep(ctx context.Context, config struct{}, input *TestRegistrationInput) (*TestRegistrationOutput, error) {
	return nil, nil
}

func TestRegisterStepMetadata(t *testing.T) {
	p := New(t.Context(), "test")
	err := p.Register(&MyTestGroup{})
	require.NoError(t, err)

	reg, ok := p.registry.GetStep("mytestgroup.myteststep")
	require.True(t, ok)
	assert.Equal(t, "MyDocComment is a test doc comment.\n", reg.Documentation)
	assert.Contains(t, reg.SourceCode, "func (s *MyTestGroup) MyTestStep")
	assert.Contains(t, reg.SourceCode, "return nil, nil")
	assert.Contains(t, reg.SourceFile, "plugin_test.go")
}

type TestBindInput struct {
	A pluginschema.Col[float64]
	B pluginschema.Col[float64]
}

type TestBindOutput struct {
	A pluginschema.Col[float64]
	B pluginschema.Col[float64]
}

type TestFrame struct {
	A pluginschema.Col[float64]
	B pluginschema.Col[float64]
}
