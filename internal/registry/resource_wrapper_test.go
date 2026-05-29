package registry

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type dummyResource struct {
	closed bool
}

func (d *dummyResource) Init(ctx context.Context) error {
	return nil
}

func (d *dummyResource) Close() error {
	d.closed = true
	return nil
}

func TestResourceWrapper(t *testing.T) {
	res := &dummyResource{}
	wrapper := &resourceWrapper{
		instance: res,
	}

	assert.Equal(t, res, wrapper.instance)

	// Initially (lastUsed is 0), it should be timed out for any positive timeout
	assert.True(t, wrapper.isTimedOut(time.Second))

	// Update last used
	before := time.Now().UnixNano()
	wrapper.updateLastUsed()
	after := time.Now().UnixNano()

	last := wrapper.lastUsed.Load()
	assert.GreaterOrEqual(t, last, before)
	assert.LessOrEqual(t, last, after)

	// Since we just updated it, it should not be timed out for a 1-second timeout
	assert.False(t, wrapper.isTimedOut(time.Second))

	// If we set lastUsed to a time in the past (e.g. 2 seconds ago)
	twoSecondsAgo := time.Now().Add(-2 * time.Second).UnixNano()
	wrapper.lastUsed.Store(twoSecondsAgo)

	// It should be timed out for a 1-second timeout
	assert.True(t, wrapper.isTimedOut(1*time.Second))

	// It should NOT be timed out for a 3-second timeout
	assert.False(t, wrapper.isTimedOut(3*time.Second))
}
