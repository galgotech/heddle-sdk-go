package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"log"
	"os"
	"strings"
	"text/template"
)

// To snake case utility
func toSnakeCase(s string) string {
	var res []rune
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				res = append(res, '_')
			}
			res = append(res, r+32)
		} else {
			res = append(res, r)
		}
	}
	return string(res)
}

type FieldInfo struct {
	Name          string
	ArrowType     string
	GoType        string
	IsNested      bool
	InnerStruct   StructInfo
	IsNestedInput bool
}

type StructInfo struct {
	Name   string
	Fields []FieldInfo
}

type StepInfo struct {
	Name       string
	MethodName string
	Input      StructInfo
	Output     StructInfo
	CallArgs   []string
}

type ResourceInfo struct {
	Name   string
	GoType string
}

func main() {
	var targetStruct string
	flag.StringVar(&targetStruct, "struct", "Steps", "Name of the target struct")
	flag.Parse()

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.ParseComments)
	if err != nil {
		log.Fatal(err)
	}

	var pkgName string
	structs := make(map[string]*ast.StructType)
	methods := make(map[string][]*ast.FuncDecl)
	var stepsStruct *ast.StructType

	for name, pkg := range pkgs {
		pkgName = name
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.TypeSpec:
					if s, ok := x.Type.(*ast.StructType); ok {
						structs[x.Name.Name] = s
						if x.Name.Name == targetStruct {
							stepsStruct = s
						}
					}
				case *ast.FuncDecl:
					if x.Recv != nil && len(x.Recv.List) > 0 {
						switch t := x.Recv.List[0].Type.(type) {
						case *ast.StarExpr:
							if id, ok := t.X.(*ast.Ident); ok {
								methods[id.Name] = append(methods[id.Name], x)
							}
						case *ast.Ident:
							methods[t.Name] = append(methods[t.Name], x)
						}
					}
				}
				return true
			})
		}
	}

	if stepsStruct == nil {
		log.Fatalf("Could not find struct %s", targetStruct)
	}

	var resources []ResourceInfo
	var configFields []FieldInfo

	for _, field := range stepsStruct.Fields.List {
		if len(field.Names) == 0 || !field.Names[0].IsExported() {
			continue
		}

		isResource := false
		switch t := field.Type.(type) {
		case *ast.IndexExpr:
			if sel, ok := t.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "ResourceSchema" {
				isResource = true
				goType := typeToString(t.Index)
				goType = strings.TrimPrefix(goType, "*")
				resources = append(resources, ResourceInfo{
					Name:   field.Names[0].Name,
					GoType: goType,
				})
			}
		}

		if !isResource {
			goType := typeToString(field.Type)
			configFields = append(configFields, FieldInfo{
				Name:      field.Names[0].Name,
				GoType:    goType,
				ArrowType: goTypeToArrow(goType),
			})
		}
	}

	var steps []StepInfo
	for _, method := range methods[targetStruct] {
		if len(method.Type.Params.List) < 1 {
			continue
		}

		firstParamType := typeToString(method.Type.Params.List[0].Type)
		if firstParamType != "context.Context" {
			continue
		}

		step := StepInfo{
			Name:       toSnakeCase(method.Name.Name),
			MethodName: method.Name.Name,
		}

		callArgs := []string{"ctx"}

		for _, param := range method.Type.Params.List[1:] {
			paramType := typeToString(param.Type)

			if strings.HasPrefix(paramType, "schema.FrameInput[") {
				innerType := strings.TrimSuffix(strings.TrimPrefix(paramType, "schema.FrameInput["), "]")
				step.Input = extractStructInfo(innerType, structs)
				callArgs = append(callArgs, "inFrame")
			} else if strings.HasPrefix(paramType, "schema.FrameOutput[") {
				innerType := strings.TrimSuffix(strings.TrimPrefix(paramType, "schema.FrameOutput["), "]")
				step.Output = extractStructInfo(innerType, structs)
				callArgs = append(callArgs, "outFrame")
			} else if paramType == "schema.Void" || paramType == "Void" {
				// Ignored, schema.Void is removed. Wait, do we add an empty argument? No.
			}
		}

		step.CallArgs = callArgs
		steps = append(steps, step)
	}

	tmpl := template.Must(template.New("gen").Funcs(template.FuncMap{
		"toSnakeCase": toSnakeCase,
		"join":        strings.Join,
		"arrowBuilder": func(arrowType string) string {
			switch arrowType {
			case "int64":
				return "*array.Int64Builder"
			case "int32":
				return "*array.Int32Builder"
			case "utf8":
				return "*array.StringBuilder"
			case "bool":
				return "*array.BooleanBuilder"
			case "float64":
				return "*array.Float64Builder"
			default:
				return "*array.UnknownBuilder"
			}
		},
		"newArrowBuilder": func(arrowType string) string {
			switch arrowType {
			case "int64":
				return "array.NewInt64Builder(memory.DefaultAllocator)"
			case "int32":
				return "array.NewInt32Builder(memory.DefaultAllocator)"
			case "utf8":
				return "array.NewStringBuilder(memory.DefaultAllocator)"
			case "bool":
				return "array.NewBooleanBuilder(memory.DefaultAllocator)"
			case "float64":
				return "array.NewFloat64Builder(memory.DefaultAllocator)"
			default:
				return "nil"
			}
		},
		"arrowArrayType": func(arrowType string) string {
			switch arrowType {
			case "int64":
				return "*array.Int64"
			case "int32":
				return "*array.Int32"
			case "utf8":
				return "*array.String"
			case "bool":
				return "*array.Boolean"
			case "float64":
				return "*array.Float64"
			default:
				return "arrow.Array"
			}
		},
	}).Parse(codegenTemplate))

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, map[string]interface{}{
		"Package":      pkgName,
		"StructName":   targetStruct,
		"Steps":        steps,
		"Resources":    resources,
		"ConfigFields": configFields,
	})
	if err != nil {
		log.Fatal(err)
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		fmt.Println(buf.String())
		log.Fatal(err)
	}

	fileName := fmt.Sprintf("%s_steps.gen.go", toSnakeCase(targetStruct))
	err = os.WriteFile(fileName, formatted, 0644)
	if err != nil {
		log.Fatal(err)
	}
}

