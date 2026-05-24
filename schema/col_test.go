package schema_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/galgotech/heddle-sdk-go/internal/accessor"
	"github.com/galgotech/heddle-sdk-go/schema"
)

func TestCol(t *testing.T) {
	data := []string{"foo", "bar", "baz"}
	col := schema.NewColString(data)

	// Test Len and Value
	assert.Equal(t, 3, col.Len())
	assert.Equal(t, "foo", col.Value(0))
	assert.Equal(t, "bar", col.Value(1))
	assert.Equal(t, "baz", col.Value(2))

	// Test unique auto-populated Snowflake IDs
	ids := col.GetIDs(accessor.Token{})
	assert.NotEqual(t, int64(0), ids.Value(0))
	assert.NotEqual(t, int64(0), ids.Value(1))
	assert.NotEqual(t, int64(0), ids.Value(2))
	assert.NotEqual(t, ids.Value(0), ids.Value(1))
	assert.NotEqual(t, ids.Value(1), ids.Value(2))
	assert.NotEqual(t, ids.Value(0), ids.Value(2))
}
