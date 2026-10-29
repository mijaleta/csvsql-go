package main

import (
	"fmt"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------- lexer

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tQIdent
	tString
	tInt
	tReal
	tOp
)

type token struct {
	kind  tokKind
	text  string // identifier name, string contents, number text or operator
	start int
	end   int
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

func isIdentChar(c byte) bool { return isIdentStart(c) || isDigit(c) || c == '$' }

func lex(src string) ([]token, error) {
	var toks []token
	i, n := 0, len(src)
	for i < n {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			i++
		case c == '-' && i+1 < n && src[i+1] == '-':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			j := strings.Index(src[i+2:], "*/")
			if j < 0 {
				i = n
			} else {
				i += j + 4
			}
		case c == '\'':
			var b strings.Builder
			j := i + 1
			for {
				if j >= n {
					return nil, fmt.Errorf("unrecognized token: %s", src[i:])
				}
				if src[j] == '\'' {
					if j+1 < n && src[j+1] == '\'' {
						b.WriteByte('\'')
						j += 2
						continue
					}
					break
				}
				b.WriteByte(src[j])
				j++
			}
			toks = append(toks, token{tString, b.String(), i, j + 1})
			i = j + 1
		case c == '"':
			var b strings.Builder
			j := i + 1
			for {
				if j >= n {
					return nil, fmt.Errorf("unrecognized token: %s", src[i:])
				}
				if src[j] == '"' {
					if j+1 < n && src[j+1] == '"' {
						b.WriteByte('"')
						j += 2
						continue
					}
					break
				}
				b.WriteByte(src[j])
				j++
			}
			toks = append(toks, token{tQIdent, b.String(), i, j + 1})
			i = j + 1
		case isDigit(c) || (c == '.' && i+1 < n && isDigit(src[i+1])):
			j := i
			for j < n && isDigit(src[j]) {
				j++
			}
			kind := tInt
			if j < n && src[j] == '.' {
				kind = tReal
				j++
				for j < n && isDigit(src[j]) {
					j++
				}
			}
			if j < n && (src[j] == 'e' || src[j] == 'E') {
				k := j + 1
				if k < n && (src[k] == '+' || src[k] == '-') {
					k++
				}
				if k >= n || !isDigit(src[k]) {
					return nil, fmt.Errorf("unrecognized token: %s", src[i:k])
				}
				for k < n && isDigit(src[k]) {
					k++
				}
				j = k
				kind = tReal
			}
			if j < n && isIdentChar(src[j]) {
				k := j
				for k < n && isIdentChar(src[k]) {
					k++
				}
				return nil, fmt.Errorf("unrecognized token: %s", src[i:k])
			}
			toks = append(toks, token{kind, src[i:j], i, j})
			i = j
		case isIdentStart(c):
			j := i
			for j < n && isIdentChar(src[j]) {
				j++
			}
			toks = append(toks, token{tIdent, src[i:j], i, j})
			i = j
		default:
			two := ""
			if i+1 < n {
				two = src[i : i+2]
			}
			switch two {
			case "||", "==", "!=", "<>", "<=", ">=":
				toks = append(toks, token{tOp, two, i, i + 2})
				i += 2
				continue
			}
			if strings.IndexByte("(),.*+-/%=<>;", c) < 0 {
				return nil, fmt.Errorf("unrecognized token: %q", string(c))
			}
			toks = append(toks, token{tOp, string(c), i, i + 1})
			i++
		}
	}
	toks = append(toks, token{tEOF, "", n, n})
	return toks, nil
}

// ---------------------------------------------------------------- AST

type Expr interface{ span() (int, int) }

type pos struct{ start, end int }

func (p pos) span() (int, int) { return p.start, p.end }

