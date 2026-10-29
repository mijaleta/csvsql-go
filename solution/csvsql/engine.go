package main

import (
	"fmt"
	"sort"
	"strings"
)

// Ctx is the row an expression is evaluated against. Parent links to the
// enclosing query's row for correlated subqueries.
type Ctx struct {
	Row    []Value
	Agg    []Value
	Parent *Ctx
}

type Ev func(c *Ctx) Value

type compileError struct{ msg string }

func (e *compileError) Error() string { return e.msg }

func cfail(format string, args ...any) {
	panic(&compileError{fmt.Sprintf(format, args...)})
}

type ColInfo struct {
	table string // lower-case alias of the source
	name  string // declared name
	lname string
}

type Scope struct {
	cols   []ColInfo
	tables map[string]bool
	parent *Scope
	owner  *SelectPlan
}

func (s *Scope) find(table, name string) (int, bool) {
	lname, ltab := strings.ToLower(name), strings.ToLower(table)
	match := -1
	for i, c := range s.cols {
		if c.lname != lname || (table != "" && c.table != ltab) {
			continue
		}
		if match >= 0 {
			cfail("ambiguous column name: %s", name)
		}
		match = i
	}
	if match >= 0 {
		return match, true
	}
	if table != "" && s.tables[ltab] {
		cfail("no such column: %s.%s", table, name)
	}
	return -1, false
}

type exprEnv struct {
	scope    *Scope
	aggs     *[]*aggSpec // nil: aggregate functions are not allowed here
	inAgg    bool
	aliases  map[string]Expr
	aliasing map[string]bool
	refs     map[int]bool // depth-0 columns referenced
	outer    bool         // references a column of an enclosing query
}

type compiler struct {
	src string
	db  *DB
}

// ---------------------------------------------------------------- expressions

func unparen(e Expr) Expr {
	for {
		p, ok := e.(*Paren)
		if !ok {
			return e
		}
		e = p.X
	}
}

var aggNames = map[string]bool{"count": true, "sum": true, "avg": true, "total": true, "min": true, "max": true}

func isAggCall(f *Func) bool {
	if !aggNames[f.Name] {
		return false
	}
	if f.Name == "min" || f.Name == "max" {
		return len(f.Args) == 1
	}
	return true
}

// hasAgg reports whether e calls an aggregate function outside any subquery.
func hasAgg(e Expr) bool {
	found := false
	walk(e, func(x Expr) bool {
		if f, ok := x.(*Func); ok && isAggCall(f) {
			found = true
		}
		return !found
	})
	return found
}

func hasSubquery(e Expr) bool {
	found := false
	walk(e, func(x Expr) bool {
		switch x.(type) {
		case *SubExpr, *InSub, *Exists:
			found = true
		}
		return !found
	})
	return found
}

// walk visits e and its subexpressions, not descending into subqueries.
func walk(e Expr, f func(Expr) bool) {
	if e == nil || !f(e) {
		return
	}
	switch x := e.(type) {
	case *Unary:
		walk(x.X, f)
	case *Binary:
		walk(x.L, f)
		walk(x.R, f)
	case *IsExpr:
		walk(x.L, f)
		walk(x.R, f)
	case *InList:
		walk(x.X, f)
		for _, a := range x.List {
			walk(a, f)
		}
	case *InSub:
		walk(x.X, f)
	case *Between:
		walk(x.X, f)
		walk(x.Lo, f)
		walk(x.Hi, f)
	case *Like:
		walk(x.X, f)
		walk(x.Pat, f)
	case *Case:
		walk(x.Operand, f)
		for _, w := range x.Whens {
			walk(w.Cond, f)
			walk(w.Then, f)
		}
		walk(x.Else, f)
	case *Cast:
		walk(x.X, f)
	case *Func:
		for _, a := range x.Args {
			walk(a, f)
		}
	case *Paren:
		walk(x.X, f)
	}
}

func colEv(depth, idx int) Ev {
	switch depth {
	case 0:
		return func(c *Ctx) Value { return c.Row[idx] }
	case 1:
		return func(c *Ctx) Value { return c.Parent.Row[idx] }
	}
	return func(c *Ctx) Value {
		for k := 0; k < depth; k++ {
			c = c.Parent
		}
		return c.Row[idx]
	}
}

