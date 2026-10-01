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
		indent := strings.Repeat(" ", lineIndent(out[:p]))
		for k, c := range t.comments {
			text := commentText(c)
			switch {
			case i > 0 && atLineStart && onPrevLine(t.comments, k):
				ins = append(ins, insertion{ends[i-1], " " + text})
			case atLineStart:
				ins = append(ins, insertion{p, text + "\n" + indent})
			default:
				if p > 0 && !strings.ContainsRune(" (\n", rune(out[p-1])) {
					text = " " + text
				}
				if isLineComment(c) {
					ins = append(ins, insertion{p, text + "\n" + indent})
				} else {
					ins = append(ins, insertion{p, text + " "})
				}
			}
		}
	}
	if len(tokens) > 0 {
		end := ends[len(tokens)-1]
		for k, c := range eof {
			if onPrevLine(eof, k) {
				ins = append(ins, insertion{end, " " + commentText(c)})
			} else {
				ins = append(ins, insertion{end, "\n" + commentText(c)})
			}
		}
	}
	if len(ins) == 0 {
		return out
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

// onPrevLine reports whether comments[k] is on the same line as the token
// before the comments, i.e. no line break comes before it.
func onPrevLine(comments []token.TokenComment, k int) bool {
	for _, c := range comments[:k+1] {
		if strings.Contains(c.Space, "\n") {
			return false
		}
	}
	for _, c := range comments[:k] {
		if isLineComment(c) {
			return false
		}
	}
	return true
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
