package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/cloudspannerecosystem/memefish"
	memefishast "github.com/cloudspannerecosystem/memefish/ast"
)

func runLintScan(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: go-spantool lint-scan <go_files...>")
		os.Exit(1)
	}

	diags, err := AnalyzeScanFiles(args)
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

func AnalyzeScanFiles(paths []string) ([]Diagnostic, error) {
	var allDiags []Diagnostic
	fset := token.NewFileSet()
	for _, path := range paths {
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		diags := AnalyzeScanFile(fset, file, path)
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

func AnalyzeScanFile(fset *token.FileSet, file *ast.File, path string) []Diagnostic {
	spannerIdent := spannerLocalName(file)
	if spannerIdent == "" {
		return nil
	}

	// Collect function declarations in this file for scanXxx resolution
	funcDecls := make(map[string]*ast.FuncDecl)
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Recv != nil {
			continue
		}
		funcDecls[fd.Name.Name] = fd
	}

	var diags []Diagnostic
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		enclosingBody := fd.Body
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			sqlStr, sqlPos, callbackFn, rowName := findStatementAndCallback(call, spannerIdent)
			if sqlStr == "" || callbackFn == nil {
				return true
			}

			selInfo, err := extractSelectInfo(sqlStr)
			if err != nil {
				pos := fset.Position(sqlPos)
				diags = append(diags, Diagnostic{
					File:    path,
					Line:    pos.Line,
					Message: fmt.Sprintf("failed to parse SQL: %v", err),
				})
				return true
			}

			cbInfo := analyzeFuncBody(callbackFn.Body, rowName, spannerIdent, file, funcDecls, 0, enclosingBody)
			if cbInfo == nil {
				if !hasNolintComment(file, fset, callbackFn.Pos()) {
					pos := fset.Position(callbackFn.Pos())
					diags = append(diags, Diagnostic{
						File:    path,
						Line:    pos.Line,
						Message: "could not detect row.Columns or row.ToStruct usage in callback (to suppress, add //nolint:spantool comment)",
					})
				}
				return true
			}

			msgs := matchColumns(selInfo, cbInfo, fset, path)
			diags = append(diags, msgs...)

			return true
		})
	}

	return diags
}

func findStatementAndCallback(call *ast.CallExpr, spannerIdent string) (sqlStr string, sqlPos token.Pos, callbackFn *ast.FuncLit, rowName string) {
	for _, arg := range call.Args {
		if sqlStr == "" {
			if s, pos, ok := extractStatementSQL(arg, spannerIdent); ok {
				sqlStr = s
				sqlPos = pos
			}
		}
		if callbackFn == nil {
			if fn, name, ok := extractRowCallback(arg, spannerIdent); ok {
				callbackFn = fn
				rowName = name
			}
		}
	}
	return
}

func extractStatementSQL(expr ast.Expr, spannerIdent string) (string, token.Pos, bool) {
	comp, ok := expr.(*ast.CompositeLit)
	if !ok {
		return "", 0, false
	}
	sel, ok := comp.Type.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Statement" {
		return "", 0, false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok || ident.Name != spannerIdent {
		return "", 0, false
	}

	for _, elt := range comp.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "SQL" {
			continue
		}
		lit, ok := kv.Value.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			continue
		}
		sql, err := unquoteStringLit(lit.Value)
		if err != nil {
			continue
		}
		return sql, lit.Pos(), true
	}
	return "", 0, false
}

func unquoteStringLit(raw string) (string, error) {
	if len(raw) < 2 {
		return "", fmt.Errorf("invalid string literal")
	}
	if raw[0] == '`' {
		return raw[1 : len(raw)-1], nil
	}
	return strconv.Unquote(raw)
}

func extractRowCallback(expr ast.Expr, spannerIdent string) (*ast.FuncLit, string, bool) {
	fn, ok := expr.(*ast.FuncLit)
	if !ok {
		return nil, "", false
	}
	name, ok := isSpannerRowCallback(fn, spannerIdent)
	if !ok {
		return nil, "", false
	}
	return fn, name, true
}

func isSpannerRowCallback(fn *ast.FuncLit, spannerIdent string) (string, bool) {
	if fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
		return "", false
	}
	param := fn.Type.Params.List[0]
	if len(param.Names) == 0 {
		return "", false
	}
	if param.Names[0].Name == "_" {
		return "", false
	}
	if !isSpannerRowType(param.Type, spannerIdent) {
		return "", false
	}

	if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
		return "", false
	}
	resIdent, ok := fn.Type.Results.List[0].Type.(*ast.Ident)
	if !ok || resIdent.Name != "error" {
		return "", false
	}

	return param.Names[0].Name, true
}

