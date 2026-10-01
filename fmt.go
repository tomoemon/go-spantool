package main

import (
	"bytes"
	"cmp"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
)

func runFmtSQL(args []string) {
	os.Exit(fmtSQLMain(args, os.Stdin, os.Stdout, os.Stderr))
}

// fmtSQLOptions are the output modes of fmt-sql. They can be combined, as in gofmt.
type fmtSQLOptions struct {
	write bool // write the result back to the file
	list  bool // print the paths of files whose formatting differs
	diff  bool // print a unified diff of the changes
}

// stdinName is how standard input is named in the -l and -d output, as in gofmt.
const stdinName = "<standard input>"

// fmtSQLMain runs fmt-sql and returns the exit code. With no files (or "-"),
// it reads Go source from stdin and writes the result to stdout.
func fmtSQLMain(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fmt-sql", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var opt fmtSQLOptions
	fs.BoolVar(&opt.write, "w", false, "write result to (source) file instead of stdout")
	fs.BoolVar(&opt.list, "l", false, "list files whose formatting differs from fmt-sql's")
	fs.BoolVar(&opt.diff, "d", false, "display diffs instead of rewriting files")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "usage: go-spantool fmt-sql [-l] [-d] [-w] [file.go ...]")
		_, _ = fmt.Fprintln(stderr, "With no files, or with -, reads Go source from standard input.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0 // -h, as in gofmt
		}
		return 2
	}

	paths := fs.Args()
	if len(paths) == 0 {
		paths = []string{"-"}
	}
	exitCode := 0
	for _, path := range paths {
		name, in := path, io.Reader(nil)
		if path == "-" {
			name, in = stdinName, stdin
		}
		if err := processFile(name, in, stdout, opt); err != nil {
			_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
			exitCode = 1
		}
	}
	return exitCode
}

// processFile formats the Go source read from in, or from the file name when
// in is nil, and prints or writes the result as opt says.
func processFile(name string, in io.Reader, stdout io.Writer, opt fmtSQLOptions) error {
	if opt.write && in != nil {
		return fmt.Errorf("-w cannot be used with standard input, as there is no file to write back to; " +
			"omit -w to print the formatted source to standard output")
	}
	var src []byte
	var err error
	if in == nil {
		src, err = os.ReadFile(name)
	} else {
		src, err = io.ReadAll(in)
	}
	if err != nil {
		return err
	}
	if in != nil && len(src) == 0 {
		return errors.New("expected Go source on standard input, but it was empty; " +
			"fmt-sql reads standard input when no files are given, so pass the .go files to format")
	}

	out, err := formatGoFile(src)
	if err != nil {
		return err
	}
	changed := !bytes.Equal(src, out)

	if opt.list && changed {
		if _, err := fmt.Fprintln(stdout, name); err != nil {
			return err
		}
	}
	if opt.diff && changed {
		before, after := string(src), string(out)
		edits := myers.ComputeEdits("", before, after)
		if _, err := fmt.Fprint(stdout, gotextdiff.ToUnified(name+".orig", name, before, edits)); err != nil {
			return err
		}
	}
	if opt.write && changed {
		info, err := os.Stat(name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(name, out, info.Mode()); err != nil {
			return err
		}
	}
	if opt.list || opt.diff || opt.write {
		return nil
	}

	// Standard input is always printed, as in gofmt; a file only when it
	// changed, after a header
	if in == nil {
		if !changed {
			return nil
		}
		if _, err := fmt.Fprintf(stdout, "--- %s\n", name); err != nil {
			return err
		}
	}
	_, err = stdout.Write(out)
	return err
}

func formatGoFile(src []byte) ([]byte, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	// Collect SQL fields from spanner.Statement{SQL: ...} literals
	spannerIdent := spannerLocalName(file)
	if spannerIdent == "" {
		return src, nil
	}
	fields, fieldErrors := collectSpannerSQLFields(fset, file, spannerIdent)
	if len(fieldErrors) > 0 {
		return nil, fmt.Errorf("cannot format spanner.Statement SQL fields:\n%s", strings.Join(fieldErrors, "\n"))
	}
	if len(fields) == 0 {
		return src, nil
	}

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
		newExpr := sqlFieldExpr(formatted)
		if err := verifySQLFieldExpr(newExpr, formatted); err != nil {
			return nil, fmt.Errorf("line %d: %w", start.Line, err)
		}
		end := fset.Position(f.expr.End()).Offset
		result.Write(src[prev:start.Offset])
		result.WriteString(newExpr)
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
	expr ast.Expr // a string literal, or string literals joined with +
	sql  string   // the SQL that expr evaluates to
}

// collectSpannerSQLFields collects SQL fields from spanner.Statement{SQL: ...}
// in the AST, in source order. It returns error messages, also in source
// order, for SQL fields that are neither a string literal nor string literals
// joined with +, or that contain a Go comment.
func collectSpannerSQLFields(fset *token.FileSet, file *ast.File, spannerIdent string) ([]sqlField, []string) {
	var values []ast.Expr
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
			if ok && key.Name == "SQL" {
				values = append(values, kv.Value)
			}
		}

		return true
	})
	// ast.Inspect visits a statement nested in an earlier field (e.g. Params)
	// after the enclosing statement's SQL field
	slices.SortFunc(values, func(a, b ast.Expr) int { return cmp.Compare(a.Pos(), b.Pos()) })

	var fields []sqlField
	var errs []string
	for _, v := range values {
		sql, err := concatStringLits(v)
		if err != nil {
			errs = append(errs, fmt.Sprintf("  line %d: %v; the SQL field must be a string literal or string literals joined with +, "+
				"since SQL built at run time is not supported", fset.Position(v.Pos()).Line, err))
			continue
		}
		if c := commentInExpr(file, v); c != nil {
			errs = append(errs, fmt.Sprintf("  line %d: found Go comment %s between the joined string literals; "+
				"rewriting the field would drop it, so move it outside the field or into the SQL as a -- comment",
				fset.Position(c.Pos()).Line, c.Text))
			continue
		}
		fields = append(fields, sqlField{expr: v, sql: sql})
	}
	return fields, errs
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
		if text[i] == '`' {
			if j := strings.IndexAny(text[i+1:], "`\n"); j >= 0 && text[i+1+j] == '`' {
				quoted = text[i : i+j+2]
			}
		}
		parts = append(parts, strconv.Quote(quoted))
		text = text[i+len(quoted):]
	}
}

// indexNotRaw returns the index and size of the first character in s that a
// raw string literal cannot hold as is, or -1: a backtick, a carriage return
// (dropped from raw strings), NUL, a byte order mark, or invalid UTF-8.
func indexNotRaw(s string) (int, int) {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == '`' || r == '\r' || r == 0 || r == '\uFEFF' || (r == utf8.RuneError && size == 1) {
			return i, size
		}
		i += size
	}
	return -1, 0
}

// verifySQLFieldExpr checks that the Go expression written for an SQL field
// evaluates to the formatted SQL, so that a rewrite never changes the query.
func verifySQLFieldExpr(expr, formatted string) error {
	want := "\n" + formatted + "\n"
	e, err := parser.ParseExpr(expr)
	if err != nil {
		return fmt.Errorf("the rewritten SQL field is not a valid Go expression (%v); %s\nexpression: %s", err, reportBug, expr)
	}
	got, err := concatStringLits(e)
	if err != nil || got != want {
		return fmt.Errorf("the rewritten SQL field does not evaluate to the formatted SQL; %s\nexpected: %q\nactual:   %q", reportBug, want, got)
	}
	return nil
}
