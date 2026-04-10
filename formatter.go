package main

import (
	"strings"

	"github.com/cloudspannerecosystem/memefish"
	"github.com/cloudspannerecosystem/memefish/token"
)

// FormatSQL formats SQL using a token-based approach. It inserts newlines and
// indentation after clause keywords (SELECT, FROM, WHERE, etc.) and normalizes
// keywords to uppercase. It also validates SQL syntax and returns an error if
// the SQL is malformed.
func FormatSQL(sql string) (string, error) {
	if _, err := memefish.ParseStatement("", sql); err != nil {
		return "", err
	}

	tokens, err := tokenize(sql)
	if err != nil {
		return "", err
	}
	return formatTokens(tokens), nil
}

type tok struct {
	kind     token.TokenKind
	raw      string
	comments []token.TokenComment
}

func tokenize(sql string) ([]tok, error) {
	lex := &memefish.Lexer{
		File: &token.File{Buffer: sql},
	}
	var tokens []tok
	for {
		if err := lex.NextToken(); err != nil {
			return nil, err
		}
		if lex.Token.Kind == token.TokenEOF {
			break
		}
		tokens = append(tokens, tok{
			kind:     lex.Token.Kind,
			raw:      lex.Token.Raw,
			comments: lex.Token.Comments,
		})
	}
	return tokens, nil
}

var clauseKeywords = map[string]bool{
	"SELECT": true, "FROM": true, "WHERE": true, "HAVING": true,
	"LIMIT": true, "SET": true, "INTO": true,
	"VALUES": true, "RETURNING": true,
}

var joinModifiers = map[string]bool{
	"LEFT": true, "RIGHT": true, "INNER": true,
	"FULL": true, "CROSS": true, "OUTER": true,
}

