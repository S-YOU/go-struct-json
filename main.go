package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/ioutil"
	"log"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/iancoleman/strcase"
	"github.com/jinzhu/inflection"
	"github.com/k0kubun/pp"
	"github.com/knq/snaker"
)

var (
	out  = flag.String("o", "", "output file")
	kind = flag.String("kind", "go", "override kind")
)

type Field struct {
	GoName     string   `json:"Name"`
	GoVarName  string   `json:"name"`
	NameJson   string   `json:"nameJson"`
	GoType     string   `json:"Type"`
	GoBaseType string   `json:"baseType"`
	IsArray    bool     `json:"isArray"`
	NotNull    bool     `json:"notNull"`
	Tag        string   `json:"tag,omitempty"`
	TagFaker   string   `json:"tagFaker,omitempty"`
	TagFixture string   `json:"tagFixture,omitempty"`
	TagGql     string   `json:"tagGql,omitempty"`
	Docs       []string `json:"docs,omitempty"`
	Comments   []string `json:"comments,omitempty"`
	Key        string   `json:"key"`
}

type Struct struct {
	GoName      string   `json:"Name"`
	GoVarName   string   `json:"name"`
	NameJson    string   `json:"nameJson"`
	GoShortName string   `json:"n"`
	GoNames     string   `json:"Names"`
	GoVarNames  string   `json:"names"`
	Docs        []string `json:"docs,omitempty"`
	Comments    []string `json:"comments,omitempty"`
	Fields      []Field  `json:"fields"`
	Embeds      []Field  `json:"-"`
	Key         string   `json:"key"`
}

func main() {
	flag.Parse()

	if err := process(); err != nil {
		log.Fatalln(err)
	}
}

type FileContent struct {
	Kind    string    `json:"kind"`
	SrcKind string    `json:"srcKind"`
	Data    []*Struct `json:"data"`
}

func process() error {
	tail := flag.Args()
	structs := make([]*Struct, 0)
	for _, p := range tail {
		structs = append(structs, parseFile(p)...)
	}
	sort.Slice(structs, func(i, j int) bool {
		return structs[i].Key < structs[j].Key
	})
	fileContent := FileContent{
		Kind:    *kind,
		SrcKind: "go",
		Data:    structs,
	}
	parsedJson, err := json.MarshalIndent(fileContent, "", "\t")
	if err != nil {
		return err
	}

	if *out == "-" {
		if _, err := os.Stdout.Write(parsedJson); err != nil {
			return err
		}
	} else {
		outFile := *out
		if outFile == "" {
			if len(tail) == 1 {
				outFile = strings.Replace(tail[0], ".go", ".json", 1)
			} else {
				log.Fatalln("outFile is none")
			}
		}
		if err := ioutil.WriteFile(outFile, parsedJson, 0644); err != nil {
			return err
		}
	}

	return nil
}

func plural(s string) string {
	out := inflection.Plural(s)
	if out == "information" {
		return "informations"
	} else if out == "Information" {
		return "Informations"
	}
	return out
}