func (cp *compiler) colRef(env *exprEnv, r *ColRef) Ev {
	depth := 0
	for s := env.scope; s != nil; s = s.parent {
		if idx, ok := s.find(r.Table, r.Name); ok {
			for t := env.scope; t != s; t = t.parent {
				if t.owner != nil {
					t.owner.correlated = true
				}
			}
			if depth == 0 && env.refs != nil {
				env.refs[idx] = true
			}
			if depth > 0 {
				env.outer = true
			}
			return colEv(depth, idx)
		}
		if depth == 0 && r.Table == "" && env.aliases != nil {
			key := strings.ToLower(r.Name)
			if ae, ok := env.aliases[key]; ok {
				if env.aliasing[key] {
					cfail("misuse of aliased expression %s", r.Name)
				}
				env.aliasing[key] = true
				ev := cp.expr(env, ae)
				delete(env.aliasing, key)
				return ev
			}
		}
		depth++
	}
	if r.Table != "" {
		cfail("no such column: %s.%s", r.Table, r.Name)
	}
	cfail("no such column: %s", r.Name)
	return nil
}

// colName is the declared name of the column a reference resolves to.
func colName(scope *Scope, r *ColRef) string {
	for s := scope; s != nil; s = s.parent {
		if idx, ok := s.find(r.Table, r.Name); ok {
			return s.cols[idx].name
		}
	}
	return r.Name
}

func (cp *compiler) expr(env *exprEnv, e Expr) Ev {
	switch x := e.(type) {
	case *Lit:
		v := x.V
		return func(*Ctx) Value { return v }
	case *ColRef:
		return cp.colRef(env, x)
	case *Paren:
		return cp.expr(env, x.X)
	case *Unary:
		a := cp.expr(env, x.X)
		switch x.Op {
		case "-":
			return func(c *Ctx) Value { return negate(a(c)) }
		case "+":
			return a
		}
		return func(c *Ctx) Value {
			b, ok := truth(a(c))
			if !ok {
				return Null
			}
			return BoolV(!b)
		}
	case *Binary:
		return cp.binary(env, x)
	case *IsExpr:
		l, r, not := cp.expr(env, x.L), cp.expr(env, x.R), x.Not
		return func(c *Ctx) Value {
			a, b := l(c), r(c)
			var eq bool
			switch {
			case a.K == KNull || b.K == KNull:
				eq = a.K == b.K
			default:
				eq = compare(a, b) == 0
			}
			return BoolV(eq != not)
		}
	case *InList:
		xe := cp.expr(env, x.X)
		list := make([]Ev, len(x.List))
		for i, a := range x.List {
			list[i] = cp.expr(env, a)
		}
		not := x.Not
		return func(c *Ctx) Value {
			if len(list) == 0 {
				return BoolV(not)
			}
			v := xe(c)
			if v.K == KNull {
				return Null
			}
			sawNull := false
			for _, ev := range list {
				w := ev(c)
				if w.K == KNull {
					sawNull = true
				} else if compare(v, w) == 0 {
					return BoolV(!not)
				}
			}
			if sawNull {
				return Null
			}
			return BoolV(not)
		}
	case *InSub:
		return cp.inSub(env, x)
	case *Between:
		xe, lo, hi, not := cp.expr(env, x.X), cp.expr(env, x.Lo), cp.expr(env, x.Hi), x.Not
		return func(c *Ctx) Value {
			v := xe(c)
			ge := cmpOp(">=", v, lo(c))
			le := cmpOp("<=", v, hi(c))
			r := and3(ge, le)
			if not && r.K != KNull {
				return BoolV(r.I == 0)
			}
			return r
		}
	case *Like:
		xe, pe, not := cp.expr(env, x.X), cp.expr(env, x.Pat), x.Not
		return func(c *Ctx) Value {
			v, p := xe(c), pe(c)
			if v.K == KNull || p.K == KNull {
				return Null
			}
			return BoolV(likeMatch(textOf(v), textOf(p)) != not)
		}
	case *Case:
		return cp.caseExpr(env, x)
	case *Cast:
		a := cp.expr(env, x.X)
		var conv func(Value) Value
		switch x.Type {
		case "INTEGER", "INT":
			conv = castInteger
		case "REAL", "FLOAT", "DOUBLE":
			conv = castReal
		case "TEXT":
			conv = castText
		case "NUMERIC":
			conv = func(v Value) Value { return toNumeric(v) }
		default:
			cfail("unknown type in CAST: %s", x.Type)
		}
		return func(c *Ctx) Value { return conv(a(c)) }
	case *Func:
		return cp.funcCall(env, x)
	case *SubExpr:
		plan := cp.subquery(env, x.Sub)
		if plan.ncols != 1 {
			cfail("sub-select returns %d columns - expected 1", plan.ncols)
		}
		return func(c *Ctx) Value {
			rows := plan.run(c)
			if len(rows) == 0 {
				return Null
			}
			return rows[0][0]
		}
	case *Exists:
		plan := cp.subquery(env, x.Sub)
		return func(c *Ctx) Value { return BoolV(len(plan.run(c)) > 0) }
	}
	cfail("unsupported expression")
	return nil
}

