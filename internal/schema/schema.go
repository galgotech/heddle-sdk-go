package schema

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/galgotech/heddle-lang/pkg/schema"

	pluginschema "github.com/galgotech/heddle-sdk-go/schema"
)

var packageName string

func init() {
	packageName = reflect.TypeFor[pluginschema.Void]().PkgPath()
}

// ExtractFieldSchema performs type introspection to derive a basic JSON schema for Step/Resource configurations.
func ExtractFieldSchema(t reflect.Type) (schema.FieldSchema, error) {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
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
		jsonTag := field.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}
		name := jsonTag
		if name == "" {
			name = field.Name
		} else {
			parts := strings.Split(name, ",")
			name = parts[0]
			if name == "" {
				name = field.Name
			}
		}

		fieldSchema.Fields = append(fieldSchema.Fields, schema.Field{
			Name: name,
			Type: field.Type.Kind().String(),
		})
	}
	return fieldSchema, nil
}

func IsResource(t reflect.Type) bool {
	return (t.Name() == "Resource" || strings.HasPrefix(t.Name(), "Resource[")) && t.PkgPath() == packageName
}

func IsCol(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return strings.HasPrefix(t.Name(), "Col") && t.PkgPath() == packageName
}

// ExtractInputOutputSchema uses reflection to derive a Heddle schema from a reflect.Type (expected to be a struct).
func ExtractInputOutputSchema(t reflect.Type) (schema.FrameSchema, error) {
	if t == nil {
		return schema.FrameSchema{}, fmt.Errorf("ExtractSchema: nil type or generic interface not allowed")
	}

	if t == reflect.TypeFor[*pluginschema.Any]() || t == reflect.TypeFor[pluginschema.Any]() {
		return schema.FrameSchema{IsDynamic: true}, nil
	}

	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return schema.FrameSchema{}, fmt.Errorf("ExtractSchema: expected pointer to struct or struct, got %s", t.Kind())
	}

	if t == reflect.TypeFor[pluginschema.Void]() {
		return schema.FrameSchema{IsVoid: true}, nil
	}

	frameSchema := schema.FrameSchema{
		Fields: []schema.FrameSchemaField{},
	}

	for f := range t.Fields() {
		if f.Anonymous {
			continue
		}
		fieldType := f.Type

		if IsCol(fieldType) {
			tType := fieldType
			if tType.Kind() == reflect.Pointer {
				tType = tType.Elem()
			}
			typeName := tType.Name()
			var arrowType string
			if strings.Contains(typeName, "Int8") {
				arrowType = "int8"
			} else if strings.Contains(typeName, "Int16") {
				arrowType = "int16"
			} else if strings.Contains(typeName, "Int32") {
				arrowType = "int32"
			} else if strings.Contains(typeName, "Int64") {
				arrowType = "int64"
			} else if strings.Contains(typeName, "Uint8") {
				arrowType = "uint8"
			} else if strings.Contains(typeName, "Uint16") {
				arrowType = "uint16"
			} else if strings.Contains(typeName, "Uint32") {
				arrowType = "uint32"
			} else if strings.Contains(typeName, "Uint64") {
				arrowType = "uint64"
			} else if strings.Contains(typeName, "Float32") {
				arrowType = "float32"
			} else if strings.Contains(typeName, "Float64") {
				arrowType = "float64"
			} else if strings.Contains(typeName, "Boolean") {
				arrowType = "bool"
			} else if strings.Contains(typeName, "String") {
				arrowType = "utf8"
			} else if strings.Contains(typeName, "Struct") || strings.Contains(typeName, "ColStruct") {
				arrowType = "struct"
			}
			if arrowType != "" {
				frameSchema.Fields = append(frameSchema.Fields, schema.FrameSchemaField{
					Name:      f.Name,
					ArrowType: arrowType,
				})
			}
		}
	}

	return frameSchema, nil
}
