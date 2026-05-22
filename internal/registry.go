package internal

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
	"time"

	"go.uber.org/zap"

	"github.com/galgotech/heddle-lang/pkg/logger"
	baseplugin "github.com/galgotech/heddle-lang/pkg/plugin"
	"github.com/galgotech/heddle-lang/pkg/schema"
	"github.com/galgotech/heddle-sdk-go/internal/resourcelink"
	pluginschema "github.com/galgotech/heddle-sdk-go/schema"
)

type Registry interface {
	RegisterGroup(group any) error
	ResolveSchema(req baseplugin.ResolveSchemaRequest) baseplugin.ResolveSchemaResponse
	GetStep(name string) (StepRegistration, bool)
	GetResource(name string) (resourceRegistration, bool)
	AllSteps() map[string]StepRegistration
	AllResources() map[string]resourceRegistration
	CloseAllResources()
}

type schemaRegistry struct {
	namespace string
	resources map[string]resourceRegistration
	steps     map[string]StepRegistration
	mu        sync.RWMutex
}

func (r *schemaRegistry) registerResourceLocked(name string, resource any) error {
	typ := reflect.TypeOf(resource)

	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	if typ.Kind() != reflect.Struct {
		return fmt.Errorf("resource %q must be a struct or a pointer to a struct", name)
	}

	// Validate that the pointer to this struct implements the Resource interface
	ptrTyp := reflect.PointerTo(typ)
	if !ptrTyp.Implements(reflect.TypeFor[pluginschema.ResourceInterface]()) {
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

	var inputSchema *schema.FrameSchema = targetStep.InputSchema
	var outputSchema *schema.FrameSchema = targetStep.OutputSchema
	hasResolved := false

	ptrType := reflect.PointerTo(targetStep.GroupInstance.Type())

	// Resolve dynamic input schema using method ResolveInput on the group receiver
	if method, ok := ptrType.MethodByName("ResolveInput"); ok {
		receiverVal := reflect.New(targetStep.GroupInstance.Type())
		receiverVal.Elem().Set(targetStep.GroupInstance)
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
			hasResolved = true
		}
	}

	// Resolve dynamic output schema using method ResolveOutput on the group receiver
	if method, ok := ptrType.MethodByName("ResolveOutput"); ok {
		receiverVal := reflect.New(targetStep.GroupInstance.Type())
		receiverVal.Elem().Set(targetStep.GroupInstance)
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
			hasResolved = true
		}
	}

	if !hasResolved {
		if resolver, ok := configVal.Interface().(pluginschema.TypeResolver); ok {
			colsInput, err := resolver.ResolveTypeInput(context.Background(), configVal.Interface(), req.StepName)
			if err != nil {
				return baseplugin.ResolveSchemaResponse{Error: fmt.Sprintf("failed to resolve type input: %v", err)}
			}
			colsOutput, err := resolver.ResolveTypeOutput(context.Background(), configVal.Interface(), req.StepName)
			if err != nil {
				return baseplugin.ResolveSchemaResponse{Error: fmt.Sprintf("failed to resolve type output: %v", err)}
			}
			inputSchema = convertColSchemaToFrameSchema(colsInput)
			outputSchema = convertColSchemaToFrameSchema(colsOutput)
		}
	}

	return baseplugin.ResolveSchemaResponse{
		Input:  inputSchema,
		Output: outputSchema,
	}
}

