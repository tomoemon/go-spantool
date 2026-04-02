package main

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func analyzeScanSrc(t *testing.T, src string) []Diagnostic {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "test.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	return AnalyzeScanFile(fset, file, "test.go")
}

func TestScan_ColumnsMatch(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT UserID, Username, Email FROM User`" + `}, func(row *spanner.Row) error {
		var a, b, c interface{}
		return row.Columns(&a, &b, &c)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics, got %v", diags)
	}
}

func TestScan_ColumnsMismatch(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT UserID, Username, Email FROM User`" + `}, func(row *spanner.Row) error {
		var a, b interface{}
		return row.Columns(&a, &b)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, "3 columns") || !strings.Contains(diags[0].Message, "2 arguments") {
		t.Errorf("unexpected message: %s", diags[0].Message)
	}
}

func TestScan_ToStructMatch(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
type myRow struct {
	UserID   int64  ` + "`spanner:\"UserID\"`" + `
	Username string ` + "`spanner:\"Username\"`" + `
}
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT UserID, Username FROM User`" + `}, func(row *spanner.Row) error {
		var v myRow
		return row.ToStruct(&v)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics, got %v", diags)
	}
}

func TestScan_ToStructMissingColumn(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
type myRow struct {
	UserID   int64  ` + "`spanner:\"UserID\"`" + `
	Username string ` + "`spanner:\"Username\"`" + `
}
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT UserID, Username, Email FROM User`" + `}, func(row *spanner.Row) error {
		var v myRow
		return row.ToStruct(&v)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, `"Email"`) && strings.Contains(d.Message, "no corresponding struct field") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected diagnostic about missing struct field for Email, got %v", diags)
	}
}

func TestScan_ToStructExtraField(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
type myRow struct {
	UserID   int64  ` + "`spanner:\"UserID\"`" + `
	Username string ` + "`spanner:\"Username\"`" + `
	Extra    string ` + "`spanner:\"Extra\"`" + `
}
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT UserID, Username FROM User`" + `}, func(row *spanner.Row) error {
		var v myRow
		return row.ToStruct(&v)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, `"Extra"`) && strings.Contains(d.Message, "no corresponding SELECT column") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected diagnostic about extra struct field, got %v", diags)
	}
}

func TestScan_ScanFuncResolution(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func scanUser(row *spanner.Row) error {
	var a, b, c interface{}
	return row.Columns(&a, &b, &c)
}
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT UserID, Username, Email FROM User`" + `}, func(row *spanner.Row) error {
		return scanUser(row)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics, got %v", diags)
	}
}

func TestScan_ScanFuncMismatch(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func scanUser(row *spanner.Row) error {
	var a, b interface{}
	return row.Columns(&a, &b)
}
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT UserID, Username, Email FROM User`" + `}, func(row *spanner.Row) error {
		return scanUser(row)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, "3 columns") || !strings.Contains(diags[0].Message, "2 arguments") {
		t.Errorf("unexpected message: %s", diags[0].Message)
	}
}

func TestScan_NoSpannerImport(t *testing.T) {
	src := `package x
func f() {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics without spanner import, got %v", diags)
	}
}

func TestScan_SpannerAlias(t *testing.T) {
	src := `package x
import sp "cloud.google.com/go/spanner"
func f() {
	helper(ctx, sp.Statement{SQL: ` + "`SELECT UserID, Username FROM User`" + `}, func(row *sp.Row) error {
		var a, b interface{}
		return row.Columns(&a, &b)
	})
}
func helper(ctx interface{}, stmt sp.Statement, fn func(*sp.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics with alias, got %v", diags)
	}
}

func TestScan_SelectStar(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT * FROM User`" + `}, func(row *spanner.Row) error {
		var a, b interface{}
		return row.Columns(&a, &b)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics for SELECT *, got %v", diags)
	}
}

