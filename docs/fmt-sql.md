# fmt-sql

Formats SQL inside `spanner.Statement{SQL: ...}` literals in Go source files.

## Formatting rules

- Newline before clause keywords (SELECT, FROM, WHERE, HAVING, LIMIT, etc.)
- Each item in SELECT list on its own line
- Keywords normalized to uppercase
- AND/OR placed at the beginning of lines within WHERE/HAVING
- JOIN modifiers grouped on one line
- CASE/WHEN/END indentation
- Recursive subquery formatting
- SQL syntax validation via [memefish](https://github.com/cloudspannerecosystem/memefish)

## Examples

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

## Limitations

SQL must be a backtick string literal:

```go
spanner.Statement{SQL: `SELECT 1`}  // accepted
```

Double-quoted strings and variables are not supported:

```go
spanner.Statement{SQL: "SELECT 1"}   // rejected: must be a backtick string literal
spanner.Statement{SQL: buildSQL()}   // rejected: must be a backtick string literal
```

SQL syntax errors are reported:

```go
spanner.Statement{SQL: `SELEC 1 FORM User`}  // rejected: SQL syntax error
```
