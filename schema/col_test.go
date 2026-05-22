package schema

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCol(t *testing.T) {
	data := []string{"foo", "bar", "baz"}
	col := NewCol(data)

	// Test Len and Value
	assert.Equal(t, 3, col.Len())
	assert.Equal(t, "foo", col.Value(0))
	assert.Equal(t, "bar", col.Value(1))
	assert.Equal(t, "baz", col.Value(2))

	// Test unique auto-populated Snowflake IDs
	assert.NotEqual(t, int64(0), col.ID(0))
	assert.NotEqual(t, int64(0), col.ID(1))
	assert.NotEqual(t, int64(0), col.ID(2))
	assert.NotEqual(t, col.ID(0), col.ID(1))
	assert.NotEqual(t, col.ID(1), col.ID(2))
	assert.NotEqual(t, col.ID(0), col.ID(2))

	// Test bounds safety for ID
	assert.Equal(t, int64(0), col.ID(-1))
	assert.Equal(t, int64(0), col.ID(3))

	// Test IsDeleted and Delete
	assert.False(t, col.IsDeleted(0))
	assert.False(t, col.IsDeleted(1))

	col.Delete(1)
	assert.True(t, col.IsDeleted(1))
	assert.False(t, col.IsDeleted(0))

	// Test bounds safety for Delete and IsDeleted
	col.Delete(-1)
	col.Delete(3)
	assert.False(t, col.IsDeleted(-1))
	assert.False(t, col.IsDeleted(3))
}

func TestAny(t *testing.T) {
	anyObj := &Any{}

	val, ok := anyObj.Get("nonexistent")
	assert.False(t, ok)
	assert.Nil(t, val)

	anyObj.Set("foo", "bar")
	val, ok = anyObj.Get("foo")
	assert.True(t, ok)
	assert.Equal(t, "bar", val)

	cols := anyObj.Columns()
	assert.NotNil(t, cols)
	assert.Equal(t, 1, len(cols))
	assert.Equal(t, "bar", cols["foo"])
}