func isSpannerRowType(expr ast.Expr, spannerIdent string) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Row" {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == spannerIdent
}

const (
	scanModeColumns  = "columns"
	scanModeToStruct = "toStruct"
)

type scanInfo struct {
	mode       string
	argCount   int      // for columns: number of arguments
	structTags []string // for toStruct: spanner tag names
	callPos    token.Pos
}

const maxScanDepth = 3

func analyzeFuncBody(body *ast.BlockStmt, rowName string, spannerIdent string, file *ast.File, funcDecls map[string]*ast.FuncDecl, depth int, enclosingBody *ast.BlockStmt) *scanInfo {
	var result *scanInfo
	ast.Inspect(body, func(n ast.Node) bool {
		if result != nil {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		// Case A/B: row.Columns(...) or row.ToStruct(...)
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == rowName {
				switch sel.Sel.Name {
				case "Columns":
					result = &scanInfo{
						mode:     scanModeColumns,
						argCount: len(call.Args),
						callPos:  call.Pos(),
					}
					return false
				case "ToStruct":
					if len(call.Args) == 1 {
						tags := resolveToStructTags(call.Args[0], body, enclosingBody, file)
						if tags != nil {
							result = &scanInfo{
								mode:       scanModeToStruct,
								structTags: tags,
								callPos:    call.Pos(),
							}
							return false
						}
					}
				}
			}
		}

		// Case C: scanXxx(row)
		if depth < maxScanDepth {
			if funcIdent, ok := call.Fun.(*ast.Ident); ok {
				for _, arg := range call.Args {
					if argIdent, ok := arg.(*ast.Ident); ok && argIdent.Name == rowName {
						if fd, exists := funcDecls[funcIdent.Name]; exists {
							innerRowName := findRowParamName(fd, spannerIdent)
							if innerRowName != "" {
								info := analyzeFuncBody(fd.Body, innerRowName, spannerIdent, file, funcDecls, depth+1, enclosingBody)
								if info != nil {
									result = info
									return false
								}
							}
						}
						break
					}
				}
			}
		}

		return true
	})
	return result
}

func findRowParamName(fd *ast.FuncDecl, spannerIdent string) string {
	if fd.Type.Params == nil {
		return ""
	}
	for _, param := range fd.Type.Params.List {
		if isSpannerRowType(param.Type, spannerIdent) && len(param.Names) > 0 {
			return param.Names[0].Name
		}
	}
	return ""
}

func resolveToStructTags(arg ast.Expr, body *ast.BlockStmt, enclosingBody *ast.BlockStmt, file *ast.File) []string {
	// arg should be &v (UnaryExpr with AND)
	unary, ok := arg.(*ast.UnaryExpr)
	if !ok || unary.Op != token.AND {
		return nil
	}
	varIdent, ok := unary.X.(*ast.Ident)
	if !ok {
		return nil
	}

	// Find the variable's type in the function body
	typeName := findVarTypeName(body, varIdent.Name)
	if typeName == "" {
		return nil
	}

	// Search order: callback body -> enclosing function body -> file top-level
	if tags := extractStructSpannerTagsFromBlock(body, typeName); tags != nil {
		return tags
	}
	if tags := extractStructSpannerTagsFromBlock(enclosingBody, typeName); tags != nil {
		return tags
	}
	return extractStructSpannerTags(file, typeName)
}

func findVarTypeName(body *ast.BlockStmt, varName string) string {
	for _, stmt := range body.List {
		switch s := stmt.(type) {
		case *ast.DeclStmt:
			gd, ok := s.Decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range vs.Names {
					if name.Name == varName {
						if ident, ok := vs.Type.(*ast.Ident); ok {
							return ident.Name
						}
					}
				}
			}
		case *ast.AssignStmt:
			if s.Tok != token.DEFINE {
				continue
			}
			for i, lhs := range s.Lhs {
				if ident, ok := lhs.(*ast.Ident); ok && ident.Name == varName {
					if i < len(s.Rhs) {
						if comp, ok := s.Rhs[i].(*ast.CompositeLit); ok {
							if typeIdent, ok := comp.Type.(*ast.Ident); ok {
								return typeIdent.Name
							}
						}
					}
				}
			}
		}
	}
	return ""
}

func hasNolintComment(file *ast.File, fset *token.FileSet, pos token.Pos) bool {
	targetLine := fset.Position(pos).Line
	for _, cg := range file.Comments {
		for _, c := range cg.List {
			if fset.Position(c.Pos()).Line == targetLine && strings.Contains(c.Text, "nolint:spantool") {
				return true
			}
		}
	}
	return false
}

