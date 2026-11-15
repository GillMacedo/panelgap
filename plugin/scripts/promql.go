package main

import (
	"fmt"
	"regexp"
	"strings"
)

// varMark stands in for a Grafana variable inside a query. It is a rune that
// can't appear in PromQL, and the lexer treats it as part of an identifier,
// so node_${kind}_bytes stays one token and becomes a name pattern.
const varMark = '§'

// grafanaVar matches $name, ${name}, ${name:format} and [[name]].
var grafanaVar = regexp.MustCompile(`\$\{[^}]*\}|\$[A-Za-z0-9_]+|\[\[[^\]]*\]\]`)

// replaceVars swaps every Grafana variable in q for varMark.
func replaceVars(q string) string {
	return grafanaVar.ReplaceAllString(q, string(varMark))
}

type matcher struct {
	label, op, value string
	hasVar           bool
}

// selector is one vector selector in a query, such as
// node_cpu_seconds_total{mode="idle"}.
type selector struct {
	name     string         // the name as written, with varMark for variables; "" if none
	pattern  *regexp.Regexp // set when the name has a variable or comes from __name__=~
	matchers []matcher
	inAbsent bool // inside absent() or absent_over_time(), where a missing metric is the point
}

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tNumber
	tString
	tOp
	tLParen
	tRParen
	tLBrace
	tRBrace
	tLBracket
	tRBracket
	tComma
	tAt
)

type token struct {
	kind tokKind
	text string // for strings, the unquoted value
	pos  int
}

func isIdentStart(r rune) bool {
	return r == '_' || r == ':' || r == varMark || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isIdentChar(r rune) bool { return isIdentStart(r) || (r >= '0' && r <= '9') }

func lex(q string) ([]token, error) {
	rs := []rune(q)
	var toks []token
	for i := 0; i < len(rs); {
		r := rs[i]
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			i++
		case r == '#':
			for i < len(rs) && rs[i] != '\n' {
				i++
			}
		case r >= '0' && r <= '9' || (r == '.' && i+1 < len(rs) && rs[i+1] >= '0' && rs[i+1] <= '9'):
			// Numbers, hex, exponents and durations such as 1h30m are one token.
			start := i
			for i < len(rs) && (isIdentChar(rs[i]) || rs[i] == '.') {
				if (rs[i] == 'e' || rs[i] == 'E') && i+1 < len(rs) && (rs[i+1] == '-' || rs[i+1] == '+') {
					i++
				}
				i++
			}
			toks = append(toks, token{tNumber, string(rs[start:i]), start})
		case isIdentStart(r):
			start := i
			for i < len(rs) && isIdentChar(rs[i]) {
				i++
			}
			toks = append(toks, token{tIdent, string(rs[start:i]), start})
		case r == '"' || r == '\'' || r == '`':
			start := i
			i++
			var b strings.Builder
			closed := false
			for i < len(rs) {
				c := rs[i]
				if c == r {
					closed = true
					i++
					break
				}
				if c == '\\' && r != '`' && i+1 < len(rs) {
					b.WriteRune(rs[i+1])
					i += 2
					continue
				}
				if c == '\n' && r != '`' {
					break
				}
				b.WriteRune(c)
				i++
			}
			if !closed {
				return nil, fmt.Errorf("string starting at character %d is never closed", start+1)
			}
			toks = append(toks, token{tString, b.String(), start})
		default:
			start := i
			two := ""
			if i+1 < len(rs) {
				two = string(rs[i : i+2])
			}
			switch {
			case two == "=~" || two == "!~" || two == "!=" || two == "==" || two == "<=" || two == ">=":
				toks = append(toks, token{tOp, two, start})
				i += 2
			case strings.ContainsRune("=<>+-*/%^", r):
				toks = append(toks, token{tOp, string(r), start})
				i++
			case r == '(':
				toks = append(toks, token{tLParen, "(", start})
				i++
			case r == ')':
				toks = append(toks, token{tRParen, ")", start})
				i++
			case r == '{':
				toks = append(toks, token{tLBrace, "{", start})
				i++
			case r == '}':
				toks = append(toks, token{tRBrace, "}", start})
				i++
			case r == '[':
				toks = append(toks, token{tLBracket, "[", start})
				i++
			case r == ']':
				toks = append(toks, token{tRBracket, "]", start})
				i++
			case r == ',':
				toks = append(toks, token{tComma, ",", start})
				i++
			case r == '@':
				toks = append(toks, token{tAt, "@", start})
				i++
			default:
				return nil, fmt.Errorf("unexpected %q at character %d", string(r), start+1)
			}
		}
	}
	return append(toks, token{kind: tEOF, pos: len(rs)}), nil
}

