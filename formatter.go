package main

import (
	"fmt"
	"strings"

	"github.com/cloudspannerecosystem/memefish"
	"github.com/cloudspannerecosystem/memefish/ast"
	"github.com/cloudspannerecosystem/memefish/token"
)

// FormatSQL formats SQL. It parses the SQL with memefish to find the syntactic
// role of each token (see layout), then writes the original tokens with
// newlines and indentation around clauses, conditions and subqueries, and
// normalizes keywords to uppercase. It returns an error if the SQL is malformed.
func FormatSQL(sql string) (string, error) {
	stmt, err := memefish.ParseStatement("", sql)
	if err != nil {
		return "", err
	}

	tokens, eof, err := tokenize(sql)
	if err != nil {
		return "", err
	}
	formatted := formatTokens(tokens, eof, buildLayout(stmt, tokens))
	if err := verifyEquivalent(stmt, formatted); err != nil {
		return "", err
	}
	if err := verifyComments(commentTexts(tokens, eof), formatted); err != nil {
		return "", err
	}
	return formatted, nil
}

const reportBug = "this is a fmt-sql bug; please report it with the SQL at https://github.com/tomoemon/go-spantool/issues"

// verifyEquivalent checks that formatting did not change the meaning of the
// SQL, by comparing the canonical SQL of the original and formatted statements.
func verifyEquivalent(orig ast.Statement, formatted string) error {
	stmt, err := memefish.ParseStatement("", formatted)
	if err != nil {
		return fmt.Errorf("formatted SQL is no longer valid (%v); %s\nformatted SQL:\n%s", err, reportBug, formatted)
	}
	if want, got := orig.SQL(), stmt.SQL(); want != got {
		return fmt.Errorf("formatting changed the meaning of the SQL; %s\nexpected: %s\nactual:   %s", reportBug, want, got)
	}
	return nil
}

type tok struct {
	kind     token.TokenKind
	raw      string
	pos      token.Pos
	comments []token.TokenComment
}

// tokenize splits sql into tokens. It also returns the comments after the last
// token, which memefish attaches to the end-of-input token.
func tokenize(sql string) ([]tok, []token.TokenComment, error) {
	lex := &memefish.Lexer{
		File: &token.File{Buffer: sql},
	}
	var tokens []tok
	for {
		if err := lex.NextToken(); err != nil {
			return nil, nil, err
		}
		if lex.Token.Kind == token.TokenEOF {
			return tokens, lex.Token.Comments, nil
		}
		tokens = append(tokens, tok{
			kind:     lex.Token.Kind,
			raw:      lex.Token.Raw,
			pos:      lex.Token.Pos,
			comments: lex.Token.Comments,
		})
	}
}

// upperWords lists words that are uppercased even though memefish does not
// lex them as reserved keywords.
var upperWords = map[string]bool{
	"SELECT": true, "FROM": true, "WHERE": true, "HAVING": true,
	"LIMIT": true, "SET": true, "INTO": true, "VALUES": true, "RETURNING": true,
	"ON": true, "OFFSET": true, "INSERT": true, "UPDATE": true, "DELETE": true,
}

// noSpaceBefore lists symbols that should not have a space before them.
var noSpaceBefore = map[string]bool{
	".": true, ",": true, ")": true, "]": true, ";": true,
}

// noSpaceAfter lists symbols that should not have a space after them.
var noSpaceAfter = map[string]bool{
	".": true, "(": true, "[": true,
}

func upper(t tok) string {
	if _, ok := token.KeywordsMap[t.kind]; ok {
		return string(t.kind)
	}
	if u := strings.ToUpper(t.raw); upperWords[u] {
		return u
	}
	return t.raw
}

func isKeywordLike(t tok, keyword string) bool {
	if string(t.kind) == keyword {
		return true
	}
	return strings.EqualFold(t.raw, keyword)
}

func needsSpace(prev, cur tok) bool {
	if noSpaceAfter[string(prev.kind)] {
		return false
	}
	if noSpaceBefore[string(cur.kind)] {
		return false
	}
	// Function call: no space before ( immediately following an identifier or keyword
	if string(cur.kind) == "(" && (prev.kind == token.TokenIdent || isFuncKeyword(prev)) {
		return false
	}
	// Array subscript: no space before [ following an operand
	if string(cur.kind) == "[" && (prev.kind == token.TokenIdent || prev.kind == token.TokenParam || string(prev.kind) == ")" || string(prev.kind) == "]") {
		return false
	}
	return true
}

