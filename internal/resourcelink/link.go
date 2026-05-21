package resourcelink

import "time"

var (
	InitState func(r any, ttl time.Duration)
	Configure func(r any, config map[string]any)
	Set       func(r any, val any)
)