func parseFile(p string) []*Struct {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, p, nil, parser.ParseComments)
	if err != nil {
		panic(err)
	}

	structs := make([]*Struct, 0)

	for _, node := range f.Decls {
		switch node.(type) {

		case *ast.GenDecl:
			genDecl := node.(*ast.GenDecl)
			for _, spec := range genDecl.Specs {
				switch spec.(type) {
				case *ast.TypeSpec:
					typeSpec := spec.(*ast.TypeSpec)

					switch typeSpec.Type.(type) {
					case *ast.StructType:
						typeName := typeSpec.Name.Name
						singularName := inflection.Singular(typeName)
						pluralName := plural(singularName)
						goName := snaker.ForceCamelIdentifier(singularName)
						st := &Struct{
							GoName:      goName,
							GoVarName:   typeName,
							GoNames:     snaker.ForceCamelIdentifier(pluralName),
							GoVarNames:  strcase.ToLowerCamel(pluralName),
							NameJson:    jsonName(typeName),
							GoShortName: shortName(goName),
							Key:         goName,
						}
						if genDecl.Doc != nil {
							st.Docs = make([]string, len(genDecl.Doc.List))
							for i, comment := range genDecl.Doc.List {
								st.Docs[i] = comment.Text
							}
						}
						if typeSpec.Comment != nil {
							st.Comments = make([]string, len(typeSpec.Comment.List))
							for i, comment := range typeSpec.Comment.List {
								st.Comments[i] = comment.Text
							}
						}
						structType := typeSpec.Type.(*ast.StructType)
						for _, field := range structType.Fields.List {
							tag, tagFaker, tagFixture, tagGql := "", "", "", ""
							if field.Tag != nil {
								tag, err = strconv.Unquote(field.Tag.Value)
								if err != nil {
									panic(err)
								}
								v := reflect.StructTag(tag)
								tagFaker = v.Get("faker")
								tagFixture = v.Get("fixture")
								if strings.HasPrefix(tagFixture, "string:") {
									tagFixture = strconv.Quote(tagFixture[7:])
								}
								tagGql = v.Get("gql")
							}
							comments := []string(nil)
							if field.Comment != nil {
								for _, c := range field.Comment.List {
									comments = append(comments, c.Text)
								}
							}
							docs := []string(nil)
							if field.Doc != nil {
								for _, c := range field.Doc.List {
									docs = append(docs, c.Text)
								}
							}
							fieldType := getType(field.Type)
							isArray, notNull := getIsArrayAndNotNull(field.Type)
							baseType := fieldType
							if isArray {
								fieldType = "[]" + fieldType
							}
							if len(field.Names) == 0 {
								st.Fields = append(st.Fields, Field{
									GoType:     fieldType,
									GoBaseType: baseType,
									IsArray:    isArray,
									NotNull:    notNull,
									Tag:        tag,
									TagFaker:   tagFaker,
									TagFixture: tagFixture,
									TagGql:     tagGql,
									Docs:       docs,
									Comments:   comments,
									Key:        fieldType,
								})
							} else {
								for _, name := range field.Names {
									nameJson := jsonName(name.Name)
									st.Fields = append(st.Fields, Field{
										GoName:     name.Name,
										GoVarName:  lowerCamel(name.Name),
										NameJson:   nameJson,
										GoType:     fieldType,
										GoBaseType: baseType,
										IsArray:    isArray,
										NotNull:    notNull,
										Tag:        tag,
										TagFaker:   tagFaker,
										TagFixture: tagFixture,
										TagGql:     tagGql,
										Docs:       docs,
										Comments:   comments,
										Key:        nameJson,
									})
								}
							}
						}
						structs = append(structs, st)
					}
				}
			}
		}
	}

	return structs
}

var shortNameRe = regexp.MustCompile("[A-Z]")

func shortName(s string) string {
	return strings.ToLower(strings.Join(shortNameRe.FindAllString(s, -1), ""))
}

func jsonName(s string) string {
	return strings.ReplaceAll(strcase.ToLowerCamel(s), "ID", "Id")
}

func lowerCamel(s string) string {
	if s == "" {
		return ""
	}
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToLower(r)) + s[n:]
}

func getType(field ast.Expr) string {
	switch typ := field.(type) {
	case *ast.Ident:
		return typ.Name
	case *ast.SelectorExpr:
		return fmt.Sprintf("%s.%s", typ.X, getType(typ.Sel))
	case *ast.StarExpr:
		return fmt.Sprintf("*%s", getType(typ.X))
	case *ast.ArrayType:
		return fmt.Sprintf("%s", getType(typ.Elt))
	default:
		_, _ = pp.Println("unknown getType", typ)
		return "---"
	}
}

func getIsArrayAndNotNull(field ast.Expr) (isArray bool, notNull bool) {
	switch typ := field.(type) {
	case *ast.Ident:
		return false, true
	case *ast.SelectorExpr:
		_, b := getIsArrayAndNotNull(typ.Sel)
		return false, b
	case *ast.StarExpr:
		a, _ := getIsArrayAndNotNull(typ.X)
		return a, false
	case *ast.ArrayType:
		_, b := getIsArrayAndNotNull(typ.Elt)
		return true, b
	default:
		return false, true
	}
}
