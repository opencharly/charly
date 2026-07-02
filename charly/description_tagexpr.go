package main

import (
	"fmt"
	"strings"
	"unicode"
)

// Tag-expression grammar (Cucumber-compatible minimal subset):
//
//   disjunction = conjunction ( "or" conjunction )*
//   conjunction = atom ( "and" atom )*
//   atom        = "@" IDENT | "not" atom | "(" disjunction ")"
//
// Identifiers are matched leniently: a leading '@' is optional so
// that `--tag smoke` and `--tag @smoke` behave identically.
//
// The empty expression matches every tag set (useful as a default
// value that doesn't accidentally filter anything out).

// TagExpr is an opaque compiled tag expression. Nil means "match
// everything" (no filter). Match returns true when the given tag set
// satisfies the expression.
type TagExpr struct {
	node tagNode
	raw  string
}

// String returns the raw source the expression was compiled from.
// Useful for error messages and reporting.
func (t *TagExpr) String() string {
	if t == nil {
		return ""
	}
	return t.raw
}

// Match returns true when the given tag slice satisfies the
// expression. A nil TagExpr matches everything (allowing code paths
// like `if tagExpr.Match(tags) { … }` without nil-checking at each
// call site).
func (t *TagExpr) Match(tags []string) bool {
	if t == nil || t.node == nil {
		return true
	}
	set := make(map[string]bool, len(tags))
	for _, tag := range tags {
		set[normalizeTag(tag)] = true
	}
	return t.node.check(set)
}

// ParseTagExpr compiles a tag expression. Empty / whitespace input
// produces a nil TagExpr that matches everything. Syntax errors
// return a descriptive error so the CLI can surface them rather than
// silently matching or silently failing.
func ParseTagExpr(src string) (*TagExpr, error) {
	trimmed := strings.TrimSpace(src)
	if trimmed == "" {
		return nil, nil
	}
	toks, err := lexTagExpr(trimmed)
	if err != nil {
		return nil, err
	}
	p := &tagParser{toks: toks}
	node, err := p.parseDisjunction()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.toks) {
		return nil, fmt.Errorf("unexpected token %q at position %d", p.toks[p.pos].value, p.pos)
	}
	return &TagExpr{node: node, raw: trimmed}, nil
}

// CombineTagFilters composes an include-filter expression and an
// exclude-filter expression into a single effective expression. Either
// side may be nil. Output semantics:
//
//   - include nil, exclude nil  → always matches
//   - include X,   exclude nil  → matches when X is true
//   - include nil, exclude Y    → matches when Y is false
//   - include X,   exclude Y    → matches when X is true AND Y is false
//
// Used by the CLI to merge `--tag X` / `--tag-exclude Y` / `--tags Z`
// flags into a single filter predicate.
func CombineTagFilters(include, exclude *TagExpr) *TagExpr {
	if include == nil && exclude == nil {
		return nil
	}
	var node tagNode
	switch {
	case include != nil && exclude != nil:
		node = &tagAnd{left: include.node, right: &tagNot{of: exclude.node}}
	case include != nil:
		node = include.node
	case exclude != nil:
		node = &tagNot{of: exclude.node}
	}
	raw := ""
	if include != nil {
		raw = include.raw
	}
	if exclude != nil {
		if raw != "" {
			raw = "(" + raw + ") and not (" + exclude.raw + ")"
		} else {
			raw = "not (" + exclude.raw + ")"
		}
	}
	return &TagExpr{node: node, raw: raw}
}

// ---------------------------------------------------------------------------
// AST + checkuator
// ---------------------------------------------------------------------------

type tagNode interface {
	check(set map[string]bool) bool
}

type tagLeaf struct{ name string }

func (l *tagLeaf) check(set map[string]bool) bool { return set[l.name] }

type tagNot struct{ of tagNode }

func (n *tagNot) check(set map[string]bool) bool { return !n.of.check(set) }

type tagAnd struct{ left, right tagNode }

