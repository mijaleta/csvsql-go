# csvsql: a SQL query engine over CSV files, in Go

This is a Harbor task in the FrontierSWE format. The agent starts with an empty
Go module and has to build `csvsql`, a command-line program that runs a SQL
`SELECT` over a folder of CSV files and prints the result as CSV, matching
SQLite 3.45 exactly. That means a parser, joins (fast enough for tables of
150,000 rows), grouping and aggregates with SQLite's float summation,
correlated subqueries, set operations, SQLite's value and NULL rules, and the
exact output format. Only the Go standard library is allowed, the container
has no network, and SQLite is removed from the image.

## How it's scored

The verifier rebuilds the agent's module as an unprivileged user with a fresh
build cache, then runs the binary on 292 hidden cases over hidden tables.
Every expected output was produced by SQLite. A case passes only if stdout
matches byte for byte and the exit status matches. The score is the fraction
of cases that pass.

## What's in here

- `instruction.md`: what the agent is told
- `task.toml`: time limits and resources (the agent gets 20 hours)
- `environment/`: the Dockerfile and the starting module, with 41 public example cases
- `tests/`: the hidden verifier, its tables and cases, and `build_cases.py`,
  which regenerates the tables and expected outputs with Python's sqlite3
- `solution/`: the reference implementation

## Running it

```
harbor run -p . -a oracle   # reference solution, scores 1.0
harbor run -p . -a nop      # does nothing, scores 0.0
```