func extractStructInfo(name string, structs map[string]*ast.StructType) StructInfo {
	s, ok := structs[name]
	if !ok {
		return StructInfo{Name: name}
	}

	info := StructInfo{Name: name}
	for _, field := range s.Fields.List {
		if len(field.Names) == 0 {
			continue
		}
		goType := typeToString(field.Type)
		if strings.HasPrefix(goType, "schema.FrameInput[") || strings.HasPrefix(goType, "schema.FrameOutput[") || strings.HasPrefix(goType, "schema.Frame[") {
			innerType := goType
			innerType = strings.TrimPrefix(innerType, "schema.FrameInput[")
			innerType = strings.TrimPrefix(innerType, "schema.FrameOutput[")
			innerType = strings.TrimPrefix(innerType, "schema.Frame[")
			innerType = strings.TrimSuffix(innerType, "]")
			innerStruct := extractStructInfo(innerType, structs)
			info.Fields = append(info.Fields, FieldInfo{
				Name:          field.Names[0].Name,
				GoType:        goType,
				ArrowType:     "list",
				IsNested:      true,
				InnerStruct:   innerStruct,
				IsNestedInput: strings.HasPrefix(goType, "schema.FrameInput[") || strings.HasPrefix(goType, "schema.Frame["),
			})
			continue
		}
		info.Fields = append(info.Fields, FieldInfo{
			Name:      field.Names[0].Name,
			GoType:    goType,
			ArrowType: goTypeToArrow(goType),
		})
	}
	return info
}

func goTypeToArrow(t string) string {
	switch t {
	case "int", "int64":
		return "int64"
	case "int32":
		return "int32"
	case "string":
		return "utf8"
	case "bool":
		return "bool"
	case "float64":
		return "float64"
	default:
		return "unknown"
	}
}

