package registry

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"runtime"

	"github.com/galgotech/heddle-lang/pkg/logger"
	"go.uber.org/zap"

	"github.com/galgotech/heddle-sdk-go/internal/schema"
	publicschema "github.com/galgotech/heddle-sdk-go/schema"
)

func reflecStruct(instance any) (reflect.Type, error) {
	structVal := reflect.ValueOf(instance)
	structType := structVal.Type()

	if structType.Kind() != reflect.Struct {
		return nil, fmt.Errorf("Register: expected struct, got %s", structType.Kind())
	}

	return structType, nil
}

func reflectResource(fieldType reflect.StructField) (ResourceRegistration, error) {
	resourcePointerType := fieldType.Type.Field(0).Type
	if resourcePointerType.Kind() != reflect.Pointer {
		return ResourceRegistration{}, fmt.Errorf("Register: expected pointer to struct, got %s", resourcePointerType.Kind())
	}

	if resourcePointerType.Elem().Kind() != reflect.Struct {
		return ResourceRegistration{}, fmt.Errorf("resource %q must pointer to a struct, got pointer to %s", fieldType.Name, resourcePointerType.Elem().Kind())
	}

	resourceType := resourcePointerType.Elem()
	resourceName := toSnakeCase(resourceType.Name())

	fieldSchema, err := schema.ExtractFieldSchema(resourceType)
	if err != nil {
		return ResourceRegistration{}, fmt.Errorf("resource %q config: %w", resourceName, err)
	}

	var (
		docText    string
		sourceCode string
		sourceFile string
		sourceLine int
	)

	if method, ok := resourcePointerType.MethodByName("Init"); ok {
		pc := method.Func.Pointer()
		if fn := runtime.FuncForPC(pc); fn != nil {
			file, line := fn.FileLine(pc)
			sourceFile = file
			sourceLine = line

			if file != "" {
				if data, err := os.ReadFile(file); err == nil {
					fset := token.NewFileSet()
					if fileAST, err := parser.ParseFile(fset, file, data, parser.ParseComments); err == nil {
						for _, decl := range fileAST.Decls {
							genDecl, ok := decl.(*ast.GenDecl)
							if !ok || genDecl.Tok != token.TYPE {
								continue
							}

							for _, spec := range genDecl.Specs {
								typeSpec, ok := spec.(*ast.TypeSpec)
								if !ok || typeSpec.Name.Name != resourceType.Name() {
									continue
								}
								// Found matching struct type declaration!
								var doc *ast.CommentGroup
								if genDecl.Doc != nil {
									doc = genDecl.Doc
								} else if typeSpec.Doc != nil {
									doc = typeSpec.Doc
								}

								if doc != nil {
									docText = doc.Text()
								}

								sourceLine = fset.Position(genDecl.Pos()).Line

								startPos := fset.Position(genDecl.Pos()).Offset

								endPos := fset.Position(genDecl.End()).Offset
								if startPos >= 0 && endPos <= len(data) && startPos < endPos {
									sourceCode = string(data[startPos:endPos])
								}

								break
							}
						}
					}
				}
			}
		}
	}

	return ResourceRegistration{
		Name:         resourceName,
		FieldSchema:  fieldSchema,
		ResourceType: resourceType,

		Documentation: docText,
		SourceCode:    sourceCode,
		SourceFile:    sourceFile,
		SourceLine:    sourceLine,
	}, nil
}

