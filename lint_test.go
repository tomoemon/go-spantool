package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func testSchema(t *testing.T) *Schema {
	t.Helper()
	src, err := os.ReadFile("testdata/test.ddl")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := ParseDDL(string(src))
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func analyzeSrc(t *testing.T, schema *Schema, src string) []Diagnostic {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "test.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	return AnalyzeFile(fset, file, schema, "test.go")
}

func TestAnalyzer_ValidInsert(t *testing.T) {
	schema := testSchema(t)
	src := `package x
import "cloud.google.com/go/spanner"
func f() []*spanner.Mutation {
	return []*spanner.Mutation{
		spanner.InsertMap("User", map[string]any{
			"UserID":    int64(1),
			"Username":  "test",
			"Email":     "test@example.com",
			"DeletedAt": nil,
			"CreatedAt": nil,
			"UpdatedAt": nil,
		}),
	}
}
`
	diags := analyzeSrc(t, schema, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics, got %v", diags)
	}
}

func TestAnalyzer_UnknownColumn(t *testing.T) {
	schema := testSchema(t)
	src := `package x
import "cloud.google.com/go/spanner"
func f() []*spanner.Mutation {
	return []*spanner.Mutation{
		spanner.InsertMap("User", map[string]any{
			"UserID":    int64(1),
			"Username":  "test",
			"Email":     "test@example.com",
			"BadColumn": "oops",
			"CreatedAt": nil,
			"UpdatedAt": nil,
		}),
	}
}
`
	diags := analyzeSrc(t, schema, src)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, `no column "BadColumn"`) {
		t.Errorf("unexpected message: %s", diags[0].Message)
	}
}

func TestAnalyzer_MissingRequired(t *testing.T) {
	schema := testSchema(t)
	src := `package x
import "cloud.google.com/go/spanner"
func f() []*spanner.Mutation {
	return []*spanner.Mutation{
		spanner.InsertMap("User", map[string]any{
			"UserID":   int64(1),
			"Username": "test",
		}),
	}
}
`
	diags := analyzeSrc(t, schema, src)
	missing := map[string]bool{}
	for _, d := range diags {
		if strings.Contains(d.Message, "missing required") {
			for _, col := range []string{"Email", "CreatedAt", "UpdatedAt"} {
				if strings.Contains(d.Message, col) {
					missing[col] = true
				}
			}
		}
	}
	for _, col := range []string{"Email", "CreatedAt", "UpdatedAt"} {
		if !missing[col] {
			t.Errorf("expected missing required column %q to be reported", col)
		}
	}
}

func TestAnalyzer_VariableMapRejected(t *testing.T) {
	schema := testSchema(t)
	src := `package x
import "cloud.google.com/go/spanner"
func f() []*spanner.Mutation {
	mp := map[string]any{"UserID": int64(1)}
	return []*spanner.Mutation{
		spanner.InsertMap("User", mp),
	}
}
`
	diags := analyzeSrc(t, schema, src)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, "must be a map[string]any literal") {
		t.Errorf("unexpected message: %s", diags[0].Message)
	}
}

func TestAnalyzer_UnknownTable(t *testing.T) {
	schema := testSchema(t)
	src := `package x
import "cloud.google.com/go/spanner"
func f() []*spanner.Mutation {
	return []*spanner.Mutation{
		spanner.InsertMap("Nonexistent", map[string]any{
			"ID": int64(1),
		}),
	}
}
`
	diags := analyzeSrc(t, schema, src)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, `unknown table "Nonexistent"`) {
		t.Errorf("unexpected message: %s", diags[0].Message)
	}
}

func TestAnalyzer_UpdateMissingPK(t *testing.T) {
	schema := testSchema(t)
	src := `package x
import "cloud.google.com/go/spanner"
func f() []*spanner.Mutation {
	return []*spanner.Mutation{
		spanner.UpdateMap("User", map[string]any{
			"Username": "newname",
		}),
	}
}
`
	diags := analyzeSrc(t, schema, src)
	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, `missing primary key column "UserID"`) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected missing primary key diagnostic, got %v", diags)
	}
}

func TestAnalyzer_UpdatePartialOK(t *testing.T) {
	schema := testSchema(t)
	src := `package x
import "cloud.google.com/go/spanner"
func f() []*spanner.Mutation {
	return []*spanner.Mutation{
		spanner.UpdateMap("User", map[string]any{
			"UserID":   int64(1),
			"Username": "newname",
		}),
	}
}
`
	diags := analyzeSrc(t, schema, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics for partial update, got %v", diags)
	}
}