// keywords are PromQL words that are never metric names. They are
// case-insensitive in PromQL.
var keywords = map[string]bool{
	"and": true, "or": true, "unless": true, "atan2": true, "bool": true, "offset": true,
	"by": true, "without": true, "on": true, "ignoring": true, "group_left": true, "group_right": true,
	"inf": true, "nan": true,
}

var aggregations = map[string]bool{
	"sum": true, "min": true, "max": true, "avg": true, "group": true, "stddev": true, "stdvar": true,
	"count": true, "count_values": true, "bottomk": true, "topk": true, "quantile": true,
	"limitk": true, "limit_ratio": true,
}

// labelListWords take a list of label names in parentheses.
var labelListWords = map[string]bool{
	"by": true, "without": true, "on": true, "ignoring": true, "group_left": true, "group_right": true,
}

// Nodes of a parsed query, reduced to what decides whether it returns data.
type qnode interface{}

type (
	selNode    struct{ idx int }      // a vector selector, by index into the selectors
	allNode    struct{ kids []qnode } // arithmetic, comparisons, and, function args: every part is needed
	orNode     struct{ kids []qnode } // any branch is enough
	unlessNode struct {
		left    qnode
		filters []qnode // the right side of unless only removes series
	}
	alwaysNode struct{} // absent(): returns data whether or not its argument does
)

type qparser struct {
	toks   []token
	i      int
	sels   []selector
	absent int // depth inside absent()
}

// parseQuery parses a PromQL query whose Grafana variables were already
// replaced by varMark. It returns the selectors and a tree that says which of
// them the result depends on.
func parseQuery(q string) ([]selector, qnode, error) {
	toks, err := lex(q)
	if err != nil {
		return nil, nil, err
	}
	p := &qparser{toks: toks}
	if toks[0].kind == tEOF {
		return nil, nil, fmt.Errorf("empty query")
	}
	n, err := p.expr()
	if err != nil {
		return nil, nil, err
	}
	if t := p.peek(); t.kind != tEOF {
		return nil, nil, fmt.Errorf("unexpected %q at character %d", t.text, t.pos+1)
	}
	return p.sels, n, nil
}

// parseSelectors returns only the selectors of a query.
func parseSelectors(q string) ([]selector, error) {
	sels, _, err := parseQuery(q)
	return sels, err
}

func (p *qparser) peek() token { return p.toks[p.i] }
func (p *qparser) next() token {
	t := p.toks[p.i]
	if t.kind != tEOF {
		p.i++
	}
	return t
}

func (p *qparser) isWord(t token, words ...string) bool {
	if t.kind != tIdent {
		return false
	}
	for _, w := range words {
		if strings.EqualFold(t.text, w) {
			return true
		}
	}
	return false
}

func (p *qparser) expr() (qnode, error) {
	first, err := p.andExpr()
	if err != nil {
		return nil, err
	}
	kids := []qnode{first}
	for p.isWord(p.peek(), "or") {
		p.next()
		if err := p.modifiers(); err != nil {
			return nil, err
		}
		n, err := p.andExpr()
		if err != nil {
			return nil, err
		}
		kids = append(kids, n)
	}
	if len(kids) == 1 {
		return first, nil
	}
	return orNode{kids}, nil
}

func (p *qparser) andExpr() (qnode, error) {
	left, err := p.arith()
	if err != nil {
		return nil, err
	}
	for p.isWord(p.peek(), "and", "unless") {
		op := strings.ToLower(p.next().text)
		if err := p.modifiers(); err != nil {
			return nil, err
		}
		right, err := p.arith()
		if err != nil {
			return nil, err
		}
		if op == "and" {
			left = allNode{[]qnode{left, right}}
		} else if u, ok := left.(unlessNode); ok {
			u.filters = append(u.filters, right)
			left = u
		} else {
			left = unlessNode{left: left, filters: []qnode{right}}
		}
	}
	return left, nil
}