func cmpOp(op string, a, b Value) Value {
	if a.K == KNull || b.K == KNull {
		return Null
	}
	r := compare(a, b)
	switch op {
	case "=":
		return BoolV(r == 0)
	case "!=":
		return BoolV(r != 0)
	case "<":
		return BoolV(r < 0)
	case "<=":
		return BoolV(r <= 0)
	case ">":
		return BoolV(r > 0)
	}
	return BoolV(r >= 0)
}

func and3(a, b Value) Value {
	x, okx := truth(a)
	y, oky := truth(b)
	if (okx && !x) || (oky && !y) {
		return IntV(0)
	}
	if okx && oky {
		return IntV(1)
	}
	return Null
}

func (cp *compiler) binary(env *exprEnv, x *Binary) Ev {
	l, r := cp.expr(env, x.L), cp.expr(env, x.R)
	switch x.Op {
	case "+", "-", "*", "/", "%":
		op := x.Op[0]
		return func(c *Ctx) Value { return arith(op, l(c), r(c)) }
	case "||":
		return func(c *Ctx) Value { return concat(l(c), r(c)) }
	case "AND":
		return func(c *Ctx) Value {
			a, oka := truth(l(c))
			if oka && !a {
				return IntV(0)
			}
			b, okb := truth(r(c))
			if okb && !b {
				return IntV(0)
			}
			if oka && okb {
				return IntV(1)
			}
			return Null
		}
	case "OR":
		return func(c *Ctx) Value {
			a, oka := truth(l(c))
			if oka && a {
				return IntV(1)
			}
			b, okb := truth(r(c))
			if okb && b {
				return IntV(1)
			}
			if oka && okb {
				return IntV(0)
			}
			return Null
		}
	}
	op := x.Op
	return func(c *Ctx) Value { return cmpOp(op, l(c), r(c)) }
}

func (cp *compiler) caseExpr(env *exprEnv, x *Case) Ev {
	var operand Ev
	if x.Operand != nil {
		operand = cp.expr(env, x.Operand)
	}
	conds := make([]Ev, len(x.Whens))
	thens := make([]Ev, len(x.Whens))
	for i, w := range x.Whens {
		conds[i] = cp.expr(env, w.Cond)
		thens[i] = cp.expr(env, w.Then)
	}
	var els Ev
	if x.Else != nil {
		els = cp.expr(env, x.Else)
	}
	return func(c *Ctx) Value {
		if operand != nil {
			v := operand(c)
			for i, cond := range conds {
				w := cond(c)
				if v.K != KNull && w.K != KNull && compare(v, w) == 0 {
					return thens[i](c)
				}
			}
		} else {
			for i, cond := range conds {
				if isTrue(cond(c)) {
					return thens[i](c)
				}
			}
		}
		if els != nil {
			return els(c)
		}
		return Null
	}
}