func isFuncKeyword(t tok) bool {
	switch string(t.kind) {
	case "CAST", "EXTRACT", "ARRAY", "STRUCT", "UNNEST", "IF":
		return true
	}
	// COUNT, SUM, COALESCE, etc. are treated as identifiers, not keywords
	return false
}

// scope is the indentation state of a query: the whole statement or a subquery.
type scope struct {
	close        int // index of the ")" closing the subquery, -1 for the statement
	baseDepth    int // parenthesis depth just inside the subquery
	closeIndent  int // indentation of the closing ")"
	clauseIndent int // indentation of clause keywords
	bodyIndent   int // indentation of clause bodies
	condGroups   []condGroup
	cases        []caseFrame
}

// condGroup is an expanded parenthesized group of conditions.
type condGroup struct {
	close       int // index of the closing ")"
	outerIndent int // indentation of the closing ")"
}

// caseFrame is an expanded CASE expression.
type caseFrame struct {
	owner  int // index of the CASE token
	indent int // indentation of CASE and END
}

type printer struct {
	tokens        []tok
	lay           *layout
	b             strings.Builder
	starts, ends  []int // output range of each written token, for placing comments
	prev          int   // index of the last written token, -1 at the start
	suppressSpace bool
	depth         int // parenthesis depth
	sc            *scope
	outer         []*scope // scopes enclosing sc
}

func formatTokens(tokens []tok, eof []token.TokenComment, lay *layout) string {
	p := &printer{
		tokens: tokens,
		lay:    lay,
		starts: make([]int, len(tokens)),
		ends:   make([]int, len(tokens)),
		prev:   -1,
		sc:     &scope{close: -1, bodyIndent: 2},
	}
	for i := 0; i < len(tokens); i++ {
		i = p.token(i)
	}
	return strings.TrimSpace(placeComments(p.b.String(), tokens, eof, p.starts, p.ends))
}

// token writes tokens[i] and returns the index of the last token it wrote.
func (p *printer) token(i int) int {
	t := p.tokens[i]
	sc := p.sc

	if end, ok := p.lay.hints[i]; ok {
		return p.hint(i, end)
	}
	if close, ok := p.lay.subqueries[i]; ok {
		p.openSubquery(i, close)
		return i
	}
	if close, ok := p.lay.condGroups[i]; ok {
		p.openCondGroup(i, close)
		return i
	}
	switch string(t.kind) {
	case "(":
		p.depth++
	case ")":
		if i == sc.close {
			p.closeSubquery(i)
			return i
		}
		if n := len(sc.condGroups); n > 0 && sc.condGroups[n-1].close == i {
			p.closeCondGroup(i)
			return i
		}
		p.depth--
	}

	// CASE is expanded only outside parentheses, e.g. not inside a function call
	if m, ok := p.lay.cases[i]; ok && p.depth == sc.baseDepth && p.casePart(i, m) {
		return i
	}
	if c, ok := p.lay.clauses[i]; ok {
		return p.clause(i, c)
	}
	if k, ok := p.lay.listSeps[i]; ok {
		p.write(i, ",")
		if k == listCTE {
			p.newline(sc.clauseIndent)
		} else {
			p.newline(sc.bodyIndent)
		}
		p.suppressSpace = true
		return i
	}
	if p.lay.condOps[i] {
		p.newline(sc.bodyIndent + 2*len(sc.condGroups))
		p.write(i, upper(t))
		return i
	}

	p.space(i)
	p.write(i, upper(t))
	return i
}

func (p *printer) write(i int, s string) {
	p.starts[i] = p.b.Len()
	p.b.WriteString(s)
	p.ends[i] = p.b.Len()
	p.prev = i
	p.suppressSpace = false
}

func (p *printer) newline(indent int) {
	p.b.WriteString("\n")
	p.b.WriteString(strings.Repeat(" ", indent))
}