func TestScan_SQLParseError(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`NOT VALID SQL`" + `}, func(row *spanner.Row) error {
		var a interface{}
		return row.Columns(&a)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic for SQL parse error, got %d: %v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, "failed to parse SQL") {
		t.Errorf("unexpected message: %s", diags[0].Message)
	}
}

func TestScan_DoubleQuoteSQL(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: "SELECT UserID, Username FROM User"}, func(row *spanner.Row) error {
		var a, b interface{}
		return row.Columns(&a, &b)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics for double-quoted SQL, got %v", diags)
	}
}

func TestScan_JoinWithAlias(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT u.UserID, u.Username, s.SourceUserID AS SourceID FROM User u JOIN Subscription s ON u.UserID = s.TargetUserID`" + `}, func(row *spanner.Row) error {
		var a, b, c interface{}
		return row.Columns(&a, &b, &c)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics, got %v", diags)
	}
}

func TestScan_ToStructFieldNameFallback(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
type myRow struct {
	UserID   int64
	Username string
}
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT UserID, Username FROM User`" + `}, func(row *spanner.Row) error {
		var v myRow
		return row.ToStruct(&v)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics with field name fallback, got %v", diags)
	}
}

func TestScan_CTE(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`WITH active_users AS (SELECT UserID, Username FROM User WHERE DeletedAt IS NULL) SELECT UserID, Username FROM active_users`" + `}, func(row *spanner.Row) error {
		var a, b interface{}
		return row.Columns(&a, &b)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics for CTE query, got %v", diags)
	}
}

func TestScan_CTEMismatch(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`WITH active_users AS (SELECT UserID, Username FROM User WHERE DeletedAt IS NULL) SELECT UserID, Username FROM active_users`" + `}, func(row *spanner.Row) error {
		var a interface{}
		return row.Columns(&a)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, "2 columns") || !strings.Contains(diags[0].Message, "1 arguments") {
		t.Errorf("unexpected message: %s", diags[0].Message)
	}
}

func TestScan_UnionFirstSelect(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT UserID, Username FROM User UNION ALL SELECT UserID, Username FROM User`" + `}, func(row *spanner.Row) error {
		var a, b interface{}
		return row.Columns(&a, &b)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics for UNION query, got %v", diags)
	}
}

func TestScan_UnionMismatch(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT UserID, Username, Email FROM User UNION ALL SELECT UserID, Username, Email FROM User`" + `}, func(row *spanner.Row) error {
		var a, b interface{}
		return row.Columns(&a, &b)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, "3 columns") || !strings.Contains(diags[0].Message, "2 arguments") {
		t.Errorf("unexpected message: %s", diags[0].Message)
	}
}

func TestScan_SubqueryInFrom(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT sub.UserID, sub.Username FROM (SELECT UserID, Username FROM User WHERE DeletedAt IS NULL) AS sub`" + `}, func(row *spanner.Row) error {
		var a, b interface{}
		return row.Columns(&a, &b)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics for subquery in FROM, got %v", diags)
	}
}

func TestScan_SubqueryInWhere(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT UserID, Username FROM User WHERE UserID IN (SELECT TargetUserID FROM Subscription WHERE SourceUserID = @id)`" + `}, func(row *spanner.Row) error {
		var a, b interface{}
		return row.Columns(&a, &b)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics for subquery in WHERE, got %v", diags)
	}
}

func TestScan_GroupByWithAggregate(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT Username, COUNT(*) AS cnt FROM User GROUP BY Username`" + `}, func(row *spanner.Row) error {
		var a, b interface{}
		return row.Columns(&a, &b)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics for GROUP BY query, got %v", diags)
	}
}

func TestScan_GroupByMismatch(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT Username, COUNT(*) AS cnt, MAX(CreatedAt) AS latest FROM User GROUP BY Username`" + `}, func(row *spanner.Row) error {
		var a, b interface{}
		return row.Columns(&a, &b)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, "3 columns") || !strings.Contains(diags[0].Message, "2 arguments") {
		t.Errorf("unexpected message: %s", diags[0].Message)
	}
}