func (cp *compiler) funcCall(env *exprEnv, f *Func) Ev {
	if isAggCall(f) {
		if env.aggs == nil || env.inAgg {
			cfail("misuse of aggregate function %s()", f.Name)
		}
		spec := &aggSpec{name: f.Name, distinct: f.Distinct, star: f.Star}
		switch {
		case f.Star && f.Name != "count":
			cfail("wrong number of arguments to function %s()", f.Name)
		case f.Star:
		case len(f.Args) != 1:
			cfail("wrong number of arguments to function %s()", f.Name)
		default:
			sub := *env
			sub.inAgg = true
			spec.arg = cp.expr(&sub, f.Args[0])
			env.outer = env.outer || sub.outer
		}
		k := len(*env.aggs)
		*env.aggs = append(*env.aggs, spec)
		return func(c *Ctx) Value { return c.Agg[k] }
	}
	fn, ok := scalarFuncs[f.Name]
	if !ok {
		cfail("no such function: %s", f.Name)
	}
	if f.Star || f.Distinct || len(f.Args) < fn.min || (fn.max >= 0 && len(f.Args) > fn.max) {
		cfail("wrong number of arguments to function %s()", f.Name)
	}
	args := make([]Ev, len(f.Args))
	for i, a := range f.Args {
		args[i] = cp.expr(env, a)
	}
	impl := fn.fn
	return func(c *Ctx) Value {
		vals := make([]Value, len(args))
		for i, a := range args {
			vals[i] = a(c)
		}
		return impl(vals)
	}
}

func (cp *compiler) subquery(env *exprEnv, sel *Select) *SelectPlan {
	plan := cp.compileSelect(sel, env.scope)
	if plan.correlated {
		env.outer = true
	}
	return plan
}

func (cp *compiler) inSub(env *exprEnv, x *InSub) Ev {
	xe := cp.expr(env, x.X)
	plan := cp.subquery(env, x.Sub)
	if plan.ncols != 1 {
		cfail("sub-select returns %d columns - expected 1", plan.ncols)
	}
	not := x.Not
	type set struct {
		keys    map[string]bool
		hasNull bool
		empty   bool
	}
	var cached *set
	build := func(c *Ctx) *set {
		if cached != nil {
			return cached
		}
		rows := plan.run(c)
		s := &set{keys: make(map[string]bool, len(rows)), empty: len(rows) == 0}
		var kb []byte
		for _, r := range rows {
			if r[0].K == KNull {
				s.hasNull = true
				continue
			}
			kb = appendKey(kb[:0], r[0])
			s.keys[string(kb)] = true
		}
		if !plan.correlated {
			cached = s
		}
		return s
	}
	return func(c *Ctx) Value {
		v := xe(c)
		s := build(c)
		if s.empty {
			return BoolV(not)
		}
		if v.K == KNull {
			return Null
		}
		if s.keys[string(appendKey(nil, v))] {
			return BoolV(!not)
		}
		if s.hasNull {
			return Null
		}
		return BoolV(not)
	}
}

// ---------------------------------------------------------------- SELECT

type orderSpec struct {
	out  int // index of an output column, or -1 to use ev
	ev   Ev
	desc bool
}

type SelectPlan struct {
	cores      []*CorePlan
	ops        []string
	names      []string
	ncols      int
	order      []orderSpec // compound selects only
	limit      Ev
	offset     Ev
	correlated bool
	cached     bool
	cache      [][]Value
}

type CorePlan struct {
	from     *FromPlan
	where    Ev
	isAgg    bool
	groupBy  []Ev
	aggs     []*aggSpec
	having   Ev
	items    []Ev
	names    []string
	distinct bool
	order    []orderSpec
	width    int
}

func ordinal(n int) string {
	switch n {
	case 1:
		return "1st"
	case 2:
		return "2nd"
	case 3:
		return "3rd"
	}
	return fmt.Sprintf("%dth", n)
}

// intTerm reports whether an ORDER BY or GROUP BY term is a bare integer.
func intTerm(e Expr) (int64, bool) {
	if l, ok := e.(*Lit); ok && l.V.K == KInt {
		return l.V.I, true
	}
	return 0, false
}

func (cp *compiler) constant(e Expr) Ev {
	if e == nil {
		return nil
	}
	return cp.expr(&exprEnv{scope: &Scope{}}, e)
}

