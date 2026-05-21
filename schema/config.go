package schema

import (
	"github.com/galgotech/heddle-lang/pkg/schema"
)

// Config represents the configuration contract for a Heddle Step or Resource.
// It can optionally implement the Validate method to provide custom validation logic.
type Config struct {
	resource Resource
}

func (c *Config) HasResource() bool {
	return c.resource != nil
}

func (c *Config) GetResource() Resource {
	return c.resource
}

func (c *Config) SetResource(r Resource) {
	c.resource = r
}

// TypeResolver is an optional interface that configurations can implement
// to provide dynamic input and output schemas based on their values.
type TypeResolver interface {
	ResolveTypes() (input *schema.FrameSchema, output *schema.FrameSchema, err error)
}
