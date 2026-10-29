package main

import (
	"encoding/binary"
	"math"
	"strconv"
	"strings"
)

// Kind is the storage class of a value.
type Kind uint8

const (
	KNull Kind = iota
	KInt
	KReal
	KText
)

// Value is one SQL value. Only the field that matches K is meaningful.
type Value struct {
	K Kind
	I int64
	F float64
	S string
}

var Null = Value{}

func IntV(i int64) Value    { return Value{K: KInt, I: i} }
func RealV(f float64) Value { return Value{K: KReal, F: f} }
func TextV(s string) Value  { return Value{K: KText, S: s} }

func BoolV(b bool) Value {
	if b {
		return IntV(1)
	}
	return IntV(0)
}

func (v Value) float() float64 {
	switch v.K {
	case KInt:
		return float64(v.I)
	case KReal:
		return v.F
	case KText:
		return numericPrefix(v.S).float()
	}
	return 0
}

// class orders the storage classes: NULL < numbers < text.
func class(v Value) int {
	switch v.K {
	case KNull:
		return 0
	case KText:
		return 2
	}
	return 1
}

func intRealCmp(i int64, r float64) int {
	if math.IsNaN(r) {
		return 1
	}
	if r < -9223372036854775808.0 {
		return 1
	}
	if r >= 9223372036854775808.0 {
		return -1
	}
	y := int64(r)
	if i < y {
		return -1
	}
	if i > y {
		return 1
	}
	fr := r - float64(y)
	if fr > 0 {
		return -1
	}
	if fr < 0 {
		return 1
	}
	return 0
}

// compare is the total order used by ORDER BY, DISTINCT, GROUP BY, min and max.
func compare(a, b Value) int {
	ca, cb := class(a), class(b)
	if ca != cb {
		if ca < cb {
			return -1
		}
		return 1
	}
	switch ca {
	case 0:
		return 0
	case 2:
		return strings.Compare(a.S, b.S)
	}
	switch {
	case a.K == KInt && b.K == KInt:
		switch {
		case a.I < b.I:
			return -1
		case a.I > b.I:
			return 1
		}
		return 0
	case a.K == KInt:
		return intRealCmp(a.I, b.F)
	case b.K == KInt:
		return -intRealCmp(b.I, a.F)
	}
	switch {
	case a.F < b.F:
		return -1
	case a.F > b.F:
		return 1
	}
	return 0
}

// appendKey appends an encoding of v such that two values get the same
// encoding exactly when compare reports them equal.
func appendKey(buf []byte, v Value) []byte {
	switch v.K {
	case KNull:
		return append(buf, 'n')
	case KInt:
		buf = append(buf, 'i')
		return binary.LittleEndian.AppendUint64(buf, uint64(v.I))
	case KReal:
		if v.F == math.Trunc(v.F) && v.F >= -9223372036854775808.0 && v.F < 9223372036854775808.0 {
			buf = append(buf, 'i')
			return binary.LittleEndian.AppendUint64(buf, uint64(int64(v.F)))
		}
		buf = append(buf, 'r')
		return binary.LittleEndian.AppendUint64(buf, math.Float64bits(v.F))
	}
	buf = append(buf, 't')
	buf = binary.AppendUvarint(buf, uint64(len(v.S)))
	return append(buf, v.S...)
}

func rowKey(buf []byte, row []Value) []byte {
	for _, v := range row {
		buf = appendKey(buf, v)
	}
	return buf
}

// ---------------------------------------------------------------- conversions

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func skipSpace(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' || s[i] == '\f' || s[i] == '\v') {
		i++
	}
	return s[i:]
}

// realPrefix returns the longest prefix of s that reads as a decimal number,
// and whether it has a fraction or exponent.
func realPrefix(s string) (string, bool) {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	start := i
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	digits := i - start
	isReal := false
	if i < len(s) && s[i] == '.' {
		j := i + 1
		for j < len(s) && isDigit(s[j]) {
			j++
		}
		if digits > 0 || j > i+1 {
			digits += j - i - 1
			i = j
			isReal = true
		}
	}
	if digits == 0 {
		return "", false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		if j < len(s) && isDigit(s[j]) {
			for j < len(s) && isDigit(s[j]) {
				j++
			}
			i = j
			isReal = true
		}
	}
	return s[:i], isReal
}