func isArithOp(t token) bool {
	if t.kind == tOp {
		switch t.text {
		case "+", "-", "*", "/", "%", "^", "==", "!=", "<", ">", "<=", ">=":
			return true
		}
	}
	return t.kind == tIdent && strings.EqualFold(t.text, "atan2")
}

// arith reads operands joined by arithmetic and comparison operators. The
// result needs every operand, so their precedence doesn't matter here.
func (p *qparser) arith() (qnode, error) {
	first, err := p.unary()
	if err != nil {
		return nil, err
	}
	kids := []qnode{first}
	for isArithOp(p.peek()) {
		p.next()
		if p.isWord(p.peek(), "bool") {
			p.next()
		}
		if err := p.modifiers(); err != nil {
			return nil, err
		}
		n, err := p.unary()
		if err != nil {
			return nil, err
		}
		kids = append(kids, n)
	}
	if len(kids) == 1 {
		return first, nil
	}
	return allNode{kids}, nil
}

// modifiers skips on(...), ignoring(...), group_left(...) and group_right(...).
func (p *qparser) modifiers() error {
	for p.isWord(p.peek(), "on", "ignoring", "group_left", "group_right") {
		w := strings.ToLower(p.next().text)
		if p.peek().kind == tLParen {
			if err := p.labelList(); err != nil {
				return err
			}
		} else if w == "on" || w == "ignoring" {
			return fmt.Errorf("%s needs a label list at character %d", w, p.peek().pos+1)
		}
	}
	return nil
}

func (p *qparser) labelList() error {
	open := p.next()
	for {
		t := p.next()
		switch t.kind {
		case tRParen:
			return nil
		case tIdent, tComma, tString:
		default:
			return fmt.Errorf("label list at character %d isn't closed", open.pos+1)
		}
	}
}

func (p *qparser) unary() (qnode, error) {
	for t := p.peek(); t.kind == tOp && (t.text == "-" || t.text == "+"); t = p.peek() {
		p.next()
	}
	n, err := p.primary()
	if err != nil {
		return nil, err
	}
	// Range, subquery, offset and @ change when data is read, not whether
	// the series exist.
	for {
		t := p.peek()
		switch {
		case t.kind == tLBracket:
			p.next()
			for p.peek().kind != tRBracket {
				if k := p.peek().kind; k == tEOF || k == tLBracket {
					return nil, fmt.Errorf("bracket at character %d isn't closed", t.pos+1)
				}
				p.next()
			}
			p.next()
		case p.isWord(t, "offset"):
			p.next()
			if s := p.peek(); s.kind == tOp && (s.text == "-" || s.text == "+") {
				p.next()
			}
			if d := p.next(); d.kind != tNumber && !(d.kind == tIdent && d.text == string(varMark)) {
				return nil, fmt.Errorf("offset needs a duration at character %d", d.pos+1)
			}
		case t.kind == tAt:
			p.next()
			if s := p.peek(); s.kind == tOp && (s.text == "-" || s.text == "+") {
				p.next()
			}
			a := p.next()
			if a.kind == tIdent && p.peek().kind == tLParen {
				p.next()
				if r := p.next(); r.kind != tRParen {
					return nil, fmt.Errorf("@ %s( isn't closed at character %d", a.text, r.pos+1)
				}
			} else if a.kind != tNumber && !(a.kind == tIdent && a.text == string(varMark)) {
				return nil, fmt.Errorf("@ needs a time at character %d", a.pos+1)
			}
		default:
			return n, nil
		}
	}
}

func (p *qparser) primary() (qnode, error) {
	t := p.next()
	switch t.kind {
	case tNumber, tString:
		return allNode{}, nil
	case tLParen:
		n, err := p.expr()
		if err != nil {
			return nil, err
		}
		if c := p.next(); c.kind != tRParen {
			return nil, fmt.Errorf("parenthesis at character %d isn't closed", t.pos+1)
		}
		return n, nil
	case tLBrace:
		p.i--
		return p.selector("")
	case tIdent:
		lower := strings.ToLower(t.text)
		if lower == "inf" || lower == "nan" {
			return allNode{}, nil
		}
		next := p.peek()
		if aggregations[t.text] && (next.kind == tLParen || p.isWord(next, "by", "without")) {
			return p.aggregation()
		}
		if next.kind == tLParen {
			return p.call(t.text)
		}
		if keywords[lower] {
			return nil, fmt.Errorf("unexpected %q at character %d", t.text, t.pos+1)
		}
		// A variable on its own, not followed by matchers, is usually a
		// number such as $__range_s. It can't be checked either way.
		if t.text == string(varMark) && next.kind != tLBrace {
			return allNode{}, nil
		}
		return p.selector(t.text)
	}
	if t.kind == tEOF {
		return nil, fmt.Errorf("the query ends where a value is expected")
	}
	return nil, fmt.Errorf("unexpected %q at character %d", t.text, t.pos+1)
}

