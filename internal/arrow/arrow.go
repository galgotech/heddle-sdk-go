package arrow

import (
	"fmt"
	"reflect"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
	"github.com/apache/arrow/go/v18/arrow/memory"
)

func SliceToArrowArray(sliceAny any) (arrow.Array, error) {
	mem := memory.DefaultAllocator

	switch s := sliceAny.(type) {
	case []int8:
		b := array.NewInt8Builder(mem)
		defer b.Release()

		b.AppendValues(s, nil)

		return b.NewArray(), nil
	case []int16:
		b := array.NewInt16Builder(mem)
		defer b.Release()

		b.AppendValues(s, nil)

		return b.NewArray(), nil
	case []int32:
		b := array.NewInt32Builder(mem)
		defer b.Release()

		b.AppendValues(s, nil)

		return b.NewArray(), nil
	case []int64:
		b := array.NewInt64Builder(mem)
		defer b.Release()

		b.AppendValues(s, nil)

		return b.NewArray(), nil
	case []uint8:
		b := array.NewUint8Builder(mem)
		defer b.Release()

		b.AppendValues(s, nil)

		return b.NewArray(), nil
	case []uint16:
		b := array.NewUint16Builder(mem)
		defer b.Release()

		b.AppendValues(s, nil)

		return b.NewArray(), nil
	case []uint32:
		b := array.NewUint32Builder(mem)
		defer b.Release()

		b.AppendValues(s, nil)

		return b.NewArray(), nil
	case []uint64:
		b := array.NewUint64Builder(mem)
		defer b.Release()

		b.AppendValues(s, nil)

		return b.NewArray(), nil
	case []float32:
		b := array.NewFloat32Builder(mem)
		defer b.Release()

		b.AppendValues(s, nil)

		return b.NewArray(), nil
	case []float64:
		b := array.NewFloat64Builder(mem)
		defer b.Release()

		b.AppendValues(s, nil)

		return b.NewArray(), nil
	case []bool:
		b := array.NewBooleanBuilder(mem)
		defer b.Release()

		b.AppendValues(s, nil)

		return b.NewArray(), nil
	case []string:
		b := array.NewStringBuilder(mem)
		defer b.Release()

		b.AppendValues(s, nil)

		return b.NewArray(), nil
	default:
		val := reflect.ValueOf(sliceAny)
		elemType := val.Type().Elem()

		if val.Kind() != reflect.Slice {
			return nil, fmt.Errorf("SliceToArrowArray: expected slice, got %T", sliceAny)
		}

		switch elemType.Kind() {
		case reflect.Int8:
			b := array.NewInt8Builder(mem)
			defer b.Release()

			for i := 0; i < val.Len(); i++ {
				b.Append(int8(val.Index(i).Int()))
			}

			return b.NewArray(), nil
		case reflect.Int16:
			b := array.NewInt16Builder(mem)
			defer b.Release()

			for i := 0; i < val.Len(); i++ {
				b.Append(int16(val.Index(i).Int()))
			}

			return b.NewArray(), nil
		case reflect.Int32:
			b := array.NewInt32Builder(mem)
			defer b.Release()

			for i := 0; i < val.Len(); i++ {
				b.Append(int32(val.Index(i).Int()))
			}

			return b.NewArray(), nil
		case reflect.Int64:
			b := array.NewInt64Builder(mem)
			defer b.Release()

			for i := 0; i < val.Len(); i++ {
				b.Append(val.Index(i).Int())
			}

			return b.NewArray(), nil
		case reflect.Uint8:
			b := array.NewUint8Builder(mem)
			defer b.Release()

			for i := 0; i < val.Len(); i++ {
				b.Append(uint8(val.Index(i).Uint()))
			}

			return b.NewArray(), nil
		case reflect.Uint16:
			b := array.NewUint16Builder(mem)
			defer b.Release()

			for i := 0; i < val.Len(); i++ {
				b.Append(uint16(val.Index(i).Uint()))
			}

			return b.NewArray(), nil
		case reflect.Uint32:
			b := array.NewUint32Builder(mem)
			defer b.Release()

			for i := 0; i < val.Len(); i++ {
				b.Append(uint32(val.Index(i).Uint()))
			}

			return b.NewArray(), nil
		case reflect.Uint64:
			b := array.NewUint64Builder(mem)
			defer b.Release()

			for i := 0; i < val.Len(); i++ {
				b.Append(val.Index(i).Uint())
			}

			return b.NewArray(), nil
		case reflect.Float32:
			b := array.NewFloat32Builder(mem)
			defer b.Release()

			for i := 0; i < val.Len(); i++ {
				b.Append(float32(val.Index(i).Float()))
			}

			return b.NewArray(), nil
		case reflect.Float64:
			b := array.NewFloat64Builder(mem)
			defer b.Release()

			for i := 0; i < val.Len(); i++ {
				b.Append(val.Index(i).Float())
			}

			return b.NewArray(), nil
		case reflect.Bool:
			b := array.NewBooleanBuilder(mem)
			defer b.Release()

			for i := 0; i < val.Len(); i++ {
				b.Append(val.Index(i).Bool())
			}

			return b.NewArray(), nil
		case reflect.String:
			b := array.NewStringBuilder(mem)
			defer b.Release()

			for i := 0; i < val.Len(); i++ {
				b.Append(val.Index(i).String())
			}

			return b.NewArray(), nil
		case reflect.Struct:
			dt, err := GoTypeToArrowDataType(elemType)
			if err != nil {
				return nil, err
			}

			structType := dt.(*arrow.StructType)

			builder := array.NewStructBuilder(mem, structType)
			defer builder.Release()

			for i := 0; i < val.Len(); i++ {
				if err := AppendGoValueToBuilder(builder, val.Index(i)); err != nil {
					return nil, err
				}
			}

			return builder.NewArray(), nil
		}

		return nil, fmt.Errorf("unsupported slice element type: %s", elemType.Kind())
	}
}

