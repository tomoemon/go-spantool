# go-spantool

A static analysis and formatting tool for Google Cloud Spanner Go code.

## Overview

- [lint-mutation](#lint-mutation) - Validate mutation map literals against DDL schema
- [lint-scan](#lint-scan) - Detect mismatches between SELECT columns and `row.Columns` / `row.ToStruct`
- [fmt-sql](#fmt-sql) - Auto-format SQL in `spanner.Statement{SQL: ...}` literals

## Features

### lint-mutation

Validates Spanner mutation map literals (`spanner.InsertMap`, `spanner.UpdateMap`, `spanner.InsertOrUpdateMap`) against your DDL schema.

Detection rules:

- Table name must be a string literal
- Second argument must be a `map[string]any{...}` literal
- Map keys must be string literals
- Column names must exist in the DDL
- Generated columns cannot be written to
- `InsertMap` / `InsertOrUpdateMap`: all NOT NULL columns without DEFAULT must be present
- `UpdateMap`: primary key columns must be included

Given the following DDL:

```sql
CREATE TABLE User (
  UserID INT64 NOT NULL,
  Username STRING(255) NOT NULL,
  Email STRING(255) NOT NULL,
  DeletedAt TIMESTAMP,
  CreatedAt TIMESTAMP NOT NULL,
  UpdatedAt TIMESTAMP NOT NULL,
) PRIMARY KEY(UserID);
```

Valid - all required NOT NULL columns are present:

```go
spanner.InsertMap("User", map[string]any{
    "UserID":    int64(1),
    "Username":  "alice",
    "Email":     "alice@example.com",
    "CreatedAt": spanner.CommitTimestamp,
    "UpdatedAt": spanner.CommitTimestamp,
})
```

Error - missing required column `Email`:

```go
spanner.InsertMap("User", map[string]any{
    "UserID":    int64(1),
    "Username":  "alice",
    // "Email" is NOT NULL and has no DEFAULT, so it must be present
    "CreatedAt": spanner.CommitTimestamp,
    "UpdatedAt": spanner.CommitTimestamp,
})
// => InsertMap("User"): missing required NOT NULL column "Email"
```

Error - unknown column name:

```go
spanner.InsertMap("User", map[string]any{
    "UserID":   int64(1),
    "FullName": "alice",  // not defined in DDL
    ...
})
// => table "User" has no column "FullName"
```

Error - table name is not a string literal:

```go
tbl := "User"
spanner.InsertMap(tbl, map[string]any{...})
// => spanner.InsertMap: table name must be a string literal
```

Error - map is passed as a variable:

```go
m := map[string]any{"UserID": int64(1)}
spanner.InsertMap("User", m)
// => spanner.InsertMap("User", ...): second argument must be a map[string]any literal
```

### lint-scan

Validates that SELECT columns in `spanner.Statement{SQL: ...}` match the scan usage in the associated `func(*spanner.Row) error` callback.

Detection targets any function call whose arguments contain both a `spanner.Statement{SQL: ...}` literal and a `func(*spanner.Row) error` callback:

```go
anyHelper(ctx, spanner.Statement{SQL: `SELECT A, B, C FROM ...`}, func(row *spanner.Row) error {
    // row.Columns, row.ToStruct, or a scan helper function
})
```

Detection rules:

- `row.Columns(&a, &b, &c)`: the number of arguments must match the number of SELECT columns
- `row.ToStruct(&v)`: the struct's `spanner:"..."` tags (or field names) must match the SELECT column names
- Scan helper functions (e.g. `scanUser(row)`) are resolved within the same file and analyzed recursively
- `SELECT *` and `t.*` are skipped (column count is indeterminate without DDL)
- Both backtick and double-quoted SQL strings are supported
- Spanner package alias imports are supported
- `-strict` mode: reports an error when `row.Columns` / `row.ToStruct` usage cannot be detected in the callback (e.g. row is passed to an external package function)

Valid - column count matches:

```go
helper(ctx, spanner.Statement{SQL: `SELECT UserID, Username, Email FROM User`}, func(row *spanner.Row) error {
    var a, b, c interface{}
    return row.Columns(&a, &b, &c)
})
```

Error - column count mismatch:

```go
helper(ctx, spanner.Statement{SQL: `SELECT UserID, Username, Email FROM User`}, func(row *spanner.Row) error {
    var a, b interface{}
    return row.Columns(&a, &b)
})
// => SELECT has 3 columns but row.Columns has 2 arguments
```

Valid - ToStruct with matching tags:

```go
type userRow struct {
    UserID   int64  `spanner:"UserID"`
    Username string `spanner:"Username"`
}

helper(ctx, spanner.Statement{SQL: `SELECT UserID, Username FROM User`}, func(row *spanner.Row) error {
    var v userRow
    return row.ToStruct(&v)
})
```

Error - struct has a field not in SELECT:

```go
type userRow struct {
    UserID   int64  `spanner:"UserID"`
    Username string `spanner:"Username"`
    Extra    string `spanner:"Extra"`
}

helper(ctx, spanner.Statement{SQL: `SELECT UserID, Username FROM User`}, func(row *spanner.Row) error {
    var v userRow
    return row.ToStruct(&v)
})
// => struct field "Extra" has no corresponding SELECT column
```

Valid - scan helper function resolution:

```go
func scanUser(row *spanner.Row) error {
    var a, b, c interface{}
    return row.Columns(&a, &b, &c)
}

helper(ctx, spanner.Statement{SQL: `SELECT UserID, Username, Email FROM User`}, func(row *spanner.Row) error {
    return scanUser(row)
})
```

### fmt-sql

Formats SQL inside `spanner.Statement{SQL: ...}` literals in Go source files.

Formatting rules:

- Newline before clause keywords (SELECT, FROM, WHERE, HAVING, LIMIT, etc.)
- Each item in SELECT list on its own line
- Keywords normalized to uppercase
- AND/OR placed at the beginning of lines within WHERE/HAVING
- JOIN modifiers grouped on one line
- CASE/WHEN/END indentation
- Recursive subquery formatting
- SQL syntax validation via [memefish](https://github.com/cloudspannerecosystem/memefish)

Before formatting:

```go
var stmt = spanner.Statement{SQL: `select u.UserID, u.Username from User u left join Subscription s on u.UserID = s.TargetUserID where u.UserID = @userID and s.SourceUserID = @sourceUserID order by u.CreatedAt desc limit @limit offset @offset`}
```

After `go tool go-spantool fmt-sql -w`:

```go
var stmt = spanner.Statement{SQL: `
SELECT
  u.UserID,
  u.Username
FROM
  User u
LEFT JOIN
  Subscription s ON u.UserID = s.TargetUserID
WHERE
  u.UserID = @userID
  AND s.SourceUserID = @sourceUserID
ORDER BY
  u.CreatedAt DESC
LIMIT
  @limit
OFFSET
  @offset
`}
```

Valid - SQL must be a backtick string literal:

```go
spanner.Statement{SQL: `SELECT 1`}  // accepted
```

Error - double-quoted strings and variables are not supported:

```go
spanner.Statement{SQL: "SELECT 1"}   // rejected: must be a backtick string literal
spanner.Statement{SQL: buildSQL()}   // rejected: must be a backtick string literal
```

Error - SQL syntax error:

```go
spanner.Statement{SQL: `SELEC 1 FORM User`}  // rejected: SQL syntax error
```

## Installation

```bash
go get -tool github.com/tomoemon/go-spantool@latest
```

## Usage

### lint-mutation

```bash
go tool go-spantool lint-mutation -ddl schema.sql ./path/to/*.go
```

### lint-scan

```bash
go tool go-spantool lint-scan ./path/to/*.go

# Strict mode: error when scan usage cannot be detected
go tool go-spantool lint-scan -strict ./path/to/*.go
```

### fmt-sql

```bash
# Print formatted output to stdout
go tool go-spantool fmt-sql ./path/to/*.go

# Write changes back to files
go tool go-spantool fmt-sql -w ./path/to/*.go
```

## Testing

```bash
go test ./...
```

## License

MIT
