package registry

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"time"

	"github.com/galgotech/heddle-sdk-go/schema"
)

type resourceState[T schema.ResourceDefinition] struct {
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

		if err := inst.Init(context.Background()); err == nil {
			s.instance = inst
			s.startTickerLocked()
		}
	}

	s.lastUsed = time.Now()
	return s.instance
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
