package main

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ---------------------------------------------------------------- aggregates

type aggSpec struct {
	name     string
	distinct bool
	star     bool
	arg      Ev
}

type aggState interface {
	step(v Value)
	final() Value
}

func (s *aggSpec) newState() aggState {
	var st aggState
	switch s.name {
	case "count":
		st = &countState{}
	case "min":
		st = &minMaxState{}
	case "max":
		st = &minMaxState{max: true}
	default:
		st = &sumState{kind: s.name}
	}
	if s.distinct {
		st = &distinctState{inner: st, seen: map[string]bool{}}
	}
	return st
}

type countState struct{ n int64 }

func (s *countState) step(v Value) {
	if v.K != KNull {
		s.n++
	}
}
func (s *countState) final() Value { return IntV(s.n) }

type minMaxState struct {
	max  bool
	have bool
	best Value
}

func (s *minMaxState) step(v Value) {
	if v.K == KNull {
		return
	}
	if !s.have {
		s.have, s.best = true, v
		return
	}
	c := compare(s.best, v)
	if (s.max && c < 0) || (!s.max && c > 0) {
		s.best = v
	}
}

func (s *minMaxState) final() Value {
	if !s.have {
		return Null
	}
	return s.best
}

// sumState follows SQLite: integer sums stay exact until a real value (or an
// overflow) arrives, then the sum continues with Kahan-Babuska-Neumaier
// compensation.
type sumState struct {
	kind   string // sum, total, avg
	cnt    int64
	approx bool
	iSum   int64
	rSum   float64
	rErr   float64
}

const twoTo52 = 4503599627370496

func (s *sumState) kbnStep(r float64) {
	t := s.rSum + r
	if math.Abs(s.rSum) > math.Abs(r) {
		s.rErr += (s.rSum - t) + r
	} else {
		s.rErr += (r - t) + s.rSum
	}
	s.rSum = t
}

func (s *sumState) kbnStepInt(i int64) {
	if i <= -twoTo52 || i >= twoTo52 {
		sm := i % 16384
		s.kbnStep(float64(i - sm))
		s.kbnStep(float64(sm))
		return
	}
	s.kbnStep(float64(i))
}

func (s *sumState) kbnInit(i int64) {
	if i <= -twoTo52 || i >= twoTo52 {
		sm := i % 16384
		s.rSum, s.rErr = float64(i-sm), float64(sm)
		return
	}
	s.rSum, s.rErr = float64(i), 0
}

func (s *sumState) step(v Value) {
	if v.K == KNull {
		return
	}
	v = toNumeric(v)
	s.cnt++
	if !s.approx {
		if v.K != KInt {
			s.kbnInit(s.iSum)
			s.approx = true
			s.kbnStep(v.F)
			return
		}
		if x, ok := addInt(s.iSum, v.I); ok {
			s.iSum = x
			return
		}
		s.kbnInit(s.iSum)
		s.approx = true
		s.kbnStepInt(v.I)
		return
	}
	if v.K == KInt {
		s.kbnStepInt(v.I)
	} else {
		s.kbnStep(v.F)
	}
}

func (s *sumState) real() float64 {
	if !s.approx {
		return float64(s.iSum)
	}
	r := s.rSum
	if !math.IsInf(s.rErr, 0) && !math.IsNaN(s.rErr) {
		r += s.rErr
	}
	return r
}

func (s *sumState) final() Value {
	switch s.kind {
	case "total":
		return RealV(s.real())
	case "avg":
		if s.cnt == 0 {
			return Null
		}
		return RealV(s.real() / float64(s.cnt))
	}
	if s.cnt == 0 {
		return Null
	}
	if s.approx {
		return RealV(s.real())
	}
	return IntV(s.iSum)
}

type distinctState struct {
	inner aggState
	seen  map[string]bool
}

func (s *distinctState) step(v Value) {
	if v.K == KNull {
		return
	}
	k := string(appendKey(nil, v))
	if s.seen[k] {
		return
	}
	s.seen[k] = true
	s.inner.step(v)
}

func (s *distinctState) final() Value { return s.inner.final() }

// ---------------------------------------------------------------- scalar functions

type scalarFunc struct {
	min, max int // max < 0: no upper limit
	fn       func(args []Value) Value
}

var scalarFuncs map[string]scalarFunc

func anyNull(args []Value) bool {
	for _, a := range args {
		if a.K == KNull {
			return true
		}
	}
	return false
}

