package schema_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

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

	// Test range-over-function via All()
	var iteratedValues []string
	var iteratedIndices []int
	for i, e := range col.All() {
		iteratedIndices = append(iteratedIndices, i)
		iteratedValues = append(iteratedValues, e)
	}
	assert.Equal(t, []int{0, 1, 2}, iteratedIndices)
	assert.Equal(t, data, iteratedValues)
}
