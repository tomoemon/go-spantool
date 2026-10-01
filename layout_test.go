package main

import (
	"fmt"
	"slices"
	"testing"

	"github.com/cloudspannerecosystem/memefish"
)

// describeLayout lists the layout marks of sql as "role:token" strings in token order.
func describeLayout(t *testing.T, sql string) []string {
	t.Helper()
	stmt, err := memefish.ParseStatement("", sql)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := tokenize(sql)
	if err != nil {
		t.Fatal(err)
	}
	lay := buildLayout(stmt, tokens)
	var marks []string
	for i, tk := range tokens {
		if c, ok := lay.clauses[i]; ok {
			marks = append(marks, fmt.Sprintf("clause%d:%s..%s", c.kind, tk.raw, tokens[c.last].raw))
		}
		if lay.condOps[i] {
			marks = append(marks, "cond:"+tk.raw)
		}
		if _, ok := lay.condGroups[i]; ok {
			marks = append(marks, "group:(")
		}
		if _, ok := lay.subqueries[i]; ok {
			marks = append(marks, "subquery:(")
		}
		if k, ok := lay.listSeps[i]; ok {
			marks = append(marks, fmt.Sprintf("sep%d:,", k))
		}
		if c, ok := lay.cases[i]; ok {
			marks = append(marks, fmt.Sprintf("case%d:%s", c.part, tk.raw))
		}
		if _, ok := lay.hints[i]; ok {
			marks = append(marks, "hint:@")
		}
	}
	return marks
}

func TestBuildLayout(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want []string
	}{
		{
			name: "BETWEEN AND is not a condition separator",
			sql:  `SELECT a, b FROM A JOIN B ON A.d BETWEEN B.s AND B.e AND A.x = B.x`,
			want: []string{"clause0:SELECT..SELECT", "sep0:,", "clause0:FROM..FROM", "clause0:JOIN..JOIN", "clause0:ON..ON", "cond:AND"},
		},
		{
			name: "join modifiers and GROUP BY are one clause each",
			sql:  `SELECT a FROM A LEFT OUTER JOIN B ON A.x = B.x GROUP BY a HAVING COUNT(*) > 1 OR a = 0`,
			want: []string{"clause0:SELECT..SELECT", "clause0:FROM..FROM", "clause0:LEFT..JOIN", "clause0:ON..ON", "clause0:GROUP..BY", "clause0:HAVING..HAVING", "cond:OR"},
		},
		{
			name: "ON CONFLICT is not a join condition",
			sql:  `INSERT INTO foo (x) VALUES (1) ON CONFLICT ON UNIQUE CONSTRAINT foo_x DO NOTHING`,
			want: []string{"clause0:INTO..INTO", "clause0:VALUES..VALUES"},
		},
		{
			name: "OFFSET is a clause only in LIMIT",
			sql:  `SELECT a[OFFSET(0)] FROM UNNEST(@v) AS a WITH OFFSET AS o LIMIT 1 OFFSET 2`,
			want: []string{"clause0:SELECT..SELECT", "clause0:FROM..FROM", "clause0:LIMIT..LIMIT", "clause0:OFFSET..OFFSET"},
		},
		{
			name: "CTE list and set operation",
			sql:  `WITH a AS (SELECT 1 AS x), b AS (SELECT 2 AS x) SELECT x FROM a UNION ALL SELECT x FROM b`,
			want: []string{"clause2:WITH..WITH", "subquery:(", "clause0:SELECT..SELECT", "sep1:,", "subquery:(", "clause0:SELECT..SELECT", "clause0:SELECT..SELECT", "clause0:FROM..FROM", "clause1:UNION..ALL", "clause0:SELECT..SELECT", "clause0:FROM..FROM"},
		},
		{
			name: "parenthesized join stays inline but its subqueries do not",
			sql:  `SELECT * FROM (a JOIN (SELECT id FROM u) AS b ON a.id = b.id AND a.x = 1)`,
			want: []string{"clause0:SELECT..SELECT", "clause0:FROM..FROM", "subquery:(", "clause0:SELECT..SELECT", "clause0:FROM..FROM"},
		},
		{
			name: "only operands of AND / OR are conditions",
			sql:  `SELECT * FROM t WHERE NOT (a OR b) AND IF(c AND d, TRUE, FALSE) AND (e AND f) = TRUE`,
			want: []string{"clause0:SELECT..SELECT", "clause0:FROM..FROM", "clause0:WHERE..WHERE", "group:(", "cond:OR", "cond:AND", "cond:AND"},
		},
		{
			name: "EXISTS subquery and CASE",
			sql:  `SELECT CASE WHEN EXISTS (SELECT 1 FROM u) THEN 1 ELSE 0 END FROM t@{FORCE_INDEX=i}`,
			want: []string{"clause0:SELECT..SELECT", "case0:CASE", "case1:WHEN", "subquery:(", "clause0:SELECT..SELECT", "clause0:FROM..FROM", "case2:ELSE", "case3:END", "clause0:FROM..FROM", "hint:@"},
		},
		{
			name: "DML clauses",
			sql:  `UPDATE t SET a = 1, b = 2 WHERE c = 3 AND d = 4`,
			want: []string{"clause0:SET..SET", "clause0:WHERE..WHERE", "cond:AND"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := describeLayout(t, tt.sql)
			if !slices.Equal(got, tt.want) {
				t.Errorf("layout mismatch:\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}
