package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
)

func runLintMutation(args []string) {
	fs := flag.NewFlagSet("lint-mutation", flag.ExitOnError)
	ddlPath := fs.String("ddl", "", "path to DDL file")
	_ = fs.Parse(args)

	if *ddlPath == "" || fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: go-spantool lint-mutation -ddl <ddl_file> <go_files...>")
		os.Exit(1)
	}

	ddlSrc, err := os.ReadFile(*ddlPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading DDL: %v\n", err)
		os.Exit(1)
	}

	schema, err := ParseDDL(string(ddlSrc))
	if err != nil {
		fmt.Fprintf(os.Stderr, "parsing DDL: %v\n", err)
		os.Exit(1)
	}

	diags, err := AnalyzeFiles(schema, fs.Args())
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	if len(diags) > 0 {
		for _, d := range diags {
			fmt.Fprintln(os.Stderr, d.String())
		}
		os.Exit(1)
	}
}

type Severity int

const (
	SeverityError Severity = iota
	SeverityWarning
)

type Diagnostic struct {
	Severity Severity
	File     string
	Line     int
	Message  string
}

func (d Diagnostic) String() string {
	if d.Severity == SeverityWarning {
		return fmt.Sprintf("%s:%d: warning: %s", d.File, d.Line, d.Message)
	}
	return fmt.Sprintf("%s:%d: %s", d.File, d.Line, d.Message)
}

func AnalyzeFile(fset *token.FileSet, file *ast.File, schema *Schema, path string) []Diagnostic {
	spannerIdent := spannerLocalName(file)
	if spannerIdent == "" {
		return nil
	}

	var diags []Diagnostic
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		funcName, ident := mutationFuncName(call, spannerIdent)
		if funcName == "" {
			return true
		}

		if len(call.Args) < 2 {
			return true
		}

		// Check 1st arg: table name must be a string literal
		tableLit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || tableLit.Kind != token.STRING {
			pos := fset.Position(call.Args[0].Pos())
			diags = append(diags, Diagnostic{
				File:    path,
				Line:    pos.Line,
				Message: fmt.Sprintf("%s.%s: table name must be a string literal", ident, funcName),
			})
			return true
		}
		tableName := strings.Trim(tableLit.Value, `"`)

		table, ok := schema.Tables[tableName]
		if !ok {
			pos := fset.Position(tableLit.Pos())
			diags = append(diags, Diagnostic{
				File:    path,
				Line:    pos.Line,
				Message: fmt.Sprintf("unknown table %q", tableName),
			})
			return true
		}

		// Check 2nd arg: must be a map composite literal
		mapLit, ok := call.Args[1].(*ast.CompositeLit)
		if !ok {
			pos := fset.Position(call.Args[1].Pos())
			diags = append(diags, Diagnostic{
				File:    path,
				Line:    pos.Line,
				Message: fmt.Sprintf("%s.%s(%q, ...): second argument must be a map[string]any literal", ident, funcName, tableName),
			})
			return true
		}

		// Validate map keys
		keys := extractMapKeys(fset, mapLit, path, &diags, ident, funcName, tableName)

		// Check column existence
		for _, key := range keys {
			col := table.ColumnByName(key.name)
			if col == nil {
				diags = append(diags, Diagnostic{
					File:    path,
					Line:    key.line,
					Message: fmt.Sprintf("table %q has no column %q", tableName, key.name),
				})
				continue
			}
			if col.Generated {
				diags = append(diags, Diagnostic{
					File:    path,
					Line:    key.line,
					Message: fmt.Sprintf("column %q in table %q is a generated column and cannot be written", key.name, tableName),
				})
			}
		}

		// Check completeness for Insert/InsertOrUpdate
		if funcName == "InsertMap" || funcName == "InsertOrUpdateMap" {
			keySet := make(map[string]bool)
			for _, k := range keys {
				keySet[k.name] = true
			}
			for _, reqCol := range table.RequiredColumns() {
				if !keySet[reqCol] {
					pos := fset.Position(mapLit.Pos())
					diags = append(diags, Diagnostic{
						File:    path,
						Line:    pos.Line,
						Message: fmt.Sprintf("%s(%q): missing required NOT NULL column %q", funcName, tableName, reqCol),
					})
				}
			}
		}

		// Check primary key presence for Update
		if funcName == "UpdateMap" {
			keySet := make(map[string]bool)
			for _, k := range keys {
				keySet[k.name] = true
			}
			for _, pk := range table.PrimaryKeys {
				if !keySet[pk] {
					pos := fset.Position(mapLit.Pos())
					diags = append(diags, Diagnostic{
						File:    path,
						Line:    pos.Line,
						Message: fmt.Sprintf("UpdateMap(%q): missing primary key column %q", tableName, pk),
					})
				}
			}
		}

		return true
	})

	return diags
}

type mapKey struct {
	name string
	line int
}

func extractMapKeys(fset *token.FileSet, mapLit *ast.CompositeLit, path string, diags *[]Diagnostic, ident, funcName, tableName string) []mapKey {
	var keys []mapKey
	for _, elt := range mapLit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		lit, ok := kv.Key.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			pos := fset.Position(kv.Key.Pos())
			*diags = append(*diags, Diagnostic{
				File:    path,
				Line:    pos.Line,
				Message: fmt.Sprintf("%s.%s(%q): map key must be a string literal", ident, funcName, tableName),
			})
			continue
		}
		pos := fset.Position(lit.Pos())
		keys = append(keys, mapKey{
			name: strings.Trim(lit.Value, `"`),
			line: pos.Line,
		})
	}
	return keys
}

func mutationFuncName(call *ast.CallExpr, spannerIdent string) (string, string) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", ""
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok || ident.Name != spannerIdent {
		return "", ""
	}
	switch sel.Sel.Name {
	case "InsertMap", "UpdateMap", "InsertOrUpdateMap":
		return sel.Sel.Name, ident.Name
	}
	return "", ""
}

func AnalyzeFiles(schema *Schema, paths []string) ([]Diagnostic, error) {
	var allDiags []Diagnostic
	fset := token.NewFileSet()
	for _, path := range paths {
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		diags := AnalyzeFile(fset, file, schema, path)
		allDiags = append(allDiags, diags...)
	}
	sort.Slice(allDiags, func(i, j int) bool {
		if allDiags[i].File != allDiags[j].File {
			return allDiags[i].File < allDiags[j].File
		}
		return allDiags[i].Line < allDiags[j].Line
	})
	return allDiags, nil
}
