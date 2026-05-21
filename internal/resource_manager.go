package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/galgotech/heddle-sdk-go/schema"
)

type activeResourceWrapper struct {
	instance schema.Resource
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
	ctx               context.Context
	registry          Registry
	activeResources   map[string]*activeResourceWrapper
	mu                sync.RWMutex
	ttl               time.Duration
	cleanerCancelFunc context.CancelFunc
}

func (rm *ResourceManager) Get(id string) (schema.Resource, bool) {
	rm.mu.RLock()
	wrapper, ok := rm.activeResources[id]
	rm.mu.RUnlock()
	if !ok {
		return nil, false
	}
	wrapper.touch()
	return wrapper.instance, true
}

func (rm *ResourceManager) Set(id string, res schema.Resource) {
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

// InitializeResource instantiates a registered resource type, maps the provided configuration map,
// starts the resource, and registers it in the active resources map under the given ID.
func (rm *ResourceManager) InitializeResource(id string, resourceTypeName string, config map[string]any) error {
	resReg, ok := rm.registry.GetResource(resourceTypeName)
	if !ok {
		return fmt.Errorf("resource type %q not registered in namespace %s", resourceTypeName)
	}

	// Instantiate the registered type via reflect.New
	val := reflect.New(resReg.ResourceType)

	// Map configuration map[string]any if provided
	if config != nil {
		configBytes, err := json.Marshal(config)
		if err != nil {
			return fmt.Errorf("failed to marshal configuration map for resource %q: %w", id, err)
		}
		if err := json.Unmarshal(configBytes, val.Interface()); err != nil {
			return fmt.Errorf("failed to unmarshal configuration for resource %q: %w", id, err)
		}
	}

	// Verify the instance implements the Resource interface
	resInstance, ok := val.Interface().(schema.Resource)
	if !ok {
		return fmt.Errorf("resource type %q does not implement Resource interface", resourceTypeName)
	}

	// Start the resource
	if err := resInstance.Start(rm.ctx); err != nil {
		return fmt.Errorf("failed to start resource %q: %w", id, err)
	}

	// Register in the active resources map via ResourceManager
	rm.Set(id, resInstance)

	return nil
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

func NewResourceManager(ctx context.Context, registry Registry, ttl time.Duration) *ResourceManager {
	if ttl <= 0 {
		ttl = 15 * time.Minute // default TTL
	}

	return &ResourceManager{
		ctx:             ctx,
		registry:        registry,
		activeResources: make(map[string]*activeResourceWrapper),
		ttl:             ttl,
	}
}