func convertColSchemaToFrameSchema(cols []pluginschema.ColSchema) *schema.FrameSchema {
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
	return &schema.FrameSchema{
		Fields: fields,
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
	maps.Copy(resourcesCopy, r.resources)
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

func (r *schemaRegistry) RegisterGroup(group any) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	groupVal := reflect.ValueOf(group)
	groupType := groupVal.Type()

	var prototype reflect.Value

	if groupType.Kind() == reflect.Pointer {
		if groupType.Elem().Kind() != reflect.Struct {
			return fmt.Errorf("RegisterGroup: expected pointer to struct, got pointer to %s", groupType.Elem().Kind())
		}
		groupType = groupType.Elem()
		groupVal = groupVal.Elem()

		prototype = reflect.New(groupType).Elem()
		prototype.Set(groupVal)
	} else if groupType.Kind() == reflect.Struct {
		prototype = reflect.New(groupType).Elem()
		prototype.Set(groupVal)
	} else {
		return fmt.Errorf("RegisterGroup: expected pointer to struct, got %s", groupType.Kind())
	}

	// 1. Iterate over fields of groupType to find and register Resource fields
	for i := 0; i < groupType.NumField(); i++ {
		field := groupType.Field(i)
		if isResource(field.Type) {
			// Initialize the internal state of the Resource field in prototype
			resField := prototype.Field(i)
			if resField.CanAddr() {
				// Use 15 * time.Minute as the default idle timeout / TTL
				resourcelink.InitState(resField.Addr().Interface(), 15*time.Minute)
			}

			// The underlying resource type R is field.Type.Field(0).Type
			resType := field.Type.Field(0).Type

			structType := resType
			if structType.Kind() == reflect.Pointer {
				structType = structType.Elem()
			}
			resName := strings.ToLower(structType.Name())

			if _, exists := r.resources[resName]; !exists {
				var resourceObj any
				if resType.Kind() == reflect.Pointer {
					resourceObj = reflect.New(resType.Elem()).Interface()
				} else {
					resourceObj = reflect.New(resType).Interface()
				}

				err := r.registerResourceLocked(resName, resourceObj)
				if err != nil {
					return fmt.Errorf("failed to register resource %s: %w", resName, err)
				}
			}
		}
	}

	// 2. Iterate over methods of *groupType to register Steps
	ptrType := reflect.PointerTo(groupType)

	for i := 0; i < ptrType.NumMethod(); i++ {
		method := ptrType.Method(i)
		methodType := method.Type // func(receiver, ctx, config, in) (*out, error)

		if method.Name == "ResolveTypes" || method.Name == "ResolveInput" || method.Name == "ResolveOutput" {
			continue
		}

		if methodType.NumIn() != 4 || methodType.NumOut() != 2 {
			continue
		}

		ctxType := methodType.In(1)
		configType := methodType.In(2)
		inputType := methodType.In(3)
		outputType := methodType.Out(0)
		errType := methodType.Out(1)

		if !ctxType.Implements(reflect.TypeFor[context.Context]()) {
			continue
		}
		if errType != reflect.TypeFor[error]() {
			continue
		}

		stepConfigSchema, err := extractResourceAndConfigSchema(configType)
		if err != nil {
			return fmt.Errorf("step config schema for %s: %w", method.Name, err)
		}

		// Step name is only the method name in lowercase, independent of the struct name.
		stepName := strings.ToLower(method.Name)

		if inputType.Kind() != reflect.Pointer {
			return fmt.Errorf("step %q input: must be a pointer type, got %s", stepName, inputType.String())
		}
		if outputType.Kind() != reflect.Pointer {
			return fmt.Errorf("step %q output: must be a pointer type, got %s", stepName, outputType.String())
		}

		inputSchema, err := extractInputOutputSchema(inputType)
		if err != nil {
			return fmt.Errorf("step %q input: %w", stepName, err)
		}

		outputSchema, err := extractInputOutputSchema(outputType)
		if err != nil {
			return fmt.Errorf("step %q output: %w", stepName, err)
		}

		inputFieldsIndex := []int{}
		inType := inputType
		if inType.Kind() == reflect.Pointer {
			inType = inType.Elem()
		}
		if inType != reflect.TypeFor[pluginschema.Any]() {
			for j := 0; j < inType.NumField(); j++ {
				f := inType.Field(j)
				if !f.Anonymous {
					inputFieldsIndex = append(inputFieldsIndex, j)
				}
			}
		}

		outputFieldsIndex := []int{}
		outType := outputType
		if outType.Kind() == reflect.Pointer {
			outType = outType.Elem()
		}
		if outType != reflect.TypeFor[pluginschema.Any]() {
			for j := 0; j < outType.NumField(); j++ {
				f := outType.Field(j)
				if !f.Anonymous {
					outputFieldsIndex = append(outputFieldsIndex, j)
				}
			}
		}

		doc, code, file, line := extractMetadata(method.Func.Pointer())

		if existing, conflict := r.steps[stepName]; conflict {
			return fmt.Errorf("step %q already registered from %s (conflict with %s.%s)",
				stepName, existing.SourceFile,
				groupType.Name(), method.Name)
		}

		logger.L().Debug("Registering method step", zap.String("name", stepName))
		r.steps[stepName] = StepRegistration{
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
			inputFieldsIndex:  inputFieldsIndex,
			outputFieldsIndex: outputFieldsIndex,
			GroupInstance:     prototype,
		}
	}

	return nil
}

func (r *schemaRegistry) CloseAllResources() {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, step := range r.steps {
		v := step.GroupInstance
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			if isResource(field.Type()) {
				method := field.Addr().MethodByName("Close")
				if method.IsValid() {
					method.Call(nil)
				}
			}
		}
	}
}

func NewRegistry(namespace string) Registry {
	return &schemaRegistry{
		namespace: namespace,
		resources: make(map[string]resourceRegistration),
		steps:     make(map[string]StepRegistration),
	}
}