// space writes a space before tokens[i] if it needs one.
func (p *printer) space(i int) {
	if !p.suppressSpace && p.prev >= 0 && needsSpace(p.tokens[p.prev], p.tokens[i]) {
		p.b.WriteString(" ")
	}
}

// words writes tokens[first..last] separated by spaces.
func (p *printer) words(first, last int) {
	for j := first; j <= last; j++ {
		if j > first {
			p.b.WriteString(" ")
		}
		p.write(j, upper(p.tokens[j]))
	}
}

func (p *printer) clause(i int, c clause) int {
	sc := p.sc
	switch c.kind {
	case clauseWith:
		if p.b.Len() > 0 {
			p.newline(sc.clauseIndent)
		}
		p.write(i, upper(p.tokens[i]))
	case clauseSetOp:
		p.b.WriteString("\n")
		p.newline(sc.clauseIndent)
		p.words(i, c.last)
		p.suppressSpace = true
	case clauseBlock:
		if p.b.Len() > 0 {
			p.newline(sc.clauseIndent)
		}
		p.words(i, c.last)
		p.newline(sc.bodyIndent)
		p.suppressSpace = true
	}
	return c.last
}

// casePart writes a part of an expanded CASE expression. It returns false if
// the part belongs to a CASE that is not expanded.
func (p *printer) casePart(i int, m caseMark) bool {
	sc := p.sc
	n := len(sc.cases)
	u := upper(p.tokens[i])
	if m.part == casePartCase {
		indent := sc.bodyIndent
		if n == 0 {
			p.space(i)
		} else {
			// Nested CASE starts on its own line
			indent = sc.cases[n-1].indent + 4
			p.newline(indent)
		}
		p.write(i, u)
		sc.cases = append(sc.cases, caseFrame{owner: i, indent: indent})
		return true
	}
	if n == 0 || sc.cases[n-1].owner != m.owner {
		return false
	}
	top := sc.cases[n-1]
	if m.part == casePartEnd {
		sc.cases = sc.cases[:n-1]
		p.newline(top.indent)
	} else {
		p.newline(top.indent + 2)
	}
	p.write(i, u)
	return true
}

// hint writes the hint `@{...}` at tokens[i..end] without spaces, as memefish
// splits it into separate tokens.
func (p *printer) hint(i, end int) int {
	for j := i; j <= end; j++ {
		switch string(p.tokens[j].kind) {
		case ",":
			p.write(j, ", ")
		default:
			p.write(j, p.tokens[j].raw)
		}
	}
	return end
}

func (p *printer) openSubquery(i, close int) {
	p.depth++
	p.space(i)
	p.write(i, "(")
	// The closing ) aligns with the line that opened the subquery,
	// and the body is indented one level deeper.
	closeIndent := currentLineIndent(&p.b)
	p.outer = append(p.outer, p.sc)
	p.sc = &scope{
		close:        close,
		baseDepth:    p.depth,
		closeIndent:  closeIndent,
		clauseIndent: closeIndent + 2,
		bodyIndent:   closeIndent + 4,
	}
	p.suppressSpace = true
}

func (p *printer) closeSubquery(i int) {
	p.newline(p.sc.closeIndent)
	p.write(i, ")")
	p.sc = p.outer[len(p.outer)-1]
	p.outer = p.outer[:len(p.outer)-1]
	p.depth--
}

func (p *printer) openCondGroup(i, close int) {
	sc := p.sc
	p.depth++
	outerIndent := sc.bodyIndent + 2*len(sc.condGroups)
	sc.condGroups = append(sc.condGroups, condGroup{close: close, outerIndent: outerIndent})
	p.space(i)
	p.write(i, "(")
	p.newline(outerIndent + 2)
	p.suppressSpace = true
}

func (p *printer) closeCondGroup(i int) {
	sc := p.sc
	cg := sc.condGroups[len(sc.condGroups)-1]
	sc.condGroups = sc.condGroups[:len(sc.condGroups)-1]
	p.newline(cg.outerIndent)
	p.write(i, ")")
	p.depth--
}

// currentLineIndent returns the number of leading spaces on the line being written.
func currentLineIndent(b *strings.Builder) int {
	s := b.String()
	line := s[strings.LastIndex(s, "\n")+1:]
	return len(line) - len(strings.TrimLeft(line, " "))
}
