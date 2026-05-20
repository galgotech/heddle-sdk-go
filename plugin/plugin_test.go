package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"reflect"
	"syscall"
	"testing"
	"time"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/flight"
	baseplugin "github.com/galgotech/heddle-lang/pkg/plugin"
	"github.com/galgotech/heddle-lang/pkg/runtime"
	"github.com/galgotech/heddle-lang/pkg/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type TestResource struct {
	Host string
	Port int
}

func (r *TestResource) Start(ctx context.Context) error { return nil }

func TestRegisterResource(t *testing.T) {
	p := New("test")
	err := p.RegisterResource("test_res", TestResource{})
	require.NoError(t, err)

	reg, ok := p.resources["test_res"]
	require.True(t, ok)
	assert.Equal(t, "test_res", reg.Name)
	require.NotNil(t, reg.ResourceSchema)
	assert.Equal(t, 2, len(reg.ResourceSchema.Fields))
	assert.Equal(t, "Host", reg.ResourceSchema.Fields[0].Name)
	assert.Equal(t, "Port", reg.ResourceSchema.Fields[1].Name)
}

func TestPluginRegistrationIncludesResources(t *testing.T) {
	p := New("test")
	err := p.RegisterResource("my_resource", TestResource{})
	require.NoError(t, err)

	// Since we want to test the registration structure, let's manually build it
	// using the same logic as in plugin.go for verification.

	resources := make(map[string]*schema.ResourceAndConfigSchema)
	for name, res := range p.resources {
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
	p := New("test")
	err := p.RegisterResource("test_res", TestResource{})
	require.NoError(t, err)

	ctx := context.Background()
	config := map[string]any{"Host": "127.0.0.1", "Port": 8080}
	err = p.InitializeResource(ctx, "my_active_res", "test_res", config)
	require.NoError(t, err)
	require.NotNil(t, p.activeResources["my_active_res"])

	activeRes, ok := p.activeResources["my_active_res"]
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
	socketPath := runtime.WorkerUDSPath // Use the default for now as it's hardcoded
	_ = os.Remove(socketPath)

	p := New("test-namespace")

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
	HeddleFrame
	Data *Int64
}

type TestRegistrationOutput struct {
	HeddleFrame
	Result *String
}

func TestStepRegistration_NewInputOutput(t *testing.T) {
	reg := &StepRegistration{
		InputType:         reflect.TypeFor[*TestRegistrationInput](),
		OutputType:        reflect.TypeFor[*TestRegistrationOutput](),
		inputFieldsIndex:  []int{1}, // Index 1 is Data
		outputFieldsIndex: []int{1}, // Index 1 is Result
	}

	inputVal, outputVal := reg.newInputOutput()

	// Verify input
	assert.Equal(t, reflect.Pointer, inputVal.Kind())
	assert.Equal(t, reflect.Struct, inputVal.Elem().Kind())
	assert.Equal(t, reflect.Pointer, inputVal.Elem().Field(1).Kind())
	assert.Equal(t, reflect.Struct, inputVal.Elem().Field(1).Elem().Kind())

	data, ok := (inputVal.Elem().Field(1).Interface()).(*Int64)
	assert.True(t, ok)
	assert.NotNil(t, data)

	input := inputVal.Interface().(*TestRegistrationInput)
	assert.NotNil(t, input.Data, "Input field Data should be initialized")

	// Verify output
	assert.Equal(t, reflect.Pointer, outputVal.Kind())
	assert.Equal(t, reflect.Struct, outputVal.Elem().Kind())
	assert.Equal(t, reflect.Pointer, outputVal.Elem().Field(1).Kind())
	assert.Equal(t, reflect.Struct, outputVal.Elem().Field(1).Elem().Kind())

	result, ok := (outputVal.Elem().Field(1).Interface()).(*String)
	assert.True(t, ok)
	assert.NotNil(t, result)

	output := outputVal.Interface().(*TestRegistrationOutput)
	assert.NotNil(t, output.Result, "Output field Result should be initialized")
}

// MyDocComment is a test doc comment.
func MyTestStep(ctx context.Context, config struct{}, input *TestRegistrationInput, output *TestRegistrationOutput) error {
	return nil
}

func TestRegisterStepMetadata(t *testing.T) {
	p := New("test")
	err := p.RegisterStep("my_step", MyTestStep)
	require.NoError(t, err)

	reg, ok := p.steps["my_step"]
	require.True(t, ok)
	assert.Equal(t, "MyDocComment is a test doc comment.\n", reg.Documentation)
	assert.Contains(t, reg.SourceCode, "func MyTestStep")
	assert.Contains(t, reg.SourceCode, "return nil")
	assert.Contains(t, reg.SourceFile, "plugin_test.go")
}

type TestBindInput struct {
	HeddleFrame
	A *Float64
	B *Float64
}

type TestBindOutput struct {
	HeddleFrame
	A *Float64
	B *Float64
}

func TestBindWithRegistrationNewInputOutput(t *testing.T) {
	reg := &StepRegistration{
		InputType:              reflect.TypeFor[*TestBindInput](),
		OutputType:             reflect.TypeFor[*TestBindOutput](),
		inputHeddleFrameIndex:  0,
		outputHeddleFrameIndex: 0,
		inputFieldsIndex:       []int{1, 2},
		outputFieldsIndex:      []int{1, 2},
	}

	columns := make(map[string]arrow.Array)
	columns["A"] = NewFloat64([]float64{1.1, 2.2}).arrayFloat64
	columns["B"] = NewFloat64([]float64{1.1, 2.2}).arrayFloat64

	input, output := reg.newInputOutput()
	assert.NotNil(t, input)
	assert.NotNil(t, output)

	err := bind(input, reg.inputFieldsIndex, columns)
	assert.NoError(t, err)

	assert.Equal(t, 1.1, input.Interface().(*TestBindInput).A.Value(0))
	assert.Equal(t, 2.2, input.Interface().(*TestBindInput).A.Value(1))
	assert.Equal(t, 1.1, input.Interface().(*TestBindInput).B.Value(0))
	assert.Equal(t, 2.2, input.Interface().(*TestBindInput).B.Value(1))

	// Now bind to output
	err = bind(output, reg.outputFieldsIndex, columns)
	assert.NoError(t, err)

	assert.Equal(t, 1.1, output.Interface().(*TestBindOutput).A.Value(0))
	assert.Equal(t, 2.2, output.Interface().(*TestBindOutput).A.Value(1))
	assert.Equal(t, 1.1, output.Interface().(*TestBindOutput).B.Value(0))
	assert.Equal(t, 2.2, output.Interface().(*TestBindOutput).B.Value(1))
}

type TestFrame struct {
	HeddleFrame
	A *Float64
	B *Float64
}

func TestBindAllColumns(t *testing.T) {

	columns := make(map[string]arrow.Array)
	columns["A"] = NewFloat64([]float64{1.1, 2.2}).arrayFloat64
	columns["B"] = NewFloat64([]float64{1.1, 2.2}).arrayFloat64

	frameType := reflect.TypeFor[*TestFrame]()
	frameValue := reflect.New(frameType.Elem())
	v := frameValue.Elem()
	v.Field(1).Set(reflect.New(v.Field(1).Type().Elem()))
	v.Field(2).Set(reflect.New(v.Field(2).Type().Elem()))

	err := bind(frameValue, []int{1, 2}, columns)
	assert.NoError(t, err)

	frame := frameValue.Interface().(*TestFrame)
	assert.NotNil(t, frame.A.arrayFloat64)
	assert.NotNil(t, frame.B.arrayFloat64)
	assert.Equal(t, 2, frame.A.Len())
	assert.Equal(t, 2, frame.B.Len())
}

func TestBindPartialColumns(t *testing.T) {
	columns := make(map[string]arrow.Array)
	columns["A"] = NewFloat64([]float64{1.1, 2.2}).arrayFloat64

	frameType := reflect.TypeFor[*TestFrame]()
	frameValue := reflect.New(frameType.Elem())
	v := frameValue.Elem()
	v.Field(1).Set(reflect.New(v.Field(1).Type().Elem()))
	v.Field(2).Set(reflect.New(v.Field(2).Type().Elem()))

	err := bind(frameValue, []int{1, 2}, columns)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "column \"B\" is required but missing")
}
