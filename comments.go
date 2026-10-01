package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/cloudspannerecosystem/memefish/token"
)

// commentText returns the comment as written, without the line break that
// ends a line comment.
func commentText(c token.TokenComment) string {
	if isLineComment(c) {
		return strings.TrimRight(c.Raw, " \t\r\n")
	}
	return c.Raw
}

// isLineComment reports whether c is a `--` or `#` comment, which runs to
// the end of the line.
func isLineComment(c token.TokenComment) bool {
	return !strings.HasPrefix(c.Raw, "/*")
}

// commentTexts returns the text of all comments in tokens and eof, in order.
func commentTexts(tokens []tok, eof []token.TokenComment) []string {
	var texts []string
	for _, t := range tokens {
		for _, c := range t.comments {
			texts = append(texts, commentText(c))
		}
	}
	for _, c := range eof {
		texts = append(texts, commentText(c))
	}
	return texts
}

type insertion struct {
	at   int
	text string
}

// placeComments inserts the comments of tokens, and the comments before the
// end of input (eof), into out, where tokens[i] was written at
// out[starts[i]:ends[i]].
//
// A comment that followed the previous token on the same line is appended to
// the line of the previous token. A comment on its own line is put on its own
// line before the token, at the token's indentation. When the token is in the
// middle of a line, the comment is put just before it, and a line comment
// breaks the line.
func placeComments(out string, tokens []tok, eof []token.TokenComment, starts, ends []int) string {
	var ins []insertion
	for i, t := range tokens {
		if len(t.comments) == 0 {
			continue
		}
		p := starts[i]
		lineStart := strings.LastIndex(out[:p], "\n") + 1
		atLineStart := strings.TrimLeft(out[lineStart:p], " ") == ""
		rest := out[lineStart:]
		lineIndent := rest[:len(rest)-len(strings.TrimLeft(rest, " "))]
		sameLine := i > 0 // still on the line of the previous token
		for _, c := range t.comments {
			text := commentText(c)
			if strings.Contains(c.Space, "\n") {
				sameLine = false
			}
			switch {
			case sameLine && atLineStart:
				ins = append(ins, insertion{ends[i-1], " " + text})
			case atLineStart:
				ins = append(ins, insertion{p, text + "\n" + out[lineStart:p]})
			default:
				if p > 0 && !strings.ContainsRune(" (\n", rune(out[p-1])) {
					text = " " + text
				}
				if isLineComment(c) {
					ins = append(ins, insertion{p, text + "\n" + lineIndent})
				} else {
					ins = append(ins, insertion{p, text + " "})
				}
			}
			if isLineComment(c) {
				sameLine = false
			}
		}
	}

	if len(tokens) > 0 {
		end := ends[len(tokens)-1]
		sameLine := true
		for _, c := range eof {
			if strings.Contains(c.Space, "\n") {
				sameLine = false
			}
			if sameLine {
				ins = append(ins, insertion{end, " " + commentText(c)})
			} else {
				ins = append(ins, insertion{end, "\n" + commentText(c)})
			}
			if isLineComment(c) {
				sameLine = false
			}
		}
	}

	sort.SliceStable(ins, func(a, b int) bool { return ins[a].at < ins[b].at })
	var b strings.Builder
	last := 0
	for _, in := range ins {
		b.WriteString(out[last:in.at])
		b.WriteString(in.text)
		last = in.at
	}
	b.WriteString(out[last:])
	return b.String()
}

// verifyComments checks that the formatted SQL has the same comments, in the
// same order, as the original.
func verifyComments(want []string, formatted string) error {
	tokens, eof, err := tokenize(formatted)
	if err != nil {
		return fmt.Errorf("formatted SQL can no longer be tokenized (%v); %s\nformatted SQL:\n%s", err, reportBug, formatted)
	}
	if got := commentTexts(tokens, eof); !slices.Equal(want, got) {
		return fmt.Errorf("formatting did not keep the comments; %s\nexpected comments: %q\nactual comments:   %q", reportBug, want, got)
	}
	return nil
}
