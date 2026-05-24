package schema

import (
	"github.com/apache/arrow/go/v18/arrow/array"
)

type Int8 = array.Int8
type Int16 = array.Int16
type Int32 = array.Int32
type Int64 = array.Int64
type Uint8 = array.Uint8
type Uint16 = array.Uint16
type Uint32 = array.Uint32
type Uint64 = array.Uint64
type Float32 = array.Float32
type Float64 = array.Float64
type Boolean = array.Boolean
type String = array.String
type Struct = array.Struct

type goTypes interface {
	int8 | int16 | int32 | int64 | uint8 | uint16 | uint32 | uint64 | float32 | float64 | bool | string
}

type heddleType interface {
	Int8 |
		Int16 |
		Int32 |
		Int64 |
		Uint8 |
		Uint16 |
		Uint32 |
		Uint64 |
		Float32 |
		Float64 |
		Boolean |
		String |
		Struct
	// array.List |
	// array.LargeString |
	// array.LargeBinary |
	// array.Binary |
	// array.Timestamp |
	// array.Date64 |
	// array.Time32 |
	// array.Time64 |
	// array.Decimal128 |
	// array.Decimal256
}
