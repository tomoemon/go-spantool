package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
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
	sqlLits, litErrors := collectSpannerSQLLits(fset, file, spannerIdent)
	if len(litErrors) > 0 {
		return nil, fmt.Errorf("spanner.Statement SQL field must be a backtick string literal:\n%s", strings.Join(litErrors, "\n"))
	}
	if len(sqlLits) == 0 {
		return src, nil
	}

	result := make([]byte, len(src))
	copy(result, src)
	offset := 0
	var syntaxErrors []string

	for _, lit := range sqlLits {
		raw := lit.Value
		if len(raw) < 2 || raw[0] != '`' || raw[len(raw)-1] != '`' {
			continue
		}

		inner := raw[1 : len(raw)-1]
		formatted, fmtErr := FormatSQL(strings.TrimSpace(inner))
		if fmtErr != nil {
			pos := fset.Position(lit.Pos())
			syntaxErrors = append(syntaxErrors, fmt.Sprintf("  line %d: %v", pos.Line, fmtErr))
			continue
		}

		newLit := "`\n" + formatted + "\n`"
		if newLit == raw {
			continue
		}

		start := fset.Position(lit.Pos()).Offset + offset
		end := fset.Position(lit.End()).Offset + offset
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

// collectSpannerSQLLits collects SQL values from spanner.Statement{SQL: `...`}
// in the AST. It returns error messages if any SQL field is not a backtick
// string literal.
func collectSpannerSQLLits(fset *token.FileSet, file *ast.File, spannerIdent string) ([]*ast.BasicLit, []string) {
	var lits []*ast.BasicLit
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
			lit, ok := kv.Value.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || len(lit.Value) < 2 || lit.Value[0] != '`' {
				pos := fset.Position(kv.Value.Pos())
				errs = append(errs, fmt.Sprintf("  line %d: SQL field must be a backtick string literal", pos.Line))
				continue
			}
			lits = append(lits, lit)
		}

		return true
	})
	return lits, errs
}