func TestScan_Having(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT Username, COUNT(*) AS cnt FROM User GROUP BY Username HAVING COUNT(*) > 1`" + `}, func(row *spanner.Row) error {
		var a, b interface{}
		return row.Columns(&a, &b)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics for HAVING query, got %v", diags)
	}
}

func TestScan_LimitOffset(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT UserID, Username FROM User ORDER BY CreatedAt DESC LIMIT 10 OFFSET 5`" + `}, func(row *spanner.Row) error {
		var a, b interface{}
		return row.Columns(&a, &b)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics for LIMIT/OFFSET query, got %v", diags)
	}
}

func TestScan_CaseWhen(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT UserID, CASE WHEN DeletedAt IS NULL THEN 'active' ELSE 'deleted' END AS status FROM User`" + `}, func(row *spanner.Row) error {
		var a, b interface{}
		return row.Columns(&a, &b)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics for CASE WHEN query, got %v", diags)
	}
}

func TestScan_Distinct(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT DISTINCT Username, Email FROM User`" + `}, func(row *spanner.Row) error {
		var a, b interface{}
		return row.Columns(&a, &b)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics for DISTINCT query, got %v", diags)
	}
}

func TestScan_MultipleJoins(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT u.UserID, u.Username, s.SourceUserID, d.Title FROM User u LEFT JOIN Subscription s ON u.UserID = s.TargetUserID INNER JOIN SearchDoc d ON u.UserID = d.DocID`" + `}, func(row *spanner.Row) error {
		var a, b, c, d interface{}
		return row.Columns(&a, &b, &c, &d)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics for multiple JOINs query, got %v", diags)
	}
}

func TestScan_NestedSubquery(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT UserID, Username FROM User WHERE UserID IN (SELECT TargetUserID FROM Subscription WHERE SourceUserID IN (SELECT UserID FROM User WHERE Username = @name))`" + `}, func(row *spanner.Row) error {
		var a, b interface{}
		return row.Columns(&a, &b)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics for nested subquery, got %v", diags)
	}
}

func TestScan_CTEMultiple(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`WITH cte1 AS (SELECT UserID FROM User WHERE DeletedAt IS NULL), cte2 AS (SELECT TargetUserID FROM Subscription) SELECT cte1.UserID, cte2.TargetUserID FROM cte1 JOIN cte2 ON cte1.UserID = cte2.TargetUserID`" + `}, func(row *spanner.Row) error {
		var a, b interface{}
		return row.Columns(&a, &b)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics for multiple CTEs, got %v", diags)
	}
}

func TestScan_UnionIntersectExcept(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT UserID FROM User INTERSECT DISTINCT SELECT TargetUserID AS UserID FROM Subscription`" + `}, func(row *spanner.Row) error {
		var a interface{}
		return row.Columns(&a)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics for INTERSECT query, got %v", diags)
	}
}

func TestScan_ExpressionColumns(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT UserID, CONCAT(Username, '@example.com') AS full_email, TIMESTAMP_DIFF(CURRENT_TIMESTAMP(), CreatedAt, DAY) AS age_days FROM User`" + `}, func(row *spanner.Row) error {
		var a, b, c interface{}
		return row.Columns(&a, &b, &c)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics for expression columns, got %v", diags)
	}
}

func TestScan_SubqueryAsColumn(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT UserID, (SELECT COUNT(*) FROM Subscription s WHERE s.TargetUserID = u.UserID) AS follower_count FROM User u`" + `}, func(row *spanner.Row) error {
		var a, b interface{}
		return row.Columns(&a, &b)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics for subquery as column, got %v", diags)
	}
}

func TestScan_GroupByToStruct(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
type countResult struct {
	Username string ` + "`spanner:\"Username\"`" + `
	Cnt      int64  ` + "`spanner:\"cnt\"`" + `
}
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT Username, COUNT(*) AS cnt FROM User GROUP BY Username`" + `}, func(row *spanner.Row) error {
		var v countResult
		return row.ToStruct(&v)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics for GROUP BY with ToStruct, got %v", diags)
	}
}