func (cp *compiler) compileSelect(sel *Select, parent *Scope) *SelectPlan {
	plan := &SelectPlan{ops: sel.Ops}
	var simpleOrder []OrderTerm
	if len(sel.Cores) == 1 {
		simpleOrder = sel.OrderBy
	}
	for i, core := range sel.Cores {
		c := cp.compileCore(core, parent, plan, simpleOrder)
		if i > 0 && len(c.items) != len(plan.cores[0].items) {
			cfail("SELECTs to the left and right of %s do not have the same number of result columns", sel.Ops[i-1])
		}
		plan.cores = append(plan.cores, c)
	}
	plan.names = plan.cores[0].names
	plan.ncols = len(plan.names)
	if len(sel.Cores) > 1 {
		for i, t := range sel.OrderBy {
			idx := -1
			if n, ok := intTerm(t.X); ok {
				if n < 1 || n > int64(plan.ncols) {
					cfail("%s ORDER BY term out of range - should be between 1 and %d", ordinal(i+1), plan.ncols)
				}
				idx = int(n - 1)
			} else if r, ok := unparen(t.X).(*ColRef); ok && r.Table == "" {
				for j, n := range plan.names {
					if strings.EqualFold(n, r.Name) {
						idx = j
						break
					}
				}
			}
			if idx < 0 {
				cfail("%s ORDER BY term does not match any column in the result set", ordinal(i+1))
			}
			plan.order = append(plan.order, orderSpec{out: idx, desc: t.Desc})
		}
	}
	plan.limit = cp.constant(sel.Limit)
	plan.offset = cp.constant(sel.Offset)
	return plan
}

func (cp *compiler) compileCore(core *Core, parent *Scope, owner *SelectPlan, orderBy []OrderTerm) *CorePlan {
	scope := &Scope{parent: parent, owner: owner, tables: map[string]bool{}}
	plan := &CorePlan{distinct: core.Distinct}
	plan.from = cp.compileFrom(core.From, scope)
	plan.width = len(scope.cols)

	aliases := map[string]Expr{}
	for _, it := range core.Items {
		if it.Alias != "" {
			key := strings.ToLower(it.Alias)
			if _, dup := aliases[key]; !dup {
				aliases[key] = it.Expr
			}
		}
	}
	newEnv := func(aggs bool, useAliases bool) *exprEnv {
		env := &exprEnv{scope: scope}
		if aggs {
			env.aggs = &plan.aggs
		}
		if useAliases {
			env.aliases = aliases
			env.aliasing = map[string]bool{}
		}
		return env
	}

	plan.isAgg = len(core.GroupBy) > 0 || core.Having != nil
	for _, it := range core.Items {
		if !it.Star && hasAgg(it.Expr) {
			plan.isAgg = true
		}
	}
	for _, t := range orderBy {
		if hasAgg(t.X) {
			plan.isAgg = true
		}
	}

	if core.Where != nil {
		plan.where = cp.expr(newEnv(false, true), core.Where)
	}
	for i, g := range core.GroupBy {
		if n, ok := intTerm(g); ok {
			if n < 1 || n > int64(len(core.Items)) || core.Items[n-1].Star {
				cfail("%s GROUP BY term out of range - should be between 1 and %d", ordinal(i+1), len(core.Items))
			}
			plan.groupBy = append(plan.groupBy, cp.expr(newEnv(false, false), core.Items[n-1].Expr))
			continue
		}
		plan.groupBy = append(plan.groupBy, cp.expr(newEnv(false, true), g))
	}

	for _, it := range core.Items {
		if it.Star {
			found := false
			for i, col := range scope.cols {
				if it.StarTable != "" && col.table != strings.ToLower(it.StarTable) {
					continue
				}
				found = true
				plan.items = append(plan.items, colEv(0, i))
				plan.names = append(plan.names, col.name)
			}
			if !found {
				if it.StarTable != "" {
					cfail("no such table: %s", it.StarTable)
				}
				if len(core.From) == 0 {
					cfail("no tables specified")
				}
			}
			continue
		}
		plan.items = append(plan.items, cp.expr(newEnv(plan.isAgg, false), it.Expr))
		switch {
		case it.Alias != "":
			plan.names = append(plan.names, it.Alias)
		default:
			if r, ok := unparen(it.Expr).(*ColRef); ok {
				plan.names = append(plan.names, colName(scope, r))
			} else {
				s, e := it.Expr.span()
				plan.names = append(plan.names, cp.src[s:e])
			}
		}
	}

	if core.Having != nil {
		plan.having = cp.expr(newEnv(true, true), core.Having)
	}

	for i, t := range orderBy {
		spec := orderSpec{out: -1, desc: t.Desc}
		if n, ok := intTerm(t.X); ok {
			if n < 1 || n > int64(len(plan.items)) {
				cfail("%s ORDER BY term out of range - should be between 1 and %d", ordinal(i+1), len(plan.items))
			}
			spec.out = int(n - 1)
		} else if r, ok := t.X.(*ColRef); ok && r.Table == "" {
			for j, it := range core.Items {
				if it.Alias != "" && strings.EqualFold(it.Alias, r.Name) {
					spec.out = itemOutputIndex(core.Items, j, plan, scope)
					break
				}
			}
		}
		if spec.out < 0 {
			spec.ev = cp.expr(newEnv(plan.isAgg, true), t.X)
		}
		plan.order = append(plan.order, spec)
	}
	return plan
}

