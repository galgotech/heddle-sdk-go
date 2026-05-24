package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"

	"go.uber.org/zap"

	"github.com/galgotech/heddle-lang/pkg/logger"
	baseplugin "github.com/galgotech/heddle-lang/pkg/plugin"
	"github.com/galgotech/heddle-lang/pkg/schema"

	internalschema "github.com/galgotech/heddle-sdk-go/internal/schema"
	pluginschema "github.com/galgotech/heddle-sdk-go/schema"
)

type Registry interface {
	Register(structStep any) error

	ResolveSchema(request baseplugin.ResolveSchemaRequest) baseplugin.ResolveSchemaResponse
	GetStep(name string) (StepRegistration, bool)
	GetResource(name string) (ResourceRegistration, bool)
	AllSteps() map[string]StepRegistration
	AllResources() map[string]ResourceRegistration
	CloseAllResources()
}

type schemaRegistry struct {
	resourceStates map[string]pluginschema.ResourceDefinition
	resources      map[string]ResourceRegistration
	steps          map[string]StepRegistration
	mu             sync.RWMutex
}

func (r *schemaRegistry) Register(instance any) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	pointerVal := reflect.ValueOf(instance)
	pointerType := pointerVal.Type()

	if pointerType.Kind() != reflect.Pointer {
		return fmt.Errorf("Register: expected pointer to struct, got %s", pointerType.Kind())
	}
	if pointerType.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("Register: expected pointer to struct, got pointer to %s", pointerType.Elem().Kind())
	}
	structVal := pointerVal.Elem()
	structType := pointerType.Elem()

	// 1. Iterate over fields of groupType to find and register Resource fields
	for fieldType := range structType.Fields() {
		if internalschema.IsResource(fieldType.Type) {
			// Initialize the internal state of the Resource field in prototype

			// The underlying resource type R is field.Type.Field(0).Type
			resourcePointerType := fieldType.Type.Field(0).Type
			if resourcePointerType.Kind() != reflect.Pointer {
				return fmt.Errorf("Register: expected pointer to struct, got pointer to %s", resourcePointerType.Kind())
			}
			resourceType := resourcePointerType.Elem()
			resourceName := strings.ToLower(resourceType.Name())

			if _, exists := r.resources[resourceName]; exists {
				return fmt.Errorf("Resource %s already registered", resourceName)
			}

			resourceInstance := reflect.New(resourceType).Interface()
			err := r.registerResourceLocked(resourceName, resourceInstance)
			if err != nil {
				return fmt.Errorf("failed to register resource %s: %w", resourceName, err)
			}
		}
	}

	// 2. Iterate over methods of *groupType to register Steps
	for method := range pointerType.Methods() {
		methodType := method.Type // func(receiver, ctx, config, in) *out

		if method.Name == "ResolveTypeInput" || method.Name == "ResolveTypeOutput" {
			logger.L().Debug("Skipping method %s", zap.String("method", method.Name))
			continue
		}

		if methodType.NumIn() != 4 || methodType.NumOut() != 1 {
			return fmt.Errorf("step %s has wrong signature: %s", method.Name, methodType.String())
		}

		ctxType := methodType.In(1)
		configType := methodType.In(2)
		inputType := methodType.In(3)
		outputType := methodType.Out(0)

		if !ctxType.Implements(reflect.TypeFor[context.Context]()) {
			return fmt.Errorf("step %s first arg must be context.Context, got %s", method.Name, ctxType.String())
		}
		if configType.Kind() != reflect.Struct {
			return fmt.Errorf("step %s config must be a pointer to a struct, got %s", method.Name, configType.String())
		}
		if inputType.Kind() != reflect.Pointer || inputType.Elem().Kind() != reflect.Struct {
			return fmt.Errorf("step %s input must be a pointer to a struct, got %s", method.Name, inputType.String())
		}
		if outputType.Kind() != reflect.Pointer || outputType.Elem().Kind() != reflect.Struct {
			return fmt.Errorf("step %s output must be a pointer to a struct, got %s", method.Name, outputType.String())
		}

		stepConfigSchema, err := internalschema.ExtractFieldSchema(configType)
		if err != nil {
			return fmt.Errorf("step config schema for %s: %w", method.Name, err)
		}

		// Step name is converted from CamelCase/PascalCase to snake_case.
		stepName := toSnakeCase(method.Name)

		inputSchema, err := internalschema.ExtractInputOutputSchema(inputType)
		if err != nil {
			return fmt.Errorf("step %q input: %w", stepName, err)
		}

		outputSchema, err := internalschema.ExtractInputOutputSchema(outputType)
		if err != nil {
			return fmt.Errorf("step %q output: %w", stepName, err)
		}

		inputFieldsIndex := []int{}
		if inputType.Elem() != reflect.TypeFor[pluginschema.Any]() {
			for j := 0; j < inputType.Elem().NumField(); j++ {
				f := inputType.Elem().Field(j)
				if internalschema.IsCol(f.Type) && !f.Anonymous {
					inputFieldsIndex = append(inputFieldsIndex, j)
				}
			}
		}

		outputFieldsIndex := []int{}
		if outputType.Elem() != reflect.TypeFor[pluginschema.Any]() {
			for j := 0; j < outputType.Elem().NumField(); j++ {
				f := outputType.Elem().Field(j)
				if !f.Anonymous {
					outputFieldsIndex = append(outputFieldsIndex, j)
				}
			}
		}

		doc, code, file, line := extractFuncMetadata(method.Func.Pointer())
		if existing, conflict := r.steps[stepName]; conflict {
			return fmt.Errorf("step %q already registered from %s (conflict with %s.%s)",
				stepName, existing.SourceFile,
				structType.Name(), method.Name)
		}

		logger.L().Debug("Registering method step", zap.String("name", stepName))
		r.steps[stepName] = StepRegistration{
			StructVal:         structVal,
			Name:              stepName,
			Func:              method.Func,
			ConfigSchema:      stepConfigSchema,
			ConfigType:        configType,
			InputSchema:       inputSchema,
			InputType:         inputType,
			OutputSchema:      outputSchema,
			OutputType:        outputType,
			Documentation:     doc,
			SourceCode:        code,
			SourceFile:        file,
			SourceLine:        line,
			InputFieldsIndex:  inputFieldsIndex,
			OutputFieldsIndex: outputFieldsIndex,
		}
	}

	return nil
}

