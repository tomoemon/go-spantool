# fmt-sql

Formats SQL inside `spanner.Statement{SQL: ...}` literals in Go source files.

## Formatting rules

- Newline before clause keywords (SELECT, FROM, WHERE, HAVING, LIMIT, etc.)
- Each item in SELECT list on its own line
- Keywords normalized to uppercase
- AND/OR placed at the beginning of lines within WHERE/HAVING and JOIN's ON
- JOIN modifiers grouped on one line
- CASE/WHEN/END indentation
- Recursive subquery formatting (including EXISTS / IN subqueries and CTEs); the body is indented one level deeper than the line that opens it, and the closing parenthesis aligns with that line
- Comments are kept: a comment after a token stays at the end of that line, and a comment on its own line stays on its own line
- SQL syntax validation via [memefish](https://github.com/cloudspannerecosystem/memefish)
- The formatted SQL is checked to have the same meaning and the same comments as the original; if not, `fmt-sql` reports an error instead of rewriting the file

## How it works

fmt-sql parses the SQL with memefish and walks the AST to record the syntactic role of each token (`layout.go`): which tokens start a clause, which AND / OR separate conditions, which commas separate list items, and which parentheses open a subquery or a group of conditions. The printer (`formatter.go`) then writes the original tokens and decides line breaks from these roles, not from the surrounding keywords. So a keyword used in several roles, such as the AND of `BETWEEN x AND y` or the OFFSET of `WITH OFFSET`, needs no special case.

To format a new construct, record its role in `layoutBuilder.clause` (or `layoutBuilder.walk` for subqueries). For example, conditions are expanded under the nodes that call `b.cond` (WHERE, ON and HAVING); adding another node there formats its conditions the same way. Constructs without a role are written on one line.

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
  Subscription s
ON
  u.UserID = s.TargetUserID
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

Graph queries (`GRAPH ... MATCH ...`) are kept as written.

SQL syntax errors are reported:

```go
spanner.Statement{SQL: `SELEC 1 FORM User`}  // rejected: SQL syntax error
```
