package sample

const listSubscriptions = `SELECT u.UserID, u.Username, u.Email, u.CreatedAt, u.UpdatedAt FROM` + " `Subscription`" + ` s JOIN User u ON s.TargetUserID = u.UserID WHERE s.SourceUserID = @sourceUserID ORDER BY s.CreatedAt DESC LIMIT @limit OFFSET @offset`

const countThreadComments = `
SELECT count(*) AS cnt
FROM Comment c
JOIN Thread t ON c.ThreadID = t.ThreadID
WHERE t.ProjectID = @projectID
`

const notSQL = `This is not SQL, just a regular string`

var dynamicString = "SELECT * FROM t" // double-quoted strings are not targeted
