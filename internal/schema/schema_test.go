package schema

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pluginschema "github.com/galgotech/heddle-sdk-go/schema"
)

type SchemaTable struct {
	ID     int64
	Email  string
	Active bool
}

func TestExtractSchema(t *testing.T) {
	// 1. Extract schema from struct
	s, err := ExtractSchema(reflect.TypeFor[SchemaTable]())
	require.NoError(t, err)
	require.NotNil(t, s)

	// 2. Validate fields
	assert.Equal(t, 3, len(s.Fields))

	// Field 0: ID
	assert.Equal(t, "ID", s.Fields[0].Name)
	assert.Equal(t, "int64", s.Fields[0].ArrowType)

	// Field 1: Email
	assert.Equal(t, "Email", s.Fields[1].Name)
	assert.Equal(t, "utf8", s.Fields[1].ArrowType)

	// Field 2: Active
	assert.Equal(t, "Active", s.Fields[2].Name)
	assert.Equal(t, "bool", s.Fields[2].ArrowType)
}

type MySubStruct struct {
	Val string
}

type SchemaTableWithStruct struct {
	Nested pluginschema.Frame[MySubStruct]
}

func TestExtractSchema_Struct(t *testing.T) {
	s, err := ExtractSchema(reflect.TypeFor[SchemaTableWithStruct]())
	require.NoError(t, err)
	require.NotNil(t, s)

	require.Equal(t, 1, len(s.Fields))
	assert.Equal(t, "Nested", s.Fields[0].Name)
	assert.Equal(t, "struct", s.Fields[0].ArrowType)
}

func TestExtractConfigSchema(t *testing.T) {
	type ConfigTest struct {
		Name    string
		Timeout int
		hidden  string
	}

	s, err := ExtractFieldSchema(reflect.TypeFor[ConfigTest]())
	require.NoError(t, err)
	require.NotNil(t, s)

	assert.Equal(t, 3, len(s.Fields))
	assert.Equal(t, "Name", s.Fields[0].Name)
	assert.Equal(t, "string", s.Fields[0].Type)
	assert.Equal(t, "Timeout", s.Fields[1].Name)
	assert.Equal(t, "int", s.Fields[1].Type)
}
