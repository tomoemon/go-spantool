package main

import (
	"flag"
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

type ScanOption struct {
	NoStar bool
}

func runLintScan(args []string) {
	fs := flag.NewFlagSet("lint-scan", flag.ExitOnError)
	noStar := fs.Bool("no-star", false, "forbid SELECT * and SELECT t.* usage")
	_ = fs.Parse(args)

	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: go-spantool lint-scan [-no-star] <go_files...>")
		os.Exit(1)
	}

	opt := ScanOption{NoStar: *noStar}
	diags, err := AnalyzeScanFiles(fs.Args(), opt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	hasError := false
	for _, d := range diags {
		fmt.Fprintln(os.Stderr, d.String())
		if d.Severity == SeverityError {
			hasError = true
		}
	}
	if hasError {
		os.Exit(1)
	}
}

func AnalyzeScanFiles(paths []string, opt ScanOption) ([]Diagnostic, error) {
	var allDiags []Diagnostic
	fset := token.NewFileSet()
	for _, path := range paths {
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		diags := AnalyzeScanFile(fset, file, path, opt)
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

func AnalyzeScanFile(fset *token.FileSet, file *ast.File, path string, opt ScanOption) []Diagnostic {
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

			stmtInfo, callbackFn, rowName := findStatementAndCallback(call, spannerIdent)
			if stmtInfo == nil {
				return true
			}

			// Check params consistency
			diags = append(diags, matchParams(stmtInfo, file, fset, path)...)

			if callbackFn == nil {
				return true
			}

			selInfo, err := extractSelectInfo(stmtInfo.sql)
			if err != nil {
				pos := fset.Position(stmtInfo.sqlPos)
				diags = append(diags, Diagnostic{
					File:    path,
					Line:    pos.Line,
					Message: fmt.Sprintf("failed to parse SQL: %v", err),
				})
				return true
			}

			if selInfo.hasStar {
				pos := fset.Position(stmtInfo.sqlPos)
				if opt.NoStar {
					diags = append(diags, Diagnostic{
						File:    path,
						Line:    pos.Line,
						Message: "SELECT * is not allowed; explicitly list the columns to select",
					})
				} else {
					diags = append(diags, Diagnostic{
						Severity: SeverityWarning,
						File:     path,
						Line:     pos.Line,
						Message:  "SELECT * skipped column count validation; use -no-star flag to forbid SELECT *",
					})
				}
				return true
			}

			cbInfo := analyzeFuncBody(callbackFn.Body, rowName, spannerIdent, file, funcDecls, 0, enclosingBody)
			if cbInfo == nil {
				if !hasNolintComment(file, fset, callbackFn.Pos()) {
					pos := fset.Position(callbackFn.Pos())
					diags = append(diags, Diagnostic{
						File:    path,
						Line:    pos.Line,
						Message: "could not detect row.Columns or row.ToStruct usage in callback (to suppress, add //nolint:spantool comment to this line)",
					})
				}
				return true
			}
			if cbInfo.mode == scanModeToStructUnresolved {
				if !hasNolintComment(file, fset, cbInfo.callPos) {
					pos := fset.Position(cbInfo.callPos)
					diags = append(diags, Diagnostic{
						File:    path,
						Line:    pos.Line,
						Message: "row.ToStruct variable type could not be resolved; ensure the variable is declared with a struct type in the callback, enclosing function, or package scope (to suppress, add //nolint:spantool comment to this line)",
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

type statementInfo struct {
	sql        string
	sqlPos     token.Pos
	paramsKeys []string  // nil means Params could not be statically resolved
	paramsOK   bool      // true if Params was a resolvable map literal
	hasParams  bool      // true if Params field exists in the Statement literal
	paramsPos  token.Pos // position of the Params field value (for nolint detection)
}

func findStatementAndCallback(call *ast.CallExpr, spannerIdent string) (stmt *statementInfo, callbackFn *ast.FuncLit, rowName string) {
	for _, arg := range call.Args {
		if stmt == nil {
			if info, ok := extractStatementInfo(arg, spannerIdent); ok {
				stmt = info
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

func extractStatementInfo(expr ast.Expr, spannerIdent string) (*statementInfo, bool) {
	comp, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	sel, ok := comp.Type.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Statement" {
		return nil, false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok || ident.Name != spannerIdent {
		return nil, false
	}

	info := &statementInfo{}
	foundSQL := false
	for _, elt := range comp.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case "SQL":
			lit, ok := kv.Value.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			sql, err := unquoteStringLit(lit.Value)
			if err != nil {
				continue
			}
			info.sql = sql
			info.sqlPos = lit.Pos()
			foundSQL = true
		case "Params":
			info.hasParams = true
			info.paramsPos = kv.Value.Pos()
			keys, ok := extractMapLiteralKeys(kv.Value)
			if ok {
				info.paramsKeys = keys
				info.paramsOK = true
			}
		}
	}
	if !foundSQL {
		return nil, false
	}
	return info, true
}

func extractMapLiteralKeys(expr ast.Expr) ([]string, bool) {
	comp, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	var keys []string
	for _, elt := range comp.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		lit, ok := kv.Key.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return nil, false
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			return nil, false
		}
		keys = append(keys, s)
	}
	return keys, true
}

func extractSQLParams(sql string) ([]string, error) {
	stmt, err := memefish.ParseStatement("", sql)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var params []string
	memefishast.Inspect(stmt, func(n memefishast.Node) bool {
		if p, ok := n.(*memefishast.Param); ok {
			if !seen[p.Name] {
				seen[p.Name] = true
				params = append(params, p.Name)
			}
		}
		return true
	})
	return params, nil
}

func matchParams(stmtInfo *statementInfo, file *ast.File, fset *token.FileSet, path string) []Diagnostic {
	if stmtInfo.hasParams && !stmtInfo.paramsOK {
		if !hasNolintComment(file, fset, stmtInfo.paramsPos) {
			pos := fset.Position(stmtInfo.paramsPos)
			return []Diagnostic{{
				File:    path,
				Line:    pos.Line,
				Message: "Params must be a map literal for static analysis (to suppress, add //nolint:spantool comment to this line)",
			}}
		}
		return nil
	}
	if !stmtInfo.paramsOK {
		return nil
	}

	sqlParams, err := extractSQLParams(stmtInfo.sql)
	if err != nil {
		// SQL parse errors are reported elsewhere
		return nil
	}

	pos := fset.Position(stmtInfo.sqlPos)

	var diags []Diagnostic

	paramsSet := make(map[string]bool, len(stmtInfo.paramsKeys))
	for _, k := range stmtInfo.paramsKeys {
		paramsSet[k] = true
	}
	sqlParamsSet := make(map[string]bool, len(sqlParams))
	for _, p := range sqlParams {
		sqlParamsSet[p] = true
	}

	for _, p := range sqlParams {
		if !paramsSet[p] {
			diags = append(diags, Diagnostic{
				File:    path,
				Line:    pos.Line,
				Message: fmt.Sprintf("SQL parameter @%s is used in SQL but not provided in Params map", p),
			})
		}
	}
	for _, k := range stmtInfo.paramsKeys {
		if !sqlParamsSet[k] {
			diags = append(diags, Diagnostic{
				File:    path,
				Line:    pos.Line,
				Message: fmt.Sprintf("Params key %q is not referenced in SQL", k),
			})
		}
	}

	return diags
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
	scanModeColumns            = "columns"
	scanModeToStruct           = "toStruct"
	scanModeToStructUnresolved = "toStructUnresolved"
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
						} else {
							result = &scanInfo{
								mode:    scanModeToStructUnresolved,
								callPos: call.Pos(),
							}
						}
						return false
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

	// Find the variable's type: callback body -> enclosing function body -> file top-level
	typeName, anonStruct := findVarType(body, varIdent.Name)
	if typeName == "" && anonStruct == nil && enclosingBody != nil {
		typeName, anonStruct = findVarType(enclosingBody, varIdent.Name)
	}
	if typeName == "" && anonStruct == nil {
		typeName, anonStruct = findVarTypeInFile(file, varIdent.Name)
	}
	if typeName == "" && anonStruct == nil {
		return nil
	}
	if anonStruct != nil {
		return extractTagsFromStructType(anonStruct)
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

func findVarType(body *ast.BlockStmt, varName string) (string, *ast.StructType) {
	for _, stmt := range body.List {
		switch s := stmt.(type) {
		case *ast.DeclStmt:
			gd, ok := s.Decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			if typeName, st := findVarTypeInSpecs(gd.Specs, varName); typeName != "" || st != nil {
				return typeName, st
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
								return typeIdent.Name, nil
							}
						}
					}
				}
			}
		}
	}
	return "", nil
}

func findVarTypeInFile(file *ast.File, varName string) (string, *ast.StructType) {
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		if typeName, st := findVarTypeInSpecs(gd.Specs, varName); typeName != "" || st != nil {
			return typeName, st
		}
	}
	return "", nil
}

func findVarTypeInSpecs(specs []ast.Spec, varName string) (string, *ast.StructType) {
	for _, spec := range specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for _, name := range vs.Names {
			if name.Name == varName {
				if ident, ok := vs.Type.(*ast.Ident); ok {
					return ident.Name, nil
				}
				if st, ok := vs.Type.(*ast.StructType); ok {
					return "", st
				}
			}
		}
	}
	return "", nil
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
		if len(selInfo.cols) != cbInfo.argCount {
			diags = append(diags, Diagnostic{
				File:    path,
				Line:    pos.Line,
				Message: fmt.Sprintf("SELECT has %d columns but row.Columns has %d arguments", len(selInfo.cols), cbInfo.argCount),
			})
		}
	case scanModeToStruct:
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
