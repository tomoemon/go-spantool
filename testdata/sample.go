package sample

import "cloud.google.com/go/spanner"

type userRow struct {
	UserID   int64  `spanner:"UserID"`
	Username string `spanner:"Username"`
	Extra    string `spanner:"Extra"`
}

func example() {
	// columns mismatch: SELECT has 3 columns but row.Columns has 2 arguments
	helper(ctx, spanner.Statement{SQL: `SELECT UserID, Username, Email FROM User`}, func(row *spanner.Row) error {
		var a, b interface{}
		return row.Columns(&a, &b)
	})

	// toStruct mismatch: struct field "Extra" has no corresponding SELECT column
	helper(ctx, spanner.Statement{SQL: `SELECT UserID, Username FROM User`}, func(row *spanner.Row) error {
		var v userRow
		return row.ToStruct(&v)
	})

	// SELECT * warning
	helper(ctx, spanner.Statement{SQL: `SELECT * FROM User`}, func(row *spanner.Row) error {
		var a interface{}
		return row.Columns(&a)
	})
}

func helper(ctx interface{}, stmt spanner.Statement, fn func(*spanner.Row) error) {}