func extractTagsFromStructType(st *ast.StructType) []string {
	var tags []string
	for _, field := range st.Fields.List {
		if field.Tag != nil {
			tagStr, err := strconv.Unquote(field.Tag.Value)
			if err != nil {
				continue
			}
			spannerTag := reflect.StructTag(tagStr).Get("spanner")
			if spannerTag != "" && spannerTag != "-" {
				tags = append(tags, spannerTag)
				continue
			}
		}
		// Fallback to field name
		for _, name := range field.Names {
			tags = append(tags, name.Name)
		}
	}
	return tags
}

func extractStructSpannerTagsFromBlock(body *ast.BlockStmt, typeName string) []string {
	if body == nil {
		return nil
	}
	for _, stmt := range body.List {
		ds, ok := stmt.(*ast.DeclStmt)
		if !ok {
			continue
		}
		gd, ok := ds.Decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != typeName {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			return extractTagsFromStructType(st)
		}
	}
	return nil
}

func extractStructSpannerTags(file *ast.File, typeName string) []string {
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != typeName {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			return extractTagsFromStructType(st)
		}
	}
	return nil
}

type selectInfo struct {
	cols    []string
	hasStar bool
}

func extractSelectInfo(sql string) (*selectInfo, error) {
	stmt, err := memefish.ParseStatement("", sql)
	if err != nil {
		return nil, err
	}

	qs, ok := stmt.(*memefishast.QueryStatement)
	if !ok {
		return nil, fmt.Errorf("not a query statement")
	}

	sel := unwrapToSelect(qs.Query)
	if sel == nil {
		return nil, fmt.Errorf("could not find SELECT clause")
	}

	info := &selectInfo{}
	for _, item := range sel.Results {
		switch v := item.(type) {
		case *memefishast.Star:
			info.hasStar = true
		case *memefishast.DotStar:
			info.hasStar = true
		case *memefishast.Alias:
			info.cols = append(info.cols, v.As.Alias.Name)
		case *memefishast.ExprSelectItem:
			name := exprColumnName(v.Expr)
			info.cols = append(info.cols, name)
		}
	}

	return info, nil
}

func unwrapToSelect(q memefishast.QueryExpr) *memefishast.Select {
	switch v := q.(type) {
	case *memefishast.Select:
		return v
	case *memefishast.Query:
		return unwrapToSelect(v.Query)
	case *memefishast.SubQuery:
		return unwrapToSelect(v.Query)
	case *memefishast.CompoundQuery:
		if len(v.Queries) > 0 {
			return unwrapToSelect(v.Queries[0])
		}
	}
	return nil
}

func exprColumnName(expr memefishast.Expr) string {
	switch v := expr.(type) {
	case *memefishast.Path:
		if len(v.Idents) > 0 {
			return v.Idents[len(v.Idents)-1].Name
		}
	case *memefishast.Ident:
		return v.Name
	}
	// For function calls, expressions etc., return empty string
	return ""
}

func matchColumns(selInfo *selectInfo, cbInfo *scanInfo, fset *token.FileSet, path string) []Diagnostic {
	var diags []Diagnostic
	pos := fset.Position(cbInfo.callPos)

	switch cbInfo.mode {
	case scanModeColumns:
		if selInfo.hasStar {
			return nil
		}
		if len(selInfo.cols) != cbInfo.argCount {
			diags = append(diags, Diagnostic{
				File:    path,
				Line:    pos.Line,
				Message: fmt.Sprintf("SELECT has %d columns but row.Columns has %d arguments", len(selInfo.cols), cbInfo.argCount),
			})
		}
	case scanModeToStruct:
		if selInfo.hasStar {
			return nil
		}
		sqlLower := make(map[string]string, len(selInfo.cols))
		sqlSet := make(map[string]bool, len(selInfo.cols))
		for _, c := range selInfo.cols {
			if c != "" {
				low := strings.ToLower(c)
				sqlLower[c] = low
				sqlSet[low] = true
			}
		}
		tagLower := make(map[string]string, len(cbInfo.structTags))
		tagSet := make(map[string]bool, len(cbInfo.structTags))
		for _, t := range cbInfo.structTags {
			low := strings.ToLower(t)
			tagLower[t] = low
			tagSet[low] = true
		}
		for _, c := range selInfo.cols {
			if c == "" {
				continue
			}
			if !tagSet[sqlLower[c]] {
				diags = append(diags, Diagnostic{
					File:    path,
					Line:    pos.Line,
					Message: fmt.Sprintf("SELECT column %q has no corresponding struct field", c),
				})
			}
		}
		for _, t := range cbInfo.structTags {
			if !sqlSet[tagLower[t]] {
				diags = append(diags, Diagnostic{
					File:    path,
					Line:    pos.Line,
					Message: fmt.Sprintf("struct field %q has no corresponding SELECT column", t),
				})
			}
		}
	}

	return diags
}
