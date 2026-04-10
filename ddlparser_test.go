package main

import (
	"os"
	"testing"
)

func TestParseDDL(t *testing.T) {
	src, err := os.ReadFile("testdata/test.ddl")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := ParseDDL(string(src))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("tables parsed", func(t *testing.T) {
		if len(schema.Tables) != 4 {
			t.Fatalf("got %d tables, want 4", len(schema.Tables))
		}
		for _, name := range []string{"User", "SearchDoc", "WithDefault", "OrderItem"} {
			if _, ok := schema.Tables[name]; !ok {
				t.Errorf("missing table %q", name)
			}
		}
	})

	t.Run("User columns", func(t *testing.T) {
		user := schema.Tables["User"]
		wantCols := []struct {
			name    string
			notNull bool
		}{
			{"UserID", true},
			{"Username", true},
			{"Email", true},
			{"DeletedAt", false},
			{"CreatedAt", true},
			{"UpdatedAt", true},
		}
		if len(user.Columns) != len(wantCols) {
			t.Fatalf("got %d columns, want %d", len(user.Columns), len(wantCols))
		}
		for i, wc := range wantCols {
			c := user.Columns[i]
			if c.Name != wc.name {
				t.Errorf("column %d: got name %q, want %q", i, c.Name, wc.name)
			}
			if c.NotNull != wc.notNull {
				t.Errorf("column %q: got NotNull=%v, want %v", c.Name, c.NotNull, wc.notNull)
			}
		}
	})

	t.Run("User primary keys", func(t *testing.T) {
		user := schema.Tables["User"]
		if len(user.PrimaryKeys) != 1 || user.PrimaryKeys[0] != "UserID" {
			t.Errorf("got primary keys %v, want [UserID]", user.PrimaryKeys)
		}
	})

	t.Run("User required columns", func(t *testing.T) {
		user := schema.Tables["User"]
		req := user.RequiredColumns()
		want := map[string]bool{
			"UserID": true, "Username": true, "Email": true,
			"CreatedAt": true, "UpdatedAt": true,
		}
		if len(req) != len(want) {
			t.Fatalf("got %d required columns, want %d: %v", len(req), len(want), req)
		}
		for _, c := range req {
			if !want[c] {
				t.Errorf("unexpected required column %q", c)
			}
		}
	})

	t.Run("generated column excluded from required", func(t *testing.T) {
		doc := schema.Tables["SearchDoc"]
		col := doc.ColumnByName("Title_Tokens")
		if col == nil {
			t.Fatal("Title_Tokens column not found")
		}
		if !col.Generated {
			t.Error("Title_Tokens should be Generated")
		}
		req := doc.RequiredColumns()
		for _, c := range req {
			if c == "Title_Tokens" {
				t.Error("Title_Tokens should not be in required columns")
			}
		}
	})

	t.Run("default column excluded from required", func(t *testing.T) {
		wd := schema.Tables["WithDefault"]
		col := wd.ColumnByName("Status")
		if col == nil {
			t.Fatal("Status column not found")
		}
		if !col.HasDefault {
			t.Error("Status should have HasDefault=true")
		}
		req := wd.RequiredColumns()
		for _, c := range req {
			if c == "Status" {
				t.Error("Status should not be in required columns")
			}
		}
	})
}
