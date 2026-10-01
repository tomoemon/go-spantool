package main

import (
	"sort"

	"github.com/cloudspannerecosystem/memefish/ast"
	"github.com/cloudspannerecosystem/memefish/token"
)

// clauseKind is how a clause is laid out.
type clauseKind int

const (
	// clauseBlock puts the keywords on their own line and the body on the
	// following lines, one level deeper (SELECT, FROM, WHERE, JOIN, ...).
	clauseBlock clauseKind = iota
	// clauseSetOp puts a blank line before a set operator (UNION ALL, ...).
	clauseSetOp
	// clauseWith starts WITH on a new line and keeps the first CTE on it.
	clauseWith
)

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

// layout holds the syntactic role of tokens, keyed by token index. It is
// derived from the AST so that the printer does not have to guess the role of
// a token from the tokens around it.
type layout struct {
	clauseStarts map[int]clauseKind // first keyword of a clause
	clauseEnds   map[int]clauseKind // last keyword of a clause, e.g. BY of GROUP BY
	keywords     map[int]bool       // keywords that memefish lexes as identifiers, e.g. OFFSET
	condOps      map[int]bool       // AND / OR that separate conditions
	condGroups   map[int]int        // "(" of a parenthesized condition group -> its ")"
	subqueries   map[int]int        // "(" of a subquery -> its ")"
	listSeps     map[int]listKind
	cases        map[int]casePart // parts of CASE expressions laid out on several lines
	hints        map[int]int      // "@" of a hint -> its "}"
}

type layoutBuilder struct {
	*layout
	tokens []tok
}

func buildLayout(stmt ast.Statement, tokens []tok) *layout {
	b := &layoutBuilder{
		layout: &layout{
			clauseStarts: map[int]clauseKind{},
			clauseEnds:   map[int]clauseKind{},
			keywords:     map[int]bool{},
			condOps:      map[int]bool{},
			condGroups:   map[int]int{},
			subqueries:   map[int]int{},
			listSeps:     map[int]listKind{},
			cases:        map[int]casePart{},
			hints:        map[int]int{},
		},
		tokens: tokens,
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
			b.subquery(b.at(n.Lparen), n.Rparen, n.Query)
			return false
		case *ast.ScalarSubQuery:
			b.subquery(b.at(n.Lparen), n.Rparen, n.Query)
			return false
		case *ast.SubQueryInCondition:
			b.subquery(b.at(n.Lparen), n.Rparen, n.Query)
			return false
		case *ast.SubQueryTableExpr:
			b.subquery(b.at(n.Lparen), n.Rparen, n.Query)
			return false
		case *ast.ArraySubQuery:
			b.subquery(b.find(n.Array, n.Query.Pos(), "("), n.Rparen, n.Query)
			return false
		case *ast.ExistsSubQuery:
			if n.Hint != nil {
				b.walk(n.Hint, inline)
			}
			b.subquery(b.find(n.Exists, n.Query.Pos(), "("), n.Rparen, n.Query)
			return false
		case *ast.CTE:
			b.subquery(b.find(n.Name.End(), n.QueryExpr.Pos(), "("), n.Rparen, n.QueryExpr)
			return false
		case *ast.ParenTableExpr:
			if !inline {
				b.walk(n.Source, true)
				return false
			}
		}
		if !inline {
			b.clause(n)
		}
		return true
	})
}

