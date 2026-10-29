# csvsql: a SQL query engine over CSV files, in Go

Implement `csvsql`, a command-line program that runs one SQL `SELECT`
statement over a directory of CSV files and prints the result as CSV. For the
subset of SQL described below it must give exactly the results SQLite 3.45
gives on the same tables.

The Go module in `/app` (module `csvsql`, `package main`) currently only
prints "not implemented". Replace it with a full implementation.

## Constraints

- Go, standard library only. The environment has no network access and no
  modules are vendored, so `go.mod` must not require anything.
- SQLite is not installed, and your program must not run other programs.
- Your code is graded by copying `/app` (without `.git/`) to a fresh
  directory and running `go build -o csvsql .` there with the installed Go
  1.24 toolchain, offline, with `CGO_ENABLED=0` and an empty build cache.

## How you are scored

A hidden suite of a few hundred cases is run against the binary. Each case is
a list of command-line arguments, the expected stdout and the expected exit
status. The program runs with its working directory set to a folder of CSV
tables. A case passes only when **stdout matches byte for byte and the exit
status matches**. stderr is never compared. Each case has a 10 second limit.
A case that expects an error also comes with a related valid "control"
command line, and passes only if the control is answered correctly too.
Your score is the fraction of cases that pass.

The hidden cases cover everything in this document: CSV input, output
formatting, expressions and their types, the built-in functions, filtering,
grouping and aggregates, joins, subqueries, set operations, error handling
and the command line. Some tables have 50,000 and 150,000 rows, and queries
join, group and filter them within the time limit. Every expected output was
produced by SQLite 3.45.

`/app/examples/cases.jsonl` holds 41 public cases in the same format, with
their tables in `/app/examples/data/`. `python3 /app/run_examples.py [-v]`
builds your program and runs them. The hidden cases use different tables and
are much more thorough, so test well beyond the examples.

## Command line

```
csvsql [-d DIR] QUERY
```

- `QUERY` is one SQL statement. It is the only argument that does not start
  with `-`.
- `-d DIR` reads the tables from `DIR` instead of the current directory. It
  may come before or after the query.
- Exit status 0 on success. On any error in the query (syntax error, unknown
  table, column or function, and the other errors listed below), print a
  message to stderr, **print nothing to stdout** and exit with status 1.
- Usage errors exit with status 2 and print nothing to stdout: no query, more
  than one query, an unknown option, `-d` without a value, or a `DIR` that is
  not an existing directory.

## Tables

Every file `NAME.csv` directly inside the directory is a table called `NAME`
(subdirectories are not searched). Table names, like all identifiers, are
matched case-insensitively, so `Regions.csv` can be queried as `regions`.

Files are UTF-8 CSV in the RFC 4180 style. Records are separated by `\n` or
`\r\n`, and the file ends with a line break. A field that starts with `"` is
quoted: it runs to the next `"` that is not doubled, may contain commas and
line breaks, and `""` inside it stands for one `"`. Unquoted fields are taken
exactly as written, spaces included.

The first record is the header. Each header field is `name:TYPE`, where TYPE
is `INTEGER`, `REAL` or `TEXT` (any letter case). The name is everything
before the last `:`. Every following record has one field per column:

| Column type | Field | Value |
|---|---|---|
| `INTEGER` | empty (quoted or not) | `NULL` |
| `INTEGER` | otherwise | the 64-bit integer written there |
| `REAL` | empty (quoted or not) | `NULL` |
| `REAL` | otherwise | the double written there (`3` is read as `3.0`) |
| `TEXT` | empty and unquoted | `NULL` |
| `TEXT` | `""` | the empty string |
| `TEXT` | otherwise | the text |

The input files are always valid.

## Output

The first line is the header: the result column names. Then one line per
result row. Fields are separated by commas and every line ends with `\n`.

A field is written in double quotes, with each `"` doubled, if it is the empty
string or contains a comma, `"`, `\r` or `\n`. Otherwise it is written as is.
This applies to the header too.

