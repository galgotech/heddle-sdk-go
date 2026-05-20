package plugin

import (
	"context"
	"sync"
	"time"
)

type activeResourceWrapper struct {
	instance Resource
	lastUsed time.Time
	mu       sync.Mutex
}

func (w *activeResourceWrapper) touch() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lastUsed = time.Now()
}

func (w *activeResourceWrapper) getLastUsed() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastUsed
}

type ResourceManager struct {
	activeResources   map[string]*activeResourceWrapper
	mu                sync.RWMutex
	ttl               time.Duration
	cleanerCancelFunc context.CancelFunc
}

func (rm *ResourceManager) Get(id string) (Resource, bool) {
	rm.mu.RLock()
	wrapper, ok := rm.activeResources[id]
	rm.mu.RUnlock()
	if !ok {
		return nil, false
	}
	wrapper.touch()
	return wrapper.instance, true
}

func (rm *ResourceManager) Set(id string, res Resource) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	// If a resource with this ID already exists, close it first
	if old, exists := rm.activeResources[id]; exists {
		old.instance.Close()
	}

	rm.activeResources[id] = &activeResourceWrapper{
		instance: res,
		lastUsed: time.Now(),
	}
}

func (rm *ResourceManager) Remove(id string) bool {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	wrapper, ok := rm.activeResources[id]
	if !ok {
		return false
	}
	delete(rm.activeResources, id)
	wrapper.instance.Close()
	return true
}

func (rm *ResourceManager) CloseAll() {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if rm.cleanerCancelFunc != nil {
		rm.cleanerCancelFunc()
		rm.cleanerCancelFunc = nil
	}

	for id, wrapper := range rm.activeResources {
		wrapper.instance.Close()
		delete(rm.activeResources, id)
	}
}

func (rm *ResourceManager) StartCleaner(ctx context.Context) {
	rm.mu.Lock()
	if rm.cleanerCancelFunc != nil {
		rm.cleanerCancelFunc()
	}
	cleanerCtx, cancel := context.WithCancel(ctx)
	rm.cleanerCancelFunc = cancel
	rm.mu.Unlock()

	ticker := time.NewTicker(1 * time.Minute)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-cleanerCtx.Done():
				return
			case <-ticker.C:
				rm.cleanupIdle()
			}
		}
	}()
}

func (rm *ResourceManager) cleanupIdle() {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	now := time.Now()
	for id, wrapper := range rm.activeResources {
		if now.Sub(wrapper.getLastUsed()) > rm.ttl {
			// Resource has expired
			wrapper.instance.Close()
			delete(rm.activeResources, id)
		}
	}
}

func NewResourceManager(ttl time.Duration) *ResourceManager {
	if ttl <= 0 {
		ttl = 15 * time.Minute // default TTL
	}
	return &ResourceManager{
		activeResources: make(map[string]*activeResourceWrapper),
		ttl:             ttl,
	}
}
