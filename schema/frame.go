package schema

type FrameInput[T any] struct {
	Iterator func(yield func(item T) error) error
}

func (r FrameInput[T]) Each(yield func(item T) error) error {
	if r.Iterator != nil {
		return r.Iterator(yield)
	}
	return nil
}

type FrameOutput[T any] struct {
	Appender func(item T)
}

func (r FrameOutput[T]) Add(value T) {
	if r.Appender != nil {
		r.Appender(value)
	}
}