func typeToString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return typeToString(t.X) + "." + t.Sel.Name
	case *ast.StarExpr:
		return "*" + typeToString(t.X)
	case *ast.IndexExpr:
		return typeToString(t.X) + "[" + typeToString(t.Index) + "]"
	default:
		return ""
	}
}

const codegenTemplate = `// Code generated by heddle-gen. DO NOT EDIT.
package {{.Package}}

import (
	"context"
	"encoding/json"

	"github.com/apache/arrow/go/v18/arrow"
	"github.com/apache/arrow/go/v18/arrow/array"
	"github.com/apache/arrow/go/v18/arrow/memory"

	"github.com/galgotech/heddle-lang/pkg/schema"
	"github.com/galgotech/heddle-sdk-go/plugin"
	pluginschema "github.com/galgotech/heddle-sdk-go/schema"
)

func Register{{if eq .StructName "Steps"}}{{.StructName}}{{else}}{{.StructName}}Steps{{end}}(p *plugin.Plugin) error {
	globalSteps := &{{.StructName}}{}
	var err error

	{{range .Resources}}
	err = p.RegisterResource(plugin.ResourceRegistration{
		Name: "{{.Name | printf "%s" | toSnakeCase}}",
		Init: func(ctx context.Context, configJSON string) (pluginschema.Resource, error) {
			var cfg map[string]any
			if configJSON != "" {
				if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
					return nil, err
				}
			}
			res := &{{.GoType}}{}
			configBytes, _ := json.Marshal(cfg)
			if len(configBytes) > 0 && string(configBytes) != "null" {
				json.Unmarshal(configBytes, res)
			}
			err := res.Init(ctx)
			return res, err
		},
	})
	if err != nil {
		return err
	}
	{{end}}

	{{range .Steps}}
	err = p.RegisterStep(plugin.StepRegistration{
		Name: "{{.Name}}",
		ConfigSchema: schema.FieldSchema{
			Fields: []schema.Field{
				{{range $.ConfigFields}}
				{Name: "{{.Name}}", Type: "{{.ArrowType}}"},
				{{end}}
			},
		},
		{{if .Input.Name}}
		InputSchema: []schema.ColumnSchema{
				{{range .Input.Fields}}
				{Name: "{{.Name}}", ArrowType: "{{.ArrowType}}"},
				{{end}}
		},
		{{else}}
		InputSchema: []schema.ColumnSchema{},
		{{end}}
		{{if .Output.Name}}
		OutputSchema: []schema.ColumnSchema{
			{{range .Output.Fields}}
			{Name: "{{.Name}}", ArrowType: "{{.ArrowType}}"},
			{{end}}
		},
		{{else}}
		OutputSchema: []schema.ColumnSchema{},
		{{end}}
		Invoke: func(ctx context.Context, configJSON string, inColumns map[string]arrow.Array) (map[string]arrow.Array, error) {
			stepInst := *globalSteps
			if configJSON != "" {
				if err := json.Unmarshal([]byte(configJSON), &stepInst); err != nil {
					return nil, err
				}
			}

			{{if .Input.Name}}
			{{range .Input.Fields}}
			{{if .IsNested}}
			var in_{{.Name}} *array.List
			if col, ok := inColumns["{{.Name}}"]; ok && col != nil {
				in_{{.Name}} = col.(*array.List)
			}
			{{else}}
			in_{{.Name}} := inColumns["{{.Name}}"].({{arrowArrayType .ArrowType}})
			{{end}}
			{{end}}
			
			inLen := 0
			{{if gt (len .Input.Fields) 0}}
			if in_{{(index .Input.Fields 0).Name}} != nil {
				inLen = in_{{(index .Input.Fields 0).Name}}.Len()
			}
			{{end}}

			outBuilder__errors := array.NewStringBuilder(memory.DefaultAllocator)
			defer outBuilder__errors.Release()

			inFrame := pluginschema.FrameInput[{{.Input.Name}}]{
				Iterator: func(yield func(item {{.Input.Name}}) error) error {
					for i := 0; i < inLen; i++ {
						{{range .Input.Fields}}
						{{if .IsNested}}
						{{if .IsNestedInput}}var nestedFrame_{{.Name}} pluginschema.FrameInput[{{.InnerStruct.Name}}]{{else}}var nestedFrame_{{.Name}} pluginschema.FrameOutput[{{.InnerStruct.Name}}]{{end}}
						if in_{{.Name}} != nil && !in_{{.Name}}.IsNull(i) {
							start := int(in_{{.Name}}.Offsets()[i])
							end := int(in_{{.Name}}.Offsets()[i+1])
							listValues := in_{{.Name}}.ListValues()
							
							if structArr, ok := listValues.(*array.Struct); ok {
								{{range .InnerStruct.Fields}}
								var l_{{.Name}} {{arrowArrayType .ArrowType}}
								{{end}}
								
								for c := 0; c < structArr.NumField(); c++ {
									field := structArr.DataType().(*arrow.StructType).Field(c)
									colArr := structArr.Field(c)
									switch field.Name {
									{{range .InnerStruct.Fields}}
									case "{{.Name}}":
										l_{{.Name}}, _ = colArr.({{arrowArrayType .ArrowType}})
									{{end}}
									}
								}
								
								{{if .IsNestedInput}}
								nestedFrame_{{.Name}} = pluginschema.FrameInput[{{.InnerStruct.Name}}]{
									Iterator: func(y func(m {{.InnerStruct.Name}}) error) error {
										for j := start; j < end; j++ {
											m := {{.InnerStruct.Name}}{}
											{{range .InnerStruct.Fields}}
											if l_{{.Name}} != nil && !l_{{.Name}}.IsNull(j) {
												m.{{.Name}} = l_{{.Name}}.Value(j)
											}
											{{end}}
											if err := y(m); err != nil {
												itemBytes, _ := json.Marshal(m)
												errorPayload, _ := json.Marshal(map[string]any{"error": err.Error(), "item": json.RawMessage(itemBytes)})
												outBuilder__errors.Append(string(errorPayload))
											}
										}
										return nil
									},
								}
								{{end}}
							}
						}
						{{end}}
						{{end}}

						item := {{.Input.Name}}{
							{{range .Input.Fields}}
							{{if .IsNested}}
							{{.Name}}: nestedFrame_{{.Name}},
							{{else}}
							{{.Name}}: in_{{.Name}}.Value(i),
							{{end}}
							{{end}}
						}
						if err := yield(item); err != nil {
							itemBytes, _ := json.Marshal(item)
							errorPayload, _ := json.Marshal(map[string]any{"error": err.Error(), "item": json.RawMessage(itemBytes)})
							outBuilder__errors.Append(string(errorPayload))
						}
					}
					return nil
				},
			}
			{{end}}

			{{if .Output.Name}}
			{{range .Output.Fields}}
			outBuilder_{{.Name}} := {{newArrowBuilder .ArrowType}}
			defer outBuilder_{{.Name}}.Release()
			{{end}}

			outFrame := pluginschema.FrameOutput[{{.Output.Name}}]{
				Appender: func(item {{.Output.Name}}) {
					{{range .Output.Fields}}
					outBuilder_{{.Name}}.Append(item.{{.Name}})
					{{end}}
				},
			}
			{{end}}

			// inject resources
			{{range $.Resources}}
			if resInst, err := p.Registry().GetResource("{{.Name | printf "%s" | toSnakeCase}}"); err == nil {
				if resType, ok := resInst.(*{{.GoType}}); ok {
					stepInst.{{.Name}}.SetResource(resType)
				}
			}
			{{end}}

			err := stepInst.{{.MethodName}}({{join .CallArgs ", "}})
			if err != nil {
				return nil, err
			}

			outColumns := make(map[string]arrow.Array)
			{{if .Output.Name}}
			{{range .Output.Fields}}
			outColumns["{{.Name}}"] = outBuilder_{{.Name}}.NewArray()
			{{end}}
			{{end}}

			{{if .Input.Name}}
			if outBuilder__errors.Len() > 0 {
				outColumns["_errors"] = outBuilder__errors.NewArray()
			}
			{{end}}

			return outColumns, nil
		},
	})
	if err != nil {
		return err
	}
	{{end}}

	return nil
}
`
