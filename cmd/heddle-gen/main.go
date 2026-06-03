package main

import (
	"bytes"
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
	Name      string
	ArrowType string
	GoType    string
}

type StructInfo struct {
	Name   string
	Fields []FieldInfo
}

type StepInfo struct {
	Name       string
	MethodName string
	Config     StructInfo
	Input      StructInfo
	Output     StructInfo
}

type ResourceInfo struct {
	Name   string
	GoType string
}

func main() {
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
						if x.Name.Name == "Steps" {
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
		log.Fatal("Could not find Steps struct")
	}

	var resources []ResourceInfo
	for _, field := range stepsStruct.Fields.List {
		if len(field.Names) == 0 {
			continue
		}
		// find schema.ResourceSchema[*Connection]
		switch t := field.Type.(type) {
		case *ast.IndexExpr:
			if sel, ok := t.X.(*ast.SelectorExpr); ok {
				if sel.Sel.Name == "ResourceSchema" {
					goType := typeToString(t.Index)
					// remove pointer
					goType = strings.TrimPrefix(goType, "*")
					resources = append(resources, ResourceInfo{
						Name:   field.Names[0].Name,
						GoType: goType,
					})
				}
			}
		}
	}

	var steps []StepInfo
	for _, method := range methods["Steps"] {
		if method.Name.Name == "ResolveTypeInput" || method.Name.Name == "ResolveTypeOutput" {
			continue
		}
		if len(method.Type.Params.List) != 4 {
			continue // ctx, config, in, out
		}

		step := StepInfo{
			Name:       toSnakeCase(method.Name.Name),
			MethodName: method.Name.Name,
		}

		// Config
		configTypeName := typeToString(method.Type.Params.List[1].Type)
		step.Config = extractStructInfo(configTypeName, structs)

		// Input Frame
		inFrameTypeName := extractFrameType(method.Type.Params.List[2].Type)
		if inFrameTypeName != "" {
			if inFrameTypeName != "schema.Void" && inFrameTypeName != "Void" {
				step.Input = extractStructInfo(inFrameTypeName, structs)
			}
		}

		// Output Frame
		outFrameTypeName := extractFrameType(method.Type.Params.List[3].Type)
		if outFrameTypeName != "" {
			if outFrameTypeName != "schema.Void" && outFrameTypeName != "Void" {
				step.Output = extractStructInfo(outFrameTypeName, structs)
			}
		}

		steps = append(steps, step)
	}

	tmpl := template.Must(template.New("gen").Funcs(template.FuncMap{
		"toSnakeCase": toSnakeCase,
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
		"Package":   pkgName,
		"Steps":     steps,
		"Resources": resources,
	})
	if err != nil {
		log.Fatal(err)
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		fmt.Println(buf.String())
		log.Fatal(err)
	}

	err = os.WriteFile("example_steps.gen.go", formatted, 0644)
	if err != nil {
		log.Fatal(err)
	}
}

func extractFrameType(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.IndexExpr:
		if sel, ok := t.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "Frame" {
			return typeToString(t.Index)
		}
	case *ast.SelectorExpr:
		if t.Sel.Name == "Void" {
			return "schema.Void"
		}
	case *ast.Ident:
		if t.Name == "Void" {
			return "schema.Void"
		}
	}
	return ""
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
		if strings.HasPrefix(goType, "schema.Frame") {
			continue // nested frame not supported perfectly yet, omit from arrow columns
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
	"github.com/galgotech/heddle-sdk-go/internal/registry"
	pluginschema "github.com/galgotech/heddle-sdk-go/schema"
)

func RegisterSteps(reg registry.Registry, steps *Steps) error {
	var err error

	{{range .Resources}}
	err = reg.RegisterResource(registry.ResourceRegistration{
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
	err = reg.RegisterStep(registry.StepRegistration{
		Name: "{{.Name}}",
		ConfigSchema: schema.FieldSchema{
			Fields: []schema.Field{
				{{range .Config.Fields}}
				{Name: "{{.Name}}", Type: "{{.ArrowType}}"},
				{{end}}
			},
		},
		{{if .Input.Name}}
		InputSchema: schema.FrameSchema{
			Columns: []schema.ColumnSchema{
				{{range .Input.Fields}}
				{Name: "{{.Name}}", ArrowType: "{{.ArrowType}}"},
				{{end}}
			},
		},
		{{else}}
		InputSchema: schema.FrameSchema{},
		{{end}}
		{{if .Output.Name}}
		OutputSchema: schema.FrameSchema{
			Columns: []schema.ColumnSchema{
				{{range .Output.Fields}}
				{Name: "{{.Name}}", ArrowType: "{{.ArrowType}}"},
				{{end}}
			},
		},
		{{else}}
		OutputSchema: schema.FrameSchema{},
		{{end}}
		Invoke: func(ctx context.Context, configJSON string, inColumns map[string]arrow.Array) (map[string]arrow.Array, error) {
			var cfg {{.Config.Name}}
			if configJSON != "" {
				if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
					return nil, err
				}
			}

			{{if .Input.Name}}
			{{range .Input.Fields}}
			in_{{.Name}} := inColumns["{{.Name}}"].({{arrowArrayType .ArrowType}})
			{{end}}
			
			inLen := 0
			{{if gt (len .Input.Fields) 0}}
			if in_{{(index .Input.Fields 0).Name}} != nil {
				inLen = in_{{(index .Input.Fields 0).Name}}.Len()
			}
			{{end}}

			inFrame := pluginschema.Frame[{{.Input.Name}}]{
				Iterator: func(yield func(item {{.Input.Name}})) error {
					for i := 0; i < inLen; i++ {
						item := {{.Input.Name}}{
							{{range .Input.Fields}}
							{{.Name}}: in_{{.Name}}.Value(i),
							{{end}}
						}
						yield(item)
					}
					return nil
				},
			}
			{{else}}
			inFrame := pluginschema.Void{}
			{{end}}

			{{if .Output.Name}}
			{{range .Output.Fields}}
			outBuilder_{{.Name}} := {{newArrowBuilder .ArrowType}}
			defer outBuilder_{{.Name}}.Release()
			{{end}}

			outFrame := pluginschema.Frame[{{.Output.Name}}]{
				Appender: func(item {{.Output.Name}}) {
					{{range .Output.Fields}}
					outBuilder_{{.Name}}.Append(item.{{.Name}})
					{{end}}
				},
			}
			{{else}}
			outFrame := pluginschema.Void{}
			{{end}}

			// inject resources
			{{range $.Resources}}
			if resInst, err := reg.GetResource("{{.Name | printf "%s" | toSnakeCase}}"); err == nil {
				if resType, ok := resInst.(*{{.GoType}}); ok {
					steps.{{.Name}}.SetResource(resType)
				}
			}
			{{end}}

			err := steps.{{.MethodName}}(ctx, cfg, inFrame, outFrame)
			if err != nil {
				return nil, err
			}

			outColumns := make(map[string]arrow.Array)
			{{if .Output.Name}}
			{{range .Output.Fields}}
			outColumns["{{.Name}}"] = outBuilder_{{.Name}}.NewArray()
			{{end}}
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
`
