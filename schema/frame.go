package schema

type Frame[T any] struct {
	Iterator func(yield func(item T)) error
	Appender func(item T)
}

func (r Frame[T]) Add(value T) {
	if r.Appender != nil {
		r.Appender(value)
	}
}

func (r Frame[T]) Each(yield func(item T)) error {
	if r.Iterator != nil {
		return r.Iterator(yield)
	}
	return nil
}