func asciiMap(s string, upper bool) string {
	b := []byte(s)
	for i, c := range b {
		if upper && c >= 'a' && c <= 'z' {
			b[i] = c - 32
		} else if !upper && c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func intArg(v Value) int64 {
	v = toNumeric(v)
	if v.K == KReal {
		return realToInt(v.F)
	}
	return v.I
}

func substr(args []Value) Value {
	if anyNull(args) {
		return Null
	}
	runes := []rune(textOf(args[0]))
	l := int64(len(runes))
	p1 := intArg(args[1])
	var p2 int64 = math.MaxInt32
	negP2 := false
	if len(args) == 3 {
		p2 = intArg(args[2])
		if p2 < 0 {
			negP2, p2 = true, -p2
		}
	}
	if p1 < 0 {
		p1 += l
		if p1 < 0 {
			p2 += p1
			if p2 < 0 {
				p2 = 0
			}
			p1 = 0
		}
	} else if p1 > 0 {
		p1--
	} else if p2 > 0 {
		p2--
	}
	if negP2 {
		p1 -= p2
		if p1 < 0 {
			p2 += p1
			p1 = 0
		}
	}
	if p1 >= l {
		return TextV("")
	}
	end := p1 + p2
	if end > l {
		end = l
	}
	return TextV(string(runes[p1:end]))
}

func trimFunc(left, right bool) func([]Value) Value {
	return func(args []Value) Value {
		if anyNull(args) {
			return Null
		}
		s := textOf(args[0])
		set := " "
		if len(args) == 2 {
			set = textOf(args[1])
		}
		if set == "" {
			return TextV(s)
		}
		in := func(r rune) bool { return strings.ContainsRune(set, r) }
		if left {
			s = strings.TrimLeftFunc(s, in)
		}
		if right {
			s = strings.TrimRightFunc(s, in)
		}
		return TextV(s)
	}
}

// roundTo rounds the way SQLite's round() does: half away from zero on the
// decimal digits of the value.
func roundTo(r float64, n int64) float64 {
	if r < -twoTo52 || r > twoTo52 {
		return r
	}
	if n == 0 {
		if r < 0 {
			return float64(int64(r - 0.5))
		}
		return float64(int64(r + 0.5))
	}
	neg := math.Signbit(r)
	_, digits, exp := splitE(strconv.FormatFloat(math.Abs(r), 'e', 25, 64))
	keep := exp + 1 + int(n)
	var out []byte
	switch {
	case keep < 0:
		out = []byte("0")
		exp = 0
	case keep >= len(digits):
		out = []byte(digits)
	default:
		out = []byte(digits[:keep])
		if digits[keep] >= '5' {
			i := len(out) - 1
			for ; i >= 0; i-- {
				if out[i] == '9' {
					out[i] = '0'
					continue
				}
				out[i]++
				break
			}
			if i < 0 {
				out = append([]byte("1"), out...)
				exp++
			}
		}
		if len(out) == 0 {
			out = []byte("0")
		}
	}
	s := "0." + string(out) + "e" + strconv.Itoa(exp+1)
	f, _ := strconv.ParseFloat(s, 64)
	if neg {
		f = -f
	}
	return f
}

func init() {
	scalarFuncs = map[string]scalarFunc{
		"abs": {1, 1, func(a []Value) Value {
			v := a[0]
			switch v.K {
			case KNull:
				return Null
			case KInt:
				if v.I < 0 {
					if v.I == math.MinInt64 {
						return RealV(-float64(v.I))
					}
					return IntV(-v.I)
				}
				return v
			}
			return RealV(math.Abs(castReal(v).F))
		}},
		"length": {1, 1, func(a []Value) Value {
			if a[0].K == KNull {
				return Null
			}
			return IntV(int64(utf8.RuneCountInString(textOf(a[0]))))
		}},
		"lower": {1, 1, func(a []Value) Value {
			if a[0].K == KNull {
				return Null
			}
			return TextV(asciiMap(textOf(a[0]), false))
		}},
		"upper": {1, 1, func(a []Value) Value {
			if a[0].K == KNull {
				return Null
			}
			return TextV(asciiMap(textOf(a[0]), true))
		}},
		"substr":    {2, 3, substr},
		"substring": {2, 3, substr},
		"trim":      {1, 2, trimFunc(true, true)},
		"ltrim":     {1, 2, trimFunc(true, false)},
		"rtrim":     {1, 2, trimFunc(false, true)},
		"replace": {3, 3, func(a []Value) Value {
			if anyNull(a) {
				return Null
			}
			s, old := textOf(a[0]), textOf(a[1])
			if old == "" {
				return TextV(s)
			}
			return TextV(strings.ReplaceAll(s, old, textOf(a[2])))
		}},
		"instr": {2, 2, func(a []Value) Value {
			if anyNull(a) {
				return Null
			}
			s, sub := textOf(a[0]), textOf(a[1])
			i := strings.Index(s, sub)
			if i < 0 {
				return IntV(0)
			}
			return IntV(int64(utf8.RuneCountInString(s[:i])) + 1)
		}},
		"round": {1, 2, func(a []Value) Value {
			if anyNull(a) {
				return Null
			}
			var n int64
			if len(a) == 2 {
				n = intArg(a[1])
				if n < 0 {
					n = 0
				}
				if n > 30 {
					n = 30
				}
			}
			return RealV(roundTo(castReal(a[0]).F, n))
		}},
		"coalesce": {2, -1, func(a []Value) Value {
			for _, v := range a {
				if v.K != KNull {
					return v
				}
			}
			return Null
		}},
		"ifnull": {2, 2, func(a []Value) Value {
			if a[0].K != KNull {
				return a[0]
			}
			return a[1]
		}},
		"nullif": {2, 2, func(a []Value) Value {
			if compare(a[0], a[1]) == 0 {
				return Null
			}
			return a[0]
		}},
		"typeof": {1, 1, func(a []Value) Value {
			return TextV([]string{"null", "integer", "real", "text"}[a[0].K])
		}},
		"max": {2, -1, func(a []Value) Value {
			if anyNull(a) {
				return Null
			}
			best := 0
			for i := 1; i < len(a); i++ {
				if compare(a[best], a[i]) < 0 {
					best = i
				}
			}
			return a[best]
		}},
		"min": {2, -1, func(a []Value) Value {
			if anyNull(a) {
				return Null
			}
			best := 0
			for i := 1; i < len(a); i++ {
				if compare(a[best], a[i]) >= 0 {
					best = i
				}
			}
			return a[best]
		}},
	}
}
