package internal

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/galgotech/heddle-lang/pkg/schema"

	pluginschema "github.com/galgotech/heddle-sdk-go/schema"
)

// extractResourceAndConfigSchema performs type introspection to derive a basic JSON schema for Step/Resource configurations.
func extractResourceAndConfigSchema(t reflect.Type) (*schema.ResourceAndConfigSchema, error) {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("ExtractConfigSchema: expected struct, got %s", t.Kind())
	}

	res := &schema.ResourceAndConfigSchema{
		Fields: []schema.ConfigField{},
	}

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if isResource(field.Type) {
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

		res.Fields = append(res.Fields, schema.ConfigField{
			Name: name,
			Type: field.Type.Kind().String(),
		})
	}
	return res, nil
}

func isResource(t reflect.Type) bool {
	return strings.HasPrefix(t.Name(), "Resource[") && t.PkgPath() == "github.com/galgotech/heddle-sdk-go/schema"
}

func isCol(t reflect.Type) bool {
	return (t.Name() == "Col" || strings.HasPrefix(t.Name(), "Col[")) && t.PkgPath() == "github.com/galgotech/heddle-sdk-go/schema"
}

// extractInputOutputSchema uses reflection to derive a Heddle schema from a reflect.Type (expected to be a struct).
func extractInputOutputSchema(t reflect.Type) (*schema.FrameSchema, error) {
	if t == nil {
		return nil, fmt.Errorf("ExtractSchema: nil type or generic interface not allowed")
	}

	if t == reflect.TypeFor[*pluginschema.Any]() || t == reflect.TypeFor[pluginschema.Any]() {
		return &schema.FrameSchema{IsDynamic: true}, nil
	}

	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("ExtractSchema: expected pointer to struct or struct, got %s", t.Kind())
	}

	if t == reflect.TypeFor[pluginschema.Void]() {
		return &schema.FrameSchema{IsVoid: true}, nil
	}

	res := &schema.FrameSchema{
		Fields: []schema.FrameSchemaField{},
	}

	for f := range t.Fields() {
		if f.Anonymous {
			continue
		}
		fieldType := f.Type

		if isCol(fieldType) {
			if fieldType.NumField() > 0 {
				elemType := fieldType.Field(0).Type.Elem()
				var arrowType string
				switch elemType.Kind() {
				case reflect.Int8:
					arrowType = "int8"
				case reflect.Int16:
					arrowType = "int16"
				case reflect.Int32:
					arrowType = "int32"
				case reflect.Int64:
					arrowType = "int64"
				case reflect.Uint8:
					arrowType = "uint8"
				case reflect.Uint16:
					arrowType = "uint16"
				case reflect.Uint32:
					arrowType = "uint32"
				case reflect.Uint64:
					arrowType = "uint64"
				case reflect.Float32:
					arrowType = "float32"
				case reflect.Float64:
					arrowType = "float64"
				case reflect.Bool:
					arrowType = "bool"
				case reflect.String:
					arrowType = "utf8"
				}
				if arrowType != "" {
					res.Fields = append(res.Fields, schema.FrameSchemaField{
						Name:      f.Name,
						ArrowType: arrowType,
					})
				}
			}
		}
	}

	return res, nil
}