func TestAnalyzer_GeneratedColumnRejected(t *testing.T) {
	schema := testSchema(t)
	src := `package x
import "cloud.google.com/go/spanner"
func f() []*spanner.Mutation {
	return []*spanner.Mutation{
		spanner.InsertMap("SearchDoc", map[string]any{
			"DocID":        int64(1),
			"Title":        "test",
			"Title_Tokens": "bad",
			"UpdatedAt":    nil,
		}),
	}
}
`
	diags := analyzeSrc(t, schema, src)
	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, "generated column") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected generated column diagnostic, got %v", diags)
	}
}

func TestAnalyzer_DefaultColumnOptional(t *testing.T) {
	schema := testSchema(t)
	src := `package x
import "cloud.google.com/go/spanner"
func f() []*spanner.Mutation {
	return []*spanner.Mutation{
		spanner.InsertMap("WithDefault", map[string]any{
			"ID":        int64(1),
			"Name":      "test",
			"CreatedAt": nil,
		}),
	}
}
`
	diags := analyzeSrc(t, schema, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics when DEFAULT column omitted, got %v", diags)
	}
}

func TestAnalyzer_TableNameVariable(t *testing.T) {
	schema := testSchema(t)
	src := `package x
import "cloud.google.com/go/spanner"
func f() []*spanner.Mutation {
	tbl := "User"
	return []*spanner.Mutation{
		spanner.InsertMap(tbl, map[string]any{
			"UserID": int64(1),
		}),
	}
}
`
	diags := analyzeSrc(t, schema, src)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, "table name must be a string literal") {
		t.Errorf("unexpected message: %s", diags[0].Message)
	}
}

func TestAnalyzer_UpdateCompositePK_AllPresent(t *testing.T) {
	schema := testSchema(t)
	src := `package x
import "cloud.google.com/go/spanner"
func f() []*spanner.Mutation {
	return []*spanner.Mutation{
		spanner.UpdateMap("OrderItem", map[string]any{
			"OrderID":  int64(1),
			"ItemID":   int64(2),
			"Quantity": int64(3),
		}),
	}
}
`
	diags := analyzeSrc(t, schema, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics when all composite PK columns present, got %v", diags)
	}
}

func TestAnalyzer_UpdateCompositePK_PartialMissing(t *testing.T) {
	schema := testSchema(t)
	src := `package x
import "cloud.google.com/go/spanner"
func f() []*spanner.Mutation {
	return []*spanner.Mutation{
		spanner.UpdateMap("OrderItem", map[string]any{
			"OrderID":  int64(1),
			"Quantity": int64(3),
		}),
	}
}
`
	diags := analyzeSrc(t, schema, src)
	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, `missing primary key column "ItemID"`) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected missing primary key diagnostic for ItemID, got %v", diags)
	}
}

func TestAnalyzer_UpdateCompositePK_AllMissing(t *testing.T) {
	schema := testSchema(t)
	src := `package x
import "cloud.google.com/go/spanner"
func f() []*spanner.Mutation {
	return []*spanner.Mutation{
		spanner.UpdateMap("OrderItem", map[string]any{
			"Quantity": int64(3),
		}),
	}
}
`
	diags := analyzeSrc(t, schema, src)
	missingPKs := map[string]bool{}
	for _, d := range diags {
		for _, pk := range []string{"OrderID", "ItemID"} {
			if strings.Contains(d.Message, fmt.Sprintf("missing primary key column %q", pk)) {
				missingPKs[pk] = true
			}
		}
	}
	for _, pk := range []string{"OrderID", "ItemID"} {
		if !missingPKs[pk] {
			t.Errorf("expected missing primary key diagnostic for %q, got %v", pk, diags)
		}
	}
}

func TestAnalyzer_InsertCompositePK_MissingOne(t *testing.T) {
	schema := testSchema(t)
	src := `package x
import "cloud.google.com/go/spanner"
func f() []*spanner.Mutation {
	return []*spanner.Mutation{
		spanner.InsertMap("OrderItem", map[string]any{
			"OrderID":  int64(1),
			"Quantity": int64(3),
			"Price":    1.5,
		}),
	}
}
`
	diags := analyzeSrc(t, schema, src)
	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, `missing required NOT NULL column "ItemID"`) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected missing required column diagnostic for ItemID, got %v", diags)
	}
}

func TestAnalyzer_SpannerAlias(t *testing.T) {
	schema := testSchema(t)
	src := `package x
import sp "cloud.google.com/go/spanner"
func f() []*sp.Mutation {
	return []*sp.Mutation{
		sp.InsertMap("User", map[string]any{
			"UserID":    int64(1),
			"Username":  "test",
			"Email":     "test@example.com",
			"CreatedAt": nil,
			"UpdatedAt": nil,
		}),
	}
}
`
	diags := analyzeSrc(t, schema, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics with alias, got %v", diags)
	}
}

func TestAnalyzer_NoSpannerImport(t *testing.T) {
	schema := testSchema(t)
	src := `package x
func f() {}
`
	diags := analyzeSrc(t, schema, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics without spanner import, got %v", diags)
	}
}