func reflectFunctionStep(structType reflect.Type, method reflect.Method) (StepRegistration, error) {
	methodType := method.Type // func(receiver, ctx, config, in) *out
	if methodType.NumIn() != 5 || methodType.NumOut() != 1 {
		return StepRegistration{}, fmt.Errorf("step %s has wrong signature: %s", method.Name, methodType.String())
	}

	ctxType := methodType.In(1)
	configType := methodType.In(2)
	inputType := methodType.In(3)
	outputType := methodType.In(4)
	returnType := methodType.Out(0)

	if !ctxType.Implements(reflect.TypeFor[context.Context]()) {
		return StepRegistration{}, fmt.Errorf("step %s first arg must be context.Context, got %s", method.Name, ctxType.String())
	}

	if configType.Kind() != reflect.Struct {
		return StepRegistration{}, fmt.Errorf("step %s config must be a struct, got %s", method.Name, configType.String())
	}

	if !schema.IsRef(inputType) {
		return StepRegistration{}, fmt.Errorf("step %s input must be a Ref, got %s", method.Name, inputType.String())
	}

	if !schema.IsRef(outputType) {
		return StepRegistration{}, fmt.Errorf("step %s output must be a Ref, got %s", method.Name, outputType.String())
	}

	if !returnType.Implements(reflect.TypeFor[error]()) {
		return StepRegistration{}, fmt.Errorf("step %s return type must implement error, got %s", method.Name, returnType.String())
	}

	stepConfigSchema, err := schema.ExtractFieldSchema(configType)
	if err != nil {
		return StepRegistration{}, fmt.Errorf("step config schema for %s: %w", method.Name, err)
	}

	// Step name is converted from CamelCase/PascalCase to snake_case.
	stepName := toSnakeCase(method.Name)

	inputSchema, err := schema.ExtractSchema(inputType.FieldByIndex([]int{publicschema.UnderLineTypePosition}).Type)
	if err != nil {
		return StepRegistration{}, fmt.Errorf("step %q input: %w", stepName, err)
	}

	outputSchema, err := schema.ExtractSchema(outputType.FieldByIndex([]int{publicschema.UnderLineTypePosition}).Type)
	if err != nil {
		return StepRegistration{}, fmt.Errorf("step %q output: %w", stepName, err)
	}

	inputFieldsIndex := []int{}
	outputFieldsIndex := []int{}

	var (
		docText    string
		sourceCode string
		sourceFile string
		sourceLine int
	)

	pc := method.Func.Pointer()
	if fn := runtime.FuncForPC(pc); fn != nil {
		file, line := fn.FileLine(pc)
		sourceFile = file
		sourceLine = line

		if file != "" {
			if data, err := os.ReadFile(file); err == nil {
				fset := token.NewFileSet()
				if fileAST, err := parser.ParseFile(fset, file, data, parser.ParseComments); err == nil {
					for _, decl := range fileAST.Decls {
						funcDecl, ok := decl.(*ast.FuncDecl)
						if !ok || funcDecl.Name.Name != method.Name {
							continue
						}

						if funcDecl.Recv == nil || len(funcDecl.Recv.List) == 0 {
							continue
						}

						recvTypeNode := funcDecl.Recv.List[0].Type

						var recvName string

						switch t := recvTypeNode.(type) {
						case *ast.Ident:
							recvName = t.Name
						case *ast.StarExpr:
							if ident, ok := t.X.(*ast.Ident); ok {
								recvName = ident.Name
							}
						}

						if recvName != structType.Name() {
							continue
						}

						// Found matching receiver and method name!
						if funcDecl.Doc != nil {
							docText = funcDecl.Doc.Text()
						}

						sourceLine = fset.Position(funcDecl.Pos()).Line

						startPos := fset.Position(funcDecl.Pos()).Offset

						endPos := fset.Position(funcDecl.End()).Offset
						if startPos >= 0 && endPos <= len(data) && startPos < endPos {
							sourceCode = string(data[startPos:endPos])
						}

						break
					}
				}
			}
		}
	}

	logger.L().Debug("Registering method step", zap.String("name", stepName))

	return StepRegistration{
		StructType:        structType,
		Name:              stepName,
		Func:              method.Func,
		ConfigSchema:      stepConfigSchema,
		ConfigType:        configType,
		InputSchema:       inputSchema,
		InputType:         inputType,
		OutputSchema:      outputSchema,
		OutputType:        outputType,
		Documentation:     docText,
		SourceCode:        sourceCode,
		SourceFile:        sourceFile,
		SourceLine:        sourceLine,
		InputFieldsIndex:  inputFieldsIndex,
		OutputFieldsIndex: outputFieldsIndex,
	}, nil
}
