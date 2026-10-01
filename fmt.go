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
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
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
	var commentErrors []string
	for _, f := range fields {
		if c := commentInExpr(file, f.expr); c != nil {
			commentErrors = append(commentErrors, fmt.Sprintf("  line %d: found %s", fset.Position(c.Pos()).Line, c.Text))
		}
	}
	if len(commentErrors) > 0 {
		return nil, fmt.Errorf("comments inside a spanner.Statement SQL field would be dropped when rewriting it; "+
			"move them outside the SQL field, or into the SQL as -- comments:\n%s", strings.Join(commentErrors, "\n"))
	}

	// Sort fields into source order so the result is built in one pass;
	// ast.Inspect visits a statement nested in an earlier field (e.g. Params)
	// after the enclosing statement's SQL field
	slices.SortFunc(fields, func(a, b sqlField) int { return int(a.expr.Pos() - b.expr.Pos()) })
	var result bytes.Buffer
	prev := 0
	var syntaxErrors []string

	for _, f := range fields {
		start := fset.Position(f.expr.Pos())
		formatted, fmtErr := FormatSQL(strings.TrimSpace(f.sql))
		if fmtErr != nil {
			syntaxErrors = append(syntaxErrors, fmt.Sprintf("  line %d: %v", start.Line, fmtErr))
			continue
		}
		end := fset.Position(f.expr.End()).Offset
		result.Write(src[prev:start.Offset])
		result.WriteString(sqlFieldExpr(formatted))
		prev = end
	}
	result.Write(src[prev:])

	if len(syntaxErrors) > 0 {
		return nil, fmt.Errorf("SQL syntax errors:\n%s", strings.Join(syntaxErrors, "\n"))
	}

	return format.Source(result.Bytes())
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
		comp, ok := spannerStatementLit(n, spannerIdent)
		if !ok {
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
		if strings.Contains(lit.Value, "`") {
			return "", fmt.Errorf("found double-quoted string literal %s; a backtick string literal cannot contain a backtick, "+
				"so join string literals with + (e.g. `SELECT * FROM ` + \"`Following`\")", lit.Value)
		}
		return "", fmt.Errorf("found double-quoted string literal %s; use a backtick string literal", lit.Value)
	}
	return concatStringLits(expr)
}

// commentInExpr returns the first comment inside expr, or nil. Rewriting
// expr would drop it.
func commentInExpr(file *ast.File, expr ast.Expr) *ast.Comment {
	for _, cg := range file.Comments {
		if cg.Pos() > expr.Pos() && cg.End() < expr.End() {
			return cg.List[0]
		}
	}
	return nil
}

// sqlFieldExpr returns the Go expression for an SQL field holding sql: a
// backtick string literal starting and ending with a newline. A raw string
// cannot contain a backtick, so backtick-quoted parts such as `Following` are
// written as double-quoted string literals joined with +, as are other
// characters a raw string cannot hold as is (see indexNotRaw).
func sqlFieldExpr(sql string) string {
	text := "\n" + sql + "\n"
	var parts []string
	for {
		i, size := indexNotRaw(text)
		if i < 0 {
			return strings.Join(append(parts, "`"+text+"`"), " + ")
		}
		if i > 0 {
			parts = append(parts, "`"+text[:i]+"`")
		}
		// A backtick-quoted identifier ends on the same line; a lone
		// backtick (e.g. in a comment) is written by itself
		quoted := text[i : i+size]
		if j := strings.IndexAny(text[i+1:], "`\n"); text[i] == '`' && j >= 0 && text[i+1+j] == '`' {
			quoted = text[i : i+j+2]
		}
		parts = append(parts, strconv.Quote(quoted))
		text = text[i+len(quoted):]
	}
}

// indexNotRaw returns the index and size of the first character in s that a
// raw string literal cannot hold as is, or -1: a backtick, a carriage return
// (dropped from raw strings), NUL, a byte order mark, or invalid UTF-8.
func indexNotRaw(s string) (int, int) {
	for i, r := range s {
		switch r {
		case '`', '\r', 0, '\uFEFF':
			return i, utf8.RuneLen(r)
		case utf8.RuneError:
			if _, size := utf8.DecodeRuneInString(s[i:]); size == 1 {
				return i, 1
			}
		}
	}
	return -1, 0
}
