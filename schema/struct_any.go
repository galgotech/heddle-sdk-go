package schema

import (
	"reflect"

	"github.com/galgotech/heddle-sdk-go/internal/accessor"
)

type Any struct {
	cols map[string][]any
}

func NewAnyAccessor(token accessor.Token, columns map[string][]any) *Any {
	return &Any{cols: columns}
}

func NewAny(columns map[string]any) *Any {
	res := make(map[string][]any)
	for k, v := range columns {
		if arr, ok := v.([]any); ok {
			res[k] = arr
		} else {
			res[k] = packAnyToSlice(v)
		}
	}
	return &Any{cols: res}
}

func (a *Any) Columns() []string {
	names := make([]string, 0, len(a.cols))
	for k := range a.cols {
		names = append(names, k)
	}
	return names
}

func (a *Any) Get(name string) ([]any, bool) {
	if a == nil || a.cols == nil {
		return nil, false
	}
	arr, ok := a.cols[name]
	return arr, ok
}

func packAnyToSlice(val any) []any {
	if val == nil {
		return nil
	}
	v := reflect.ValueOf(val)
	if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
		return []any{val}
	}

	length := v.Len()
	res := make([]any, length)
	for i := range length {
		res[i] = v.Index(i).Interface()
	}
	return res
}