// itemOutputIndex maps a select-list position to its output column, taking
// the columns that each * expands to into account.
func itemOutputIndex(items []SelectItem, j int, plan *CorePlan, scope *Scope) int {
	out := 0
	for k := 0; k < j; k++ {
		if !items[k].Star {
			out++
			continue
		}
		for _, col := range scope.cols {
			if items[k].StarTable == "" || col.table == strings.ToLower(items[k].StarTable) {
				out++
			}
		}
	}
	return out
}

// ---------------------------------------------------------------- FROM

type source struct {
	table  *Table
	sub    *SelectPlan
	off, n int
	join   string
	hashL  []Ev
	hashR  []Ev
	conds  []Ev
}

type FromPlan struct {
	sources []*source
	width   int
}

func conjuncts(e Expr) []Expr {
	if b, ok := unparen(e).(*Binary); ok && b.Op == "AND" {
		return append(conjuncts(b.L), conjuncts(b.R)...)
	}
	return []Expr{e}
}

func (cp *compiler) compileFrom(items []FromItem, scope *Scope) *FromPlan {
	fp := &FromPlan{}
	for _, it := range items {
		src := &source{off: len(scope.cols), join: it.Join}
		var names []string
		alias := it.Alias
		if it.Sub != nil {
			src.sub = cp.compileSelect(it.Sub, nil)
			names = src.sub.names
		} else {
			t, err := cp.db.table(it.Table)
			if err != nil {
				cfail("%v", err)
			}
			src.table = t
			names = t.Cols
			if alias == "" {
				alias = it.Table
			}
		}
		lalias := strings.ToLower(alias)
		scope.tables[lalias] = true
		for _, n := range names {
			scope.cols = append(scope.cols, ColInfo{table: lalias, name: n, lname: strings.ToLower(n)})
		}
		src.n = len(names)
		if it.On != nil {
			for _, conj := range conjuncts(it.On) {
				if b, ok := unparen(conj).(*Binary); ok && b.Op == "=" && !hasSubquery(b) {
					le := &exprEnv{scope: scope, refs: map[int]bool{}}
					re := &exprEnv{scope: scope, refs: map[int]bool{}}
					l, r := cp.expr(le, b.L), cp.expr(re, b.R)
					side := func(env *exprEnv) int {
						if env.outer || len(env.refs) == 0 {
							return 0
						}
						left, right := false, false
						for i := range env.refs {
							if i < src.off {
								left = true
							} else {
								right = true
							}
						}
						switch {
						case left && !right:
							return 1
						case right && !left:
							return 2
						}
						return 0
					}
					ls, rs := side(le), side(re)
					if ls == 1 && rs == 2 {
						src.hashL, src.hashR = append(src.hashL, l), append(src.hashR, r)
						continue
					}
					if ls == 2 && rs == 1 {
						src.hashL, src.hashR = append(src.hashL, r), append(src.hashR, l)
						continue
					}
				}
				src.conds = append(src.conds, cp.expr(&exprEnv{scope: scope}, conj))
			}
		}
		fp.sources = append(fp.sources, src)
	}
	fp.width = len(scope.cols)
	return fp
}