// clause records the clauses, list separators, conditions and expanded CASE
// expressions of n.
func (b *layoutBuilder) clause(n ast.Node) {
	switch n := n.(type) {
	case *ast.Select:
		b.block(b.at(n.Select))
		for k := 1; k < len(n.Results); k++ {
			b.listSeps[b.find(n.Results[k-1].End(), n.Results[k].Pos(), ",")] = listSelect
		}
	case *ast.Alias:
		b.expandCase(n.Expr)
	case *ast.ExprSelectItem:
		b.expandCase(n.Expr)
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
		b.clauseRange(clauseBlock, b.at(n.Group), b.find(n.Group, n.Exprs[0].Pos(), "BY"))
		for _, e := range n.Exprs {
			b.expandCase(e)
		}
	case *ast.OrderBy:
		b.clauseRange(clauseBlock, b.at(n.Order), b.find(n.Order, n.Items[0].Pos(), "BY"))
	case *ast.OrderByItem:
		b.expandCase(n.Expr)
	case *ast.Limit:
		b.block(b.at(n.Limit))
	case *ast.Offset:
		b.block(b.at(n.Offset))
	case *ast.WithOffset:
		b.keywords[b.at(n.Offset)] = true
	case *ast.Join:
		if n.Op == ast.CommaJoin {
			return
		}
		right := n.Right.Pos()
		if n.Hint != nil {
			right = n.Hint.Pos()
		}
		b.clauseRange(clauseBlock, b.after(n.Left.End()), b.find(n.Left.End(), right, "JOIN"))
	case *ast.CompoundQuery:
		for k := 1; k < len(n.Queries); k++ {
			op := b.after(n.Queries[k-1].End())
			last := op
			if n.AllOrDistinct != "" {
				last = op + 1
			}
			b.clauseRange(clauseSetOp, op, last)
		}
	case *ast.With:
		b.clauseRange(clauseWith, b.at(n.With), b.at(n.With))
		for k := 1; k < len(n.CTEs); k++ {
			b.listSeps[b.find(n.CTEs[k-1].End(), n.CTEs[k].Pos(), ",")] = listCTE
		}
	case *ast.Insert:
		into := b.find(n.Insert, n.TableName.Pos(), "INTO")
		b.keywordRange(b.at(n.Insert), into) // INSERT [OR UPDATE|IGNORE]
		b.block(into)
	case *ast.ValuesInput:
		b.block(b.at(n.Values))
	case *ast.Update:
		b.keywords[b.at(n.Update)] = true
		b.block(b.find(n.Update, n.Updates[0].Pos(), "SET"))
	case *ast.UpdateItemSetValue:
		if n.DefaultExpr.Expr != nil {
			b.expandCase(n.DefaultExpr.Expr)
		}
	case *ast.ConflictActionDoUpdate:
		set := b.find(n.Do, n.UpdateItems[0].Pos(), "SET")
		b.keywordRange(b.at(n.Do), set) // DO UPDATE
		b.block(set)
	case *ast.Delete:
		b.keywords[b.at(n.Delete)] = true
		if from := b.find(n.Delete, n.TableName.Pos(), "FROM"); from >= 0 {
			b.block(from)
		}
	}
}

func (b *layoutBuilder) block(i int) {
	b.clauseRange(clauseBlock, i, i)
}

func (b *layoutBuilder) clauseRange(kind clauseKind, first, last int) {
	b.clauseStarts[first] = kind
	b.clauseEnds[last] = kind
	b.keywords[first] = true
	b.keywords[last] = true
}

// keywordRange marks tokens[first..last) as keywords.
func (b *layoutBuilder) keywordRange(first, last int) {
	for i := first; i < last; i++ {
		b.keywords[i] = true
	}
}

func (b *layoutBuilder) subquery(open int, rparen token.Pos, query ast.Node) {
	b.subqueries[open] = b.at(rparen)
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
	case *ast.CaseExpr:
		b.expandCase(e)
	}
}

// expandCase lays out e on several lines if it is a CASE expression. It is
// called for expressions that stand on their own: a SELECT / ORDER BY /
// GROUP BY item, a SET value, a condition, and a result of an expanded CASE.
func (b *layoutBuilder) expandCase(e ast.Expr) {
	c, ok := e.(*ast.CaseExpr)
	if !ok {
		return
	}
	b.cases[b.at(c.Case)] = casePartCase
	for _, w := range c.Whens {
		b.cases[b.at(w.When)] = casePartWhen
		b.expandCase(w.Then)
	}
	if c.Else != nil {
		b.cases[b.at(c.Else.Else)] = casePartElse
		b.expandCase(c.Else.Expr)
	}
	b.cases[b.at(c.EndPos)] = casePartEnd
}

// at returns the index of the token starting at pos, or -1.
func (b *layoutBuilder) at(pos token.Pos) int {
	if i := b.after(pos); i < len(b.tokens) && b.tokens[i].pos == pos {
		return i
	}
	return -1
}

// after returns the index of the first token starting at or after pos, or
// len(tokens) if there is none.
func (b *layoutBuilder) after(pos token.Pos) int {
	return sort.Search(len(b.tokens), func(i int) bool { return b.tokens[i].pos >= pos })
}

// find returns the index of the first token of the given kind (a reserved
// keyword or a symbol) in [from, to), or -1. It is used for tokens whose
// position the AST does not record, such as JOIN, AND / OR and commas.
func (b *layoutBuilder) find(from, to token.Pos, kind string) int {
	for i := b.after(from); i < len(b.tokens) && b.tokens[i].pos < to; i++ {
		if string(b.tokens[i].kind) == kind {
			return i
		}
	}
	return -1
}