func (p *qparser) aggregation() (qnode, error) {
	if p.isWord(p.peek(), "by", "without") {
		p.next()
		if p.peek().kind != tLParen {
			return nil, fmt.Errorf("by and without need a label list at character %d", p.peek().pos+1)
		}
		if err := p.labelList(); err != nil {
			return nil, err
		}
	}
	n, err := p.args("")
	if err != nil {
		return nil, err
	}
	if p.isWord(p.peek(), "by", "without") {
		p.next()
		if p.peek().kind != tLParen {
			return nil, fmt.Errorf("by and without need a label list at character %d", p.peek().pos+1)
		}
		if err := p.labelList(); err != nil {
			return nil, err
		}
	}
	return n, nil
}

func (p *qparser) call(fn string) (qnode, error) {
	if fn == "absent" || fn == "absent_over_time" {
		p.absent++
		_, err := p.args(fn)
		p.absent--
		if err != nil {
			return nil, err
		}
		return alwaysNode{}, nil
	}
	return p.args(fn)
}

// args reads (a, b, ...) for a function or aggregation.
func (p *qparser) args(fn string) (qnode, error) {
	open := p.next()
	if open.kind != tLParen {
		return nil, fmt.Errorf("expected ( at character %d", open.pos+1)
	}
	var kids []qnode
	if p.peek().kind == tRParen {
		p.next()
		return allNode{}, nil
	}
	for {
		n, err := p.expr()
		if err != nil {
			return nil, err
		}
		kids = append(kids, n)
		t := p.next()
		if t.kind == tRParen {
			return allNode{kids}, nil
		}
		if t.kind != tComma {
			return nil, fmt.Errorf("%s( at character %d isn't closed", fn, open.pos+1)
		}
	}
}

func (p *qparser) selector(name string) (qnode, error) {
	s := selector{name: name, inAbsent: p.absent > 0}
	if p.peek().kind == tLBrace {
		ms, j, err := parseMatchers(p.toks, p.i, &s)
		if err != nil {
			return nil, err
		}
		s.matchers = ms
		p.i = j + 1
	}
	if err := s.finish(); err != nil {
		return nil, err
	}
	p.sels = append(p.sels, s)
	return selNode{len(p.sels) - 1}, nil
}

// yields says whether n returns data, given which selectors match something.
func yields(n qnode, ok []bool) bool {
	switch v := n.(type) {
	case selNode:
		return ok[v.idx]
	case allNode:
		for _, k := range v.kids {
			if !yields(k, ok) {
				return false
			}
		}
		return true
	case orNode:
		for _, k := range v.kids {
			if yields(k, ok) {
				return true
			}
		}
		return false
	case unlessNode:
		return yields(v.left, ok)
	}
	return true
}

// blame returns the selectors that make n return nothing. A branch of an or
// is excused when another branch with a selector returns data; a constant
// fallback such as vector(0) doesn't excuse it, because then the panel shows
// that constant forever. unless filters aren't blamed here.
func blame(n qnode, ok []bool, out *[]int) {
	switch v := n.(type) {
	case selNode:
		if !ok[v.idx] {
			*out = append(*out, v.idx)
		}
	case allNode:
		for _, k := range v.kids {
			blame(k, ok, out)
		}
	case orNode:
		for _, k := range v.kids {
			if hasSelector(k) && yields(k, ok) {
				return
			}
		}
		for _, k := range v.kids {
			blame(k, ok, out)
		}
	case unlessNode:
		blame(v.left, ok, out)
	}
}

func hasSelector(n qnode) bool {
	switch v := n.(type) {
	case selNode:
		return true
	case allNode:
		for _, k := range v.kids {
			if hasSelector(k) {
				return true
			}
		}
	case orNode:
		for _, k := range v.kids {
			if hasSelector(k) {
				return true
			}
		}
	case unlessNode:
		return hasSelector(v.left)
	}
	return false
}