func (s *source) rows() [][]Value {
	if s.table != nil {
		return s.table.Rows
	}
	return s.sub.run(nil)
}

func (fp *FromPlan) run(parent *Ctx) [][]Value {
	if len(fp.sources) == 0 {
		return [][]Value{{}}
	}
	first := fp.sources[0].rows()
	if len(fp.sources) == 1 {
		return first
	}
	rows := make([][]Value, len(first))
	for i, r := range first {
		row := make([]Value, fp.width)
		copy(row, r)
		rows[i] = row
	}
	for _, s := range fp.sources[1:] {
		rows = s.joinWith(rows, parent, fp.width)
	}
	return rows
}

func (s *source) joinWith(left [][]Value, parent *Ctx, width int) [][]Value {
	right := s.rows()
	ctx := &Ctx{Parent: parent}
	tmp := make([]Value, width)
	var out [][]Value
	ok := func(row []Value) bool {
		ctx.Row = row
		for _, c := range s.conds {
			if !isTrue(c(ctx)) {
				return false
			}
		}
		return true
	}
	emit := func(l, r []Value) {
		row := make([]Value, width)
		copy(row, l)
		copy(row[s.off:s.off+s.n], r)
		out = append(out, row)
	}
	pad := make([]Value, s.n)
	if len(s.hashL) > 0 {
		index := map[string][]int{}
		var kb []byte
		for j, r := range right {
			copy(tmp[s.off:], r)
			ctx.Row = tmp
			kb = kb[:0]
			null := false
			for _, ev := range s.hashR {
				v := ev(ctx)
				if v.K == KNull {
					null = true
					break
				}
				kb = appendKey(kb, v)
			}
			if !null {
				index[string(kb)] = append(index[string(kb)], j)
			}
		}
		for _, l := range left {
			ctx.Row = l
			kb = kb[:0]
			null := false
			for _, ev := range s.hashL {
				v := ev(ctx)
				if v.K == KNull {
					null = true
					break
				}
				kb = appendKey(kb, v)
			}
			matched := false
			if !null {
				for _, j := range index[string(kb)] {
					if len(s.conds) > 0 {
						copy(tmp, l)
						copy(tmp[s.off:s.off+s.n], right[j])
						if !ok(tmp) {
							continue
						}
					}
					emit(l, right[j])
					matched = true
				}
			}
			if !matched && s.join == "LEFT" {
				emit(l, pad)
			}
		}
		return out
	}
	for _, l := range left {
		matched := false
		copy(tmp, l)
		for _, r := range right {
			copy(tmp[s.off:s.off+s.n], r)
			if !ok(tmp) {
				continue
			}
			emit(l, r)
			matched = true
		}
		if !matched && s.join == "LEFT" {
			emit(l, pad)
		}
	}
	return out
}

// ---------------------------------------------------------------- execution

func (p *SelectPlan) run(parent *Ctx) [][]Value {
	if p.cached {
		return p.cache
	}
	var rows [][]Value
	if len(p.cores) == 1 {
		rows = p.cores[0].run(parent, true)
	} else {
		rows = p.cores[0].run(parent, false)
		for i, op := range p.ops {
			rows = setOp(op, rows, p.cores[i+1].run(parent, false))
		}
		if len(p.order) > 0 {
			sort.SliceStable(rows, func(a, b int) bool {
				for _, o := range p.order {
					c := compare(rows[a][o.out], rows[b][o.out])
					if c != 0 {
						return (c < 0) != o.desc
					}
				}
				return false
			})
		}
	}
	if p.offset != nil {
		if n := realToInt(p.offset(&Ctx{}).float()); n > 0 {
			if n >= int64(len(rows)) {
				rows = nil
			} else {
				rows = rows[n:]
			}
		}
	}
	if p.limit != nil {
		if n := realToInt(p.limit(&Ctx{}).float()); n >= 0 && n < int64(len(rows)) {
			rows = rows[:n]
		}
	}
	if !p.correlated {
		p.cached, p.cache = true, rows
	}
	return rows
}