func GoTypeToArrowDataType(t reflect.Type) (arrow.DataType, error) {
	switch t.Kind() {
	case reflect.Int8:
		return arrow.PrimitiveTypes.Int8, nil
	case reflect.Int16:
		return arrow.PrimitiveTypes.Int16, nil
	case reflect.Int32:
		return arrow.PrimitiveTypes.Int32, nil
	case reflect.Int64, reflect.Int:
		return arrow.PrimitiveTypes.Int64, nil
	case reflect.Uint8:
		return arrow.PrimitiveTypes.Uint8, nil
	case reflect.Uint16:
		return arrow.PrimitiveTypes.Uint16, nil
	case reflect.Uint32:
		return arrow.PrimitiveTypes.Uint32, nil
	case reflect.Uint64, reflect.Uint:
		return arrow.PrimitiveTypes.Uint64, nil
	case reflect.Float32:
		return arrow.PrimitiveTypes.Float32, nil
	case reflect.Float64:
		return arrow.PrimitiveTypes.Float64, nil
	case reflect.Bool:
		return arrow.FixedWidthTypes.Boolean, nil
	case reflect.String:
		return arrow.BinaryTypes.String, nil
	case reflect.Struct:
		fields := make([]arrow.Field, 0)

		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}

			dt, err := GoTypeToArrowDataType(f.Type)
			if err != nil {
				return nil, err
			}

			fields = append(fields, arrow.Field{Name: f.Name, Type: dt, Nullable: true})
		}

		return arrow.StructOf(fields...), nil
	default:
		return nil, fmt.Errorf("unsupported Go type for Arrow: %s", t.Kind())
	}
}

func AppendGoValueToBuilder(builder array.Builder, val reflect.Value) error {
	switch b := builder.(type) {
	case *array.Int8Builder:
		b.Append(int8(val.Int()))
	case *array.Int16Builder:
		b.Append(int16(val.Int()))
	case *array.Int32Builder:
		b.Append(int32(val.Int()))
	case *array.Int64Builder:
		b.Append(val.Int())
	case *array.Uint8Builder:
		b.Append(uint8(val.Uint()))
	case *array.Uint16Builder:
		b.Append(uint16(val.Uint()))
	case *array.Uint32Builder:
		b.Append(uint32(val.Uint()))
	case *array.Uint64Builder:
		b.Append(val.Uint())
	case *array.Float32Builder:
		b.Append(float32(val.Float()))
	case *array.Float64Builder:
		b.Append(val.Float())
	case *array.BooleanBuilder:
		b.Append(val.Bool())
	case *array.StringBuilder:
		b.Append(val.String())
	case *array.StructBuilder:
		b.Append(true)

		fieldIdx := 0

		for i := 0; i < val.NumField(); i++ {
			f := val.Type().Field(i)
			if !f.IsExported() {
				continue
			}

			if err := AppendGoValueToBuilder(b.FieldBuilder(fieldIdx), val.Field(i)); err != nil {
				return err
			}

			fieldIdx++
		}
	default:
		return fmt.Errorf("unsupported Arrow builder type: %T", builder)
	}

	return nil
}

func ArrowStructToGoSlice(arr *array.Struct, elemType reflect.Type) reflect.Value {
	n := arr.Len()

	result := reflect.MakeSlice(reflect.SliceOf(elemType), n, n)
	for i := range n {
		elem := result.Index(i)
		fieldIdx := 0

		for j := 0; j < elemType.NumField(); j++ {
			f := elemType.Field(j)
			if !f.IsExported() {
				continue
			}

			SetGoFieldFromArrow(elem.Field(j), arr.Field(fieldIdx), i)
			fieldIdx++
		}
	}

	return result
}

func SetGoFieldFromArrow(dst reflect.Value, arr arrow.Array, i int) {
	switch a := arr.(type) {
	case *array.Int8:
		dst.SetInt(int64(a.Value(i)))
	case *array.Int16:
		dst.SetInt(int64(a.Value(i)))
	case *array.Int32:
		dst.SetInt(int64(a.Value(i)))
	case *array.Int64:
		dst.SetInt(a.Value(i))
	case *array.Uint8:
		dst.SetUint(uint64(a.Value(i)))
	case *array.Uint16:
		dst.SetUint(uint64(a.Value(i)))
	case *array.Uint32:
		dst.SetUint(uint64(a.Value(i)))
	case *array.Uint64:
		dst.SetUint(a.Value(i))
	case *array.Float32:
		dst.SetFloat(float64(a.Value(i)))
	case *array.Float64:
		dst.SetFloat(a.Value(i))
	case *array.Boolean:
		dst.SetBool(a.Value(i))
	case *array.String:
		dst.SetString(a.Value(i))
	case *array.Struct:
		fieldIdx := 0

		for j := 0; j < dst.NumField(); j++ {
			if !dst.Type().Field(j).IsExported() {
				continue
			}

			SetGoFieldFromArrow(dst.Field(j), a.Field(fieldIdx), i)
			fieldIdx++
		}
	}
}
