# go-spantool

A static analysis and formatting tool for Google Cloud Spanner Go code.

## Motivation

Many Go SQL libraries rely on code generation (sqlc, yo, ent, etc.). When AI agents write code, a generate step in the middle of the workflow is costly — the agent must decide when to run it, wait for the output, and reconcile the result.

go-spantool takes a different approach: work directly with the standard Go Spanner SDK (`cloud.google.com/go/spanner`) and catch mistakes through static analysis. No generate step, no extra tooling — just write code and lint.

### Why this matters for AI-assisted coding

- No generate timing decisions — AI agents simply write code and run the linter
- No generated API to learn — AI models already know the standard Spanner SDK
- No context window bloat from generated code
- Boilerplate is not a problem — AI agents write repetitive code effortlessly

### Comparison with code generation tools

Advantages of go-spantool:

- Works with the standard Spanner SDK as-is — no wrapper, no generated layer
- AI agent workflow is just "write code -> lint -> fix" with no intermediate steps
- Full flexibility for complex queries and Spanner-specific features (Mutations, DML, etc.)
- Zero runtime dependencies — lint-time only

Limitations of go-spantool:

- Checks column names and counts only — no type compatibility checks between Spanner and Go types
- Only analyzes literals — dynamically built SQL or variable references are out of scope
- lint-mutation requires a DDL file to be maintained manually (automatic DDL fetching from databases is planned)

Advantages of code generation tools (sqlc, yo, etc.):

- Full type safety at the Go type level — mismatches become compile errors
- Schema changes are caught automatically by re-running the generator

Limitations of code generation tools:

- AI agents must decide when to run generate, inspect the output, and adjust — adding round-trips
- Generated APIs may not cover complex queries, requiring fallback to the raw SDK
- CI/CD pipelines need a generate step

## Overview

- [lint-mutation](docs/lint-mutation.md) - Validates mutation map literals against DDL schema
  - Undefined column names
  - Missing NOT NULL columns in InsertMap / InsertOrUpdateMap
  - Writes to generated columns
  - Missing primary key columns in UpdateMap
- [lint-scan](docs/lint-scan.md) - Detects mismatches between SELECT statements and row scanning code
  - Column count mismatch between SELECT and row.Columns arguments
  - Column name mismatch between SELECT and struct spanner tags in row.ToStruct
  - Missing or unused SQL parameters vs Params map keys
- [fmt-sql](docs/fmt-sql.md) - Auto-formats SQL in spanner.Statement literals
  - Keyword uppercasing and clause-level line breaks
  - SQL syntax validation via [memefish](https://github.com/cloudspannerecosystem/memefish)

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

# Forbid SELECT * usage
go tool go-spantool lint-scan -no-star ./path/to/*.go
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