type (
	Lit struct {
		pos
		V Value
	}
	ColRef struct {
		pos
		Table, Name string
	}
	Unary struct {
		pos
		Op string
		X  Expr
	}
	Binary struct {
		pos
		Op   string
		L, R Expr
	}
	IsExpr struct {
		pos
		L, R Expr
		Not  bool
	}
	InList struct {
		pos
		X    Expr
		List []Expr
		Not  bool
	}
	InSub struct {
		pos
		X   Expr
		Sub *Select
		Not bool
	}
	Between struct {
		pos
		X, Lo, Hi Expr
		Not       bool
	}
	Like struct {
		pos
		X, Pat Expr
		Not    bool
	}
	When struct{ Cond, Then Expr }
	Case struct {
		pos
		Operand Expr
		Whens   []When
		Else    Expr
	}
	Cast struct {
		pos
		X    Expr
		Type string
	}
	Func struct {
		pos
		Name     string
		Args     []Expr
		Star     bool
		Distinct bool
	}
	SubExpr struct {
		pos
		Sub *Select
	}
	Exists struct {
		pos
		Sub *Select
	}
	Paren struct {
		pos
		X Expr
	}
)

type SelectItem struct {
	Expr      Expr
	Alias     string
	Star      bool
	StarTable string
}

type FromItem struct {
	Table string
	Sub   *Select
	Alias string
	Join  string // "", ",", "INNER", "LEFT", "CROSS"
	On    Expr
}

type Core struct {
	Distinct bool
	Items    []SelectItem
	From     []FromItem
	Where    Expr
	GroupBy  []Expr
	Having   Expr
}

type OrderTerm struct {
	X    Expr
	Desc bool
}

type Select struct {
	Cores   []*Core
	Ops     []string
	OrderBy []OrderTerm
	Limit   Expr
	Offset  Expr
}

// ---------------------------------------------------------------- parser

var reserved = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`SELECT DISTINCT ALL FROM WHERE GROUP BY HAVING ORDER ASC DESC LIMIT
		OFFSET AS JOIN INNER LEFT OUTER CROSS ON AND OR NOT IS NULL IN LIKE BETWEEN CASE WHEN THEN
		ELSE END CAST EXISTS UNION INTERSECT EXCEPT`) {
		reserved[w] = true
	}
}

type parser struct {
	src  string
	toks []token
	i    int
}

type parseError struct{ msg string }

func (e *parseError) Error() string { return e.msg }

func (p *parser) fail(format string, args ...any) {
	panic(&parseError{fmt.Sprintf(format, args...)})
}

func (p *parser) peek() token { return p.toks[p.i] }

func (p *parser) next() token {
	t := p.toks[p.i]
	if t.kind != tEOF {
		p.i++
	}
	return t
}

func (p *parser) isKw(t token, kw string) bool {
	return t.kind == tIdent && strings.EqualFold(t.text, kw)
}

func (p *parser) atKw(kw string) bool { return p.isKw(p.peek(), kw) }

func (p *parser) acceptKw(kw string) bool {
	if p.atKw(kw) {
		p.i++
		return true
	}
	return false
}

func (p *parser) expectKw(kw string) {
	if !p.acceptKw(kw) {
		p.failNear()
	}
}

func (p *parser) atOp(op string) bool {
	t := p.peek()
	return t.kind == tOp && t.text == op
}

func (p *parser) acceptOp(op string) bool {
	if p.atOp(op) {
		p.i++
		return true
	}
	return false
}

func (p *parser) expectOp(op string) token {
	if !p.atOp(op) {
		p.failNear()
	}
	return p.next()
}

func (p *parser) failNear() {
	t := p.peek()
	if t.kind == tEOF {
		p.fail("incomplete input")
	}
	p.fail("near %q: syntax error", p.src[t.start:t.end])
}

func (p *parser) prevEnd() int { return p.toks[p.i-1].end }

// name accepts an identifier that can name a table, column or alias.
func (p *parser) name() (string, bool) {
	t := p.peek()
	if t.kind == tQIdent || (t.kind == tIdent && !reserved[strings.ToUpper(t.text)]) {
		p.i++
		return t.text, true
	}
	return "", false
}

func (p *parser) expectName() string {
	n, ok := p.name()
	if !ok {
		p.failNear()
	}
	return n
}

func parseStatement(src string) (sel *Select, err error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{src: src, toks: toks}
	defer func() {
		if r := recover(); r != nil {
			if pe, ok := r.(*parseError); ok {
				sel, err = nil, pe
				return
			}
			panic(r)
		}
	}()
	sel = p.parseSelect()
	p.acceptOp(";")
	if p.peek().kind != tEOF {
		p.failNear()
	}
	return sel, nil
}

