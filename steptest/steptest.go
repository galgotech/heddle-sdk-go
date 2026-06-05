package steptest

import "github.com/galgotech/heddle-sdk-go/schema"

// NewInput creates a mock FrameInput that yields the provided items in order.
func NewInput[T any](items ...T) schema.FrameInput[T] {
	return schema.FrameInput[T]{
		Iterator: func(yield func(item T) error) error {
			for _, item := range items {
				if err := yield(item); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

// OutputRecorder captures items appended to a FrameOutput for testing assertions.
type OutputRecorder[T any] struct {
	Items []T
}

// NewOutput creates a new OutputRecorder.
func NewOutput[T any]() *OutputRecorder[T] {
	return &OutputRecorder[T]{
		Items: make([]T, 0),
	}
}

// Frame returns a schema.FrameOutput configured to capture appended items into the recorder.
func (o *OutputRecorder[T]) Frame() schema.FrameOutput[T] {
	return schema.FrameOutput[T]{
		Appender: func(item T) {
			o.Items = append(o.Items, item)
		},
	}
}