// emptyFilters returns the selectors of unless filters that select nothing,
// so the filter removes nothing.
func emptyFilters(n qnode, ok []bool, out *[]int) {
	switch v := n.(type) {
	case allNode:
		for _, k := range v.kids {
			emptyFilters(k, ok, out)
		}
	case orNode:
		for _, k := range v.kids {
			emptyFilters(k, ok, out)
		}
	case unlessNode:
		emptyFilters(v.left, ok, out)
		for _, f := range v.filters {
			if !yields(f, ok) {
				blame(f, ok, out)
			}
			emptyFilters(f, ok, out)
		}
	}
}

// parseMatchers reads {a="b", c=~"d"} starting at the brace and returns the
// index of the closing brace. A quoted name on its own sets the metric name,
// as in Prometheus 3's {"my.metric", a="b"}.
func parseMatchers(toks []token, open int, s *selector) ([]matcher, int, error) {
	var ms []matcher
	j := open + 1
	for {
		t := toks[j]
		if t.kind == tRBrace {
			return ms, j, nil
		}
		if t.kind != tIdent && t.kind != tString {
			return nil, 0, fmt.Errorf("unexpected %q in label matchers at character %d", t.text, t.pos+1)
		}
		if t.kind == tString && (toks[j+1].kind == tComma || toks[j+1].kind == tRBrace) {
			if s.name != "" {
				return nil, 0, fmt.Errorf("metric name given twice at character %d", t.pos+1)
			}
			s.name = t.text
			j++
		} else {
			op, val := toks[j+1], toks[j+2]
			if op.kind != tOp || (op.text != "=" && op.text != "!=" && op.text != "=~" && op.text != "!~") {
				return nil, 0, fmt.Errorf("expected =, !=, =~ or !~ after %q at character %d", t.text, op.pos+1)
			}
			if val.kind != tString {
				return nil, 0, fmt.Errorf("expected a quoted value after %s%s at character %d", t.text, op.text, val.pos+1)
			}
			ms = append(ms, matcher{label: t.text, op: op.text, value: val.text, hasVar: strings.ContainsRune(val.text, varMark)})
			j += 3
		}
		switch toks[j].kind {
		case tComma:
			j++
		case tRBrace:
		default:
			return nil, 0, fmt.Errorf("expected , or } at character %d", toks[j].pos+1)
		}
	}
}

// finish moves a __name__ matcher into the selector's name and builds the
// pattern for names with variables.
func (s *selector) finish() error {
	var rest []matcher
	for _, m := range s.matchers {
		if m.label != "__name__" {
			rest = append(rest, m)
			continue
		}
		switch {
		case m.op == "=" && s.name == "":
			s.name = m.value
		case m.op == "=~" && s.name == "" && !m.hasVar:
			re, err := regexp.Compile("^(?:" + m.value + ")$")
			if err != nil {
				return fmt.Errorf("__name__ regex %q: %v", m.value, err)
			}
			s.name = m.value
			s.pattern = re
		case m.op == "=~" && s.name == "":
			s.name = m.value
			s.pattern = varPattern(m.value, true)
		}
	}
	s.matchers = rest
	if s.pattern == nil && strings.ContainsRune(s.name, varMark) {
		s.pattern = varPattern(s.name, false)
	}
	return nil
}

// varPattern turns a name with variables into a pattern where each variable
// matches anything. In a regex the rest stays regex; elsewhere it's literal.
func varPattern(name string, isRegex bool) *regexp.Regexp {
	parts := strings.Split(name, string(varMark))
	for i, p := range parts {
		if !isRegex {
			parts[i] = regexp.QuoteMeta(p)
		}
	}
	re, err := regexp.Compile("^(?:" + strings.Join(parts, ".*") + ")$")
	if err != nil {
		return regexp.MustCompile(".*")
	}
	return re
}

// requiresLabel reports whether a matcher only matches series that have the
// label. In Prometheus a missing label equals "", so a!="x" or a=~".*" also
// match series without it.
func (m matcher) requiresLabel() bool {
	if m.hasVar {
		return false
	}
	switch m.op {
	case "=":
		return m.value != ""
	case "=~":
		re, err := regexp.Compile("^(?:" + m.value + ")$")
		return err == nil && !re.MatchString("")
	}
	return false
}

// displayName shows a name with variables the way it was written, roughly.
func displayName(n string) string { return strings.ReplaceAll(n, string(varMark), "$var") }
