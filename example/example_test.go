package example

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/galgotech/heddle-sdk-go/schema"
	"github.com/galgotech/heddle-sdk-go/steptest"
)

func TestQueryStep(t *testing.T) {
	// 1. Setup Resource
	dbResource := schema.ResourceSchema[*Connection]{}
	dbResource.SetResource(&Connection{Host: "test-host"})

	steps := &Steps{
		DB: dbResource,
	}

	// 2. Prepare mock inputs using steptest with a list of inputs
	in := steptest.NewInput(
		QueryInput{
			UserID:   42,
			SubInput: steptest.NewInput(SubInput{Name: "nested1"}),
		},
		QueryInput{
			UserID:   43,
			SubInput: steptest.NewInput(SubInput{Name: "nested2"}),
		},
	)

	// 3. Prepare output recorder using steptest
	out := steptest.NewOutput[QueryOutput]()

	// 4. Run the step method directly
	err := steps.Query(t.Context(), in, out.Frame())
	assert.NoError(t, err)

	// 5. Assert outputs were captured
	assert.Len(t, out.Items, 2)
	assert.Equal(t, int64(42), out.Items[0].UserID)
	assert.Equal(t, "US (resolved via test-host)", out.Items[0].Country)

	assert.Equal(t, int64(43), out.Items[1].UserID)
	assert.Equal(t, "US (resolved via test-host)", out.Items[1].Country)
}

func TestProducerStep(t *testing.T) {
	steps := &Steps{}
	out := steptest.NewOutput[QueryOutput]()

	err := steps.TestProducer(t.Context(), out.Frame())
	assert.NoError(t, err)

	assert.Len(t, out.Items, 1)
	assert.Equal(t, int64(1), out.Items[0].UserID)
	assert.Equal(t, "Brasil", out.Items[0].Country)
}