func distinctRows(rows [][]Value) [][]Value {
	seen := map[string]bool{}
	var out [][]Value
	var kb []byte
	for _, r := range rows {
		kb = rowKey(kb[:0], r)
		if !seen[string(kb)] {
			seen[string(kb)] = true
			out = append(out, r)
		}
	}
	return out
}

func setOp(op string, left, right [][]Value) [][]Value {
	switch op {
	case "UNION ALL":
		return append(append([][]Value{}, left...), right...)
	case "UNION":
		return distinctRows(append(append([][]Value{}, left...), right...))
	}
	inRight := map[string]bool{}
	var kb []byte
	for _, r := range right {
		kb = rowKey(kb[:0], r)
		inRight[string(kb)] = true
	}
	var out [][]Value
	for _, r := range distinctRows(left) {
		kb = rowKey(kb[:0], r)
		if inRight[string(kb)] == (op == "INTERSECT") {
			out = append(out, r)
		}
	}
	return out
}

type group struct {
	states []aggState
	rep    []Value
}

func (c *CorePlan) run(parent *Ctx, withOrder bool) [][]Value {
	input := c.from.run(parent)
	ctx := &Ctx{Parent: parent}
	var outs, keys [][]Value
	order := c.order
	if !withOrder {
		order = nil
	}
	emit := func() {
		out := make([]Value, len(c.items))
		for i, ev := range c.items {
			out[i] = ev(ctx)
		}
		if len(order) > 0 {
			key := make([]Value, len(order))
			for j, o := range order {
				if o.out >= 0 {
					key[j] = out[o.out]
				} else {
					key[j] = o.ev(ctx)
				}
			}
			keys = append(keys, key)
		}
		outs = append(outs, out)
	}
	if !c.isAgg {
		for _, row := range input {
			ctx.Row = row
			if c.where != nil && !isTrue(c.where(ctx)) {
				continue
			}
			emit()
		}
	} else {
		groups := map[string]*group{}
		var list []*group
		var kb []byte
		newGroup := func() *group {
			g := &group{states: make([]aggState, len(c.aggs))}
			for i, spec := range c.aggs {
				g.states[i] = spec.newState()
			}
			return g
		}
		for _, row := range input {
			ctx.Row = row
			if c.where != nil && !isTrue(c.where(ctx)) {
				continue
			}
			kb = kb[:0]
			for _, g := range c.groupBy {
				kb = appendKey(kb, g(ctx))
			}
			g := groups[string(kb)]
			if g == nil {
				g = newGroup()
				groups[string(kb)] = g
				list = append(list, g)
			}
			for i, spec := range c.aggs {
				if spec.star {
					g.states[i].step(IntV(1))
				} else {
					g.states[i].step(spec.arg(ctx))
				}
			}
			g.rep = row
		}
		if len(c.groupBy) == 0 && len(list) == 0 {
			list = append(list, newGroup())
		}
		empty := make([]Value, c.width)
		for _, g := range list {
			ctx.Row = g.rep
			if ctx.Row == nil {
				ctx.Row = empty
			}
			ctx.Agg = make([]Value, len(g.states))
			for i, s := range g.states {
				ctx.Agg[i] = s.final()
			}
			if c.having != nil && !isTrue(c.having(ctx)) {
				continue
			}
			emit()
		}
	}
	if c.distinct {
		seen := map[string]bool{}
		var kb []byte
		var o2, k2 [][]Value
		for i, r := range outs {
			kb = rowKey(kb[:0], r)
			if seen[string(kb)] {
				continue
			}
			seen[string(kb)] = true
			o2 = append(o2, r)
			if len(keys) > 0 {
				k2 = append(k2, keys[i])
			}
		}
		outs, keys = o2, k2
	}
	if len(order) > 0 {
		idx := make([]int, len(outs))
		for i := range idx {
			idx[i] = i
		}
		sort.SliceStable(idx, func(a, b int) bool {
			ka, kb := keys[idx[a]], keys[idx[b]]
			for j, o := range order {
				c := compare(ka[j], kb[j])
				if c != 0 {
					return (c < 0) != o.desc
				}
			}
			return false
		})
		sorted := make([][]Value, len(outs))
		for i, j := range idx {
			sorted[i] = outs[j]
		}
		outs = sorted
	}
	return outs
}
