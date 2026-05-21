package schema

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"time"

	"github.com/galgotech/heddle-sdk-go/internal/resourcelink"
)

// ResourceInterface represents an external dependency or stateful object
// (e.g., database connection pool, API client) initialized by the Heddle runtime.
type ResourceInterface interface {
	Start(ctx context.Context) error
	Close() error
}

type resourceState[T ResourceInterface] struct {
	mu              sync.Mutex
	instance        T
	defaultInstance T
	lastUsed        time.Time
	ttl             time.Duration
	config          map[string]any
	stopTicker      chan struct{}
}

func (s *resourceState[T]) startTickerLocked() {
	if s.stopTicker != nil {
		return
	}
	s.stopTicker = make(chan struct{})
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.mu.Lock()
				if !isNil(s.instance) {
					ttl := s.ttl
					if ttl <= 0 {
						ttl = 15 * time.Minute
					}
					if time.Since(s.lastUsed) > ttl {
						_ = s.instance.Close()
						s.instance = *new(T)
					}
				}
				s.mu.Unlock()
			case <-s.stopTicker:
				return
			}
		}
	}()
}

func (s *resourceState[T]) get() T {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	// 1. Check if we have an active instance and it has expired due to TTL
	if !isNil(s.instance) {
		ttl := s.ttl
		if ttl <= 0 {
			ttl = 15 * time.Minute
		}
		if now.Sub(s.lastUsed) > ttl {
			_ = s.instance.Close()
			s.instance = *new(T)
		}
	}

	// 2. If no active instance, lazily initialize it
	if isNil(s.instance) {
		var inst T
		if !isNil(s.defaultInstance) {
			typ := reflect.TypeOf(s.defaultInstance)
			var val reflect.Value
			if typ.Kind() == reflect.Pointer {
				val = reflect.New(typ.Elem())
				val.Elem().Set(reflect.ValueOf(s.defaultInstance).Elem())
			} else {
				val = reflect.New(typ).Elem()
				val.Set(reflect.ValueOf(s.defaultInstance))
			}
			inst = val.Interface().(T)
		} else {
			typ := reflect.TypeFor[T]()
			var val reflect.Value
			if typ.Kind() == reflect.Pointer {
				val = reflect.New(typ.Elem())
			} else {
				val = reflect.New(typ).Elem()
			}
			inst = val.Interface().(T)
		}

		if s.config != nil {
			configBytes, err := json.Marshal(s.config)
			if err == nil {
				_ = json.Unmarshal(configBytes, inst)
			}
		}

		if err := inst.Start(context.Background()); err == nil {
			s.instance = inst
			s.startTickerLocked()
		}
	}

	s.lastUsed = time.Now()
	return s.instance
}

type Resource[T ResourceInterface] struct {
	Resource T
	state    *resourceState[T]
}

func (r *Resource[T]) initState(ttl time.Duration) {
	if r.state == nil {
		r.state = &resourceState[T]{
			ttl: ttl,
		}
	}
	if !isNil(r.Resource) {
		r.state.defaultInstance = r.Resource
	}
}

func (r *Resource[T]) configure(config map[string]any) {
	if r.state == nil {
		r.state = &resourceState[T]{}
	}
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	r.state.config = config
}

func (r *Resource[T]) set(val any) {
	if r.state == nil {
		r.state = &resourceState[T]{}
	}
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	r.state.instance = val.(T)
	r.state.lastUsed = time.Now()
	r.state.startTickerLocked()
}

func (r Resource[T]) Get() T {
	if r.state == nil {
		var zero T
		return zero
	}
	return r.state.get()
}

func (r Resource[T]) Start(ctx context.Context) error {
	if r.state == nil {
		return nil
	}
	_ = r.state.get()
	return nil
}

func (r Resource[T]) Close() error {
	if r.state == nil {
		return nil
	}
	r.state.mu.Lock()
	defer r.state.mu.Unlock()

	if r.state.stopTicker != nil {
		close(r.state.stopTicker)
		r.state.stopTicker = nil
	}

	if !isNil(r.state.instance) {
		err := r.state.instance.Close()
		r.state.instance = *new(T)
		return err
	}
	return nil
}

func isNil(i any) bool {
	if i == nil {
		return true
	}
	val := reflect.ValueOf(i)
	switch val.Kind() {
	case reflect.Pointer, reflect.Chan, reflect.Map, reflect.Slice, reflect.Func, reflect.Interface:
		return val.IsNil()
	}
	return false
}

type resourceAdmin interface {
	initState(ttl time.Duration)
	configure(config map[string]any)
	set(val any)
}

func init() {
	resourcelink.InitState = func(r any, ttl time.Duration) {
		if admin, ok := r.(resourceAdmin); ok {
			admin.initState(ttl)
		}
	}
	resourcelink.Configure = func(r any, config map[string]any) {
		if admin, ok := r.(resourceAdmin); ok {
			admin.configure(config)
		}
	}
	resourcelink.Set = func(r any, val any) {
		if admin, ok := r.(resourceAdmin); ok {
			admin.set(val)
		}
	}
}