func (p *parser) parseSelect() *Select {
	s := &Select{Cores: []*Core{p.parseCore()}}
	for {
		var op string
		switch {
		case p.acceptKw("UNION"):
			op = "UNION"
			if p.acceptKw("ALL") {
				op = "UNION ALL"
			}
		case p.acceptKw("INTERSECT"):
			op = "INTERSECT"
		case p.acceptKw("EXCEPT"):
			op = "EXCEPT"
		}
		if op == "" {
			break
		}
		s.Ops = append(s.Ops, op)
		s.Cores = append(s.Cores, p.parseCore())
	}
	if p.acceptKw("ORDER") {
		p.expectKw("BY")
		for {
			t := OrderTerm{X: p.parseExpr()}
			if p.acceptKw("DESC") {
				t.Desc = true
			} else {
				p.acceptKw("ASC")
			}
			s.OrderBy = append(s.OrderBy, t)
			if !p.acceptOp(",") {
				break
			}
		}
	}
	if p.acceptKw("LIMIT") {
		s.Limit = p.parseExpr()
		if p.acceptKw("OFFSET") {
			s.Offset = p.parseExpr()
		}
	}
	return s
}

func (p *parser) parseCore() *Core {
	p.expectKw("SELECT")
	c := &Core{}
	if p.acceptKw("DISTINCT") {
		c.Distinct = true
	} else {
		p.acceptKw("ALL")
	}
	for {
		c.Items = append(c.Items, p.parseItem())
		if !p.acceptOp(",") {
			break
		}
	}
	if p.acceptKw("FROM") {
		c.From = append(c.From, p.parseFromItem(""))
		for {
			var join string
			switch {
			case p.acceptOp(","):
				join = ","
			case p.acceptKw("JOIN"):
				join = "INNER"
			case p.atKw("INNER"):
				p.next()
				p.expectKw("JOIN")
				join = "INNER"
			case p.atKw("LEFT"):
				p.next()
				p.acceptKw("OUTER")
				p.expectKw("JOIN")
				join = "LEFT"
			case p.atKw("CROSS"):
				p.next()
				p.expectKw("JOIN")
				join = "CROSS"
			}
			if join == "" {
				break
			}
			f := p.parseFromItem(join)
			if join != "," && p.acceptKw("ON") {
				f.On = p.parseExpr()
			}
			c.From = append(c.From, f)
		}
	}
	if p.acceptKw("WHERE") {
		c.Where = p.parseExpr()
	}
	if p.acceptKw("GROUP") {
		p.expectKw("BY")
		for {
			c.GroupBy = append(c.GroupBy, p.parseExpr())
			if !p.acceptOp(",") {
				break
			}
		}
	}
	if p.acceptKw("HAVING") {
		c.Having = p.parseExpr()
	}
	return c
}

func (p *parser) parseItem() SelectItem {
	if p.acceptOp("*") {
		return SelectItem{Star: true}
	}
	t := p.peek()
	if (t.kind == tIdent || t.kind == tQIdent) && p.toks[p.i+1].kind == tOp && p.toks[p.i+1].text == "." &&
		p.toks[p.i+2].kind == tOp && p.toks[p.i+2].text == "*" {
		name := p.expectName()
		p.i += 2
		return SelectItem{Star: true, StarTable: name}
	}
	it := SelectItem{Expr: p.parseExpr()}
	if p.acceptKw("AS") {
		it.Alias = p.expectName()
	} else if n, ok := p.name(); ok {
		it.Alias = n
	}
	return it
}

func (p *parser) parseFromItem(join string) FromItem {
	f := FromItem{Join: join}
	if p.atOp("(") {
		p.next()
		f.Sub = p.parseSelect()
		p.expectOp(")")
	} else {
		f.Table = p.expectName()
	}
	if p.acceptKw("AS") {
		f.Alias = p.expectName()
	} else if n, ok := p.name(); ok {
		f.Alias = n
	}
	return f
}

func spanOf(e Expr) (int, int) { return e.span() }

func (p *parser) parseExpr() Expr { return p.parseOr() }

