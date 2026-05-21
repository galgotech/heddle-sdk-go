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

	for field := range t.Fields() {
		if field.Type == reflect.TypeFor[pluginschema.Config]() {
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
			// Handle cases like json:"name,omitempty"
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

// extractInputOutputSchema uses reflection to derive a Heddle schema from a reflect.Type (expected to be a struct).
func extractInputOutputSchema(t reflect.Type) (*schema.FrameSchema, error) {
	if t == nil {
		return nil, fmt.Errorf("ExtractSchema: nil type or generic interface not allowed; must use a struct embedding HeddleFrame")
	}

	if t.Kind() != reflect.Pointer {
		return nil, fmt.Errorf("ExtractSchema: expected pointer to struct, got %s", t.Kind())
	}

	t = t.Elem()
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("ExtractSchema: expected struct embedding HeddleFrame, got %s", t.Kind())
	}

	if t == reflect.TypeFor[pluginschema.VoidFrame]() {
		return &schema.FrameSchema{IsVoid: true}, nil
	}

	if t == reflect.TypeFor[pluginschema.DynamicFrame]() {
		return &schema.FrameSchema{IsDynamic: true}, nil
	}

	// Verify it embeds HeddleFrame recursively
	if !embedsHeddleFrame(t) {
		return nil, fmt.Errorf("ExtractSchema: type %s does not embed HeddleFrame", t.Name())
	}

	res := &schema.FrameSchema{
		Fields: []schema.FrameSchemaField{},
	}

	for f := range t.Fields() {
		if f.Anonymous {
			continue
		}
		fieldType := f.Type

		if fieldType.Kind() != reflect.Pointer {
			return nil, fmt.Errorf("ExtractSchema: expected pointer to HeddleFrame field, got %s", fieldType.Kind())
		}

		fieldType = fieldType.Elem()

		var arrowType string
		switch fieldType {
		case reflect.TypeFor[pluginschema.Int8]():
			arrowType = "int8"
		case reflect.TypeFor[pluginschema.Int16]():
			arrowType = "int16"
		case reflect.TypeFor[pluginschema.Int32]():
			arrowType = "int32"
		case reflect.TypeFor[pluginschema.Int64]():
			arrowType = "int64"
		case reflect.TypeFor[pluginschema.Uint8]():
			arrowType = "uint8"
		case reflect.TypeFor[pluginschema.Uint16]():
			arrowType = "uint16"
		case reflect.TypeFor[pluginschema.Uint32]():
			arrowType = "uint32"
		case reflect.TypeFor[pluginschema.Uint64]():
			arrowType = "uint64"
		case reflect.TypeFor[pluginschema.Float32]():
			arrowType = "float32"
		case reflect.TypeFor[pluginschema.Float64]():
			arrowType = "float64"
		case reflect.TypeFor[pluginschema.Bool]():
			arrowType = "bool"
		case reflect.TypeFor[pluginschema.String]():
			arrowType = "utf8"
		default:
			continue
		}

		res.Fields = append(res.Fields, schema.FrameSchemaField{
			Name:      f.Name,
			ArrowType: arrowType,
		})
	}

	return res, nil
}

// embedsHeddleFrame checks if a reflect.Type embeds HeddleFrame recursively.
func embedsHeddleFrame(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return false
	}
	if t == reflect.TypeFor[pluginschema.HeddleFrame]() {
		return true
	}
	for field := range t.Fields() {
		if field.Type == reflect.TypeFor[pluginschema.HeddleFrame]() {
			return true
		}
		if field.Anonymous && embedsHeddleFrame(field.Type) {
			return true
		}
	}
	return false
}