func TestScan_CTEWithUnion(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`WITH all_ids AS (SELECT UserID FROM User UNION ALL SELECT DocID AS UserID FROM SearchDoc) SELECT UserID FROM all_ids`" + `}, func(row *spanner.Row) error {
		var a interface{}
		return row.Columns(&a)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics for CTE with UNION, got %v", diags)
	}
}

func TestScan_SelectDotStar(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT u.* FROM User u`" + `}, func(row *spanner.Row) error {
		var a, b interface{}
		return row.Columns(&a, &b)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics for t.* (should skip like *), got %v", diags)
	}
}

func TestScan_ExistsSubquery(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT UserID, Username FROM User u WHERE EXISTS (SELECT 1 FROM Subscription s WHERE s.TargetUserID = u.UserID)`" + `}, func(row *spanner.Row) error {
		var a, b interface{}
		return row.Columns(&a, &b)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics for EXISTS subquery, got %v", diags)
	}
}

func TestScan_Undetectable(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT UserID, Username FROM User`" + `}, func(row *spanner.Row) error {
		return externalPkg.Process(row)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, "could not detect") {
		t.Errorf("unexpected message: %s", diags[0].Message)
	}
	if !strings.Contains(diags[0].Message, "nolint:spantool") {
		t.Errorf("expected nolint hint in message: %s", diags[0].Message)
	}
}

func TestScan_UndetectableNolint(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT UserID, Username FROM User`" + `}, func(row *spanner.Row) error { //nolint:spantool
		return externalPkg.Process(row)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics with nolint, got %v", diags)
	}
}

func TestScan_BlankParamCallback(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT 1 FROM UserBlock WHERE UserID = @userID`" + `}, func(_ *spanner.Row) error {
		found = true
		return nil
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics for blank param callback, got %v", diags)
	}
}

func TestScan_Detectable(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT UserID, Username FROM User`" + `}, func(row *spanner.Row) error {
		var a, b interface{}
		return row.Columns(&a, &b)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics when detectable, got %v", diags)
	}
}

func TestScan_ToStructLocalTypeInCallback(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT MessageID, TalkID FROM Message`" + `}, func(row *spanner.Row) error {
		type scanRow struct {
			MessageID int64 ` + "`spanner:\"MessageID\"`" + `
			TalkID    int64 ` + "`spanner:\"TalkID\"`" + `
		}
		var v scanRow
		return row.ToStruct(&v)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics, got %v", diags)
	}
}

func TestScan_ToStructLocalTypeInCallbackMismatch(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT MessageID, TalkID, CreatedAt FROM Message`" + `}, func(row *spanner.Row) error {
		type scanRow struct {
			MessageID int64 ` + "`spanner:\"MessageID\"`" + `
			TalkID    int64 ` + "`spanner:\"TalkID\"`" + `
		}
		var v scanRow
		return row.ToStruct(&v)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, `"CreatedAt"`) && strings.Contains(d.Message, "no corresponding struct field") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected diagnostic about missing struct field for CreatedAt, got %v", diags)
	}
}

func TestScan_ToStructLocalTypeInEnclosingFunc(t *testing.T) {
	src := `package x
import "cloud.google.com/go/spanner"
func f() {
	type scanRow struct {
		MessageID int64 ` + "`spanner:\"MessageID\"`" + `
		TalkID    int64 ` + "`spanner:\"TalkID\"`" + `
	}
	helper(ctx, spanner.Statement{SQL: ` + "`SELECT MessageID, TalkID FROM Message`" + `}, func(row *spanner.Row) error {
		var v scanRow
		return row.ToStruct(&v)
	})
}
func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
`
	diags := analyzeScanSrc(t, src)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics, got %v", diags)
	}
}
