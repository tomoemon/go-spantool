package main

import (
	"strings"
	"testing"

	"github.com/cloudspannerecosystem/memefish"
)

func TestFormatSQL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple select",
			input: `SELECT u.UserID, u.Username FROM User u WHERE u.UserID = @userID`,
			want: `SELECT
  u.UserID,
  u.Username
FROM
  User u
WHERE
  u.UserID = @userID`,
		},
		{
			name:  "join with pagination",
			input: `SELECT u.UserID, u.Username, u.Email, u.CreatedAt, u.UpdatedAt FROM ` + "`Subscription`" + ` s JOIN User u ON s.TargetUserID = u.UserID WHERE s.SourceUserID = @sourceUserID ORDER BY s.CreatedAt DESC LIMIT @limit OFFSET @offset`,
			want: `SELECT
  u.UserID,
  u.Username,
  u.Email,
  u.CreatedAt,
  u.UpdatedAt
FROM
  ` + "`Subscription`" + ` s
JOIN
  User u
ON
  s.TargetUserID = u.UserID
WHERE
  s.SourceUserID = @sourceUserID
ORDER BY
  s.CreatedAt DESC
LIMIT
  @limit
OFFSET
  @offset`,
		},
		{
			name:  "count with join",
			input: `SELECT COUNT(*) AS cnt FROM Comment c JOIN Thread t ON c.ThreadID = t.ThreadID WHERE t.ProjectID = @projectID`,
			want: `SELECT
  COUNT(*) AS cnt
FROM
  Comment c
JOIN
  Thread t
ON
  c.ThreadID = t.ThreadID
WHERE
  t.ProjectID = @projectID`,
		},
		{
			name:  "where with AND",
			input: `SELECT * FROM Thread WHERE UserID = @userID AND (@includeFeatured IS NULL OR @includeFeatured = TRUE OR IsFeatured = FALSE) ORDER BY IsFeatured DESC, CreatedAt DESC LIMIT @limit OFFSET @offset`,
			want: `SELECT
  *
FROM
  Thread
WHERE
  UserID = @userID
  AND (
    @includeFeatured IS NULL
    OR @includeFeatured = TRUE
    OR IsFeatured = FALSE
  )
ORDER BY
  IsFeatured DESC, CreatedAt DESC
LIMIT
  @limit
OFFSET
  @offset`,
		},
		{
			name:  "left join",
			input: `SELECT t.TransactionID FROM Transaction t LEFT JOIN ChangeLog cl ON t.TransactionID = cl.TransactionID LEFT JOIN Item i ON cl.ItemID = i.ItemID WHERE t.UserID = @userID GROUP BY t.TransactionID ORDER BY t.CreatedAt DESC LIMIT @limit OFFSET @offset`,
			want: `SELECT
  t.TransactionID
FROM
  Transaction t
LEFT JOIN
  ChangeLog cl
ON
  t.TransactionID = cl.TransactionID
LEFT JOIN
  Item i
ON
  cl.ItemID = i.ItemID
WHERE
  t.UserID = @userID
GROUP BY
  t.TransactionID
ORDER BY
  t.CreatedAt DESC
LIMIT
  @limit
OFFSET
  @offset`,
		},
		{
			name:  "long select list with aggregates",
			input: `SELECT t.TransactionID, t.TransactionType, t.UserID, t.Note, t.CreatedAt, COALESCE(SUM(CASE WHEN i.ItemType = 'PAID' AND cl.Amount > 0 THEN cl.Amount ELSE 0 END), 0) AS IncrPaid, COALESCE(SUM(CASE WHEN i.ItemType = 'BONUS' AND cl.Amount > 0 THEN cl.Amount ELSE 0 END), 0) AS IncrBonus FROM Transaction t WHERE t.UserID = @userID`,
			want: `SELECT
  t.TransactionID,
  t.TransactionType,
  t.UserID,
  t.Note,
  t.CreatedAt,
  COALESCE(SUM(CASE WHEN i.ItemType = 'PAID' AND cl.Amount > 0 THEN cl.Amount ELSE 0 END), 0) AS IncrPaid,
  COALESCE(SUM(CASE WHEN i.ItemType = 'BONUS' AND cl.Amount > 0 THEN cl.Amount ELSE 0 END), 0) AS IncrBonus
FROM
  Transaction t
WHERE
  t.UserID = @userID`,
		},
		{
			name:  "union all",
			input: `SELECT a FROM t1 UNION ALL SELECT b FROM t2`,
			want: `SELECT
  a
FROM
  t1

UNION ALL
SELECT
  b
FROM
  t2`,
		},
		{
			name:  "keywords are uppercased",
			input: `select a, b from t where a = 1 and b = 2 order by a limit 10`,
			want: `SELECT
  a,
  b
FROM
  t
WHERE
  a = 1
  AND b = 2
ORDER BY
  a
LIMIT
  10`,
		},
		{
			name: "idempotent - already formatted",
			input: `SELECT
  u.UserID,
  u.Username
FROM
  User u
WHERE
  u.UserID = @userID`,
			want: `SELECT
  u.UserID,
  u.Username
FROM
  User u
WHERE
  u.UserID = @userID`,
		},
		{
			name:  "balance with aggregates",
			input: `SELECT COALESCE(SUM(CASE WHEN ItemType = 'PAID' THEN Balance ELSE 0 END), 0) AS PaidBalance, COALESCE(SUM(CASE WHEN ItemType = 'BONUS' THEN Balance ELSE 0 END), 0) AS BonusBalance, COALESCE(SUM(Balance), 0) AS TotalBalance FROM Item WHERE UserID = @userID AND Balance > 0`,
			want: `SELECT
  COALESCE(SUM(CASE WHEN ItemType = 'PAID' THEN Balance ELSE 0 END), 0) AS PaidBalance,
  COALESCE(SUM(CASE WHEN ItemType = 'BONUS' THEN Balance ELSE 0 END), 0) AS BonusBalance,
  COALESCE(SUM(Balance), 0) AS TotalBalance
FROM
  Item
WHERE
  UserID = @userID
  AND Balance > 0`,
		},
		{
			name:  "top-level CASE",
			input: `SELECT CASE status WHEN 'ACTIVE' THEN 1 WHEN 'INACTIVE' THEN 0 ELSE -1 END AS code FROM t`,
			want: `SELECT
  CASE status
    WHEN 'ACTIVE' THEN 1
    WHEN 'INACTIVE' THEN 0
    ELSE - 1
  END AS code
FROM
  t`,
		},
		{
			name:  "nested CASE with EXISTS subquery",
			input: `SELECT t.TransactionID, CASE t.TransactionType WHEN 'DEDUCTION' THEN CASE WHEN EXISTS(SELECT 1 FROM Attachment a WHERE a.TransactionID = t.TransactionID) THEN 'FILE_UPLOAD' WHEN EXISTS(SELECT 1 FROM CommentRevision cr WHERE cr.TransactionID = t.TransactionID AND cr.Action = 'GENERATE') THEN 'TEXT_GENERATE' ELSE '' END ELSE '' END AS UsageType FROM Transaction t WHERE t.UserID = @userID`,
			want: `SELECT
  t.TransactionID,
  CASE t.TransactionType
    WHEN 'DEDUCTION' THEN
      CASE
        WHEN EXISTS (
          SELECT
            1
          FROM
            Attachment a
          WHERE
            a.TransactionID = t.TransactionID
        ) THEN 'FILE_UPLOAD'
        WHEN EXISTS (
          SELECT
            1
          FROM
            CommentRevision cr
          WHERE
            cr.TransactionID = t.TransactionID
            AND cr.Action = 'GENERATE'
        ) THEN 'TEXT_GENERATE'
        ELSE ''
      END
    ELSE ''
  END AS UsageType
FROM
  Transaction t
WHERE
  t.UserID = @userID`,
		},
		{
			name:  "where with SEARCH_SUBSTRING OR",
			input: `SELECT ProjectID FROM SearchDocument WHERE Visibility = 'PUBLIC' AND (SEARCH_SUBSTRING(Tags_Tokens, @query) OR SEARCH_SUBSTRING(Authors_Tokens, @query) OR SEARCH_SUBSTRING(Title_Tokens, @query)) ORDER BY PublishedAt DESC LIMIT @limit OFFSET @offset`,
			want: `SELECT
  ProjectID
FROM
  SearchDocument
WHERE
  Visibility = 'PUBLIC'
  AND (
    SEARCH_SUBSTRING(Tags_Tokens, @query)
    OR SEARCH_SUBSTRING(Authors_Tokens, @query)
    OR SEARCH_SUBSTRING(Title_Tokens, @query)
  )
ORDER BY
  PublishedAt DESC
LIMIT
  @limit
OFFSET
  @offset`,
		},
		{
			name:  "where paren without AND/OR is not broken",
			input: `SELECT * FROM t WHERE (a + b) > 5`,
			want: `SELECT
  *
FROM
  t
WHERE
  (a + b) > 5`,
		},
		{
			name:  "subquery in FROM",
			input: `SELECT a FROM (SELECT a FROM t WHERE x = 1) AS sub WHERE a > 0`,
			want: `SELECT
  a
FROM
  (
    SELECT
      a
    FROM
      t
    WHERE
      x = 1
  ) AS sub
WHERE
  a > 0`,
		},
		{
			name:  "subquery in WHERE IN",
			input: `SELECT * FROM t WHERE id IN (SELECT id FROM t2 WHERE active = TRUE)`,
			want: `SELECT
  *
FROM
  t
WHERE
  id IN (
    SELECT
      id
    FROM
      t2
    WHERE
      active = TRUE
  )`,
		},
		{
			name:  "table hint FORCE_INDEX",
			input: `SELECT * FROM Singers@{FORCE_INDEX=SingersByName} WHERE SingerId = @id`,
			want: `SELECT
  *
FROM
  Singers@{FORCE_INDEX=SingersByName}
WHERE
  SingerId = @id`,
		},
		{
			name:  "table hint with whitespace in source",
			input: `SELECT * FROM Singers @ { FORCE_INDEX = SingersByName }`,
			want: `SELECT
  *
FROM
  Singers@{FORCE_INDEX=SingersByName}`,
		},
		{
			name:  "table hint multiple records",
			input: `SELECT * FROM Singers@{FORCE_INDEX=Idx1, JOIN_METHOD=HASH_JOIN}`,
			want: `SELECT
  *
FROM
  Singers@{FORCE_INDEX=Idx1, JOIN_METHOD=HASH_JOIN}`,
		},
		{
			name:  "statement hint preserves casing",
			input: `@{use_additional_parallelism=true} SELECT * FROM Singers`,
			want: `@{use_additional_parallelism=true}
SELECT
  *
FROM
  Singers`,
		},
		{
			name:  "join hint",
			input: `SELECT * FROM A JOIN @{JOIN_METHOD=HASH_JOIN} B ON A.id = B.id`,
			want: `SELECT
  *
FROM
  A
JOIN
  @{JOIN_METHOD=HASH_JOIN} B
ON
  A.id = B.id`,
		},
		{
			name:  "join ON with multiple conditions",
			input: `SELECT a.ID FROM A AS a LEFT JOIN B AS b ON b.AID = a.ID AND b.UserID = @userID AND b.Type IN UNNEST(@types) WHERE a.UserID = @userID`,
			want: `SELECT
  a.ID
FROM
  A AS a
LEFT JOIN
  B AS b
ON
  b.AID = a.ID
  AND b.UserID = @userID
  AND b.Type IN UNNEST(@types)
WHERE
  a.UserID = @userID`,
		},
		{
			name:  "EXISTS subqueries inside conditional paren",
			input: `SELECT a.ID FROM A AS a WHERE a.UserID = @userID AND (NOT EXISTS(SELECT 1 FROM C AS c WHERE c.AID = a.ID) OR EXISTS(SELECT 1 FROM C AS c WHERE c.AID = a.ID AND c.Type IN UNNEST(@types)))`,
			want: `SELECT
  a.ID
FROM
  A AS a
WHERE
  a.UserID = @userID
  AND (
    NOT EXISTS (
      SELECT
        1
      FROM
        C AS c
      WHERE
        c.AID = a.ID
    )
    OR EXISTS (
      SELECT
        1
      FROM
        C AS c
      WHERE
        c.AID = a.ID
        AND c.Type IN UNNEST(@types)
    )
  )`,
		},
		{
			name:  "IN with value list",
			input: `SELECT * FROM t WHERE id IN(1, 2, 3)`,
			want: `SELECT
  *
FROM
  t
WHERE
  id IN (1, 2, 3)`,
		},
		{
			name:  "CTE",
			input: `WITH tmp AS (SELECT r.ID AS ID FROM R AS r) SELECT ID FROM tmp`,
			want: `WITH tmp AS (
  SELECT
    r.ID AS ID
  FROM
    R AS r
)
SELECT
  ID
FROM
  tmp`,
		},
		{
			name:  "multiple CTEs",
			input: `WITH t1 AS (SELECT 1 AS x), t2 AS (SELECT x FROM t1) SELECT x FROM t2`,
			want: `WITH t1 AS (
  SELECT
    1 AS x
),
t2 AS (
  SELECT
    x
  FROM
    t1
)
SELECT
  x
FROM
  t2`,
		},
		{
			name:  "UNNEST WITH OFFSET is not a CTE",
			input: `SELECT v, off FROM UNNEST(@vals) AS v WITH OFFSET AS off`,
			want: `SELECT
  v,
  off
FROM
  UNNEST(@vals) AS v WITH OFFSET AS off`,
		},
		{
			name:  "array subscript OFFSET is not a clause",
			input: `SELECT a[OFFSET(0)], b[SAFE_OFFSET(1)] FROM t LIMIT 1 OFFSET 2`,
			want: `SELECT
  a[OFFSET(0)],
  b[SAFE_OFFSET(1)]
FROM
  t
LIMIT
  1
OFFSET
  2`,
		},
		{
			name:  "array literal keeps space before [",
			input: `SELECT * FROM t WHERE a = [1, 2]`,
			want: `SELECT
  *
FROM
  t
WHERE
  a = [1, 2]`,
		},
		{
			name:  "parameter array subscript",
			input: `SELECT @arr[OFFSET(0)] AS x FROM t`,
			want: `SELECT
  @arr[OFFSET(0)] AS x
FROM
  t`,
		},
		{
			name:  "BETWEEN AND stays on one line in ON and WHERE",
			input: `SELECT a FROM A JOIN B ON A.d BETWEEN B.s AND B.e AND A.x = B.x WHERE A.n BETWEEN 1 AND 10 OR A.m = 0`,
			want: `SELECT
  a
FROM
  A
JOIN
  B
ON
  A.d BETWEEN B.s AND B.e
  AND A.x = B.x
WHERE
  A.n BETWEEN 1 AND 10
  OR A.m = 0`,
		},
		{
			name:  "INSERT ON CONFLICT is not a JOIN ON",
			input: `INSERT INTO foo (x, y) VALUES (1, 2) ON CONFLICT ON UNIQUE CONSTRAINT foo_x DO NOTHING`,
			want: `INSERT
INTO
  foo(x, y)
VALUES
  (1, 2) ON CONFLICT ON UNIQUE CONSTRAINT foo_x DO NOTHING`,
		},
		{
			name:  "insert values",
			input: `INSERT INTO Singers (SingerId, Name) VALUES (1, 'a'), (2, 'b')`,
			want: `INSERT
INTO
  Singers(SingerId, Name)
VALUES
  (1, 'a'), (2, 'b')`,
		},
		{
			name:  "insert select",
			input: `INSERT INTO Singers (SingerId, Name) SELECT SingerId, Name FROM Tmp WHERE x = 1`,
			want: `INSERT
INTO
  Singers(SingerId, Name)
SELECT
  SingerId,
  Name
FROM
  Tmp
WHERE
  x = 1`,
		},
		{
			name:  "insert or update",
			input: `INSERT OR UPDATE INTO Singers (SingerId) VALUES (1)`,
			want: `INSERT OR UPDATE
INTO
  Singers(SingerId)
VALUES
  (1)`,
		},
		{
			name:  "update set where",
			input: `UPDATE Singers SET Name = @name, Age = 3 WHERE SingerId = @id AND Age > 1`,
			want: `UPDATE Singers
SET
  Name = @name, Age = 3
WHERE
  SingerId = @id
  AND Age > 1`,
		},
		{
			name:  "delete where",
			input: `DELETE FROM Singers WHERE SingerId = @id`,
			want: `DELETE
FROM
  Singers
WHERE
  SingerId = @id`,
		},
		{
			name:  "update then return",
			input: `UPDATE Singers SET Name = 'x' WHERE TRUE THEN RETURN SingerId, Name`,
			want: `UPDATE Singers
SET
  Name = 'x'
WHERE
  TRUE THEN RETURN SingerId, Name`,
		},
		{
			name:  "group by having",
			input: `SELECT UserID, COUNT(*) AS cnt FROM Post GROUP BY UserID HAVING COUNT(*) > 1 AND MAX(Score) < 10`,
			want: `SELECT
  UserID,
  COUNT(*) AS cnt
FROM
  Post
GROUP BY
  UserID
HAVING
  COUNT(*) > 1
  AND MAX(Score) < 10`,
		},
		{
			name:  "array subquery in select list",
			input: `SELECT ARRAY(SELECT Name FROM Tag WHERE Tag.PostID = p.PostID) AS Tags FROM Post p`,
			want: `SELECT
  ARRAY(
    SELECT
      Name
    FROM
      Tag
    WHERE
      Tag.PostID = p.PostID
  ) AS Tags
FROM
  Post p`,
		},
		{
			name:  "scalar subquery in select list",
			input: `SELECT p.ID, (SELECT COUNT(*) FROM Comment c WHERE c.PostID = p.ID) AS cnt FROM Post p`,
			want: `SELECT
  p.ID,
  (
    SELECT
      COUNT(*)
    FROM
      Comment c
    WHERE
      c.PostID = p.ID
  ) AS cnt
FROM
  Post p`,
		},
		{
			name:  "nested conditional parens",
			input: `SELECT * FROM t WHERE a = 1 AND (b = 2 OR (c = 3 AND d = 4))`,
			want: `SELECT
  *
FROM
  t
WHERE
  a = 1
  AND (
    b = 2
    OR (
      c = 3
      AND d = 4
    )
  )`,
		},
		{
			name:  "NOT conditional paren",
			input: `SELECT * FROM t WHERE NOT (a = 1 OR b = 2)`,
			want: `SELECT
  *
FROM
  t
WHERE
  NOT (
    a = 1
    OR b = 2
  )`,
		},
		{
			name:  "set operation in FROM subquery",
			input: `SELECT * FROM (SELECT a FROM t1 UNION ALL SELECT a FROM t2) AS u WHERE a > 0`,
			want: `SELECT
  *
FROM
  (
    SELECT
      a
    FROM
      t1

    UNION ALL
    SELECT
      a
    FROM
      t2
  ) AS u
WHERE
  a > 0`,
		},
		{
			name:  "CASE in WHERE",
			input: `SELECT * FROM t WHERE CASE WHEN a = 1 THEN TRUE ELSE FALSE END AND b = 2`,
			want: `SELECT
  *
FROM
  t
WHERE
  CASE
    WHEN a = 1 THEN TRUE
    ELSE FALSE
  END
  AND b = 2`,
		},
		{
			name:  "parenthesized AND compared to value",
			input: `SELECT * FROM t WHERE (a AND b) = TRUE`,
			want: `SELECT
  *
FROM
  t
WHERE
  (a AND b) = TRUE`,
		},
		{
			name:  "cross join",
			input: `SELECT * FROM t1 CROSS JOIN t2`,
			want: `SELECT
  *
FROM
  t1
CROSS JOIN
  t2`,
		},
		{
			name:  "full outer join using",
			input: `SELECT * FROM a FULL OUTER JOIN b USING (id)`,
			want: `SELECT
  *
FROM
  a
FULL OUTER JOIN
  b USING (id)`,
		},
		{
			name:  "comma join",
			input: `SELECT * FROM t1, t2 WHERE t1.id = t2.id`,
			want: `SELECT
  *
FROM
  t1, t2
WHERE
  t1.id = t2.id`,
		},
		{
			name:  "select distinct",
			input: `SELECT DISTINCT a FROM t`,
			want: `SELECT
  DISTINCT a
FROM
  t`,
		},
		{
			name:  "intersect distinct",
			input: `SELECT a FROM t1 INTERSECT DISTINCT SELECT a FROM t2`,
			want: `SELECT
  a
FROM
  t1

INTERSECT DISTINCT
SELECT
  a
FROM
  t2`,
		},
		{
			name:  "IN subquery and IN UNNEST",
			input: `SELECT a FROM t WHERE x IN (SELECT y FROM u) AND z IN UNNEST(@zs)`,
			want: `SELECT
  a
FROM
  t
WHERE
  x IN (
    SELECT
      y
    FROM
      u
  )
  AND z IN UNNEST(@zs)`,
		},
		{
			name:  "IF with AND in select list and WHERE",
			input: `SELECT IF(a AND b, 1, 0) AS v FROM t WHERE IF(a AND b, TRUE, FALSE)`,
			want: `SELECT
  IF(a AND b, 1, 0) AS v
FROM
  t
WHERE
  IF(a AND b, TRUE, FALSE)`,
		},
		{
			name:  "EXISTS subquery after AND",
			input: `SELECT * FROM t WHERE a = 1 AND EXISTS (SELECT 1 FROM u WHERE u.x = t.x)`,
			want: `SELECT
  *
FROM
  t
WHERE
  a = 1
  AND EXISTS (
    SELECT
      1
    FROM
      u
    WHERE
      u.x = t.x
  )`,
		},
		{
			name:  "join subquery",
			input: `SELECT * FROM t JOIN (SELECT id FROM u) AS s ON s.id = t.id`,
			want: `SELECT
  *
FROM
  t
JOIN
  (
    SELECT
      id
    FROM
      u
  ) AS s
ON
  s.id = t.id`,
		},
		{
			name:  "parenthesized join",
			input: `SELECT * FROM (a JOIN b ON a.id = b.id) JOIN c ON c.id = a.id`,
			want: `SELECT
  *
FROM
  (a JOIN b ON a.id = b.id)
JOIN
  c
ON
  c.id = a.id`,
		},
		{
			name:  "ON with conditional paren",
			input: `SELECT * FROM a JOIN b ON a.id = b.id AND (b.x = 1 OR b.y = 2)`,
			want: `SELECT
  *
FROM
  a
JOIN
  b
ON
  a.id = b.id
  AND (
    b.x = 1
    OR b.y = 2
  )`,
		},
		{
			name:  "ORDER BY and LIMIT in subquery",
			input: `SELECT * FROM (SELECT a FROM t ORDER BY a LIMIT 1) AS s`,
			want: `SELECT
  *
FROM
  (
    SELECT
      a
    FROM
      t
    ORDER BY
      a
    LIMIT
      1
  ) AS s`,
		},
		{
			name:  "CTE with set operation",
			input: `WITH a AS (SELECT 1 AS x) SELECT x FROM a UNION ALL SELECT x FROM a`,
			want: `WITH a AS (
  SELECT
    1 AS x
)
SELECT
  x
FROM
  a

UNION ALL
SELECT
  x
FROM
  a`,
		},
		{
			name:  "mixed AND OR precedence",
			input: `SELECT * FROM t WHERE a = 1 OR b = 2 AND c = 3`,
			want: `SELECT
  *
FROM
  t
WHERE
  a = 1
  OR b = 2
  AND c = 3`,
		},
		{
			name:  "delete then return",
			input: `DELETE FROM Singers WHERE SingerId = 1 THEN RETURN SingerId`,
			want: `DELETE
FROM
  Singers
WHERE
  SingerId = 1 THEN RETURN SingerId`,
		},
		{
			name:  "table hint with IN list",
			input: `SELECT * FROM t@{FORCE_INDEX=Idx} WHERE a IN (1, 2) AND b = @b`,
			want: `SELECT
  *
FROM
  t@{FORCE_INDEX=Idx}
WHERE
  a IN (1, 2)
  AND b = @b`,
		},
		{
			name:  "IN subquery with AND followed by ORDER BY",
			input: `SELECT a FROM t WHERE b IN (SELECT b FROM u WHERE c = 1 AND d = 2) ORDER BY a`,
			want: `SELECT
  a
FROM
  t
WHERE
  b IN (
    SELECT
      b
    FROM
      u
    WHERE
      c = 1
      AND d = 2
  )
ORDER BY
  a`,
		},
		{
			name: "trailing line comments stay on their line",
			input: `SELECT a, -- first
  b -- second
FROM t -- table
WHERE x = 1 -- cond
  AND y = 2`,
			want: `SELECT
  a, -- first
  b -- second
FROM
  t -- table
WHERE
  x = 1 -- cond
  AND y = 2`,
		},
		{
			name: "own-line comments stay before the next token",
			input: `-- header
SELECT a
-- from clause
FROM t
/* block */ WHERE x = 1`,
			want: `-- header
SELECT
  a
-- from clause
FROM
  t
/* block */
WHERE
  x = 1`,
		},
		{
			name: "line comment in the middle of a line breaks it",
			input: `SELECT COALESCE(a, -- fallback
 b) AS v FROM t`,
			want: `SELECT
  COALESCE(a, -- fallback
  b) AS v
FROM
  t`,
		},
		{
			name:  "inline block comments",
			input: `SELECT /* a */ /* b */ x /* inline */ + y FROM t`,
			want: `SELECT /* a */ /* b */
  x /* inline */ + y
FROM
  t`,
		},
		{
			name: "comments at the end",
			input: `SELECT a FROM t -- trailing
-- own line
# hash`,
			want: `SELECT
  a
FROM
  t -- trailing
-- own line
# hash`,
		},
		{
			name: "comments in subquery and condition group",
			input: `SELECT * FROM t WHERE EXISTS (
  -- inside
  SELECT 1 FROM u -- u
  WHERE u.x = t.x
) AND (a = 1 -- a
 OR b = 2)`,
			want: `SELECT
  *
FROM
  t
WHERE
  EXISTS (
    -- inside
    SELECT
      1
    FROM
      u -- u
    WHERE
      u.x = t.x
  )
  AND (
    a = 1 -- a
    OR b = 2
  )`,
		},
		{
			name: "multi-line block comment is kept as written",
			input: `SELECT a FROM t /* multi
   line */ WHERE b = 1`,
			want: `SELECT
  a
FROM
  t /* multi
   line */
WHERE
  b = 1`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FormatSQL(tt.input)
			if err != nil {
				t.Fatalf("FormatSQL() error: %v", err)
			}
			if got != tt.want {
				t.Errorf("FormatSQL() mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, tt.want)
			}

			// Formatting the output again must not change it
			again, err := FormatSQL(got)
			if err != nil {
				t.Fatalf("FormatSQL() on formatted output error: %v", err)
			}
			if again != got {
				t.Errorf("FormatSQL() is not idempotent:\n--- first ---\n%s\n--- second ---\n%s", got, again)
			}
		})
	}
}