| Value | Written as |
|---|---|
| `NULL` | nothing (an empty, unquoted field) |
| integer | decimal digits, with `-` if negative |
| text | its characters, quoted when the rule above says so |
| real | see below |

A real is written with the shortest decimal digits that read back as the
same double. Let `d1 d2 … dn` be those digits and `e` the exponent such that
the value is `d1.d2…dn × 10^e`.

- If `-4 <= e < 16`, write it positionally, padding with zeros as needed,
  and always with a fractional part: `3.0`, `100.0`, `0.0001`, `2.5`,
  `1000000000000000.0`, `0.30000000000000004`.
- Otherwise use exponent form: `d1`, then `.d2…dn` if n > 1, then `e`, the
  sign, and at least two exponent digits: `1e+16`, `1e-05`, `1.5e-07`,
  `6.666666666666667e+19`, `1e+300`.
- Zero is `0.0`, and negative zero is `-0.0`.

(This is the same as Python's `repr()` of a float.)

### Result column names

- A column with `AS name` is called `name`.
- A column that is just a column reference, possibly qualified or in
  parentheses (`id`, `e.id`, `(id)`), takes the column's name as declared in
  its table or subquery, in the declared letter case. So `SELECT NAME FROM t`
  is headed `Name` if the CSV header says `Name:TEXT`.
- `*` and `t.*` give the names of the columns they expand to.
- Any other expression is named by its exact source text, from its first
  character to its last, with the original spacing and letter case:
  `SELECT   salary  /  1000 ,count(*)` gives the names `salary  /  1000` and
  `count(*)`.
- In a compound select, the names come from the first `SELECT`.

## Values and types

Every value is one of `NULL`, a 64-bit signed integer, a double (real) or a
text string. Table columns hold values of their declared type or `NULL`.

### Lexical rules

- Keywords and identifiers are case-insensitive. Identifiers are
  `[A-Za-z_][A-Za-z0-9_$]*` (non-ASCII letters are allowed too) or any text in
  double quotes (`"x y"`, with `""` for a quote). Double-quoted text is always
  an identifier, never a string.
- The reserved words, which cannot be used as unquoted identifiers, are:
  `ALL AND AS ASC BETWEEN BY CASE CAST CROSS DESC DISTINCT ELSE END EXCEPT EXISTS
  FROM GROUP HAVING IN INNER INTERSECT IS JOIN LEFT LIKE LIMIT NOT NULL OFFSET ON
  OR ORDER OUTER SELECT THEN UNION WHEN WHERE`.
- Strings are in single quotes, with `''` for a quote. A backslash has no
  special meaning.
- Numbers: `12` is an integer (a literal too large for 64 bits is a real).
  `1.5`, `.5`, `5.`, `1e3`, `2.5E-3` are reals.
- `-- comment` runs to the end of the line, and `/* comment */` can span
  lines.
- A number directly followed by letters (`12abc`) or an unterminated string
  is a syntax error.

### Operators and precedence

From loosest to tightest binding:

| Level | Operators | Associativity |
|---|---|---|
| 1 | `OR` | left |
| 2 | `AND` | left |
| 3 | `NOT` (prefix) | right |
| 4 | `=` `==` `!=` `<>` `IS` `IS NOT` `IN` `NOT IN` `LIKE` `NOT LIKE` `BETWEEN … AND …` `NOT BETWEEN` | left |
| 5 | `<` `<=` `>` `>=` | left |
| 6 | `+` `-` | left |
| 7 | `*` `/` `%` | left |
| 8 | `\|\|` | left |
| 9 | unary `-` and `+` | right |

So `1 + 2 || 3` is `1 + (2 || 3)`, `- 2 || 3` is `(-2) || 3`,
`NOT 1 = 2` is `NOT (1 = 2)` and `1 < 2 = 1` is `(1 < 2) = 1`. The operands of
`BETWEEN` and the right side of `LIKE` and `IS` are level-5 expressions.

### Truth values

Comparisons and logical operators give the integers `1` (true) and `0`
(false), or `NULL` (unknown). Where a condition is tested (`WHERE`, `ON`,
`HAVING`, `CASE WHEN`, `AND`, `OR`, `NOT`), `NULL` is unknown, a number is
true when it is not zero, and a text value is tested as the number it starts
with (see "Text to numbers" below). Only true conditions select rows.

- `AND`: false if either side is false, otherwise unknown if either side is
  unknown, otherwise true. `OR`: true if either side is true, otherwise
  unknown if either is unknown, otherwise false. `NOT` of unknown is unknown.

### Arithmetic

`+ - * / %` give `NULL` if either operand is `NULL`.

- Two integers give an integer. `/` truncates toward zero (`-7 / 2` is `-3`)
  and `%` takes the sign of the left operand (`-7 % 3` is `-1`,
  `7 % -3` is `1`).
- If either operand is a real, `+ - * /` are done in doubles and give a real.
  `%` with a real operand truncates both operands to integers, computes the
  integer remainder and returns it **as a real**: `5.5 % 2` is `1.0`,
  `-9.75 % 4` is `-1.0`.
- Division or remainder by zero (integer or real) gives `NULL`.
- Unary `-` negates a number and gives `NULL` for `NULL`.

Integer overflow and arithmetic on text values are out of scope.

### Concatenation

`a || b` is `NULL` if either side is `NULL`. Otherwise both sides are turned
into text and joined: integers as decimal digits, text as is. (Turning a real
into text is out of scope here, except through `CAST`, below.)

### Comparison and ordering

There is one total order, used by the comparison operators, `ORDER BY`,
`DISTINCT`, `GROUP BY`, set operations, `min` and `max`:

`NULL` < all numbers < all text.

Numbers compare by numeric value, integers and reals alike (`1 = 1.0` is
true; compare an integer and a real exactly, not by converting the integer to
a double). Text compares by its UTF-8 bytes, so `'B' < 'a'` and `'é' > 'z'`.

The comparison operators `= == != <> < <= > >=` give `NULL` when either side
is `NULL`, otherwise `1` or `0` by the order above. Comparing a text value
with a number is out of scope.

- `a IS b` is true when both are `NULL`, or neither is and they are equal.
  `a IS NOT b` is its negation. Both always give `0` or `1`, never `NULL`.
- `x BETWEEN lo AND hi` is `x >= lo AND x <= hi` (with the `AND` rules
  above). `NOT BETWEEN` negates it, and `NULL` stays `NULL`.
- `x IN (e1, e2, …)`: an empty list gives `0`. Otherwise `NULL` if `x` is
  `NULL`; `1` if some element equals `x`; else `NULL` if some element is
  `NULL`; else `0`. `NOT IN` swaps `0` and `1` and keeps `NULL`.
- `x LIKE p`: `NULL` if either side is `NULL`. Otherwise both are turned into
  text and matched character by character: `%` matches any run of characters
  (including none), `_` matches exactly one character, and every other
  character matches itself, with ASCII letters matching regardless of case
  (`'a' LIKE 'A'` is true, `'é' LIKE 'É'` is false). There is no escape
  character. `NOT LIKE` negates the result.

### CASE and CAST

- `CASE WHEN c1 THEN r1 [WHEN …] [ELSE e] END` gives the result of the first
  true condition, otherwise `e`, otherwise `NULL`.
- `CASE x WHEN v1 THEN r1 … END` compares `x` with each `v` using `=`, so a
  `NULL` never matches.
- `CAST(x AS INTEGER)`: integers unchanged; reals truncated toward zero;
  text: skip leading whitespace, then read an optional sign and digits
  (`'12abc'` is 12, `'-8.5'` is -8, `'abc'` is 0).
- `CAST(x AS REAL)`: integers become doubles; text: skip leading whitespace,
  then read the longest prefix that is a decimal number with an optional
  sign, fraction and exponent (`'2.5kg'` is 2.5, `'1e3'` is 1000.0, `'abc'`
  is 0.0).
- `CAST(x AS TEXT)`: integers as decimal digits, text unchanged. A real
  becomes text with 15 significant digits, trailing zeros removed and always
  a decimal point: `2.5`, `100.0`, `0.3` for `0.1 + 0.2`, and exponent form
  `1.0e+20` when the exponent is below -4 or at least 15.
- `CAST(NULL AS anything)` is `NULL`. The type name may also be `INT`.

### Text to numbers

Where a text value is tested as a condition, it is read like
`CAST(x AS REAL)` above (so `'abc'` is false and `' 3'` is true). `abs` and
`round` given text also read it that way and return a real.

## Functions

Names are case-insensitive. Calling an unknown function, or a function with
the wrong number of arguments, is an error. Unless stated otherwise, a
function returns `NULL` when any argument is `NULL`. "Length" and positions
always count Unicode characters, not bytes. Numbers given where text is
expected are used as their decimal text.

| Function | Result |
|---|---|
| `abs(x)` | Absolute value. An integer stays an integer and a real stays a real. |
| `length(x)` | Number of characters of `x` as text (`length(-3)` is 2). |
| `lower(x)`, `upper(x)` | `x` as text with only the ASCII letters changed (`upper('straße é')` is `STRAßE é`). |
| `substr(x, start)`, `substr(x, start, len)`, `substring(…)` | Part of `x` as text, as described below. |
| `trim(x)`, `ltrim(x)`, `rtrim(x)` | `x` without spaces (only U+0020) at both ends, the start, or the end. |
| `trim(x, chars)`, `ltrim(x, chars)`, `rtrim(x, chars)` | Remove any of the characters in `chars` instead. |
| `replace(x, from, to)` | Every occurrence of `from` replaced by `to`. If `from` is empty, `x` unchanged. |
| `instr(x, y)` | Position (from 1) of the first occurrence of `y` in `x`, 0 if none, 1 if `y` is empty. |
| `round(x)`, `round(x, n)` | A real, as described below. |
| `coalesce(a, b, …)` | The first argument that is not `NULL`, or `NULL`. Needs at least 2 arguments. |
| `ifnull(a, b)` | `coalesce(a, b)`. |
| `nullif(a, b)` | `NULL` if `a` and `b` are equal in the total order, otherwise `a`. |
| `typeof(x)` | `'null'`, `'integer'`, `'real'` or `'text'`. |
| `max(a, b, …)`, `min(a, b, …)` | With two or more arguments: the largest or smallest argument by the total order, `NULL` if any argument is `NULL`. For ties `max` keeps the earlier argument and `min` the later one. |

With one argument, `max` and `min` are the aggregate functions.

**substr.** Let `L` be the number of characters, `p = start`, `q = len` (or
"everything" when omitted). Work out where to start and how many characters
to take:

1. If `q < 0`, remember that and set `q = -q`.
2. If `p < 0`: `p = p + L`; if that is still negative, then `q = q + p`
   (and `q = 0` if it goes negative) and `p = 0`.
   Else if `p > 0`: `p = p - 1`.
   Else (`p = 0`): if `q > 0`, `q = q - 1`.
3. If `q` was negative in step 1: `p = p - q`; if that is negative, then
   `q = q + p` and `p = 0`.
4. The result is the characters from 0-based index `p`, at most `q` of them
   (empty if `p >= L`).

So `substr('hello', 2, 3)` is `ell`, `substr('hello', -3)` is `llo`,
`substr('hello', 0, 2)` is `h`, `substr('hello', 2, -1)` is `h` and
`substr('hello', 3, -5)` is `he`. Real `start` and `len` arguments are
truncated to integers.

**round.** `round(x, n)` returns a real; `n` defaults to 0, a negative `n`
counts as 0 and a real `n` is truncated.

- If `|x| > 2^52`, the result is `x` unchanged.
- If `n = 0`, the result is `x + 0.5` (or `x - 0.5` when `x < 0`) truncated
  toward zero: `round(2.5)` is `3.0`, `round(-2.5)` is `-3.0`,
  `round(-0.4)` is `0.0`.
- Otherwise round the **decimal expansion of the double** to `n` digits after
  the point, with halves rounded away from zero. Use at least 20 significant
  digits of the exact value: `round(2.345, 2)` is `2.35`, but
  `round(1.005, 2)` is `1.0` and `round(2.675, 2)` is `2.67` because those
  doubles are just below the halfway point, while `round(0.125, 2)` is `0.13`
  because 0.125 is exact.

## Statements

```
statement   := select [ ';' ]
select      := core { compound-op core } [ ORDER BY term { ',' term } ]
               [ LIMIT expr [ OFFSET expr ] ]
compound-op := UNION | UNION ALL | INTERSECT | EXCEPT
core        := SELECT [ DISTINCT | ALL ] item { ',' item }
               [ FROM source { join source [ ON expr ] } ]
               [ WHERE expr ] [ GROUP BY expr { ',' expr } ] [ HAVING expr ]
item        := '*' | name '.' '*' | expr [ [ AS ] name ]
source      := table [ [ AS ] name ] | '(' select ')' [ [ AS ] name ]
join        := ',' | [ INNER ] JOIN | LEFT [ OUTER ] JOIN | CROSS JOIN
term        := expr [ ASC | DESC ]
```

There is exactly one statement per query. Anything after it (other than
whitespace, comments and one `;`) is a syntax error.

Expressions are literals, `NULL`, column references `col` or `table.col`,
operators, function calls, `CASE`, `CAST`, `(expr)`, and the subquery forms
`(select)`, `EXISTS (select)`, `x IN (select)`.

### FROM and joins

Sources are combined left to right. Each source is known by its alias, or by
its table name when it has none. The columns of all sources form the input
row, in order.

- `JOIN … ON c` (inner join) keeps every combination of the left rows and the
  new source's rows for which `c` is true. `JOIN` without `ON`, `CROSS JOIN`
  and `,` keep every combination.
- `LEFT JOIN … ON c` also keeps each left row that matched nothing, with the
  new source's columns set to `NULL`.
- `ON` may only use columns of the sources joined so far.
- An unqualified column name must match exactly one column of the sources in
  the current query; matching several is an error ("ambiguous column name").
  Qualified names (`e.id`) pick the source with that alias. An unknown table
  qualifier or column is an error.
- Without `FROM`, the query works on a single row with no columns, and `*` is
  an error.

Joining two tables on an equality between their columns (`a.x = b.y`) must
be fast: the large tables are joined this way, and comparing every pair of
rows is far too slow for the time limit.

### WHERE, GROUP BY, HAVING

`WHERE` keeps the input rows for which it is true. A query is an aggregate
query when it has `GROUP BY` or `HAVING`, or uses an aggregate function in
its select list or `ORDER BY`.

- Rows go into one group per distinct list of `GROUP BY` values, with
  equality by the total order (so `NULL`s form one group). A `GROUP BY` term
  that is a plain integer literal `k` means the expression of the k-th select
  item.
- Without `GROUP BY`, all rows form one group, and **an aggregate query
  always returns exactly one row, even when no rows remain**. With
  `GROUP BY` and no rows, it returns no rows.
- `HAVING` keeps the groups for which it is true.
- In an aggregate query, select items, `HAVING` and `ORDER BY` may use
  aggregate functions and expressions of the `GROUP BY` columns. Using a
  column that is neither grouped nor inside an aggregate is out of scope.
- Aggregate functions in `WHERE`, `ON` or `GROUP BY`, or nested inside
  another aggregate, are errors.

Aggregate functions skip `NULL` arguments. With `DISTINCT` (for example
`count(DISTINCT x)`), each distinct value is used once.

| Aggregate | Result |
|---|---|
| `count(*)` | Number of rows in the group. |
| `count(x)` | Number of non-`NULL` values. |
| `sum(x)` | `NULL` if there are no values. An integer if all values are integers, otherwise a real. |
| `total(x)` | Like `sum` but always a real, `0.0` when there are no values. |
| `avg(x)` | `NULL` if there are no values, otherwise the sum as a real divided by the count. |
| `min(x)`, `max(x)` | Smallest or largest value by the total order, `NULL` if there are none. For ties the first value seen is kept. |

Real sums must be computed the way SQLite 3.45 does, so the last digit
matches. Keep an exact integer sum while every value is an integer. When the
first real arrives, switch to a double sum `s` with an error term `c`
(initially `s` = the integer sum, `c = 0`) and add each further value `v` with
the Kahan-Babuska-Neumaier step:

```
t = s + v
if |s| > |v|: c += (s - t) + v
else:         c += (v - t) + s
s = t
```

The result is `s + c` (and `avg` divides that by the count). Integer values
added after the switch go through the same step (as doubles).

### Name lookup with aliases

A name in `WHERE`, `GROUP BY` or `HAVING` is first looked up as a column. If
no column of the current query has that name, but a select item has it as its
`AS` alias, the name stands for that item's expression. For example
`SELECT customer_id, count(*) AS n FROM orders GROUP BY customer_id HAVING n > 5`.

### Subqueries

- `(select)` used as a value must return one column. It gives the first
  column of the first row, or `NULL` when there are no rows.
- `x IN (select)` must return one column, and follows the `IN` rules with the
  subquery's rows as the list. `NOT IN` likewise. So
  `NOT IN (SELECT dept_id …)` gives no rows if `dept_id` is ever `NULL`.
- `EXISTS (select)` is `1` if the subquery returns at least one row, else
  `0`.
- Subqueries in expressions may refer to columns of the enclosing queries
  (correlated subqueries). Names are looked up in the innermost query first,
  then outward.
- Subqueries in `FROM` are evaluated on their own and cannot refer to outer
  columns. Their columns are known by their result column names.
- A subquery may have its own `ORDER BY` and `LIMIT`.

An uncorrelated subquery should be evaluated once, not once per row: the
large tables are filtered with `x IN (SELECT …)` too.

### DISTINCT, compound selects, ORDER BY, LIMIT

- `SELECT DISTINCT` removes duplicate result rows (equality by the total
  order), keeping the first.
- `UNION ALL` concatenates. `UNION`, `INTERSECT` and `EXCEPT` return distinct
  rows that are in either, both, or the left but not the right side. All the
  `SELECT`s must have the same number of columns, else it is an error.
  Compound operators have equal precedence and are applied left to right.
  `ORDER BY` and `LIMIT` at the end apply to the whole compound result.
- `ORDER BY` sorts by its terms in turn, using the total order (`NULL`s come
  first in ascending and last in descending order).
  - A term that is a plain integer literal `k` means the k-th result column.
    Out of range (including 0) is an error.
  - In a simple `SELECT`, a term that is a bare name equal to an `AS` alias
    of the select list means that result column. Any other term is an
    expression evaluated on the input row (or, in an aggregate query, on the
    group).
  - In a compound select, every term must be an integer or the name of a
    result column; anything else is an error.
- `LIMIT n` keeps at most `n` rows and `OFFSET m` first skips `m` rows. Both
  are integer constants.

Every hidden query either returns at most one row or has an `ORDER BY` that
fixes the order of all rows, so you never have to guess the order of rows
that compare equal. Without `ORDER BY` you may return rows in any order.

## Errors

A query that cannot be run is an error (exit 1, nothing on stdout). This
includes:

- syntax errors, including an unterminated string, trailing text and more
  than one statement;
- an unknown table, column or table qualifier, or an ambiguous column;
- an unknown function or the wrong number of arguments;
- an aggregate function where it is not allowed;
- an `ORDER BY` or `GROUP BY` position out of range;
- compound `SELECT`s with different numbers of columns;
- a subquery used as a value or with `IN` that has more than one column.

The error message text is never tested.

## Out of scope

The hidden cases never use: integer overflow; arithmetic, comparison or `IN`
mixing text with numbers; turning a real into text other than through `CAST`;
`USING` and `NATURAL` joins; `RIGHT` and `FULL` joins; window functions,
`WITH`; `LIKE … ESCAPE`, `GLOB`, `COLLATE`; bit operators; `LIMIT x, y`;
non-grouped columns in aggregate queries; correlated subqueries in `FROM`;
`group_concat` and functions not listed above; blobs; any statement other
than `SELECT`.
