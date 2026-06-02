package execute

import (
	"reflect"
	"testing"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
	"github.com/apache/arrow/go/v18/arrow/memory"
	"github.com/galgotech/heddle-sdk-go/internal/registry"
	"github.com/galgotech/heddle-sdk-go/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type DummyConfig struct {
	Name      string
	Timeout   int
	Enabled   bool
	CamelCase string
}

func TestHydrateConfig(t *testing.T) {
	t.Run("successful hydration of all fields", func(t *testing.T) {
		step := registry.StepRegistration{
			ConfigType: reflect.TypeFor[DummyConfig](),
		}
		configJSON := `{"name":"test-config","timeout":120,"enabled":true, "camelCase":"not-exported"}`

		val, err := hydrateConfig(step, configJSON)
		require.NoError(t, err)
		require.True(t, val.IsValid())
		assert.Equal(t, reflect.Pointer, val.Kind())

		cfg, ok := val.Interface().(*DummyConfig)
		require.True(t, ok)
		assert.Equal(t, "test-config", cfg.Name)
		assert.Equal(t, 120, cfg.Timeout)
		assert.True(t, cfg.Enabled)
		assert.Equal(t, cfg.CamelCase, "not-exported")
	})

	t.Run("successful hydration with partial fields", func(t *testing.T) {
		step := registry.StepRegistration{
			ConfigType: reflect.TypeOf(DummyConfig{}),
		}
		configJSON := `{"name":"partial"}`

		val, err := hydrateConfig(step, configJSON)
		require.NoError(t, err)
		require.True(t, val.IsValid())

		cfg, ok := val.Interface().(*DummyConfig)
		require.True(t, ok)
		assert.Equal(t, "partial", cfg.Name)
		assert.Equal(t, 0, cfg.Timeout)
		assert.False(t, cfg.Enabled)
	})

	t.Run("error on invalid JSON syntax", func(t *testing.T) {
		step := registry.StepRegistration{
			ConfigType: reflect.TypeOf(DummyConfig{}),
		}
		configJSON := `{"name":`

		_, err := hydrateConfig(step, configJSON)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal config")
	})

	t.Run("error on type mismatch in JSON", func(t *testing.T) {
		step := registry.StepRegistration{
			ConfigType: reflect.TypeFor[DummyConfig](),
		}
		configJSON := `{"timeout":"not-an-integer"}`

		_, err := hydrateConfig(step, configJSON)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal config")
	})
}

type DummyInput struct {
	ID   int32
	Name string
}

func TestPrepareInput(t *testing.T) {
	t.Run("successful preparation of input value", func(t *testing.T) {
		mem := memory.DefaultAllocator

		bID := array.NewInt32Builder(mem)
		defer bID.Release()

		bID.AppendValues([]int32{1, 2}, nil)

		arrID := bID.NewInt32Array()
		defer arrID.Release()

		bName := array.NewStringBuilder(mem)
		defer bName.Release()

		bName.AppendValues([]string{"Alice", "Bob"}, nil)

		arrName := bName.NewStringArray()
		defer arrName.Release()

		columns := map[string]arrow.Array{
			"ID":   arrID,
			"Name": arrName,
		}

		step := registry.StepRegistration{
			InputType: reflect.TypeFor[schema.Frame[DummyInput]](),
		}

		val, err := prepareInput(step, columns)
		require.NoError(t, err)
		require.True(t, val.IsValid())
		assert.Equal(t, reflect.Struct, val.Kind())

		frame, ok := val.Interface().(schema.Frame[DummyInput])
		require.True(t, ok)

		var iterated []DummyInput

		err = frame.Each(func(item DummyInput) {
			iterated = append(iterated, item)
		})
		require.NoError(t, err)
		require.Len(t, iterated, 2)
		assert.Equal(t, DummyInput{ID: 1, Name: "Alice"}, iterated[0])
		assert.Equal(t, DummyInput{ID: 2, Name: "Bob"}, iterated[1])
	})

	t.Run("error when missing required column", func(t *testing.T) {
		mem := memory.DefaultAllocator

		bID := array.NewInt32Builder(mem)
		defer bID.Release()

		bID.AppendValues([]int32{1, 2}, nil)

		arrID := bID.NewInt32Array()
		defer arrID.Release()

		// Missing "Name" column
		columns := map[string]arrow.Array{
			"ID": arrID,
		}

		step := registry.StepRegistration{
			InputType: reflect.TypeFor[schema.Frame[DummyInput]](),
		}

		_, err := prepareInput(step, columns)
		assert.Error(t, err)
	})
}
