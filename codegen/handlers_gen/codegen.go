// находясь в 99_hw_5/codegen сделать:
// go build -o codegen.exe handlers_gen/codegen.go
// ./codegen.exe api.go api_handlers.go
// go test -v
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

type GenSettings struct {
	URL    string
	Method string
	Auth   bool
}

func (s *GenSettings) Validate() error {
	if _, err := url.ParseRequestURI(s.URL); err != nil {
		return err
	}

	if s.Method == "" {
		return nil
	}

	s.Method = strings.ToUpper(s.Method)
	switch s.Method {
	case http.MethodGet, http.MethodPost:
	default:
		return fmt.Errorf("unsupport method %s to api generate", s.Method)
	}

	return nil
}

const (
	labelRequired  = "required"
	labelParamName = "paramname"
	labelEnum      = "enum"
	labelDefault   = "default"
	labelMin       = "min"
	labelMax       = "max"
)

var binaryLabels = map[string]bool{
	labelRequired:  false,
	labelParamName: true,
	labelEnum:      true,
	labelDefault:   true,
	labelMin:       true,
	labelMax:       true,
}

type TagParser struct {
	Raw string

	isRequired bool
	paramName  string
	enum       []string
	defaultVal string
	min        int
	max        int

	hasParamName bool
	hasEnum      bool
	hasDefault   bool
	hasMin       bool
	hasMax       bool
}

func (p *TagParser) Parse() error {
	labels := strings.Split(p.Raw, ",")
	for _, label := range labels {
		if label == "" {
			continue
		}

		parts := strings.SplitN(label, "=", 2)
		key, value := parts[0], ""

		isBinary := binaryLabels[key]
		if isBinary && len(parts) != 2 {
			return fmt.Errorf("label %s must have a value", key)
		}

		if isBinary {
			value = parts[1]
		}

		switch key {
		case labelRequired:
			p.isRequired = true
		case labelParamName:
			p.paramName = value
			p.hasParamName = true
		case labelEnum:
			p.enum = strings.Split(value, "|")
			p.hasEnum = true
		case labelDefault:
			p.defaultVal = value
			p.hasDefault = true
		case labelMin, labelMax:
			val, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("can't convert to int label value %s", key)
			}

			if key == labelMin {
				p.min = val
				p.hasMin = true
			} else {
				p.max = val
				p.hasMax = true
			}
		}
	}

	return nil
}

func (p *TagParser) IsRequired() bool {
	return p.isRequired
}

func (p *TagParser) ParamName() string {
	return p.paramName
}

func (p *TagParser) Enum() []string {
	return p.enum
}

func (p *TagParser) Default() string {
	return p.defaultVal
}

func (p *TagParser) Min() int {
	return p.min
}

func (p *TagParser) Max() int {
	return p.max
}

func (p *TagParser) HasParamName() bool {
	return p.hasParamName
}

func (p *TagParser) HasEnum() bool {
	return p.hasEnum
}

func (p *TagParser) HasDefault() bool {
	return p.hasDefault
}

func (p *TagParser) HasMin() bool {
	return p.hasMin
}

func (p *TagParser) HasMax() bool {
	return p.hasMax
}

