package schema

import "unsafe"

// Custom kind constants mirroring reflect.Kind exactly.
const (
	KindInvalid uint8 = iota
	KindBool
	KindInt
	KindInt8
	KindInt16
	KindInt32
	KindInt64
	KindUint
	KindUint8
	KindUint16
	KindUint32
	KindUint64
	KindUintptr
	KindFloat32
	KindFloat64
	KindComplex64
	KindComplex128
	KindArray
	KindChan
	KindFunc
	KindInterface
	KindMap
	KindPointer
	KindSlice
	KindString
	KindStruct
	KindUnsafePointer
)

// eface represents an empty interface's internal layout.
type eface struct {
	rtype *rtype
	data  unsafe.Pointer
}

// rtype mirrors Go's runtime._type/abi.Type layout.
type rtype struct {
	size       uintptr
	ptrBytes   uintptr
	hash       uint32
	tflag      uint8
	align      uint8
	fieldAlign uint8
	kind       uint8
	equal      unsafe.Pointer
	gcdata     *byte
	str        int32
	ptrToThis  int32
}

// structType mirrors Go's runtime.structType/abi.StructType layout.
type structType struct {
	rtype
	pkgPath Name
	fields  []structField
}

// ptrType mirrors Go's runtime.ptrType/abi.PtrType layout.
type ptrType struct {
	rtype
	elem *rtype
}

// structField mirrors Go's runtime.structField/abi.StructField layout.
type structField struct {
	name   Name
	typ    *rtype
	offset uintptr
}

// Name mirrors Go's abi.Name layout.
type Name struct {
	Bytes *byte
}

func (n Name) ReadVarint(off int) (int, int) {
	v := 0
	for i := 0; ; i++ {
		x := *(*byte)(unsafe.Add(unsafe.Pointer(n.Bytes), off+i))
		v += int(x&0x7f) << (7 * i)
		if x&0x80 == 0 {
			return i + 1, v
		}
	}
}

func (n Name) Name() string {
	if n.Bytes == nil {
		return ""
	}
	i, l := n.ReadVarint(1)
	ptr := unsafe.Add(unsafe.Pointer(n.Bytes), 1+i)
	return unsafe.String((*byte)(ptr), l)
}

func (n Name) IsExported() bool {
	if n.Bytes == nil {
		return false
	}
	return (*n.Bytes)&(1<<0) != 0
}

func (n Name) IsEmbedded() bool {
	if n.Bytes == nil {
		return false
	}
	return (*n.Bytes)&(1<<3) != 0
}

func getRtype(zero any) *rtype {
	var i any = zero
	return (*eface)(unsafe.Pointer(&i)).rtype
}

func (r *rtype) structType() *structType {
	if r.kind&0x1f != KindStruct {
		return nil
	}
	return (*structType)(unsafe.Pointer(r))
}
