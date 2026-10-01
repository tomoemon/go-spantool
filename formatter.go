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

// noSpaceBefore lists symbols that should not have a space before them.
var noSpaceBefore = map[string]bool{
	".": true, ",": true, ")": true, "]": true, ";": true,
}

// noSpaceAfter lists symbols that should not have a space after them.
var noSpaceAfter = map[string]bool{
	".": true, "(": true, "[": true,
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
	// Table hint: `@{...}` attaches to the table name
	if string(cur.kind) == "@" && prev.kind == token.TokenIdent {
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
// Clause bodies are indented one level (2 spaces) deeper than clause keywords,
// and the closing ")" of a subquery one level shallower.
type scope struct {
	close        int   // index of the ")" closing the subquery, -1 for the statement
	clauseIndent int   // indentation of clause keywords
	condGroups   []int // indexes of the ")" closing the open condition groups
	cases        []int // indentation of the open expanded CASE expressions
}

func (sc *scope) bodyIndent() int { return sc.clauseIndent + 2 }

// condIndent is the indentation of a condition inside the open condition groups.
func (sc *scope) condIndent() int { return sc.bodyIndent() + 2*len(sc.condGroups) }

type printer struct {
	tokens        []tok
	lay           *layout
	b             strings.Builder
	starts, ends  []int // output range of each written token, for placing comments
	prev          int   // index of the last written token, -1 at the start
	suppressSpace bool
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
		sc:     &scope{close: -1},
	}
	for i := 0; i < len(tokens); i++ {
		i = p.token(i)
	}
	return strings.TrimSpace(placeComments(p.b.String(), tokens, eof, p.starts, p.ends))
}

// token writes tokens[i] and returns the index of the last token it wrote.
func (p *printer) token(i int) int {
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
	if i == sc.close {
		p.closeSubquery(i)
		return i
	}
	if n := len(sc.condGroups); n > 0 && sc.condGroups[n-1] == i {
		p.closeCondGroup(i)
		return i
	}
	if part, ok := p.lay.cases[i]; ok {
		p.casePart(i, part)
		return i
	}
	if k, ok := p.lay.listSeps[i]; ok {
		p.write(i, ",")
		if k == listCTE {
			p.newline(sc.clauseIndent)
		} else {
			p.newline(sc.bodyIndent())
		}
		return i
	}
	if p.lay.condOps[i] {
		p.newline(sc.condIndent())
		p.write(i, p.upper(i))
		return i
	}

	if kind, ok := p.lay.clauseStarts[i]; ok {
		if kind == clauseSetOp {
			p.b.WriteString("\n")
		}
		if p.b.Len() > 0 {
			p.newline(sc.clauseIndent)
		}
	}
	p.space(i)
	p.write(i, p.upper(i))
	if kind, ok := p.lay.clauseEnds[i]; ok && kind == clauseBlock {
		p.newline(sc.bodyIndent())
	}
	return i
}

// upper returns tokens[i], uppercased if it is a keyword.
func (p *printer) upper(i int) string {
	t := p.tokens[i]
	if _, ok := token.KeywordsMap[t.kind]; ok {
		return string(t.kind)
	}
	if p.lay.keywords[i] {
		return strings.ToUpper(t.raw)
	}
	return t.raw
}

func (p *printer) write(i int, s string) {
	p.starts[i] = p.b.Len()
	p.b.WriteString(s)
	p.ends[i] = p.b.Len()
	p.prev = i
	p.suppressSpace = false
}

// newline starts a new line. The next token is written without a space.
func (p *printer) newline(indent int) {
	p.b.WriteString("\n")
	p.b.WriteString(strings.Repeat(" ", indent))
	p.suppressSpace = true
}

// space writes a space before tokens[i] if it needs one.
func (p *printer) space(i int) {
	if !p.suppressSpace && p.prev >= 0 && needsSpace(p.tokens[p.prev], p.tokens[i]) {
		p.b.WriteString(" ")
	}
}

// casePart writes a part of an expanded CASE expression.
func (p *printer) casePart(i int, part casePart) {
	sc := p.sc
	n := len(sc.cases)
	switch {
	case part == casePartCase && n == 0:
		sc.cases = append(sc.cases, sc.condIndent())
		p.space(i)
	case part == casePartCase:
		// Nested CASE starts on its own line
		indent := sc.cases[n-1] + 4
		sc.cases = append(sc.cases, indent)
		p.newline(indent)
	case part == casePartEnd:
		indent := sc.cases[n-1]
		sc.cases = sc.cases[:n-1]
		p.newline(indent)
	default: // WHEN, ELSE
		p.newline(sc.cases[n-1] + 2)
	}
	p.write(i, p.upper(i))
}

// hint writes the hint `@{...}` at tokens[i..end] without spaces, as memefish
// splits it into separate tokens.
func (p *printer) hint(i, end int) int {
	p.space(i)
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
	p.space(i)
	p.write(i, "(")
	// The closing ) aligns with the line that opened the subquery,
	// and the body is indented one level deeper.
	p.outer = append(p.outer, p.sc)
	p.sc = &scope{close: close, clauseIndent: lineIndent(p.b.String()) + 2}
	p.suppressSpace = true
}

func (p *printer) closeSubquery(i int) {
	p.newline(p.sc.clauseIndent - 2)
	p.write(i, ")")
	p.sc = p.outer[len(p.outer)-1]
	p.outer = p.outer[:len(p.outer)-1]
}

func (p *printer) openCondGroup(i, close int) {
	sc := p.sc
	p.space(i)
	p.write(i, "(")
	sc.condGroups = append(sc.condGroups, close)
	p.newline(sc.condIndent())
}

func (p *printer) closeCondGroup(i int) {
	sc := p.sc
	sc.condGroups = sc.condGroups[:len(sc.condGroups)-1]
	p.newline(sc.condIndent())
	p.write(i, ")")
}

// lineIndent returns the number of leading spaces on the last line of s.
func lineIndent(s string) int {
	line := s[strings.LastIndex(s, "\n")+1:]
	return len(line) - len(strings.TrimLeft(line, " "))
}
