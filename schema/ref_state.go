package schema

import "fmt"

type refState struct {
	slices    map[string][]any
	kinds     []uint8
	offsets   []uintptr
	names     []string
	length    int
	rowOffset int
}

func newRefState(tType *rtype, columns [][]any) (*refState, error) {
	if tType.kind&0x1f == KindPointer {
		return nil, fmt.Errorf("type is a pointer")
	}

	st := tType.structType()
	if st == nil {
		return nil, fmt.Errorf("type is not a struct")
	}

	var fields []structField
	for _, field := range st.fields {
		if field.name.IsEmbedded() || !field.name.IsExported() {
			continue
		}

		fields = append(fields, field)
	}

	if len(columns) != len(fields) {
		return nil, fmt.Errorf("columns length %d does not match fields length %d", len(columns), len(fields))
	}

	slices := make(map[string][]any)
	kinds := make([]uint8, len(fields))
	offsets := make([]uintptr, len(fields))
	names := make([]string, len(fields))

	for i := 0; i < len(fields); i++ {
		field := fields[i]
		fieldName := field.name.Name()
		slices[fieldName] = columns[i]
		kinds[i] = field.typ.kind & 0x1f
		offsets[i] = field.offset
		names[i] = fieldName
	}

	length := 0
	if len(names) > 0 {
		length = len(slices[names[0]])
	}

	return &refState{
		slices:    slices,
		kinds:     kinds,
		offsets:   offsets,
		names:     names,
		length:    length,
		rowOffset: 0,
	}, nil
}