func (a *tagAnd) check(set map[string]bool) bool {
	return a.left.check(set) && a.right.check(set)
}

type tagOr struct{ left, right tagNode }

func (o *tagOr) check(set map[string]bool) bool {
	return o.left.check(set) || o.right.check(set)
}

// ---------------------------------------------------------------------------
// Lexer + parser
// ---------------------------------------------------------------------------

type tagTokenKind int

const (
	tokIdent tagTokenKind = iota
	tokAnd
	tokOr
	tokNot
	tokLParen
	tokRParen
)

type tagToken struct {
	kind  tagTokenKind
	value string
}

func lexTagExpr(src string) ([]tagToken, error) {
	var out []tagToken
	i := 0
	for i < len(src) {
		r := rune(src[i])
		switch {
		case unicode.IsSpace(r):
			i++
		case r == '(':
			out = append(out, tagToken{kind: tokLParen, value: "("})
			i++
		case r == ')':
			out = append(out, tagToken{kind: tokRParen, value: ")"})
			i++
		case r == '@' || unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_':
			start := i
			if r == '@' {
				i++
			}
			for i < len(src) {
				c := rune(src[i])
				if unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' || c == '-' || c == '.' || c == ':' {
					i++
				} else {
					break
				}
			}
			word := src[start:i]
			lower := strings.ToLower(word)
			switch lower {
			case "and":
				out = append(out, tagToken{kind: tokAnd, value: "and"})
			case "or":
				out = append(out, tagToken{kind: tokOr, value: "or"})
			case "not":
				out = append(out, tagToken{kind: tokNot, value: "not"})
			default:
				out = append(out, tagToken{kind: tokIdent, value: normalizeTag(word)})
			}
		default:
			return nil, fmt.Errorf("tag expression: unexpected character %q at position %d", r, i)
		}
	}
	return out, nil
}

type tagParser struct {
	toks []tagToken
	pos  int
}

func (p *tagParser) peek() (tagToken, bool) {
	if p.pos >= len(p.toks) {
		return tagToken{}, false
	}
	return p.toks[p.pos], true
}

func (p *tagParser) consume() tagToken { //nolint:unparam // parser API: returns the consumed token for callers that need it
	t := p.toks[p.pos]
	p.pos++
	return t
}

func (p *tagParser) parseDisjunction() (tagNode, error) {
	left, err := p.parseConjunction()
	if err != nil {
		return nil, err
	}
	for {
		t, ok := p.peek()
		if !ok || t.kind != tokOr {
			break
		}
		p.consume()
		right, err := p.parseConjunction()
		if err != nil {
			return nil, err
		}
		left = &tagOr{left: left, right: right}
	}
	return left, nil
}

func (p *tagParser) parseConjunction() (tagNode, error) {
	left, err := p.parseAtom()
	if err != nil {
		return nil, err
	}
	for {
		t, ok := p.peek()
		if !ok || t.kind != tokAnd {
			break
		}
		p.consume()
		right, err := p.parseAtom()
		if err != nil {
			return nil, err
		}
		left = &tagAnd{left: left, right: right}
	}
	return left, nil
}

func (p *tagParser) parseAtom() (tagNode, error) {
	t, ok := p.peek()
	if !ok {
		return nil, fmt.Errorf("tag expression: unexpected end of input")
	}
	switch t.kind {
	case tokNot:
		p.consume()
		inner, err := p.parseAtom()
		if err != nil {
			return nil, err
		}
		return &tagNot{of: inner}, nil
	case tokLParen:
		p.consume()
		inner, err := p.parseDisjunction()
		if err != nil {
			return nil, err
		}
		close, ok := p.peek()
		if !ok || close.kind != tokRParen {
			return nil, fmt.Errorf("tag expression: expected ')' but got %q", close.value)
		}
		p.consume()
		return inner, nil
	case tokIdent:
		p.consume()
		return &tagLeaf{name: t.value}, nil
	default:
		return nil, fmt.Errorf("tag expression: unexpected token %q", t.value)
	}
}