// numericPrefix converts text the way arithmetic does: the longest numeric
// prefix, as an integer if it has no fraction or exponent and fits.
func numericPrefix(s string) Value {
	p, isReal := realPrefix(skipSpace(s))
	if p == "" {
		return IntV(0)
	}
	if !isReal {
		if i, err := strconv.ParseInt(p, 10, 64); err == nil {
			return IntV(i)
		}
	}
	f, _ := strconv.ParseFloat(p, 64)
	return RealV(f)
}

func toNumeric(v Value) Value {
	if v.K == KText {
		return numericPrefix(v.S)
	}
	return v
}

func realToInt(f float64) int64 {
	switch {
	case math.IsNaN(f):
		return 0
	case f <= -9223372036854775808.0:
		return math.MinInt64
	case f >= 9223372036854775807.0:
		return math.MaxInt64
	}
	return int64(f)
}

func castInteger(v Value) Value {
	switch v.K {
	case KNull:
		return Null
	case KInt:
		return v
	case KReal:
		return IntV(realToInt(v.F))
	}
	s := skipSpace(v.S)
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	n, err := strconv.ParseInt(s[:i], 10, 64)
	if err != nil {
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			if strings.HasPrefix(s, "-") {
				return IntV(math.MinInt64)
			}
			return IntV(math.MaxInt64)
		}
		return IntV(0)
	}
	return IntV(n)
}

func castReal(v Value) Value {
	switch v.K {
	case KNull:
		return Null
	case KInt:
		return RealV(float64(v.I))
	case KReal:
		return v
	}
	p, _ := realPrefix(skipSpace(v.S))
	if p == "" {
		return RealV(0)
	}
	f, _ := strconv.ParseFloat(p, 64)
	return RealV(f)
}

// textOf renders a value as text, the way || and the string functions see it.
func textOf(v Value) string {
	switch v.K {
	case KInt:
		return strconv.FormatInt(v.I, 10)
	case KReal:
		return realToText(v.F)
	case KText:
		return v.S
	}
	return ""
}

func castText(v Value) Value {
	if v.K == KNull {
		return Null
	}
	return TextV(textOf(v))
}

// splitE splits strconv's 'e' format into sign, digits without the point and
// the decimal exponent of the first digit.
func splitE(s string) (neg bool, digits string, exp int) {
	if s[0] == '-' {
		neg = true
		s = s[1:]
	}
	e := strings.IndexByte(s, 'e')
	mant := s[:e]
	exp, _ = strconv.Atoi(s[e+1:])
	digits = strings.Replace(mant, ".", "", 1)
	return
}

func layout(neg bool, digits string, exp int, expForm bool, keepPointZero bool) string {
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	if expForm {
		b.WriteByte(digits[0])
		if len(digits) > 1 {
			b.WriteByte('.')
			b.WriteString(digits[1:])
		} else if keepPointZero {
			b.WriteString(".0")
		}
		b.WriteByte('e')
		if exp < 0 {
			b.WriteByte('-')
			exp = -exp
		} else {
			b.WriteByte('+')
		}
		if exp < 10 {
			b.WriteByte('0')
		}
		b.WriteString(strconv.Itoa(exp))
		return b.String()
	}
	if exp < 0 {
		b.WriteString("0.")
		b.WriteString(strings.Repeat("0", -exp-1))
		b.WriteString(digits)
		return b.String()
	}
	if len(digits) <= exp+1 {
		b.WriteString(digits)
		b.WriteString(strings.Repeat("0", exp+1-len(digits)))
		b.WriteString(".0")
		return b.String()
	}
	b.WriteString(digits[:exp+1])
	b.WriteByte('.')
	b.WriteString(digits[exp+1:])
	return b.String()
}

// realToText is SQLite's "%!.15g": 15 significant digits, always with a
// decimal point.
func realToText(f float64) string {
	if math.IsInf(f, 1) {
		return "Inf"
	}
	if math.IsInf(f, -1) {
		return "-Inf"
	}
	if f == 0 {
		if math.Signbit(f) {
			return "-0.0"
		}
		return "0.0"
	}
	neg, digits, exp := splitE(strconv.FormatFloat(f, 'e', 14, 64))
	digits = strings.TrimRight(digits, "0")
	if digits == "" {
		digits = "0"
	}
	return layout(neg, digits, exp, exp < -4 || exp >= 15, true)
}