func (p *parser) parseOr() Expr {
	l := p.parseAnd()
	for p.acceptKw("OR") {
		r := p.parseAnd()
		s, _ := spanOf(l)
		_, e := spanOf(r)
		l = &Binary{pos{s, e}, "OR", l, r}
	}
	return l
}

func (p *parser) parseAnd() Expr {
	l := p.parseNot()
	for p.acceptKw("AND") {
		r := p.parseNot()
		s, _ := spanOf(l)
		_, e := spanOf(r)
		l = &Binary{pos{s, e}, "AND", l, r}
	}
	return l
}

func (p *parser) parseNot() Expr {
	if p.atKw("NOT") {
		t := p.next()
		x := p.parseNot()
		_, e := spanOf(x)
		return &Unary{pos{t.start, e}, "NOT", x}
	}
	return p.parseEquality()
}

func (p *parser) parseEquality() Expr {
	l := p.parseComparison()
	for {
		s, _ := spanOf(l)
		t := p.peek()
		if t.kind == tOp && (t.text == "=" || t.text == "==" || t.text == "!=" || t.text == "<>") {
			p.next()
			r := p.parseComparison()
			_, e := spanOf(r)
			op := t.text
			if op == "==" {
				op = "="
			} else if op == "<>" {
				op = "!="
			}
			l = &Binary{pos{s, e}, op, l, r}
			continue
		}
		if p.acceptKw("IS") {
			not := p.acceptKw("NOT")
			r := p.parseComparison()
			_, e := spanOf(r)
			l = &IsExpr{pos{s, e}, l, r, not}
			continue
		}
		not := false
		if p.atKw("NOT") {
			nt := p.toks[p.i+1]
			if p.isKw(nt, "IN") || p.isKw(nt, "LIKE") || p.isKw(nt, "BETWEEN") {
				p.next()
				not = true
			} else {
				return l
			}
		}
		switch {
		case p.acceptKw("IN"):
			p.expectOp("(")
			if p.atKw("SELECT") {
				sub := p.parseSelect()
				p.expectOp(")")
				l = &InSub{pos{s, p.prevEnd()}, l, sub, not}
				continue
			}
			var list []Expr
			if !p.atOp(")") {
				for {
					list = append(list, p.parseExpr())
					if !p.acceptOp(",") {
						break
					}
				}
			}
			p.expectOp(")")
			l = &InList{pos{s, p.prevEnd()}, l, list, not}
		case p.acceptKw("LIKE"):
			r := p.parseComparison()
			_, e := spanOf(r)
			l = &Like{pos{s, e}, l, r, not}
		case p.acceptKw("BETWEEN"):
			lo := p.parseComparison()
			p.expectKw("AND")
			hi := p.parseComparison()
			_, e := spanOf(hi)
			l = &Between{pos{s, e}, l, lo, hi, not}
		default:
			return l
		}
	}
}

func (p *parser) parseComparison() Expr {
	l := p.parseAdditive()
	for {
		t := p.peek()
		if t.kind != tOp || (t.text != "<" && t.text != "<=" && t.text != ">" && t.text != ">=") {
			return l
		}
		p.next()
		r := p.parseAdditive()
		s, _ := spanOf(l)
		_, e := spanOf(r)
		l = &Binary{pos{s, e}, t.text, l, r}
	}
}

func (p *parser) parseAdditive() Expr {
	l := p.parseMultiplicative()
	for {
		t := p.peek()
		if t.kind != tOp || (t.text != "+" && t.text != "-") {
			return l
		}
		p.next()
		r := p.parseMultiplicative()
		s, _ := spanOf(l)
		_, e := spanOf(r)
		l = &Binary{pos{s, e}, t.text, l, r}
	}
}

func (p *parser) parseMultiplicative() Expr {
	l := p.parseConcat()
	for {
		t := p.peek()
		if t.kind != tOp || (t.text != "*" && t.text != "/" && t.text != "%") {
			return l
		}
		p.next()
		r := p.parseConcat()
		s, _ := spanOf(l)
		_, e := spanOf(r)
		l = &Binary{pos{s, e}, t.text, l, r}
	}
}