func TestFormatSQL_SyntaxError(t *testing.T) {
	_, err := FormatSQL("SELEC 10")
	if err == nil {
		t.Fatal("expected syntax error but got nil")
	}
}

func TestVerifyEquivalent(t *testing.T) {
	orig, err := memefish.ParseStatement("", "SELECT a FROM t WHERE a = 1 AND b = 2")
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyEquivalent(orig, "SELECT\n  a\nFROM\n  t\nWHERE\n  a = 1\n  AND b = 2"); err != nil {
		t.Errorf("expected equivalent SQL to pass, got: %v", err)
	}
	if err := verifyEquivalent(orig, "SELECT a FROM t WHERE a = 1 OR b = 2"); err == nil || !strings.Contains(err.Error(), "changed the meaning") {
		t.Errorf("expected meaning change to be detected, got: %v", err)
	}
	if err := verifyEquivalent(orig, "SELECT a FROM"); err == nil || !strings.Contains(err.Error(), "no longer valid") {
		t.Errorf("expected invalid SQL to be detected, got: %v", err)
	}
}

func TestVerifyComments(t *testing.T) {
	if err := verifyComments([]string{"-- a", "/* b */"}, "SELECT 1 -- a\n/* b */"); err != nil {
		t.Errorf("expected same comments to pass, got: %v", err)
	}
	if err := verifyComments([]string{"-- a", "/* b */"}, "SELECT 1 -- a"); err == nil || !strings.Contains(err.Error(), "did not keep the comments") {
		t.Errorf("expected a lost comment to be detected, got: %v", err)
	}
}
