package internal

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"runtime"
	"sync"

	"go.uber.org/zap"

	"github.com/galgotech/heddle-lang/pkg/logger"
	baseplugin "github.com/galgotech/heddle-lang/pkg/plugin"
	"github.com/galgotech/heddle-sdk-go/schema"
	pluginschema "github.com/galgotech/heddle-sdk-go/schema"
)

type Registry interface {
	RegisterResource(name string, resource any) error
	RegisterStep(name string, fn any) error
	ResolveSchema(req baseplugin.ResolveSchemaRequest) baseplugin.ResolveSchemaResponse
	GetStep(name string) (StepRegistration, bool)
	GetResource(name string) (resourceRegistration, bool)
	AllSteps() map[string]StepRegistration
	AllResources() map[string]resourceRegistration
}

type schemaRegistry struct {
	namespace string
	resources map[string]resourceRegistration
	steps     map[string]StepRegistration
	mu        sync.RWMutex
}

func (r *schemaRegistry) RegisterResource(name string, resource any) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	typ := reflect.TypeOf(resource)

	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	if typ.Kind() != reflect.Struct {
		return fmt.Errorf("resource %q must be a struct or a pointer to a struct", name)
	}

	// Validate that the pointer to this struct implements the Resource interface
	ptrTyp := reflect.PointerTo(typ)
	if !ptrTyp.Implements(reflect.TypeFor[pluginschema.Resource]()) {
		return fmt.Errorf("resource %q (pointer type %s) must implement the Resource interface", name, ptrTyp)
	}

	resourceSchema, err := extractResourceAndConfigSchema(typ)
	if err != nil {
		logger.L().Error("Failed to extract resource schema", zap.String("resource", name), zap.Error(err))
		return fmt.Errorf("resource %q config: %w", name, err)
	}

	// Use reflection on reflect.New(typ) to find the Start method pointer for metadata extraction
	var fnPtr uintptr
	dummyVal := reflect.New(typ)
	if v := dummyVal.MethodByName("Start"); v.IsValid() {
		fnPtr = v.Pointer()
	}
	doc, code, file, line := extractMetadata(fnPtr)

	r.resources[name] = resourceRegistration{
		Name:           name,
		ResourceSchema: resourceSchema,
		ResourceType:   typ,
		Documentation:  doc,
		SourceCode:     code,
		SourceFile:     file,
		SourceLine:     line,
	}

	return nil
}

func (r *schemaRegistry) RegisterStep(name string, fn any) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	typ := reflect.TypeOf(fn)

	if typ.Kind() != reflect.Func {
		return fmt.Errorf("step %q must be a function", name)
	}

	// Ensure the function signature matches one of the expected contracts:
	// func(context.Context, TConfig, TInput, TOutput) error
	if typ.NumIn() != 4 || typ.NumOut() != 1 {
		return fmt.Errorf("step %q must have signature func(ctx, config, input, output) error", name)
	}

	configType := typ.In(1)
	inputType := typ.In(2)
	outputType := typ.In(3)

	configSchema, err := extractResourceAndConfigSchema(configType)
	if err != nil {
		logger.L().Error("Failed to extract step config schema", zap.String("step", name), zap.Error(err))
		return fmt.Errorf("step %q config: %w", name, err)
	}

	inputSchema, err := extractInputOutputSchema(inputType)
	if err != nil {
		logger.L().Error("Failed to extract step input schema", zap.String("step", name), zap.Error(err))
		return fmt.Errorf("step %q input: %w", name, err)
	}

	outputSchema, err := extractInputOutputSchema(outputType)
	if err != nil {
		logger.L().Error("Failed to extract step output schema", zap.String("step", name), zap.Error(err))
		return fmt.Errorf("step %q output: %w", name, err)
	}

	var inputHeddleFrameIndex int
	inputFieldsIndex := []int{}
	inType := inputType.Elem()
	for i := 0; i < inType.NumField(); i++ {
		f := inType.Field(i)
		if f.Type == reflect.TypeFor[pluginschema.HeddleFrame]() || f.Type == reflect.TypeFor[pluginschema.DynamicFrame]() {
			inputHeddleFrameIndex = i
		} else if !f.Anonymous {
			inputFieldsIndex = append(inputFieldsIndex, i)
		}
	}

	var outputHeddleFrameIndex int
	outputFieldsIndex := []int{}
	outType := outputType.Elem()
	for i := 0; i < outType.NumField(); i++ {
		f := outType.Field(i)
		if f.Type == reflect.TypeFor[pluginschema.HeddleFrame]() || f.Type == reflect.TypeFor[pluginschema.DynamicFrame]() {
			outputHeddleFrameIndex = i
		} else if !f.Anonymous {
			outputFieldsIndex = append(outputFieldsIndex, i)
		}
	}

	doc, code, file, line := extractMetadata(reflect.ValueOf(fn).Pointer())

	logger.L().Debug("Registering step", zap.String("name", name))
	r.steps[name] = StepRegistration{
		Name:                   name,
		Func:                   reflect.ValueOf(fn),
		ConfigSchema:           configSchema,
		ConfigType:             configType,
		InputSchema:            inputSchema,
		InputType:              inputType,
		OutputSchema:           outputSchema,
		OutputType:             outputType,
		Documentation:          doc,
		SourceCode:             code,
		SourceFile:             file,
		SourceLine:             line,
		inputHeddleFrameIndex:  inputHeddleFrameIndex,
		outputHeddleFrameIndex: outputHeddleFrameIndex,
		inputFieldsIndex:       inputFieldsIndex,
		outputFieldsIndex:      outputFieldsIndex,
	}

	return nil
}