func (p *parser) parseConcat() Expr {
	l := p.parseUnary()
	for p.atOp("||") {
		p.next()
		r := p.parseUnary()
		s, _ := spanOf(l)
		_, e := spanOf(r)
		l = &Binary{pos{s, e}, "||", l, r}
	}
	return l
}

func (p *parser) parseUnary() Expr {
	t := p.peek()
	if t.kind == tOp && (t.text == "-" || t.text == "+") {
		p.next()
		x := p.parseUnary()
		_, e := spanOf(x)
		return &Unary{pos{t.start, e}, t.text, x}
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() Expr {
	t := p.peek()
	switch t.kind {
	case tInt:
		p.next()
		if n, err := strconv.ParseInt(t.text, 10, 64); err == nil {
			return &Lit{pos{t.start, t.end}, IntV(n)}
		}
		f, _ := strconv.ParseFloat(t.text, 64)
		return &Lit{pos{t.start, t.end}, RealV(f)}
	case tReal:
		p.next()
		f, _ := strconv.ParseFloat(t.text, 64)
		return &Lit{pos{t.start, t.end}, RealV(f)}
	case tString:
		p.next()
		return &Lit{pos{t.start, t.end}, TextV(t.text)}
	case tOp:
		if t.text == "(" {
			p.next()
			if p.atKw("SELECT") {
				sub := p.parseSelect()
				end := p.expectOp(")").end
				return &SubExpr{pos{t.start, end}, sub}
			}
			x := p.parseExpr()
			end := p.expectOp(")").end
			return &Paren{pos{t.start, end}, x}
		}
	case tQIdent:
		return p.parseColumn()
	case tIdent:
		switch strings.ToUpper(t.text) {
		case "NULL":
			p.next()
			return &Lit{pos{t.start, t.end}, Null}
		case "EXISTS":
			p.next()
			p.expectOp("(")
			sub := p.parseSelect()
			end := p.expectOp(")").end
			return &Exists{pos{t.start, end}, sub}
		case "CASE":
			return p.parseCase()
		case "CAST":
			p.next()
			p.expectOp("(")
			x := p.parseExpr()
			p.expectKw("AS")
			ty := p.peek()
			if ty.kind != tIdent {
				p.failNear()
			}
			p.next()
			end := p.expectOp(")").end
			return &Cast{pos{t.start, end}, x, strings.ToUpper(ty.text)}
		}
		if reserved[strings.ToUpper(t.text)] {
			p.failNear()
		}
		if nt := p.toks[p.i+1]; nt.kind == tOp && nt.text == "(" {
			return p.parseFunc()
		}
		return p.parseColumn()
	}
	p.failNear()
	return nil
}

func (p *parser) parseColumn() Expr {
	t := p.peek()
	first := p.expectName()
	if p.atOp(".") {
		p.next()
		second := p.expectName()
		return &ColRef{pos{t.start, p.prevEnd()}, first, second}
	}
	return &ColRef{pos{t.start, t.end}, "", first}
}

func (p *parser) parseFunc() Expr {
	t := p.next()
	p.expectOp("(")
	f := &Func{Name: strings.ToLower(t.text)}
	switch {
	case p.atOp("*"):
		p.next()
		f.Star = true
	case p.atOp(")"):
	default:
		if p.acceptKw("DISTINCT") {
			f.Distinct = true
		}
		for {
			f.Args = append(f.Args, p.parseExpr())
			if !p.acceptOp(",") {
				break
			}
		}
	}
	end := p.expectOp(")").end
	f.pos = pos{t.start, end}
	return f
}

func (p *parser) parseCase() Expr {
	t := p.next()
	c := &Case{}
	if !p.atKw("WHEN") {
		c.Operand = p.parseExpr()
	}
	for p.acceptKw("WHEN") {
		w := When{Cond: p.parseExpr()}
		p.expectKw("THEN")
		w.Then = p.parseExpr()
		c.Whens = append(c.Whens, w)
	}
	if len(c.Whens) == 0 {
		p.failNear()
	}
	if p.acceptKw("ELSE") {
		c.Else = p.parseExpr()
	}
	p.expectKw("END")
	c.pos = pos{t.start, p.prevEnd()}
	return c
}