func (r *schemaRegistry) registerResourceLocked(name string, resource any) error {
	resourcePointerType := reflect.TypeOf(resource)
	if resourcePointerType.Kind() != reflect.Pointer {
		return fmt.Errorf("resource %q must be a pointer to a struct", name)
	}
	if resourcePointerType.Elem().Kind() != reflect.Struct {
		logger.L().Error("Failed to register resource, it is not a pointer to a struct", zap.String("resource", name))
		return fmt.Errorf("resource %q must be a struct or a pointer to a struct", name)
	}

	// Validate that the pointer to this struct implements the Resource interface
	if !resourcePointerType.Implements(reflect.TypeFor[pluginschema.ResourceDefinition]()) {
		logger.L().Error("Failed to register resource, it does not implement the Resource interface", zap.String("resource", name))
		return fmt.Errorf("resource %q (pointer type %s) must implement the Resource interface", name, resourcePointerType)
	}

	fieldSchema, err := internalschema.ExtractFieldSchema(resourcePointerType.Elem())
	if err != nil {
		logger.L().Error("Failed to extract resource schema", zap.String("resource", name), zap.Error(err))
		return fmt.Errorf("resource %q config: %w", name, err)
	}

	// Use reflection on reflect.New(typ) to find the Start method pointer for metadata extraction
	var fnPtr uintptr
	dummyVal := reflect.New(resourcePointerType)
	if v := dummyVal.MethodByName("Init"); v.IsValid() {
		fnPtr = v.Pointer()
	}
	doc, code, file, line := extractFuncMetadata(fnPtr)

	r.resources[name] = ResourceRegistration{
		Name:         name,
		FieldSchema:  fieldSchema,
		ResourceType: resourcePointerType,

		Documentation: doc,
		SourceCode:    code,
		SourceFile:    file,
		SourceLine:    line,
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

	inputSchema := targetStep.InputSchema
	outputSchema := targetStep.OutputSchema

	ptrType := reflect.PointerTo(targetStep.StructVal.Type())

	// Resolve dynamic input schema using method ResolveInput on the group receiver
	if method, ok := ptrType.MethodByName("ResolveTypeInput"); ok {
		receiverVal := reflect.New(targetStep.StructVal.Type())
		receiverVal.Elem().Set(targetStep.StructVal)
		ctxVal := reflect.ValueOf(context.Background())
		configArg := configVal.Elem()
		if method.Type.In(2).Kind() == reflect.Pointer {
			configArg = configVal
		}
		results := method.Func.Call([]reflect.Value{
			receiverVal,
			ctxVal,
			configArg,
			reflect.ValueOf(req.StepName),
		})
		if len(results) == 2 {
			if !results[1].IsNil() {
				return baseplugin.ResolveSchemaResponse{Error: results[1].Interface().(error).Error()}
			}
			var cols []pluginschema.ColSchema
			if !results[0].IsNil() {
				cols = results[0].Interface().([]pluginschema.ColSchema)
			}
			inputSchema = convertColSchemaToFrameSchema(cols)
		}
	}

	// Resolve dynamic output schema using method ResolveOutput on the group receiver
	if method, ok := ptrType.MethodByName("ResolveTypeOutput"); ok {
		receiverVal := reflect.New(targetStep.StructVal.Type())
		receiverVal.Elem().Set(targetStep.StructVal)
		ctxVal := reflect.ValueOf(context.Background())
		configArg := configVal.Elem()
		if method.Type.In(2).Kind() == reflect.Pointer {
			configArg = configVal
		}
		results := method.Func.Call([]reflect.Value{
			receiverVal,
			ctxVal,
			configArg,
			reflect.ValueOf(req.StepName),
		})
		if len(results) == 2 {
			if !results[1].IsNil() {
				return baseplugin.ResolveSchemaResponse{Error: results[1].Interface().(error).Error()}
			}
			var cols []pluginschema.ColSchema
			if !results[0].IsNil() {
				cols = results[0].Interface().([]pluginschema.ColSchema)
			}
			outputSchema = convertColSchemaToFrameSchema(cols)
		}
	}

	return baseplugin.ResolveSchemaResponse{
		Input:  inputSchema,
		Output: outputSchema,
	}
}

func (r *schemaRegistry) GetStep(name string) (StepRegistration, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	step, ok := r.steps[name]
	return step, ok
}

func (r *schemaRegistry) GetResource(name string) (ResourceRegistration, bool) {
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

func (r *schemaRegistry) AllResources() map[string]ResourceRegistration {
	r.mu.RLock()
	defer r.mu.RUnlock()

	resourcesCopy := make(map[string]ResourceRegistration, len(r.resources))
	maps.Copy(resourcesCopy, r.resources)
	return resourcesCopy
}

func (r *schemaRegistry) CloseAllResources() {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, step := range r.steps {
		v := step.StructVal
		for _, field := range v.Fields() {
			if internalschema.IsResource(field.Type()) {
				method := field.Addr().MethodByName("Close")
				if method.IsValid() {
					method.Call(nil)
				}
			}
		}
	}
}

func extractFuncMetadata(fnPtr uintptr) (doc string, code string, file string, line int) {
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

func convertColSchemaToFrameSchema(cols []pluginschema.ColSchema) schema.FrameSchema {
	fields := make([]schema.FrameSchemaField, 0, len(cols))
	for _, col := range cols {
		arrowType := col.Type
		if arrowType == "string" {
			arrowType = "utf8"
		}
		fields = append(fields, schema.FrameSchemaField{
			Name:      col.Name,
			ArrowType: arrowType,
		})
	}
	return schema.FrameSchema{
		Fields: fields,
	}
}

func NewRegistry() Registry {
	return &schemaRegistry{
		resources: make(map[string]ResourceRegistration),
		steps:     make(map[string]StepRegistration),
	}
}
