package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatGoFile_rejectNonLiteralSQL(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name: "variable",
			src: `package x
import "cloud.google.com/go/spanner"
var _ = spanner.Statement{SQL: sql}
`,
			wantErr: "line 3: found variable sql",
		},
		{
			name: "function call",
			src: `package x
import "cloud.google.com/go/spanner"
var _ = spanner.Statement{SQL: buildSQL()}
`,
			wantErr: "line 3: found function call buildSQL()",
		},
		{
			name: "concatenation with a variable is rejected",
			src: `package x
import "cloud.google.com/go/spanner"
var _ = spanner.Statement{SQL: "SELECT * FROM " + table}
`,
			wantErr: "line 3: found variable table",
		},
		{
			name: "concatenation with a function call is rejected",
			src: `package x
import "cloud.google.com/go/spanner"
var _ = spanner.Statement{SQL: ` + "`SELECT * FROM `" + ` + fmt.Sprintf("%s", t)}
`,
			wantErr: `line 3: found function call fmt.Sprintf("%s", t)`,
		},
		{
			name: "comment inside joined string literals is rejected, not dropped",
			src: `package x
import "cloud.google.com/go/spanner"
var _ = spanner.Statement{SQL: ` + "`SELECT * FROM `" + ` + // reserved word
	"` + "`Following`" + `"}
`,
			wantErr: "line 3: found Go comment // reserved word",
		},
		{
			name: "all problems are reported together",
			src: `package x
import "cloud.google.com/go/spanner"
var _ = spanner.Statement{SQL: sql}
var _ = spanner.Statement{SQL: ` + "`SELECT * FROM `" + ` + // reserved word
	"` + "`Following`" + `"}
`,
			wantErr: "line 3: found variable sql; the SQL field must be a string literal or string literals joined with +, " +
				"since SQL built at run time is not supported\n  line 4: found Go comment // reserved word",
		},
		{
			name: "problems are reported in source order, also in a statement nested in an earlier field",
			src: `package x
import "cloud.google.com/go/spanner"
var _ = spanner.Statement{Params: f(
	spanner.Statement{SQL: inner}),
	SQL: outer}
`,
			wantErr: "line 4: found variable inner; the SQL field must be a string literal or string literals joined with +, " +
				"since SQL built at run time is not supported\n  line 5: found variable outer",
		},
		{
			name: "backtick literal is accepted",
			src: `package x
import "cloud.google.com/go/spanner"
var _ = spanner.Statement{SQL: ` + "`SELECT 1`" + `}
`,
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := formatGoFile([]byte(tt.src))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error but got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestFormatGoFile_concatenatedSQL(t *testing.T) {
	// ‵ stands for a backtick, which a raw string cannot contain
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "double-quoted string literal is rewritten as a backtick string literal",
			src: `package x

import "cloud.google.com/go/spanner"

var stmt = spanner.Statement{SQL: "SELECT a FROM t"}
`,
			want: `package x

import "cloud.google.com/go/spanner"

var stmt = spanner.Statement{SQL: ‵
SELECT
  a
FROM
  t
‵}
`,
		},
		{
			name: "double-quoted string literal with a backtick-quoted identifier",
			src: `package x

import "cloud.google.com/go/spanner"

var stmt = spanner.Statement{SQL: "SELECT FromUserID FROM ‵Following‵ WHERE FromUserID = @fromUserID"}
`,
			want: `package x

import "cloud.google.com/go/spanner"

var stmt = spanner.Statement{SQL: ‵
SELECT
  FromUserID
FROM
  ‵ + "‵Following‵" + ‵
WHERE
  FromUserID = @fromUserID
‵}
`,
		},
		{
			name: "backtick-quoted identifier is kept as a double-quoted literal",
			src: `package x

import "cloud.google.com/go/spanner"

var stmt = spanner.Statement{
	SQL: ‵select FromUserID from ‵ + "‵Following‵" + ‵ where FromUserID = @fromUserID‵,
}
`,
			want: `package x

import "cloud.google.com/go/spanner"

var stmt = spanner.Statement{
	SQL: ‵
SELECT
  FromUserID
FROM
  ‵ + "‵Following‵" + ‵
WHERE
  FromUserID = @fromUserID
‵,
}
`,
		},
		{
			name: "string literals without backtick-quoted identifiers become one literal",
			src: `package x

import "cloud.google.com/go/spanner"

var stmt = spanner.Statement{SQL: "SELECT a " + "FROM t"}
`,
			want: `package x

import "cloud.google.com/go/spanner"

var stmt = spanner.Statement{SQL: ‵
SELECT
  a
FROM
  t
‵}
`,
		},
		{
			name: "several backtick-quoted identifiers",
			src: `package x

import "cloud.google.com/go/spanner"

var stmt = spanner.Statement{SQL: "SELECT f." + "‵Order‵" + " FROM " + "‵Following‵" + " f"}
`,
			want: `package x

import "cloud.google.com/go/spanner"

var stmt = spanner.Statement{SQL: ‵
SELECT
  f.‵ + "‵Order‵" + ‵
FROM
  ‵ + "‵Following‵" + ‵ f
‵}
`,
		},
		{
			name: "characters a raw string cannot hold are kept as double-quoted literals",
			src: `package x

import "cloud.google.com/go/spanner"

var stmt = spanner.Statement{SQL: "SELECT 'a\rb', 'c\x00d' FROM " + "‵Following‵"}
`,
			want: `package x

import "cloud.google.com/go/spanner"

var stmt = spanner.Statement{SQL: ‵
SELECT
  'a‵ + "\r" + ‵b',
  'c‵ + "\x00" + ‵d'
FROM
  ‵ + "‵Following‵" + ‵
‵}
`,
		},
		{
			name: "statement nested in a field before SQL",
			src: `package x

import "cloud.google.com/go/spanner"

var stmt = spanner.Statement{Params: f(spanner.Statement{SQL: ‵select 2‵}), SQL: ‵select 1 from ‵ + "‵Order‵"}
`,
			want: `package x

import "cloud.google.com/go/spanner"

var stmt = spanner.Statement{Params: f(spanner.Statement{SQL: ‵
SELECT
  2
‵}), SQL: ‵
SELECT
  1
FROM
  ‵ + "‵Order‵" + ‵
‵}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := strings.ReplaceAll(tt.src, "‵", "`")
			want := strings.ReplaceAll(tt.want, "‵", "`")
			got, err := formatGoFile([]byte(src))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != want {
				t.Fatalf("mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
			}
			again, err := formatGoFile(got)
			if err != nil {
				t.Fatalf("unexpected error on formatted source: %v", err)
			}
			if string(again) != string(got) {
				t.Errorf("not idempotent:\n%s", again)
			}
		})
	}
}

