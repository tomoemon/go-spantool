package main

import (
	"testing"
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
  User u ON s.TargetUserID = u.UserID
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
  Thread t ON c.ThreadID = t.ThreadID
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
  ChangeLog cl ON t.TransactionID = cl.TransactionID
LEFT JOIN
  Item i ON cl.ItemID = i.ItemID
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
        WHEN EXISTS(
          SELECT
            1
          FROM
            Attachment a
          WHERE
            a.TransactionID = t.TransactionID
        ) THEN 'FILE_UPLOAD'
        WHEN EXISTS(
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
  id IN(
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
  @{JOIN_METHOD=HASH_JOIN} B ON A.id = B.id`,
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
		})
	}
}

func TestFormatSQL_SyntaxError(t *testing.T) {
	_, err := FormatSQL("SELEC 10")
	if err == nil {
		t.Fatal("expected syntax error but got nil")
	}
}
