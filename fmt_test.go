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
			wantErr: "SQL field must be a backtick string literal",
		},
		{
			name: "function call",
			src: `package x
import "cloud.google.com/go/spanner"
var _ = spanner.Statement{SQL: buildSQL()}
`,
			wantErr: "SQL field must be a backtick string literal",
		},
		{
			name: "double-quoted string is rejected",
			src: `package x
import "cloud.google.com/go/spanner"
var _ = spanner.Statement{SQL: "SELECT 1"}
`,
			wantErr: "SQL field must be a backtick string literal",
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
