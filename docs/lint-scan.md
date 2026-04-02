# lint-scan

Validates that SELECT columns in `spanner.Statement{SQL: ...}` match the scan usage in the associated `func(*spanner.Row) error` callback. Also validates that SQL parameters (`@param`) match the keys in the `Params` map.

## Detection target

Any function call whose arguments contain both a `spanner.Statement{SQL: ...}` literal and a `func(*spanner.Row) error` callback:

```go
anyHelper(ctx, spanner.Statement{SQL: `SELECT A, B, C FROM ...`}, func(row *spanner.Row) error {
    // row.Columns, row.ToStruct, or a scan helper function
})
```

## Detection rules

- `row.Columns(&a, &b, &c)`: the number of arguments must match the number of SELECT columns
- `row.ToStruct(&v)`: the struct's `spanner:"..."` tags (or field names) must match the SELECT column names
- Column name and count only: type compatibility between Spanner types (e.g. INT64) and Go types (e.g. int64) is not checked
  - Variable and type resolution scope: callback body -> enclosing function body -> same file top-level declarations (other files in the same package are not searched)
- Scan helper functions (e.g. `scanUser(row)`) are resolved within the same file and analyzed recursively
- `SELECT *` and `t.*` are skipped with a warning (column count is indeterminate without DDL). Use `-no-star` flag to forbid `SELECT *` usage entirely
- Both backtick and double-quoted SQL strings are supported
- Spanner package alias imports are supported
- Callbacks with `_` parameter (e.g. `func(_ *spanner.Row) error`) are skipped
- Reports an error when `row.Columns` / `row.ToStruct` usage cannot be detected in the callback (e.g. row is passed to an unresolvable function). Add `//nolint:spantool` comment to suppress
- `Params` map keys must match SQL parameters (`@param`): reports an error for missing or unused keys
  - Only map literals are analyzed; variable references report an error. Add `//nolint:spantool` comment to suppress
  - Params checking works independently of callback detection

## Examples

### row.Columns

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

### row.ToStruct

Valid - ToStruct with matching tags (named struct or anonymous struct):

```go
type userRow struct {
    UserID   int64  `spanner:"UserID"`
    Username string `spanner:"Username"`
}

helper(ctx, spanner.Statement{SQL: `SELECT UserID, Username FROM User`}, func(row *spanner.Row) error {
    var v userRow
    return row.ToStruct(&v)
})

// Anonymous struct is also supported
helper(ctx, spanner.Statement{SQL: `SELECT UserID, Username FROM User`}, func(row *spanner.Row) error {
    var v struct {
        UserID   int64  `spanner:"UserID"`
        Username string `spanner:"Username"`
    }
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

### Scan helper function resolution

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

### SQL parameters

Valid - SQL parameters match Params map keys:

```go
helper(ctx, spanner.Statement{
    SQL:    `SELECT UserID, Username FROM User WHERE UserID = @id AND Active = @active`,
    Params: map[string]interface{}{"id": 1, "active": true},
}, func(row *spanner.Row) error {
    var a, b interface{}
    return row.Columns(&a, &b)
})
```

Error - SQL parameter not provided in Params map:

```go
helper(ctx, spanner.Statement{
    SQL:    `SELECT UserID FROM User WHERE UserID = @id AND Active = @active`,
    Params: map[string]interface{}{"id": 1},
}, func(row *spanner.Row) error {
    var a interface{}
    return row.Columns(&a)
})
// => SQL parameter @active is used in SQL but not provided in Params map
```

Error - Params key not referenced in SQL:

```go
helper(ctx, spanner.Statement{
    SQL:    `SELECT UserID FROM User WHERE UserID = @id`,
    Params: map[string]interface{}{"id": 1, "extra": 2},
}, func(row *spanner.Row) error {
    var a interface{}
    return row.Columns(&a)
})
// => Params key "extra" is not referenced in SQL
```
