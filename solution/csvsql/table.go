package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Table struct {
	Name  string
	Cols  []string
	Types []Kind
	Rows  [][]Value
}

// DB finds tables in a directory and loads each one the first time it is used.
type DB struct {
	files  map[string]string
	tables map[string]*Table
}

func openDB(dir string) (*DB, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	db := &DB{files: map[string]string{}, tables: map[string]*Table{}}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".csv") {
			continue
		}
		key := strings.ToLower(strings.TrimSuffix(name, ".csv"))
		if _, dup := db.files[key]; !dup {
			db.files[key] = filepath.Join(dir, name)
		}
	}
	return db, nil
}

func (db *DB) table(name string) (*Table, error) {
	key := strings.ToLower(name)
	if t, ok := db.tables[key]; ok {
		return t, nil
	}
	path, ok := db.files[key]
	if !ok {
		return nil, fmt.Errorf("no such table: %s", name)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	t, err := parseTable(strings.TrimSuffix(filepath.Base(path), ".csv"), string(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %v", filepath.Base(path), err)
	}
	db.tables[key] = t
	return t, nil
}

type csvField struct {
	s      string
	quoted bool
}

// readRecord reads one CSV record starting at pos and returns the position
// after its line ending.
func readRecord(data string, pos int) ([]csvField, int, error) {
	var fields []csvField
	var b strings.Builder
	n := len(data)
	for {
		b.Reset()
		quoted := false
		if pos < n && data[pos] == '"' {
			quoted = true
			pos++
			for {
				if pos >= n {
					return nil, pos, fmt.Errorf("unterminated quoted field")
				}
				c := data[pos]
				if c == '"' {
					if pos+1 < n && data[pos+1] == '"' {
						b.WriteByte('"')
						pos += 2
						continue
					}
					pos++
					break
				}
				b.WriteByte(c)
				pos++
			}
		}
		for pos < n && data[pos] != ',' && data[pos] != '\n' && data[pos] != '\r' {
			b.WriteByte(data[pos])
			pos++
		}
		fields = append(fields, csvField{b.String(), quoted})
		if pos >= n {
			return fields, pos, nil
		}
		switch data[pos] {
		case ',':
			pos++
			continue
		case '\r':
			pos++
			if pos < n && data[pos] == '\n' {
				pos++
			}
		default:
			pos++
		}
		return fields, pos, nil
	}
}

func parseTable(name, data string) (*Table, error) {
	t := &Table{Name: name}
	header, pos, err := readRecord(data, 0)
	if err != nil {
		return nil, err
	}
	for _, f := range header {
		i := strings.LastIndexByte(f.s, ':')
		if i < 0 {
			return nil, fmt.Errorf("header field %q has no type", f.s)
		}
		var k Kind
		switch strings.ToUpper(f.s[i+1:]) {
		case "INTEGER":
			k = KInt
		case "REAL":
			k = KReal
		case "TEXT":
			k = KText
		default:
			return nil, fmt.Errorf("unknown type in %q", f.s)
		}
		t.Cols = append(t.Cols, f.s[:i])
		t.Types = append(t.Types, k)
	}
	for pos < len(data) {
		var rec []csvField
		rec, pos, err = readRecord(data, pos)
		if err != nil {
			return nil, err
		}
		if len(rec) != len(t.Cols) {
			return nil, fmt.Errorf("row %d has %d fields, expected %d", len(t.Rows)+1, len(rec), len(t.Cols))
		}
		row := make([]Value, len(rec))
		for i, f := range rec {
			switch {
			case t.Types[i] == KText:
				if f.s != "" || f.quoted {
					row[i] = TextV(f.s)
				}
			case f.s == "":
			case t.Types[i] == KInt:
				n, err := strconv.ParseInt(f.s, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("bad INTEGER %q", f.s)
				}
				row[i] = IntV(n)
			default:
				x, err := strconv.ParseFloat(f.s, 64)
				if err != nil {
					return nil, fmt.Errorf("bad REAL %q", f.s)
				}
				row[i] = RealV(x)
			}
		}
		t.Rows = append(t.Rows, row)
	}
	return t, nil
}
