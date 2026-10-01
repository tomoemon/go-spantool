package main

import (
	"sort"

	"github.com/cloudspannerecosystem/memefish/ast"
	"github.com/cloudspannerecosystem/memefish/token"
)

// clauseKind is how a clause keyword is laid out.
type clauseKind int

const (
	// clauseBlock puts the keyword on its own line and its body on the
	// following lines, one level deeper (SELECT, FROM, WHERE, JOIN, ...).
	clauseBlock clauseKind = iota
	// clauseSetOp puts a blank line before a set operator (UNION ALL, ...).
	clauseSetOp
	// clauseWith starts WITH on a new line and keeps the first CTE on it.
	clauseWith
)

type clause struct {
	kind clauseKind
	last int // index of the last keyword token, e.g. BY of GROUP BY
}

// listKind is how a list separator (comma) is laid out.
type listKind int

const (
	listSelect listKind = iota // SELECT list item: own line at body indent
	listCTE                    // CTE: own line at clause indent
)

type casePart int

const (
	casePartCase casePart = iota
	casePartWhen
	casePartElse
	casePartEnd
)

type caseMark struct {
	part  casePart
	owner int // index of the CASE token the part belongs to
}

// layout holds the syntactic role of tokens, keyed by token index. It is
// derived from the AST so that the printer does not have to guess the role of
// a keyword from the tokens around it.
type layout struct {
	clauses    map[int]clause
	condOps    map[int]bool // AND / OR that separate conditions
	condGroups map[int]int  // "(" of a parenthesized condition group -> its ")"
	subqueries map[int]int  // "(" of a subquery -> its ")"
	listSeps   map[int]listKind
	cases      map[int]caseMark
	hints      map[int]int // "@" of a hint -> its "}"
}

type layoutBuilder struct {
	*layout
	tokens []tok
	index  map[token.Pos]int // token start position -> token index
}

func buildLayout(stmt ast.Statement, tokens []tok) *layout {
	b := &layoutBuilder{
		layout: &layout{
			clauses:    map[int]clause{},
			condOps:    map[int]bool{},
			condGroups: map[int]int{},
			subqueries: map[int]int{},
			listSeps:   map[int]listKind{},
			cases:      map[int]caseMark{},
			hints:      map[int]int{},
		},
		tokens: tokens,
		index:  make(map[token.Pos]int, len(tokens)),
	}
	for i, t := range tokens {
		b.index[t.pos] = i
	}
	b.walk(stmt, false)
	return b.layout
}

// walk records the layout of the nodes under root. When inline is true (inside
// a parenthesized JOIN), clauses are kept on one line, but subqueries are still
// laid out on their own.
func (b *layoutBuilder) walk(root ast.Node, inline bool) {
	ast.Inspect(root, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Hint:
			b.hints[b.at(n.Atmark)] = b.at(n.Rbrace)
			return false
		case *ast.SubQuery:
			b.subquery(b.at(n.Lparen), b.at(n.Rparen), n.Query)
			return false
		case *ast.ScalarSubQuery:
			b.subquery(b.at(n.Lparen), b.at(n.Rparen), n.Query)
			return false
		case *ast.SubQueryInCondition:
			b.subquery(b.at(n.Lparen), b.at(n.Rparen), n.Query)
			return false
		case *ast.SubQueryTableExpr:
			b.subquery(b.at(n.Lparen), b.at(n.Rparen), n.Query)
			return false
		case *ast.ArraySubQuery:
			b.subquery(b.matchingOpen(b.at(n.Rparen)), b.at(n.Rparen), n.Query)
			return false
		case *ast.ExistsSubQuery:
			if n.Hint != nil {
				b.walk(n.Hint, inline)
			}
			b.subquery(b.matchingOpen(b.at(n.Rparen)), b.at(n.Rparen), n.Query)
			return false
		case *ast.CTE:
			b.subquery(b.matchingOpen(b.at(n.Rparen)), b.at(n.Rparen), n.QueryExpr)
			return false
		case *ast.ParenTableExpr:
			if !inline {
				b.walk(n.Source, true)
				return false
			}
		case *ast.CaseExpr:
			owner := b.at(n.Case)
			b.cases[owner] = caseMark{casePartCase, owner}
			for _, w := range n.Whens {
				b.cases[b.at(w.When)] = caseMark{casePartWhen, owner}
			}
			if n.Else != nil {
				b.cases[b.at(n.Else.Else)] = caseMark{casePartElse, owner}
			}
			b.cases[b.at(n.EndPos)] = caseMark{casePartEnd, owner}
		}
		if !inline {
			b.clause(n)
		}
		return true
	})
}

