package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"strconv"
	"strings"
)

func runFmtSQL(args []string) {
	fs := flag.NewFlagSet("fmt-sql", flag.ExitOnError)
	write := fs.Bool("w", false, "write result to (source) file instead of stdout")
	_ = fs.Parse(args)

	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: go-spantool fmt-sql [-w] file.go ...")
		os.Exit(1)
	}

	exitCode := 0
	for _, path := range fs.Args() {
		if err := processFile(path, *write); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}

func processFile(path string, write bool) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	out, err := formatGoFile(src)
	if err != nil {
		return err
	}

	if bytes.Equal(src, out) {
		return nil
	}

	if write {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		return os.WriteFile(path, out, info.Mode())
	}

	fmt.Printf("--- %s\n", path)
	_, err = os.Stdout.Write(out)
	return err
}

func formatGoFile(src []byte) ([]byte, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	// Collect SQL fields from spanner.Statement{SQL: `...`} literals
	spannerIdent := spannerLocalName(file)
	if spannerIdent == "" {
		return src, nil
	}
	fields, fieldErrors := collectSpannerSQLFields(fset, file, spannerIdent)
	if len(fieldErrors) > 0 {
		return nil, fmt.Errorf("spanner.Statement SQL field must be a backtick string literal, "+
			"or string literals joined with + to write a backtick-quoted identifier "+
			"(e.g. `SELECT * FROM ` + \"`Following`\"); SQL built at run time is not supported:\n%s",
			strings.Join(fieldErrors, "\n"))
	}
	if len(fields) == 0 {
		return src, nil
	}

	result := make([]byte, len(src))
	copy(result, src)
	offset := 0
	var syntaxErrors []string

	for _, f := range fields {
		formatted, fmtErr := FormatSQL(strings.TrimSpace(f.sql))
		if fmtErr != nil {
			pos := fset.Position(f.expr.Pos())
			syntaxErrors = append(syntaxErrors, fmt.Sprintf("  line %d: %v", pos.Line, fmtErr))
			continue
		}

		start := fset.Position(f.expr.Pos()).Offset + offset
		end := fset.Position(f.expr.End()).Offset + offset
		newLit := sqlFieldExpr(formatted)
		if newLit == string(result[start:end]) {
			continue
		}

		newResult := make([]byte, len(result[:start])+len(newLit)+len(result[end:]))
		copy(newResult, result[:start])
		copy(newResult[start:], newLit)
		copy(newResult[start+len(newLit):], result[end:])
		offset += len(newLit) - (end - start)
		result = newResult
	}

	if len(syntaxErrors) > 0 {
		return nil, fmt.Errorf("SQL syntax errors:\n%s", strings.Join(syntaxErrors, "\n"))
	}

	return format.Source(result)
}

// sqlField is the SQL field of a spanner.Statement literal.
type sqlField struct {
	expr ast.Expr // a backtick string literal, or string literals joined with +
	sql  string   // the SQL that expr evaluates to
}

// collectSpannerSQLFields collects SQL fields from spanner.Statement{SQL: ...}
// in the AST. It returns error messages for SQL fields that are neither a
// backtick string literal nor string literals joined with +.
func collectSpannerSQLFields(fset *token.FileSet, file *ast.File, spannerIdent string) ([]sqlField, []string) {
	var fields []sqlField
	var errs []string
	ast.Inspect(file, func(n ast.Node) bool {
		comp, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}

		sel, ok := comp.Type.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Statement" {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != spannerIdent {
			return true
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
			sql, err := evalSQLField(kv.Value)
			if err != nil {
				pos := fset.Position(kv.Value.Pos())
				errs = append(errs, fmt.Sprintf("  line %d: %v", pos.Line, err))
				continue
			}
			fields = append(fields, sqlField{expr: kv.Value, sql: sql})
		}

		return true
	})
	return fields, errs
}

// evalSQLField returns the SQL that expr evaluates to. expr must be a
// backtick string literal or string literals joined with +.
func evalSQLField(expr ast.Expr) (string, error) {
	if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING && lit.Value[0] != '`' {
		return "", fmt.Errorf("found double-quoted string literal %s; use a backtick string literal", lit.Value)
	}
	return concatStringLits(expr)
}

// concatStringLits returns the value of string literals joined with +.
func concatStringLits(expr ast.Expr) (string, error) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			return unquoteStringLit(e.Value)
		}
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			x, err := concatStringLits(e.X)
			if err != nil {
				return "", err
			}
			y, err := concatStringLits(e.Y)
			if err != nil {
				return "", err
			}
			return x + y, nil
		}
	}
	return "", fmt.Errorf("found %s", describeExpr(expr))
}

func describeExpr(expr ast.Expr) string {
	switch expr.(type) {
	case *ast.Ident:
		return "variable " + types.ExprString(expr)
	case *ast.CallExpr:
		return "function call " + types.ExprString(expr)
	}
	return "expression " + types.ExprString(expr)
}

// sqlFieldExpr returns the Go expression for an SQL field holding sql: a
// backtick string literal starting and ending with a newline. A raw string
// cannot contain a backtick, so backtick-quoted parts such as `Following` are
// written as double-quoted string literals joined with +.
func sqlFieldExpr(sql string) string {
	text := "\n" + sql + "\n"
	var parts []string
	for {
		i := strings.IndexByte(text, '`')
		if i < 0 {
			return strings.Join(append(parts, "`"+text+"`"), " + ")
		}
		if i > 0 {
			parts = append(parts, "`"+text[:i]+"`")
		}
		// A backtick-quoted identifier ends on the same line; a lone
		// backtick (e.g. in a comment) is written by itself
		quoted := "`"
		if j := strings.IndexAny(text[i+1:], "`\n"); j >= 0 && text[i+1+j] == '`' {
			quoted = text[i : i+j+2]
		}
		parts = append(parts, strconv.Quote(quoted))
		text = text[i+len(quoted):]
	}
}
