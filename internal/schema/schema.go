package schema

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/galgotech/heddle-lang/pkg/schema"
)

var packageName = "github.com/galgotech/heddle-sdk-go/schema"

type ResourceSetter interface {
	SetResource(val any)
}

// ExtractFieldSchema performs type introspection to derive a basic JSON schema for Step/Resource configurations.
func ExtractFieldSchema(t reflect.Type) (schema.FieldSchema, error) {
	if t.Kind() != reflect.Struct {
		return schema.FieldSchema{}, fmt.Errorf("ExtractConfigSchema: expected struct, got %s", t.Kind())
	}

	fieldSchema := schema.FieldSchema{
		Fields: []schema.Field{},
	}

	for field := range t.Fields() {
		if IsResource(field.Type) {
			continue
		}

		fieldSchema.Fields = append(fieldSchema.Fields, schema.Field{
			Name: field.Name,
			Type: field.Type.Kind().String(),
		})
	}

	return fieldSchema, nil
}

// IsResource expect schema.Resource[T schema.ResourceInterface]
func IsResource(t reflect.Type) bool {
	return (t.Name() == "ResourceSchema" || strings.HasPrefix(t.Name(), "ResourceSchema[")) && t.PkgPath() == packageName
}

func IsRef(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	return (t.Name() == "Frame" || strings.HasPrefix(t.Name(), "Frame[")) && t.PkgPath() == packageName
}

// ExtractSchema uses reflection to derive a Heddle schema from a reflect.Type (expected to be a struct).
func ExtractSchema(t reflect.Type) (schema.FrameSchema, error) {
	if t == nil {
		return schema.FrameSchema{}, fmt.Errorf("ExtractSchema: nil type or generic interface not allowed")
	}

	if t.Kind() == reflect.Pointer {
		return schema.FrameSchema{}, fmt.Errorf("ExtractSchema: pointer not allowed")
	}

	if t.Kind() != reflect.Struct {
		return schema.FrameSchema{}, fmt.Errorf("ExtractSchema: expected pointer to struct or struct, got %s", t.Kind())
	}

	frameSchema := schema.FrameSchema{
		Columns: []schema.ColumnSchema{},
	}

	for f := range t.Fields() {
		if f.Anonymous || !f.IsExported() {
			continue
		}

		fieldType := f.Type

		var arrowType string
		if IsRef(fieldType) {
			arrowType = "struct"
		} else {
			if fieldType.Kind() == reflect.Pointer {
				fieldType = fieldType.Elem()
			}

			switch fieldType.Kind() {
			case reflect.Int8:
				arrowType = "int8"
			case reflect.Int16:
				arrowType = "int16"
			case reflect.Int32:
				arrowType = "int32"
			case reflect.Int64, reflect.Int:
				arrowType = "int64"
			case reflect.Uint8:
				arrowType = "uint8"
			case reflect.Uint16:
				arrowType = "uint16"
			case reflect.Uint32:
				arrowType = "uint32"
			case reflect.Uint64, reflect.Uint:
				arrowType = "uint64"
			case reflect.Float32:
				arrowType = "float32"
			case reflect.Float64:
				arrowType = "float64"
			case reflect.Bool:
				arrowType = "bool"
			case reflect.String:
				arrowType = "utf8"
			default:
				return schema.FrameSchema{}, fmt.Errorf("unsupported type %s for field %s", fieldType.String(), f.Name)
			}
		}

		if arrowType != "" {
			frameSchema.Columns = append(frameSchema.Columns, schema.ColumnSchema{
				Name:      f.Name,
				ArrowType: arrowType,
			})
		}
	}

	return frameSchema, nil
}
