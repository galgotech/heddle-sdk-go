package schema

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/galgotech/heddle-sdk-go/internal/resourcelink"
)

type TestResource struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	StartCalls int
	CloseCalls int
	StartErr   error
	CloseErr   error
}

func (r *TestResource) Start(ctx context.Context) error {
	r.StartCalls++
	return r.StartErr
}

func (r *TestResource) Close() error {
	r.CloseCalls++
	return r.CloseErr
}

type MapResource map[string]string

func (m MapResource) Start(ctx context.Context) error { return nil }
func (m MapResource) Close() error                    { return nil }

func TestIsNil(t *testing.T) {
	assert.True(t, isNil(nil))

	var ptr *TestResource
	assert.True(t, isNil(ptr))

	var ch chan int
	assert.True(t, isNil(ch))

	var m map[string]int
	assert.True(t, isNil(m))

	var sl []int
	assert.True(t, isNil(sl))

	var fn func()
	assert.True(t, isNil(fn))

	var interf any = ptr
	assert.True(t, isNil(interf))

	assert.False(t, isNil(&TestResource{}))
	assert.False(t, isNil(123))
	assert.False(t, isNil(MapResource{}))
}

func TestResourceZeroValue(t *testing.T) {
	var r Resource[*TestResource]

	assert.Nil(t, r.Get())
	assert.NoError(t, r.Start(context.Background()))
	assert.NoError(t, r.Close())
}

func TestResourceInitStateAndDefaultInstancePointer(t *testing.T) {
	def := &TestResource{Host: "default-host", Port: 80}
	r := Resource[*TestResource]{
		Resource: def,
	}

	r.initState(10 * time.Minute)
	require.NotNil(t, r.state)
	assert.Equal(t, 10*time.Minute, r.state.ttl)
	assert.Equal(t, def, r.state.defaultInstance)

	inst := r.Get()
	require.NotNil(t, inst)
	assert.NotSame(t, def, inst, "Get should clone the defaultInstance, not return the exact same pointer")
	assert.Equal(t, "default-host", inst.Host)
	assert.Equal(t, 80, inst.Port)
	assert.Equal(t, 1, inst.StartCalls)
	assert.NotNil(t, r.state.stopTicker)

	// Second get should return the same instance without calling Start again
	inst2 := r.Get()
	assert.Same(t, inst, inst2)
	assert.Equal(t, 1, inst.StartCalls)

	// Test Start method doesn't crash or re-init if already initialized
	assert.NoError(t, r.Start(context.Background()))

	// Close resource
	assert.NoError(t, r.Close())
	assert.Equal(t, 1, inst.CloseCalls)
	assert.Nil(t, r.state.stopTicker)
	assert.Nil(t, r.state.instance)
}

func TestResourceInitStateAndDefaultInstanceValue(t *testing.T) {
	def := MapResource{"key": "val-default"}
	r := Resource[MapResource]{
		Resource: def,
	}

	r.initState(5 * time.Minute)
	require.NotNil(t, r.state)
	assert.Equal(t, def, r.state.defaultInstance)

	inst := r.Get()
	assert.Equal(t, "val-default", inst["key"])

	assert.NoError(t, r.Close())
}

func TestResourceLazyInitializationNoDefault(t *testing.T) {
	var r Resource[*TestResource]
	r.initState(0) // Should default to 15 * time.Minute during Get

	inst := r.Get()
	require.NotNil(t, inst)
	assert.Equal(t, time.Duration(0), r.state.ttl)
	assert.Equal(t, "", inst.Host)
	assert.Equal(t, 0, inst.Port)
	assert.Equal(t, 1, inst.StartCalls)

	assert.NoError(t, r.Close())
}

func TestResourceConfigure(t *testing.T) {
	var r Resource[*TestResource]
	r.configure(map[string]any{"host": "configured-host", "port": 8080})

	inst := r.Get()
	require.NotNil(t, inst)
	assert.Equal(t, "configured-host", inst.Host)
	assert.Equal(t, 8080, inst.Port)
	assert.Equal(t, 1, inst.StartCalls)

	assert.NoError(t, r.Close())
}

func TestResourceSet(t *testing.T) {
	var r Resource[*TestResource]
	custom := &TestResource{Host: "custom"}
	r.set(custom)

	inst := r.Get()
	assert.Same(t, custom, inst)
	assert.Equal(t, 0, custom.StartCalls, "set instance shouldn't call Start automatically in Get")

	assert.NoError(t, r.Close())
	assert.Equal(t, 1, custom.CloseCalls)
}

func TestResourceGetTTLExpiration(t *testing.T) {
	var r Resource[*TestResource]
	r.initState(10 * time.Millisecond)

	inst := r.Get()
	require.NotNil(t, inst)
	assert.Equal(t, 1, inst.StartCalls)

	// Manually backdate lastUsed to trigger expiration
	r.state.mu.Lock()
	r.state.lastUsed = time.Now().Add(-1 * time.Hour)
	r.state.mu.Unlock()

	inst2 := r.Get()
	require.NotNil(t, inst2)
	assert.NotSame(t, inst, inst2)
	assert.Equal(t, 1, inst.CloseCalls, "Original instance should have been closed")
	assert.Equal(t, 1, inst2.StartCalls, "New instance should have been started")

	assert.NoError(t, r.Close())
}

func TestResourceStartFailure(t *testing.T) {
	errStart := errors.New("failed to start")
	r := Resource[*TestResource]{
		Resource: &TestResource{StartErr: errStart},
	}
	r.initState(5 * time.Minute)

	inst := r.Get()
	assert.Nil(t, inst)
}

func TestResourceLinkHooks(t *testing.T) {
	var r Resource[*TestResource]

	// 1. Test InitState
	resourcelink.InitState(&r, 12*time.Minute)
	require.NotNil(t, r.state)
	assert.Equal(t, 12*time.Minute, r.state.ttl)

	// 2. Test Configure
	resourcelink.Configure(&r, map[string]any{"host": "hook-host", "port": 443})
	assert.Equal(t, "hook-host", r.state.config["host"])

	// 3. Test Set
	custom := &TestResource{Host: "hook-custom"}
	resourcelink.Set(&r, custom)
	assert.Same(t, custom, r.state.instance)

	assert.NoError(t, r.Close())
}
