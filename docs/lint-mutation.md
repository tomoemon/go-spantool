# lint-mutation

Validates Spanner mutation map literals (`spanner.InsertMap`, `spanner.UpdateMap`, `spanner.InsertOrUpdateMap`) against your DDL schema.

## Detection rules

- Table name must be a string literal
- Second argument must be a `map[string]any{...}` literal
- Map keys must be string literals
- Column names must exist in the DDL
- Generated columns cannot be written to
- `InsertMap` / `InsertOrUpdateMap`: all NOT NULL columns without DEFAULT must be present
- `UpdateMap`: primary key columns must be included

## Examples

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