// formatReal is the output format: the shortest digits that read back as the
// same double, positional for 1e-4 <= |x| < 1e16, otherwise exponent form.
func formatReal(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case math.IsNaN(f):
		return "nan"
	case f == 0:
		if math.Signbit(f) {
			return "-0.0"
		}
		return "0.0"
	}
	neg, digits, exp := splitE(strconv.FormatFloat(f, 'e', -1, 64))
	return layout(neg, digits, exp, exp < -4 || exp >= 16, false)
}

func formatValue(v Value) (string, bool) {
	switch v.K {
	case KNull:
		return "", false
	case KInt:
		return strconv.FormatInt(v.I, 10), true
	case KReal:
		return formatReal(v.F), true
	}
	return v.S, true
}

// ---------------------------------------------------------------- operators

// truth reports whether v is true; ok is false for NULL.
func truth(v Value) (b bool, ok bool) {
	switch v.K {
	case KNull:
		return false, false
	case KInt:
		return v.I != 0, true
	case KReal:
		return v.F != 0, true
	}
	n := numericPrefix(v.S)
	if n.K == KInt {
		return n.I != 0, true
	}
	return n.F != 0, true
}

func isTrue(v Value) bool {
	b, ok := truth(v)
	return ok && b
}

func addInt(a, b int64) (int64, bool) {
	c := a + b
	if (c > a) == (b > 0) {
		return c, true
	}
	return 0, false
}

func subInt(a, b int64) (int64, bool) {
	c := a - b
	if (c < a) == (b > 0) {
		return c, true
	}
	return 0, false
}

func mulInt(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	c := a * b
	if c/b != a || (a == -1 && b == math.MinInt64) || (b == -1 && a == math.MinInt64) {
		return 0, false
	}
	return c, true
}

func arith(op byte, a, b Value) Value {
	if a.K == KNull || b.K == KNull {
		return Null
	}
	a, b = toNumeric(a), toNumeric(b)
	if op == '%' {
		if a.K == KInt && b.K == KInt {
			if b.I == 0 {
				return Null
			}
			if b.I == -1 {
				return IntV(0)
			}
			return IntV(a.I % b.I)
		}
		ia, ib := realToInt(a.float()), realToInt(b.float())
		if ib == 0 {
			return Null
		}
		if ib == -1 {
			ib = 1
		}
		return RealV(float64(ia % ib))
	}
	if a.K == KInt && b.K == KInt {
		switch op {
		case '+':
			if c, ok := addInt(a.I, b.I); ok {
				return IntV(c)
			}
		case '-':
			if c, ok := subInt(a.I, b.I); ok {
				return IntV(c)
			}
		case '*':
			if c, ok := mulInt(a.I, b.I); ok {
				return IntV(c)
			}
		case '/':
			if b.I == 0 {
				return Null
			}
			if !(a.I == math.MinInt64 && b.I == -1) {
				return IntV(a.I / b.I)
			}
		}
	}
	x, y := a.float(), b.float()
	var r float64
	switch op {
	case '+':
		r = x + y
	case '-':
		r = x - y
	case '*':
		r = x * y
	case '/':
		if y == 0 {
			return Null
		}
		r = x / y
	}
	if math.IsNaN(r) {
		return Null
	}
	return RealV(r)
}

func negate(v Value) Value {
	v = toNumeric(v)
	switch v.K {
	case KInt:
		if v.I == math.MinInt64 {
			return RealV(-float64(v.I))
		}
		return IntV(-v.I)
	case KReal:
		return RealV(-v.F)
	}
	return Null
}

func concat(a, b Value) Value {
	if a.K == KNull || b.K == KNull {
		return Null
	}
	return TextV(textOf(a) + textOf(b))
}

// likeMatch implements LIKE: % matches any run of characters, _ exactly one,
// and ASCII letters match regardless of case.
func likeMatch(s, p string) bool {
	sr, pr := []rune(s), []rune(p)
	return likeRunes(sr, pr)
}

func foldRune(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + 32
	}
	return r
}

func likeRunes(s, p []rune) bool {
	si, pi := 0, 0
	starP, starS := -1, 0
	for si < len(s) {
		if pi < len(p) && p[pi] == '%' {
			starP, starS = pi, si
			pi++
			continue
		}
		if pi < len(p) && (p[pi] == '_' || foldRune(p[pi]) == foldRune(s[si])) {
			si++
			pi++
			continue
		}
		if starP >= 0 {
			starS++
			si = starS
			pi = starP + 1
			continue
		}
		return false
	}
	for pi < len(p) && p[pi] == '%' {
		pi++
	}
	return pi == len(p)
}
