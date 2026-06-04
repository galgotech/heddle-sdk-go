package registry

import (
	"sync/atomic"
	"time"

	"github.com/galgotech/heddle-sdk-go/schema"
)

type resourceWrapper struct {
	instance schema.Resource
	lastUsed atomic.Int64
}

func (rw *resourceWrapper) updateLastUsed() {
	rw.lastUsed.Store(time.Now().UnixNano())
}

func (rw *resourceWrapper) isTimedOut(timeout time.Duration) bool {
	last := time.Unix(0, rw.lastUsed.Load())
	return time.Since(last) > timeout
}