// clause records the clause keywords, list separators and conditions of n.
func (b *layoutBuilder) clause(n ast.Node) {
	switch n := n.(type) {
	case *ast.Select:
		b.block(b.at(n.Select))
		for k := 1; k < len(n.Results); k++ {
			b.listSeps[b.find(n.Results[k-1].End(), n.Results[k].Pos(), ",")] = listSelect
		}
	case *ast.From:
		b.block(b.at(n.From))
	case *ast.Where:
		b.block(b.at(n.Where))
		b.cond(n.Expr)
	case *ast.On:
		b.block(b.at(n.On))
		b.cond(n.Expr)
	case *ast.Having:
		b.block(b.at(n.Having))
		b.cond(n.Expr)
	case *ast.GroupBy:
		b.blockRange(b.at(n.Group), b.find(n.Group, n.Exprs[0].Pos(), "BY"))
	case *ast.OrderBy:
		b.blockRange(b.at(n.Order), b.find(n.Order, n.Items[0].Pos(), "BY"))
	case *ast.Limit:
		b.block(b.at(n.Limit))
	case *ast.Offset:
		b.block(b.at(n.Offset))
	case *ast.Join:
		if n.Op == ast.CommaJoin {
			return
		}
		right := n.Right.Pos()
		if n.Hint != nil {
			right = n.Hint.Pos()
		}
		b.blockRange(b.after(n.Left.End()), b.find(n.Left.End(), right, "JOIN"))
	case *ast.CompoundQuery:
		for k := 1; k < len(n.Queries); k++ {
			op := b.after(n.Queries[k-1].End())
			last := op
			if next := b.tokens[op+1]; isKeywordLike(next, "ALL") || isKeywordLike(next, "DISTINCT") {
				last = op + 1
			}
			b.clauses[op] = clause{clauseSetOp, last}
		}
	case *ast.With:
		w := b.at(n.With)
		b.clauses[w] = clause{clauseWith, w}
		for k := 1; k < len(n.CTEs); k++ {
			b.listSeps[b.find(n.CTEs[k-1].End(), n.CTEs[k].Pos(), ",")] = listCTE
		}
	case *ast.Insert:
		b.block(b.find(n.Insert, n.TableName.Pos(), "INTO"))
	case *ast.ValuesInput:
		b.block(b.at(n.Values))
	case *ast.Update:
		b.block(b.find(n.Update, n.Updates[0].Pos(), "SET"))
	case *ast.ConflictActionDoUpdate:
		b.block(b.find(n.Do, n.UpdateItems[0].Pos(), "SET"))
	case *ast.Delete:
		if from := b.find(n.Delete, n.TableName.Pos(), "FROM"); from >= 0 {
			b.block(from)
		}
	}
}

func (b *layoutBuilder) block(i int) {
	b.blockRange(i, i)
}

func (b *layoutBuilder) blockRange(first, last int) {
	b.clauses[first] = clause{clauseBlock, last}
}

func (b *layoutBuilder) subquery(open, close int, query ast.Node) {
	b.subqueries[open] = close
	b.walk(query, false)
}

// cond records the AND / OR operators that separate conditions in e, and the
// parenthesized groups of conditions that are expanded onto their own lines.
func (b *layoutBuilder) cond(e ast.Expr) {
	switch e := e.(type) {
	case *ast.BinaryExpr:
		if e.Op != ast.OpAnd && e.Op != ast.OpOr {
			return
		}
		b.condOps[b.find(e.Left.End(), e.Right.Pos(), string(e.Op))] = true
		b.cond(e.Left)
		b.cond(e.Right)
	case *ast.UnaryExpr:
		if e.Op == ast.OpNot {
			b.cond(e.Expr)
		}
	case *ast.ParenExpr:
		if inner, ok := e.Expr.(*ast.BinaryExpr); ok && (inner.Op == ast.OpAnd || inner.Op == ast.OpOr) {
			b.condGroups[b.at(e.Lparen)] = b.at(e.Rparen)
			b.cond(inner)
		}
	}
}

// at returns the index of the token starting at pos, or -1.
func (b *layoutBuilder) at(pos token.Pos) int {
	if i, ok := b.index[pos]; ok {
		return i
	}
	return -1
}

// after returns the index of the first token starting at or after pos, or
// len(tokens) if there is none.
func (b *layoutBuilder) after(pos token.Pos) int {
	return sort.Search(len(b.tokens), func(i int) bool { return b.tokens[i].pos >= pos })
}

// find returns the index of the first token in [from, to) that is the given
// keyword or symbol, or -1. It is used for keywords whose position the AST
// does not record, such as JOIN, AND / OR and commas between list items.
func (b *layoutBuilder) find(from, to token.Pos, keyword string) int {
	for i := b.after(from); i < len(b.tokens) && b.tokens[i].pos < to; i++ {
		if isKeywordLike(b.tokens[i], keyword) {
			return i
		}
	}
	return -1
}

// matchingOpen returns the index of the "(" that matches the ")" at close.
func (b *layoutBuilder) matchingOpen(close int) int {
	d := 0
	for i := close; i >= 0; i-- {
		switch string(b.tokens[i].kind) {
		case ")":
			d++
		case "(":
			d--
			if d == 0 {
				return i
			}
		}
	}
	return -1
}
