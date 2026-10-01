# fmt-sql

Formats SQL inside `spanner.Statement{SQL: ...}` literals in Go source files.

## Usage

```
go tool go-spantool fmt-sql [-l] [-d] [-w] [file.go ...]
```

| Flag | Description |
|---|---|
| (none) | Print each file whose formatting changes, after a `--- <path>` header |
| `-l` | Print only the paths of files whose formatting changes, one per line |
| `-d` | Print a unified diff of the changes |
| `-w` | Write the result back to the files |

Flags can be combined, as in gofmt (e.g. `-l -w` lists the files it rewrites).

With no files, or with `-`, fmt-sql reads Go source from standard input and writes the formatted source to standard output; source without SQL fields is written as is. `-w` cannot be used with standard input, and `-l` / `-d` name it `<standard input>`.

As in gofmt, the exit code is 0 even when files are not formatted; it is non-zero only on errors such as a Go or SQL syntax error.

### Checking formatting in CI

`-l` prints nothing when every file is formatted:

```make
sqlfmt-check:
	@files="$$(go tool go-spantool fmt-sql -l ./path/to/*.go)" || exit 1; \
	test -z "$$files" || { echo "$$files" >&2; exit 1; }
```

Use `-d` to show in the CI log what is not formatted.

### Checking staged content in a pre-commit hook

To check the index rather than the working tree, pass the staged blob through standard input:

```bash
formatted="$(git cat-file blob "$staged_blob" | go tool go-spantool fmt-sql | git hash-object --stdin)"
test "$formatted" = "$staged_blob" || echo "$go_file: SQL is not formatted" >&2
```

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

SQL must be a string literal, or string literals joined with `+`. When rewriting, fmt-sql writes the SQL as a backtick string literal:

```go
spanner.Statement{SQL: `SELECT 1`}              // accepted
spanner.Statement{SQL: "SELECT 1"}              // accepted, rewritten as a backtick string literal
spanner.Statement{SQL: "SELECT 1 " + "FROM t"}  // accepted, rewritten as one backtick string literal
```

A raw string cannot contain a backtick, so backtick-quoted identifiers (e.g. a table named like a reserved keyword) are written as double-quoted literals joined with `+`:

```go
spanner.Statement{SQL: `
SELECT
  FromUserID
FROM
  ` + "`Following`" + `
WHERE
  FromUserID = @fromUserID
`}  // accepted
```

Go comments between the joined literals are reported as an error, since rewriting the field would drop them; put them outside the field, or into the SQL as `--` comments.

Expressions that are not string literals (variables, function calls) are not supported, so that SQL is never built at run time:

```go
spanner.Statement{SQL: buildSQL()}                  // rejected: found function call buildSQL()
spanner.Statement{SQL: "SELECT * FROM " + table}    // rejected: found variable table
```

Graph queries (`GRAPH ... MATCH ...`) are kept as written.

SQL syntax errors are reported:

```go
spanner.Statement{SQL: `SELEC 1 FORM User`}  // rejected: SQL syntax error
```
