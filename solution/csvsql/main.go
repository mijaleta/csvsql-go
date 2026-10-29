// csvsql runs one SQL SELECT statement over the CSV files in a directory and
// prints the result as CSV.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

const usage = "usage: csvsql [-d DIR] QUERY"

func usageError(msg string) {
	fmt.Fprintf(os.Stderr, "csvsql: %s\n%s\n", msg, usage)
	os.Exit(2)
}

func csvQuote(s string, isNull bool) string {
	if isNull {
		return ""
	}
	if s == "" || strings.ContainsAny(s, ",\"\r\n") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

func run(dir, query string) (names []string, rows [][]Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			if ce, ok := r.(*compileError); ok {
				err = ce
				return
			}
			panic(r)
		}
	}()
	db, err := openDB(dir)
	if err != nil {
		return nil, nil, err
	}
	sel, err := parseStatement(query)
	if err != nil {
		return nil, nil, err
	}
	cp := &compiler{src: query, db: db}
	plan := cp.compileSelect(sel, nil)
	return plan.names, plan.run(nil), nil
}

func main() {
	dir := "."
	query := ""
	haveQuery := false
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-d":
			if i+1 >= len(args) {
				usageError("-d needs a directory")
			}
			i++
			dir = args[i]
		case strings.HasPrefix(a, "-") && len(a) > 1:
			usageError("unknown option " + a)
		default:
			if haveQuery {
				usageError("only one query is allowed")
			}
			query, haveQuery = a, true
		}
	}
	if !haveQuery {
		usageError("missing query")
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		usageError("not a directory: " + dir)
	}

	names, rows, err := run(dir, query)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	w := bufio.NewWriterSize(os.Stdout, 1<<16)
	for i, n := range names {
		if i > 0 {
			w.WriteByte(',')
		}
		w.WriteString(csvQuote(n, false))
	}
	w.WriteByte('\n')
	for _, r := range rows {
		for i, v := range r {
			if i > 0 {
				w.WriteByte(',')
			}
			s, ok := formatValue(v)
			w.WriteString(csvQuote(s, !ok))
		}
		w.WriteByte('\n')
	}
	w.Flush()
}
