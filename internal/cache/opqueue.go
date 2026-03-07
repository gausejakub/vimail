package cache

import (
	"database/sql"
	"encoding/json"
	"time"
)

// OpType identifies the kind of queued operation.
type OpType string

const (
	OpDelete    OpType = "delete"
	OpSend      OpType = "send"
	OpMarkRead  OpType = "mark_read"
)

// OpStatus tracks the lifecycle of a queued operation.
type OpStatus string

const (
	OpPending   OpStatus = "pending"
	OpRunning   OpStatus = "running"
	OpCompleted OpStatus = "completed"
	OpFailed    OpStatus = "failed"
)

// QueuedOp represents a persisted operation.
type QueuedOp struct {
	ID        int64
	Type      OpType
	Status    OpStatus
	Account   string
	Folder    string
	Payload   json.RawMessage
	Error     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// DeletePayload is the JSON payload for delete operations.
type DeletePayload struct {
	UIDs []uint32 `json:"uids"`
}

// SendPayload is the JSON payload for send operations.
type SendPayload struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// MarkReadPayload is the JSON payload for mark-read operations.
type MarkReadPayload struct {
	UIDs []uint32 `json:"uids"`
}

// QueueOp persists a new operation and returns its ID.
func (s *SQLiteStore) QueueOp(opType OpType, account, folder string, payload interface{}) (int64, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	now := time.Now().Format(time.RFC3339)
	res, err := s.db.Exec(`
		INSERT INTO pending_ops (type, status, account, folder, payload, error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, '', ?, ?)
	`, string(opType), string(OpPending), account, folder, string(data), now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// StartOp marks an operation as running.
func (s *SQLiteStore) StartOp(id int64) {
	now := time.Now().Format(time.RFC3339)
	s.db.Exec(`UPDATE pending_ops SET status = ?, updated_at = ? WHERE id = ?`,
		string(OpRunning), now, id)
}

// CompleteOp marks an operation as completed.
func (s *SQLiteStore) CompleteOp(id int64) {
	now := time.Now().Format(time.RFC3339)
	s.db.Exec(`UPDATE pending_ops SET status = ?, updated_at = ? WHERE id = ?`,
		string(OpCompleted), now, id)
}

// FailOp marks an operation as failed with an error message.
func (s *SQLiteStore) FailOp(id int64, errMsg string) {
	now := time.Now().Format(time.RFC3339)
	s.db.Exec(`UPDATE pending_ops SET status = ?, error = ?, updated_at = ? WHERE id = ?`,
		string(OpFailed), errMsg, now, id)
}

// PendingOps returns all operations that are pending or running (need retry).
func (s *SQLiteStore) PendingOps() []QueuedOp {
	return s.queryOps(`SELECT id, type, status, account, folder, payload, error, created_at, updated_at
		FROM pending_ops WHERE status IN (?, ?) ORDER BY created_at`,
		string(OpPending), string(OpRunning))
}

// RecentOps returns the most recent operations (for the :ops log view).
func (s *SQLiteStore) RecentOps(limit int) []QueuedOp {
	return s.queryOps(`SELECT id, type, status, account, folder, payload, error, created_at, updated_at
		FROM pending_ops ORDER BY created_at DESC LIMIT ?`, limit)
}

// CleanupOldOps removes completed/failed operations older than the given duration.
func (s *SQLiteStore) CleanupOldOps(maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge).Format(time.RFC3339)
	s.db.Exec(`DELETE FROM pending_ops WHERE status IN (?, ?) AND updated_at < ?`,
		string(OpCompleted), string(OpFailed), cutoff)
}

func (s *SQLiteStore) queryOps(query string, args ...interface{}) []QueuedOp {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var ops []QueuedOp
	for rows.Next() {
		var op QueuedOp
		var opType, status, payload, createdAt, updatedAt string
		var errStr sql.NullString
		if err := rows.Scan(&op.ID, &opType, &status, &op.Account, &op.Folder, &payload, &errStr, &createdAt, &updatedAt); err != nil {
			continue
		}
		op.Type = OpType(opType)
		op.Status = OpStatus(status)
		op.Payload = json.RawMessage(payload)
		if errStr.Valid {
			op.Error = errStr.String
		}
		op.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		op.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		ops = append(ops, op)
	}
	return ops
}