func main() {
	if len(os.Args) != 3 {
		log.Fatal("usage: ./codegen <source_file> <destination_file>")
	}

	fset := token.NewFileSet()
	srcFilePath, dstFilePath := os.Args[1], os.Args[2]
	in, err := parser.ParseFile(fset, srcFilePath, nil, parser.ParseComments)
	if err != nil {
		log.Fatalf("failed to parse file %s due %v", srcFilePath, err)
	}

	out, err := os.Create(dstFilePath)
	if err != nil {
		log.Fatalf("failed to create destination file %s due %v", dstFilePath, err)
	}

	fmt.Fprintf(out, "package %s\n\n", in.Name.Name)
	fmt.Fprintf(out, "import (\n\t\"encoding/json\"\n\t\"fmt\"\n\t\"log\"\n\t\"net/http\"\n\t\"strconv\"\n)\n\n")

	fmt.Fprintf(out, "type ResponseBody struct {\n\tError    string     `json:\"error\"`\n\tResponse interface{} `json:\"response,omitempty\"`\n}\n\n")
	fmt.Fprintf(out, "func respondError(w http.ResponseWriter, err error) error {\n\tif apiErr, ok := err.(ApiError); ok {\n\t\tview := ResponseBody{Error: apiErr.Err.Error()}\n\t\tpayload, err := json.Marshal(view)\n\t\tif err != nil {\n\t\t\treturn fmt.Errorf(\"failed to marshal error due %v\", err)\n\t\t}\n\t\tw.Header().Set(\"Content-Type\", \"application/json\")\n\t\tw.WriteHeader(apiErr.HTTPStatus)\n\t\t_, err = w.Write(payload)\n\t\tif err != nil {\n\t\t\treturn fmt.Errorf(\"failed to write response body due %v\", err)\n\t\t}\n\t\treturn nil\n\t}\n\treturn respondError(w, ApiError{HTTPStatus: http.StatusInternalServerError, Err: err})\n}\n\n")

	pattern := regexp.MustCompile(`^//\s*apigen:api\s+({.+})$`)
	type MethodInfo struct {
		Func     *ast.FuncDecl
		Settings GenSettings
	}
	structMethods := make(map[string][]MethodInfo)
	structNames := make(map[string]string)

	for _, node := range in.Decls {
		fd, ok := node.(*ast.FuncDecl)
		if !ok || fd.Doc == nil || fd.Recv == nil || len(fd.Recv.List) == 0 {
			continue
		}
		genPayload := ""
		for _, comment := range fd.Doc.List {
			groups := pattern.FindStringSubmatch(comment.Text)
			if len(groups) > 1 {
				genPayload = groups[1]
				break
			}
		}
		if genPayload == "" {
			continue
		}
		settings := GenSettings{}
		if err := json.Unmarshal([]byte(genPayload), &settings); err != nil {
			log.Fatalf("failed to unmarshal apigen settings %q due %v", genPayload, err)
		}
		if err := settings.Validate(); err != nil {
			log.Fatalf("apigen settings validation due %v", err)
		}
		rec := fd.Recv.List[0]
		star, ok := rec.Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		ident, ok := star.X.(*ast.Ident)
		if !ok {
			continue
		}
		recName := rec.Names[0].Name
		structNames[recName] = ident.Name
		structMethods[ident.Name] = append(structMethods[ident.Name], MethodInfo{fd, settings})
	}

	for structName, methods := range structMethods {
		for _, m := range methods {
			fd := m.Func
			settings := m.Settings
			recName := fd.Recv.List[0].Names[0].Name
			fmt.Fprintf(out,
				"func (%s *%s) handle%s(w http.ResponseWriter, req *http.Request) {\n",
				recName, structName, fd.Name.Name)
			if settings.Auth {
				fmt.Fprintf(out,
					"\tif req.Header.Get(\"X-Auth\") != \"100500\" {\n\t\terr := fmt.Errorf(\"unauthorized\")\n\t\tif err = respondError(w, ApiError{http.StatusForbidden, err}); err != nil { log.Println(err) }\n\t\treturn\n\t}\n")
			}
			paramsType := fd.Type.Params.List[1].Type.(*ast.Ident).Name
			fmt.Fprintf(out, "\tparams := %s{}\n", paramsType)
			var structType *ast.StructType
			for _, decl := range in.Decls {
				ts, ok := decl.(*ast.GenDecl)
				if !ok {
					continue
				}
				for _, spec := range ts.Specs {
					tspec, ok := spec.(*ast.TypeSpec)
					if !ok || tspec.Name.Name != paramsType {
						continue
					}
					st, ok := tspec.Type.(*ast.StructType)
					if ok {
						structType = st
					}
				}
			}
			for _, field := range structType.Fields.List {
				fieldType := field.Type.(*ast.Ident).Name
				for _, fieldName := range field.Names {
					paramName := strings.ToLower(fieldName.Name)
					tag := ""
					if field.Tag != nil {
						tag = reflect.StructTag(field.Tag.Value[1 : len(field.Tag.Value)-1]).Get("apivalidator")
					}
					tp := &TagParser{Raw: tag}
					tp.Parse()
					if tp.ParamName() != "" {
						paramName = tp.ParamName()
					}
					if fieldType == "int" {
						fmt.Fprintf(out, "\t%sStr := req.FormValue(\"%s\")\n", paramName, paramName)
						fmt.Fprintf(out, "\tif %sStr != \"\" {\n", paramName)
						fmt.Fprintf(out, "\t\ttmp, err := strconv.Atoi(%sStr)\n", paramName)
						fmt.Fprintf(out, "\t\tif err != nil {\n")
						fmt.Fprintf(out, "\t\t\terr := fmt.Errorf(\"%s must be int\")\n", paramName)
						fmt.Fprintf(out, "\t\t\tif err = respondError(w, ApiError{http.StatusBadRequest, err}); err != nil {\n")
						fmt.Fprintf(out, "\t\t\t\tlog.Println(err)\n")
						fmt.Fprintf(out, "\t\t\t}\n")
						fmt.Fprintf(out, "\t\t\treturn\n")
						fmt.Fprintf(out, "\t\t}\n")
						fmt.Fprintf(out, "\t\tparams.%s = tmp\n", fieldName.Name)
						fmt.Fprintf(out, "\t}\n")
					} else {
						fmt.Fprintf(out, "\tparams.%s = req.FormValue(\"%s\")\n", fieldName.Name, paramName)
					}
					if tp.HasDefault() {
						if fieldType == "int" {
							fmt.Fprintf(out, "\tif params.%s == 0 {\n\t\tparams.%s = %s\n\t}\n", fieldName.Name, fieldName.Name, tp.Default())
						} else {
							fmt.Fprintf(out, "\tif params.%s == \"\" {\n\t\tparams.%s = \"%s\"\n\t}\n", fieldName.Name, fieldName.Name, tp.Default())
						}
					}
					if tp.IsRequired() {
						if fieldType == "int" {
							fmt.Fprintf(out, "\tif params.%s == 0 {\n", fieldName.Name)
							fmt.Fprintf(out, "\t\terr := fmt.Errorf(\"%s must me not empty\")\n", paramName)
							fmt.Fprintf(out, "\t\tif err = respondError(w, ApiError{http.StatusBadRequest, err}); err != nil {\n")
							fmt.Fprintf(out, "\t\t\tlog.Println(err)\n")
							fmt.Fprintf(out, "\t\t}\n")
							fmt.Fprintf(out, "\t\treturn\n")
							fmt.Fprintf(out, "\t}\n")
						} else {
							fmt.Fprintf(out, "\tif params.%s == \"\" {\n", fieldName.Name)
							fmt.Fprintf(out, "\t\terr := fmt.Errorf(\"%s must me not empty\")\n", paramName)
							fmt.Fprintf(out, "\t\tif err = respondError(w, ApiError{http.StatusBadRequest, err}); err != nil {\n")
							fmt.Fprintf(out, "\t\t\tlog.Println(err)\n")
							fmt.Fprintf(out, "\t\t}\n")
							fmt.Fprintf(out, "\t\treturn\n")
							fmt.Fprintf(out, "\t}\n")
						}
					}
					if tp.HasEnum() {
						fmt.Fprintf(out, "\tswitch params.%s {\n", fieldName.Name)
						for _, v := range tp.Enum() {
							if fieldType == "int" {
								fmt.Fprintf(out, "\tcase %s:\n", v)
							} else {
								fmt.Fprintf(out, "\tcase \"%s\":\n", v)
							}
						}
						fmt.Fprintf(out, "\tdefault:\n")
						fmt.Fprintf(out, "\t\terr := fmt.Errorf(\"%s must be one of [%s]\")\n", paramName, strings.Join(tp.Enum(), ", "))
						fmt.Fprintf(out, "\t\tif err = respondError(w, ApiError{http.StatusBadRequest, err}); err != nil {\n")
						fmt.Fprintf(out, "\t\t\tlog.Println(err)\n")
						fmt.Fprintf(out, "\t\t}\n")
						fmt.Fprintf(out, "\t\treturn\n")
						fmt.Fprintf(out, "\t}\n")
					}
					if tp.HasMin() {
						if fieldType == "int" {
							fmt.Fprintf(out, "\tif params.%s < %d {\n", fieldName.Name, tp.Min())
							fmt.Fprintf(out, "\t\terr := fmt.Errorf(\"%s must be >= %d\")\n", paramName, tp.Min())
							fmt.Fprintf(out, "\t\tif err = respondError(w, ApiError{http.StatusBadRequest, err}); err != nil {\n")
							fmt.Fprintf(out, "\t\t\tlog.Println(err)\n")
							fmt.Fprintf(out, "\t\t}\n")
							fmt.Fprintf(out, "\t\treturn\n")
							fmt.Fprintf(out, "\t}\n")
						} else {
							fmt.Fprintf(out, "\tif len(params.%s) < %d {\n", fieldName.Name, tp.Min())
							fmt.Fprintf(out, "\t\terr := fmt.Errorf(\"%s len must be >= %d\")\n", paramName, tp.Min())
							fmt.Fprintf(out, "\t\tif err = respondError(w, ApiError{http.StatusBadRequest, err}); err != nil {\n")
							fmt.Fprintf(out, "\t\t\tlog.Println(err)\n")
							fmt.Fprintf(out, "\t\t}\n")
							fmt.Fprintf(out, "\t\treturn\n")
							fmt.Fprintf(out, "\t}\n")
						}
					}
					if tp.HasMax() {
						if fieldType == "int" {
							fmt.Fprintf(out, "\tif params.%s > %d {\n", fieldName.Name, tp.Max())
							fmt.Fprintf(out, "\t\terr := fmt.Errorf(\"%s must be <= %d\")\n", paramName, tp.Max())
							fmt.Fprintf(out, "\t\tif err = respondError(w, ApiError{http.StatusBadRequest, err}); err != nil {\n")
							fmt.Fprintf(out, "\t\t\tlog.Println(err)\n")
							fmt.Fprintf(out, "\t\t}\n")
							fmt.Fprintf(out, "\t\treturn\n")
							fmt.Fprintf(out, "\t}\n")
						} else {
							fmt.Fprintf(out, "\tif len(params.%s) > %d {\n", fieldName.Name, tp.Max())
							fmt.Fprintf(out, "\t\terr := fmt.Errorf(\"%s len must be <= %d\")\n", paramName, tp.Max())
							fmt.Fprintf(out, "\t\tif err = respondError(w, ApiError{http.StatusBadRequest, err}); err != nil {\n")
							fmt.Fprintf(out, "\t\t\tlog.Println(err)\n")
							fmt.Fprintf(out, "\t\t}\n")
							fmt.Fprintf(out, "\t\treturn\n")
							fmt.Fprintf(out, "\t}\n")
						}
					}
				}
			}
			fmt.Fprintf(out,
				"\n\tres, err := %s.%s(req.Context(), params)\n\tif err != nil {\n\t\tif err = respondError(w, err); err != nil { log.Println(err) }\n\t\treturn\n\t}\n\tbody := ResponseBody{Response: res}\n\tpayload, err := json.Marshal(body)\n\tif err != nil {\n\t\tif err = respondError(w, err); err != nil { log.Println(err) }\n\t\treturn\n\t}\n\tw.Header().Set(\"Content-Type\", \"application/json\")\n\tw.WriteHeader(http.StatusOK)\n\t_, err = w.Write(payload)\n\tif err != nil { log.Println(err) }\n}\n\n",
				recName, fd.Name.Name)
		}
	}

	for structName, methods := range structMethods {
		recName := ""
		for _, m := range methods {
			recName = m.Func.Recv.List[0].Names[0].Name
			break
		}
		fmt.Fprintf(out,
			"func (%s *%s) ServeHTTP(w http.ResponseWriter, req *http.Request) {\n\tswitch req.URL.Path {\n",
			recName, structName)
		for _, m := range methods {
			settings := m.Settings
			fmt.Fprintf(out, "\tcase \"%s\":\n", settings.URL)
			if settings.Method != "" {
				fmt.Fprintf(out, "\t\tif req.Method != \"%s\" {\n", settings.Method)
				fmt.Fprintf(out, "\t\t\terr := fmt.Errorf(\"bad method\")\n")
				fmt.Fprintf(out, "\t\t\tif err = respondError(w, ApiError{http.StatusNotAcceptable, err}); err != nil {\n")
				fmt.Fprintf(out, "\t\t\t\tlog.Println(err)\n")
				fmt.Fprintf(out, "\t\t\t}\n")
				fmt.Fprintf(out, "\t\t\treturn\n")
				fmt.Fprintf(out, "\t\t}\n")
			}
			fmt.Fprintf(out, "\t\t%s.handle%s(w, req)\n", recName, m.Func.Name.Name)
		}
		fmt.Fprintf(out, "\tdefault:\n\t\terr := fmt.Errorf(\"unknown method\"); if err = respondError(w, ApiError{http.StatusNotFound, err}); err != nil { log.Println(err) }\n\t}\n}\n\n")
	}
}