func TestFmtSQLMain(t *testing.T) {
	const unformatted = "package q\n\nimport \"cloud.google.com/go/spanner\"\n\nvar stmt = spanner.Statement{SQL: `select a from t`}\n"
	const formatted = "package q\n\nimport \"cloud.google.com/go/spanner\"\n\nvar stmt = spanner.Statement{SQL: `\nSELECT\n  a\nFROM\n  t\n`}\n"
	const noSQL = "package q\n\nfunc f() {}\n"

	// setup writes bad.go (unformatted) and good.go (formatted) into a temp
	// directory and returns their paths.
	setup := func(t *testing.T) (bad, good string) {
		t.Helper()
		dir := t.TempDir()
		bad, good = filepath.Join(dir, "bad.go"), filepath.Join(dir, "good.go")
		if err := os.WriteFile(bad, []byte(unformatted), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(good, []byte(formatted), 0o644); err != nil {
			t.Fatal(err)
		}
		return bad, good
	}
	run := func(stdin string, args ...string) (code int, stdout, stderr string) {
		var out, errOut strings.Builder
		code = fmtSQLMain(args, strings.NewReader(stdin), &out, &errOut)
		return code, out.String(), errOut.String()
	}
	readFile := func(t *testing.T, path string) string {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	t.Run("-l lists only unformatted files and exits 0", func(t *testing.T) {
		bad, good := setup(t)
		code, stdout, stderr := run("", "-l", bad, good)
		if code != 0 || stdout != bad+"\n" || stderr != "" {
			t.Errorf("got code=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
		if readFile(t, bad) != unformatted {
			t.Error("-l must not rewrite the file")
		}
	})

	t.Run("-l -w lists and rewrites", func(t *testing.T) {
		bad, good := setup(t)
		code, stdout, _ := run("", "-l", "-w", bad, good)
		if code != 0 || stdout != bad+"\n" {
			t.Errorf("got code=%d stdout=%q", code, stdout)
		}
		if readFile(t, bad) != formatted {
			t.Error("-w did not rewrite the file")
		}
	})

	t.Run("-d prints a unified diff", func(t *testing.T) {
		bad, good := setup(t)
		code, stdout, _ := run("", "-d", bad, good)
		for _, want := range []string{"--- " + bad + ".orig\n", "+++ " + bad + "\n", "-var stmt = spanner.Statement{SQL: `select a from t`}\n", "+SELECT\n"} {
			if !strings.Contains(stdout, want) {
				t.Errorf("diff does not contain %q:\n%s", want, stdout)
			}
		}
		if code != 0 || strings.Contains(stdout, good) {
			t.Errorf("got code=%d, diff mentions the formatted file:\n%s", code, stdout)
		}
	})

	t.Run("without flags prints changed files with a header", func(t *testing.T) {
		bad, good := setup(t)
		code, stdout, _ := run("", bad, good)
		if code != 0 || stdout != "--- "+bad+"\n"+formatted {
			t.Errorf("got code=%d stdout=%q", code, stdout)
		}
	})

	t.Run("no files reads standard input", func(t *testing.T) {
		code, stdout, stderr := run(unformatted)
		if code != 0 || stdout != formatted || stderr != "" {
			t.Errorf("got code=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	})

	t.Run("- reads standard input and prints source without SQL as is", func(t *testing.T) {
		code, stdout, _ := run(noSQL, "-")
		if code != 0 || stdout != noSQL {
			t.Errorf("got code=%d stdout=%q", code, stdout)
		}
	})

	t.Run("-l with standard input", func(t *testing.T) {
		code, stdout, _ := run(unformatted, "-l")
		if code != 0 || stdout != "<standard input>\n" {
			t.Errorf("got code=%d stdout=%q", code, stdout)
		}
		code, stdout, _ = run(formatted, "-l")
		if code != 0 || stdout != "" {
			t.Errorf("formatted input: got code=%d stdout=%q", code, stdout)
		}
	})

	t.Run("-w with standard input is an error", func(t *testing.T) {
		for _, in := range []string{unformatted, formatted} {
			code, stdout, stderr := run(in, "-w")
			if code != 1 || stdout != "" || !strings.Contains(stderr, "<standard input>: -w cannot be used with standard input") {
				t.Errorf("got code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
		}
	})

	t.Run("parse error exits 1", func(t *testing.T) {
		code, _, stderr := run("package q; func(", "-l")
		if code != 1 || !strings.HasPrefix(stderr, "<standard input>: ") {
			t.Errorf("got code=%d stderr=%q", code, stderr)
		}
	})
}
