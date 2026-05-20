package plugin

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type DummyResource struct {
	closed bool
	mu     sync.Mutex
}

func (r *DummyResource) Start(ctx context.Context) error { return nil }

func (r *DummyResource) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return nil
}

func (r *DummyResource) isClosed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

func TestResourceManager_SetGet(t *testing.T) {
	rm := NewResourceManager(5 * time.Second)
	res := &DummyResource{}
	rm.Set("res1", res)

	got, ok := rm.Get("res1")
	assert.True(t, ok)
	assert.Equal(t, res, got)
}

func TestResourceManager_CloseAll(t *testing.T) {
	rm := NewResourceManager(5 * time.Second)
	res1 := &DummyResource{}
	res2 := &DummyResource{}
	rm.Set("res1", res1)
	rm.Set("res2", res2)

	rm.CloseAll()

	assert.True(t, res1.isClosed())
	assert.True(t, res2.isClosed())

	_, ok1 := rm.Get("res1")
	assert.False(t, ok1)
	_, ok2 := rm.Get("res2")
	assert.False(t, ok2)
}

func TestResourceManager_TTLEviction(t *testing.T) {
	// Set TTL to 100 milliseconds
	rm := NewResourceManager(100 * time.Millisecond)
	res := &DummyResource{}
	rm.Set("res1", res)

	// Verify it's there
	_, ok := rm.Get("res1")
	assert.True(t, ok)

	// Wait 150 milliseconds
	time.Sleep(150 * time.Millisecond)

	// Trigger cleanupIdle manually
	rm.cleanupIdle()

	// Verify it's evicted and closed
	_, ok = rm.Get("res1")
	assert.False(t, ok, "Resource should be evicted after TTL")
	assert.True(t, res.isClosed(), "Resource should be closed on eviction")
}