var setOperators = map[string]bool{
	"UNION": true, "INTERSECT": true, "EXCEPT": true,
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
	u := strings.ToUpper(t.raw)
	if clauseKeywords[u] || u == "OFFSET" || u == "INSERT" || u == "UPDATE" || u == "DELETE" {
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
	return true
}

func isFuncKeyword(t tok) bool {
	switch string(t.kind) {
	case "CAST", "EXTRACT", "EXISTS", "ARRAY", "STRUCT", "UNNEST", "IN":
		return true
	}
	// COUNT, SUM, COALESCE, etc. are treated as identifiers, not keywords
	return false
}

// condParen holds information about conditional parentheses
// (parenthesized groups containing AND/OR).
type condParen struct {
	depth       int // the matching depth (depth at the opening parenthesis)
	outerIndent int // indentation for the closing parenthesis
}

// hasAndOrAtDepth scans tokens[start:] and checks whether AND/OR exists
// at the same depth as targetDepth.
func hasAndOrAtDepth(tokens []tok, start int, targetDepth int) bool {
	d := targetDepth
	for i := start; i < len(tokens); i++ {
		switch string(tokens[i].kind) {
		case "(":
			d++
		case ")":
			d--
			if d < targetDepth {
				return false
			}
		}
		if d == targetDepth && (isKeywordLike(tokens[i], "AND") || isKeywordLike(tokens[i], "OR")) {
			return true
		}
	}
	return false
}

// subqueryCtx saves the state before entering a subquery.
type subqueryCtx struct {
	baseDepth    int
	closeIndent  int
	clauseIndent int
	bodyIndent   int
	inSelectList bool
	inWhere      bool
	caseStack    []int
}

func formatTokens(tokens []tok) string {
	if len(tokens) == 0 {
		return ""
	}

	var b strings.Builder
	depth := 0
	clauseIndent := 0
	bodyIndent := 2
	inSelectList := false
	inWhere := false
	suppressSpace := false
	hintDepth := 0

	var caseStack []int
	var subStack []subqueryCtx
	var condParenStack []condParen
	skip := make(map[int]bool)

	effectiveDepth := func() int {
		if len(subStack) > 0 {
			return depth - subStack[len(subStack)-1].baseDepth
		}
		return depth
	}

	ind := func(n int) string {
		return strings.Repeat(" ", n)
	}

	for i, t := range tokens {
		if skip[i] {
			continue
		}

		u := upper(t)

		// Hint block `@{...}`: memefish splits it into separate tokens,
		// so reassemble without spaces. `@<param>` query parameters are
		// a single token, so `@` + `{` unambiguously introduces a hint.
		if hintDepth > 0 {
			switch string(t.kind) {
			case "}":
				b.WriteString("}")
				hintDepth--
				suppressSpace = false
			case ",":
				b.WriteString(", ")
				suppressSpace = true
			default:
				b.WriteString(t.raw)
				suppressSpace = true
			}
			continue
		}
		if string(t.kind) == "@" && i+1 < len(tokens) && string(tokens[i+1].kind) == "{" {
			b.WriteString("@")
			suppressSpace = true
			continue
		}
		if string(t.kind) == "{" && i > 0 && string(tokens[prevNonSkipped(tokens, i, skip)].kind) == "@" {
			b.WriteString("{")
			hintDepth++
			suppressSpace = true
			continue
		}

		// --- ( : increment depth + detect subquery ---
		if string(t.kind) == "(" {
			depth++
			if i+1 < len(tokens) && (isKeywordLike(tokens[i+1], "SELECT") || isKeywordLike(tokens[i+1], "WITH")) {
				// Write ( with normal spacing
				if i > 0 && !suppressSpace {
					if needsSpace(tokens[prevNonSkipped(tokens, i, skip)], t) {
						b.WriteString(" ")
					}
				}
				b.WriteString("(")

				// Calculate subquery indentation
				closeInd := bodyIndent
				if len(caseStack) > 0 {
					closeInd = caseStack[len(caseStack)-1] + 2
				}

				savedCS := make([]int, len(caseStack))
				copy(savedCS, caseStack)
				subStack = append(subStack, subqueryCtx{
					baseDepth:    depth,
					closeIndent:  closeInd,
					clauseIndent: clauseIndent,
					bodyIndent:   bodyIndent,
					inSelectList: inSelectList,
					inWhere:      inWhere,
					caseStack:    savedCS,
				})

				clauseIndent = closeInd + 2
				bodyIndent = closeInd + 4
				inSelectList = false
				inWhere = false
				caseStack = nil
				suppressSpace = true
				continue
			}

			// Conditional paren: ( inside WHERE/HAVING that is not a function call and contains AND/OR
			if inWhere && len(caseStack) == 0 && i > 0 {
				prevIdx := prevNonSkipped(tokens, i, skip)
				prev := tokens[prevIdx]
				isFuncCall := prev.kind == token.TokenIdent || isFuncKeyword(prev)
				if !isFuncCall && hasAndOrAtDepth(tokens, i+1, depth) {
					outerInd := bodyIndent + (effectiveDepth()-1)*2
					condParenStack = append(condParenStack, condParen{
						depth:       depth,
						outerIndent: outerInd,
					})
					if !suppressSpace && needsSpace(tokens[prevIdx], t) {
						b.WriteString(" ")
					}
					b.WriteString("(")
					b.WriteString("\n")
					b.WriteString(ind(bodyIndent + effectiveDepth()*2))
					suppressSpace = true
					continue
				}
			}
		}

		// --- ) : close subquery + close conditional paren + decrement depth ---
		if string(t.kind) == ")" {
			if len(subStack) > 0 && depth == subStack[len(subStack)-1].baseDepth {
				ctx := subStack[len(subStack)-1]
				subStack = subStack[:len(subStack)-1]

				b.WriteString("\n")
				b.WriteString(ind(ctx.closeIndent))
				b.WriteString(")")

				clauseIndent = ctx.clauseIndent
				bodyIndent = ctx.bodyIndent
				inSelectList = ctx.inSelectList
				inWhere = ctx.inWhere
				caseStack = ctx.caseStack

				depth--
				if depth < 0 {
					depth = 0
				}
				suppressSpace = false
				continue
			}

			// Close conditional paren
			if len(condParenStack) > 0 && depth == condParenStack[len(condParenStack)-1].depth {
				cp := condParenStack[len(condParenStack)-1]
				condParenStack = condParenStack[:len(condParenStack)-1]
				b.WriteString("\n")
				b.WriteString(ind(cp.outerIndent))
				b.WriteString(")")
				depth--
				if depth < 0 {
					depth = 0
				}
				suppressSpace = false
				continue
			}

			depth--
			if depth < 0 {
				depth = 0
			}
		}

		// --- formatting at effectiveDepth == 0 ---
		if effectiveDepth() == 0 {
			// CASE handling
			if isKeywordLike(t, "CASE") {
				if len(caseStack) == 0 {
					// Top-level CASE: write at the current position
					if i > 0 && !suppressSpace {
						if needsSpace(tokens[prevNonSkipped(tokens, i, skip)], t) {
							b.WriteString(" ")
						}
					}
					b.WriteString(u)
					caseStack = append(caseStack, bodyIndent)
				} else {
					// Nested CASE
					nestedInd := caseStack[len(caseStack)-1] + 4
					b.WriteString("\n")
					b.WriteString(ind(nestedInd))
					b.WriteString(u)
					caseStack = append(caseStack, nestedInd)
				}
				suppressSpace = false
				continue
			}

			if len(caseStack) > 0 && isKeywordLike(t, "WHEN") {
				top := caseStack[len(caseStack)-1]
				b.WriteString("\n")
				b.WriteString(ind(top + 2))
				b.WriteString(u)
				suppressSpace = false
				continue
			}

			if len(caseStack) > 0 && isKeywordLike(t, "ELSE") {
				top := caseStack[len(caseStack)-1]
				b.WriteString("\n")
				b.WriteString(ind(top + 2))
				b.WriteString(u)
				suppressSpace = false
				continue
			}

			if len(caseStack) > 0 && isKeywordLike(t, "END") {
				top := caseStack[len(caseStack)-1]
				caseStack = caseStack[:len(caseStack)-1]
				b.WriteString("\n")
				b.WriteString(ind(top))
				b.WriteString(u)
				suppressSpace = false
				continue
			}

			// Regular clause keyword line breaks
			wrote := handleClauseBreak(&b, tokens, i, u, &inSelectList, &inWhere, skip, &suppressSpace, clauseIndent, bodyIndent)
			if wrote {
				continue
			}
		}

		// SELECT list comma (excluding inside CASE)
		if inSelectList && effectiveDepth() == 0 && len(caseStack) == 0 && string(t.kind) == "," {
			b.WriteString(",\n")
			b.WriteString(ind(bodyIndent))
			suppressSpace = true
			continue
		}

		// WHERE AND/OR (excluding inside CASE)
		if inWhere && len(caseStack) == 0 && (isKeywordLike(t, "AND") || isKeywordLike(t, "OR")) {
			andOrIndent := bodyIndent + effectiveDepth()*2
			b.WriteString("\n")
			b.WriteString(ind(andOrIndent))
			b.WriteString(u)
			suppressSpace = false
			continue
		}

		if i > 0 && !suppressSpace {
			if needsSpace(tokens[prevNonSkipped(tokens, i, skip)], t) {
				b.WriteString(" ")
			}
		}
		suppressSpace = false
		b.WriteString(u)
	}

	return strings.TrimSpace(b.String())
}

func prevNonSkipped(tokens []tok, i int, skip map[int]bool) int {
	for j := i - 1; j >= 0; j-- {
		if !skip[j] {
			return j
		}
	}
	return 0
}

// handleClauseBreak handles newline insertion for clause keywords.
func handleClauseBreak(b *strings.Builder, tokens []tok, i int, u string, inSelectList *bool, inWhere *bool, skip map[int]bool, suppressSpace *bool, clauseIndent int, bodyIndent int) bool {
	t := tokens[i]
	nlClause := "\n" + strings.Repeat(" ", clauseIndent)
	nlBody := "\n" + strings.Repeat(" ", bodyIndent)

	// GROUP BY / ORDER BY
	if isKeywordLike(t, "GROUP") || isKeywordLike(t, "ORDER") {
		if i+1 < len(tokens) && isKeywordLike(tokens[i+1], "BY") {
			*inSelectList = false
			*inWhere = false
			b.WriteString(nlClause)
			b.WriteString(u)
			b.WriteString(" BY")
			b.WriteString(nlBody)
			skip[i+1] = true
			*suppressSpace = true
			return true
		}
	}

	// JOIN modifiers + JOIN grouped on one line
	if joinModifiers[strings.ToUpper(t.raw)] && hasFollowingJoin(tokens, i) {
		*inSelectList = false
		*inWhere = false
		b.WriteString(nlClause)
		b.WriteString(u)
		for j := i + 1; j < len(tokens); j++ {
			if isKeywordLike(tokens[j], "JOIN") {
				b.WriteString(" JOIN")
				b.WriteString(nlBody)
				skip[j] = true
				break
			}
			if joinModifiers[strings.ToUpper(tokens[j].raw)] {
				b.WriteString(" ")
				b.WriteString(upper(tokens[j]))
				skip[j] = true
			}
		}
		*suppressSpace = true
		return true
	}

	// JOIN (without modifiers)
	if isKeywordLike(t, "JOIN") {
		*inSelectList = false
		*inWhere = false
		b.WriteString(nlClause)
		b.WriteString("JOIN")
		b.WriteString(nlBody)
		*suppressSpace = true
		return true
	}

	// Set operators
	if setOperators[u] {
		*inSelectList = false
		*inWhere = false
		b.WriteString("\n\n")
		b.WriteString(strings.Repeat(" ", clauseIndent))
		b.WriteString(u)
		if i+1 < len(tokens) && (isKeywordLike(tokens[i+1], "ALL") || isKeywordLike(tokens[i+1], "DISTINCT")) {
			b.WriteString(" ")
			b.WriteString(upper(tokens[i+1]))
			skip[i+1] = true
		}
		*suppressSpace = true
		return true
	}

	// OFFSET
	if strings.EqualFold(t.raw, "OFFSET") {
		*inSelectList = false
		*inWhere = false
		b.WriteString(nlClause)
		b.WriteString("OFFSET")
		b.WriteString(nlBody)
		*suppressSpace = true
		return true
	}

	// Regular clause keywords
	if clauseKeywords[u] {
		switch u {
		case "SELECT":
			*inSelectList = true
			*inWhere = false
			if i > 0 {
				b.WriteString(nlClause)
			} else if clauseIndent > 0 {
				b.WriteString(strings.Repeat(" ", clauseIndent))
			}
			b.WriteString(u)
			b.WriteString(nlBody)
		case "WHERE", "HAVING":
			*inSelectList = false
			*inWhere = true
			b.WriteString(nlClause)
			b.WriteString(u)
			b.WriteString(nlBody)
		default:
			*inSelectList = false
			*inWhere = false
			b.WriteString(nlClause)
			b.WriteString(u)
			b.WriteString(nlBody)
		}
		*suppressSpace = true
		return true
	}

	return false
}

func hasFollowingJoin(tokens []tok, start int) bool {
	for j := start + 1; j < len(tokens) && j <= start+3; j++ {
		if isKeywordLike(tokens[j], "JOIN") {
			return true
		}
		if !joinModifiers[strings.ToUpper(tokens[j].raw)] {
			break
		}
	}
	return false
}