func (r *schemaRegistry) ResolveSchema(req baseplugin.ResolveSchemaRequest) baseplugin.ResolveSchemaResponse {
	r.mu.RLock()
	targetStep, ok := r.steps[req.StepName]
	r.mu.RUnlock()

	if !ok {
		return baseplugin.ResolveSchemaResponse{Error: fmt.Sprintf("step %s not found", req.StepName)}
	}

	configVal := reflect.New(targetStep.ConfigType)
	if req.ConfigJSON != "" {
		if err := json.Unmarshal([]byte(req.ConfigJSON), configVal.Interface()); err != nil {
			return baseplugin.ResolveSchemaResponse{Error: fmt.Sprintf("failed to unmarshal config: %v", err)}
		}
	}

	if resolver, ok := configVal.Interface().(schema.TypeResolver); ok {
		input, output, err := resolver.ResolveTypes()
		if err != nil {
			return baseplugin.ResolveSchemaResponse{Error: fmt.Sprintf("failed to resolve types: %v", err)}
		}
		return baseplugin.ResolveSchemaResponse{
			Input:  input,
			Output: output,
		}
	}

	return baseplugin.ResolveSchemaResponse{
		Input:  targetStep.InputSchema,
		Output: targetStep.OutputSchema,
	}
}

func (r *schemaRegistry) GetStep(name string) (StepRegistration, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	step, ok := r.steps[name]
	return step, ok
}

func (r *schemaRegistry) GetResource(name string) (resourceRegistration, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	res, ok := r.resources[name]
	return res, ok
}

func (r *schemaRegistry) AllSteps() map[string]StepRegistration {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stepsCopy := make(map[string]StepRegistration, len(r.steps))
	for k, v := range r.steps {
		stepsCopy[k] = v
	}
	return stepsCopy
}

func (r *schemaRegistry) AllResources() map[string]resourceRegistration {
	r.mu.RLock()
	defer r.mu.RUnlock()

	resourcesCopy := make(map[string]resourceRegistration, len(r.resources))
	for k, v := range r.resources {
		resourcesCopy[k] = v
	}
	return resourcesCopy
}

func extractMetadata(fnPtr uintptr) (doc string, code string, file string, line int) {
	f := runtime.FuncForPC(fnPtr)
	if f == nil {
		return
	}
	file, line = f.FileLine(fnPtr)

	// Try to read the source file
	data, err := os.ReadFile(file)
	if err != nil {
		return
	}

	fset := token.NewFileSet()
	// Parse the file to get AST and comments
	node, err := parser.ParseFile(fset, file, data, parser.ParseComments)
	if err != nil {
		return
	}

	for _, decl := range node.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			// Check if this function corresponds to our line
			startLine := fset.Position(d.Pos()).Line
			endLine := fset.Position(d.End()).Line
			if startLine <= line && endLine >= line {
				if d.Doc != nil {
					doc = d.Doc.Text()
				}
				// Extract source code of the function
				start := fset.Position(d.Pos()).Offset
				end := fset.Position(d.End()).Offset
				code = string(data[start:end])
				return
			}
		case *ast.GenDecl:
			// Handle types (Resources)
			for _, spec := range d.Specs {
				if tSpec, ok := spec.(*ast.TypeSpec); ok {
					startLine := fset.Position(tSpec.Pos()).Line
					endLine := fset.Position(tSpec.End()).Line
					if startLine <= line && endLine >= line {
						if d.Doc != nil {
							doc = d.Doc.Text()
						}
						// Extract source code of the type declaration
						start := fset.Position(d.Pos()).Offset
						end := fset.Position(d.End()).Offset
						code = string(data[start:end])
						return
					}
				}
			}
		}
	}
	return
}

func NewRegistry(namespace string) Registry {
	return &schemaRegistry{
		namespace: namespace,
		resources: make(map[string]resourceRegistration),
		steps:     make(map[string]StepRegistration),
	}
}
