package registry

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/galgotech/heddle-sdk-go/schema"
)

// DummyResource is a test resource.
type DummyResource struct {
	Name string
}

func (r *DummyResource) Init(ctx context.Context) error { return nil }
func (r *DummyResource) Close() error                   { return nil }

type DummyParent struct {
	Res schema.ResourceSchema[*DummyResource]
}

type DummyResourceFieldNonPointer struct {
	resource DummyResource
}

type DummyResourceFieldNonStruct struct {
	resource *int
}

type StepConfig struct {
	Val string
}

type StepInput struct {
	ID int64
}

type StepOutput struct {
	Result string
}

type UnsupportedTypeInput struct {
	Complex complex128
}

type UnsupportedTypeOutput struct {
	Channel chan int
}

type DummyStep struct{}

func (s DummyStep) ValidStep(ctx context.Context, config StepConfig, in schema.Frame[StepInput], out schema.Frame[StepOutput]) error {
	return nil
}

func (s DummyStep) BadNumIn(ctx context.Context, config StepConfig) error {
	return nil
}

func (s DummyStep) BadNumOut(ctx context.Context, config StepConfig, in schema.Frame[StepInput], out schema.Frame[StepOutput]) (int, error) {
	return 0, nil
}

func (s DummyStep) BadCtx(ctx string, config StepConfig, in schema.Frame[StepInput], out schema.Frame[StepOutput]) error {
	return nil
}

func (s DummyStep) BadConfig(ctx context.Context, config int, in schema.Frame[StepInput], out schema.Frame[StepOutput]) error {
	return nil
}

func (s DummyStep) BadInput(ctx context.Context, config StepConfig, in StepInput, out schema.Frame[StepOutput]) error {
	return nil
}

func (s DummyStep) BadOutput(ctx context.Context, config StepConfig, in schema.Frame[StepInput], out StepOutput) error {
	return nil
}

func (s DummyStep) BadReturn(ctx context.Context, config StepConfig, in schema.Frame[StepInput], out schema.Frame[StepOutput]) int {
	return 0
}

func (s DummyStep) BadInputSchema(ctx context.Context, config StepConfig, in schema.Frame[UnsupportedTypeInput], out schema.Frame[StepOutput]) error {
	return nil
}

func (s DummyStep) BadOutputSchema(ctx context.Context, config StepConfig, in schema.Frame[StepInput], out schema.Frame[UnsupportedTypeOutput]) error {
	return nil
}

func TestReflecStruct(t *testing.T) {
	// 1. Success case: pointer to struct
	inst := DummyStep{}
	structType, err := reflecStruct(inst)
	require.NoError(t, err)
	assert.Equal(t, reflect.TypeFor[DummyStep](), structType)

	// 2. Error case: pointer to non-struct
	val := 42
	_, err = reflecStruct(&val)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected struct, got ptr")
}

func TestReflectResource(t *testing.T) {
	// 1. Success case
	parentType := reflect.TypeFor[DummyParent]()
	resField, found := parentType.FieldByName("Res")
	require.True(t, found)

	resReg, err := reflectResource(resField)
	require.NoError(t, err)
	assert.Equal(t, "dummy_resource", resReg.Name)
	assert.Equal(t, reflect.TypeFor[DummyResource](), resReg.ResourceType)
	assert.Equal(t, "DummyResource is a test resource.\n", resReg.Documentation)
	assert.Contains(t, resReg.SourceCode, "type DummyResource struct")
	assert.Contains(t, resReg.SourceFile, "reflection_test.go")
	assert.True(t, resReg.SourceLine > 0)

	// 2. Error case: resource generic type is not a pointer
	nonPointerField := reflect.StructField{
		Name: "Res",
		Type: reflect.TypeFor[DummyResourceFieldNonPointer](),
	}
	_, err = reflectResource(nonPointerField)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected pointer to struct")

	// 3. Error case: resource generic type is a pointer to non-struct
	nonStructField := reflect.StructField{
		Name: "Res",
		Type: reflect.TypeFor[DummyResourceFieldNonStruct](),
	}
	_, err = reflectResource(nonStructField)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must pointer to a struct, got pointer to int")
}

func TestReflectFunctionStep(t *testing.T) {
	structType := reflect.TypeFor[DummyStep]()

	// helper to get reflect.Method by name
	getMethod := func(name string) reflect.Method {
		m, found := structType.MethodByName(name)
		require.True(t, found, "method %s not found", name)
		return m
	}

	// 1. Success case
	mValid := getMethod("ValidStep")
	stepReg, err := reflectFunctionStep(structType, mValid)
	require.NoError(t, err)
	assert.Equal(t, "valid_step", stepReg.Name)
	assert.Equal(t, reflect.TypeFor[StepConfig](), stepReg.ConfigType)
	assert.Equal(t, reflect.TypeFor[schema.Frame[StepInput]](), stepReg.InputType)
	assert.Equal(t, reflect.TypeFor[schema.Frame[StepOutput]](), stepReg.OutputType)

	// 2. Error case: BadNumIn (wrong number of arguments)
	mBadNumIn := getMethod("BadNumIn")
	_, err = reflectFunctionStep(structType, mBadNumIn)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "has wrong signature")

	// 3. Error case: BadNumOut (wrong number of return parameters)
	mBadNumOut := getMethod("BadNumOut")
	_, err = reflectFunctionStep(structType, mBadNumOut)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "has wrong signature")

	// 4. Error case: BadCtx (first arg not context.Context)
	mBadCtx := getMethod("BadCtx")
	_, err = reflectFunctionStep(structType, mBadCtx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "first arg must be context.Context")

	// 5. Error case: BadConfig (second arg not a struct)
	mBadConfig := getMethod("BadConfig")
	_, err = reflectFunctionStep(structType, mBadConfig)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "config must be a struct")

	// 6. Error case: BadInput (third arg not a Ref/Frame)
	mBadInput := getMethod("BadInput")
	_, err = reflectFunctionStep(structType, mBadInput)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "input must be a Ref")

	// 7. Error case: BadOutput (fourth arg not a Ref/Frame)
	mBadOutput := getMethod("BadOutput")
	_, err = reflectFunctionStep(structType, mBadOutput)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "output must be a Ref")

	// 8. Error case: BadReturn (return type not implementing error)
	mBadReturn := getMethod("BadReturn")
	_, err = reflectFunctionStep(structType, mBadReturn)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "return type must implement error")

	// 9. Error case: BadInputSchema (input has unsupported type)
	mBadInputSchema := getMethod("BadInputSchema")
	_, err = reflectFunctionStep(structType, mBadInputSchema)
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "input: unsupported type")
	}

	// 10. Error case: BadOutputSchema (output has unsupported type)
	mBadOutputSchema := getMethod("BadOutputSchema")
	_, err = reflectFunctionStep(structType, mBadOutputSchema)
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "output: unsupported type")
	}
}
