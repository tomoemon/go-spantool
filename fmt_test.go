package main

import (
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
			name: "double-quoted string is rejected",
			src: `package x
import "cloud.google.com/go/spanner"
var _ = spanner.Statement{SQL: "SELECT 1"}
`,
			wantErr: `line 3: found double-quoted string literal "SELECT 1"`,
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
