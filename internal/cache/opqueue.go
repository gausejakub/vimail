package cache

import (
	"database/sql"
	"encoding/json"
	"time"
)

// OpType identifies the kind of queued operation.
type OpType string

const (
	OpDelete   OpType = "delete"
	OpSend     OpType = "send"
	OpMarkRead OpType = "mark_read"
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
	ID            int64
	Type          OpType
	Status        OpStatus
	Account       string
	Folder        string
	Payload       json.RawMessage
	Error         string
	Attempts      int
	NextAttemptAt time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
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

// opLeaseDuration is how long a claimed (running) op belongs to its owner.
// If the owner crashes without completing or failing the op, another process
// may reclaim it once the lease expires. Generous enough to cover slow batch
// deletes and sends, short enough that crashed work is retried promptly.
const (
	opLeaseDuration  = 10 * time.Minute
	opMaxAttempts    = 8
	opRetryBaseDelay = 5 * time.Second
	opRetryMaxDelay  = 15 * time.Minute
)

// StartOp attempts to claim an operation for this process and reports
// whether the claim succeeded. The claim is a compare-and-swap: it succeeds
// only when the op is pending, failed (retry), or running with an expired
// lease (crashed owner). A live owner's op is never stolen. Callers must
// skip execution when StartOp returns false — another process owns the op.
func (s *SQLiteStore) StartOp(id int64) bool {
	return s.startOp(id, false)
}

// StartOpNow is StartOp with retry backoff bypassed. Full user-requested
// account syncs use it so an explicit sync attempts every queued write now.
// The attempt cap and cross-process lease rules still apply.
func (s *SQLiteStore) StartOpNow(id int64) bool {
	return s.startOp(id, true)
}

func (s *SQLiteStore) startOp(id int64, forceRetry bool) bool {
	now := time.Now().UTC()
	force := 0
	if forceRetry {
		force = 1
	}
	res, err := s.db.Exec(`UPDATE pending_ops
		SET status = ?, owner = ?, lease_until = ?, attempts = attempts + 1, next_attempt_at = NULL, updated_at = ?
		WHERE id = ? AND (
			status = ?
			OR (status = ? AND attempts < ? AND (? = 1 OR next_attempt_at IS NULL OR next_attempt_at <= ?))
			OR (status = ? AND (lease_until IS NULL OR lease_until < ?))
		)`,
		string(OpRunning), s.procID,
		now.Add(opLeaseDuration).Format(time.RFC3339), now.Format(time.RFC3339),
		id,
		string(OpPending), string(OpFailed), opMaxAttempts, force, now.Format(time.RFC3339),
		string(OpRunning), now.Format(time.RFC3339))
	if err != nil {
		return false
	}
	n, err := res.RowsAffected()
	return err == nil && n > 0
}

// CompleteOp marks an operation as completed. The update is a no-op if
// another process reclaimed the op after our lease expired (owner mismatch),
// so a late finisher cannot stomp the new owner's state.
func (s *SQLiteStore) CompleteOp(id int64) {
	now := time.Now().UTC().Format(time.RFC3339)
	s.db.Exec(`UPDATE pending_ops SET status = ?, owner = '', lease_until = NULL, updated_at = ?
		WHERE id = ? AND (owner = ? OR owner = '')`,
		string(OpCompleted), now, id, s.procID)
}

// FailOp marks an operation as failed with an error message. Failed ops
// remain retryable via PendingOps. Owner-guarded like CompleteOp.
func (s *SQLiteStore) FailOp(id int64, errMsg string) {
	now := time.Now().UTC()
	var attempts int
	if err := s.db.QueryRow(`SELECT attempts FROM pending_ops WHERE id = ? AND (owner = ? OR owner = '')`, id, s.procID).Scan(&attempts); err != nil {
		return
	}
	nextAttempt := now
	if attempts > 0 {
		delay := opRetryBaseDelay
		for i := 1; i < attempts && delay < opRetryMaxDelay; i++ {
			delay *= 2
		}
		if delay > opRetryMaxDelay {
			delay = opRetryMaxDelay
		}
		nextAttempt = now.Add(delay)
	}
	s.db.Exec(`UPDATE pending_ops SET status = ?, owner = '', lease_until = NULL, error = ?, next_attempt_at = ?, updated_at = ?
		WHERE id = ? AND (owner = ? OR owner = '')`,
		string(OpFailed), errMsg, nextAttempt.Format(time.RFC3339), now.Format(time.RFC3339), id, s.procID)
}

// PendingOps returns all operations that still need execution: pending,
// failed below the attempt cap (including ops currently backed off),
// and running ops whose lease expired (crashed owner). Running ops with a
// live lease belong to another drainer and are excluded, so two processes
// sharing the cache never re-pick each other's in-flight work.
func (s *SQLiteStore) PendingOps() []QueuedOp {
	now := time.Now().UTC().Format(time.RFC3339)
	return s.queryOps(`SELECT id, type, status, account, folder, payload, error, attempts, next_attempt_at, created_at, updated_at
		FROM pending_ops
		WHERE status = ?
		   OR (status = ? AND attempts < ?)
		   OR (status = ? AND (lease_until IS NULL OR lease_until < ?))
		ORDER BY created_at`,
		string(OpPending), string(OpFailed), opMaxAttempts, string(OpRunning), now)
}

// RetryableOps returns the subset of PendingOps whose backoff has elapsed.
// Background retry loops use this; explicit account syncs use PendingOps and
// StartOpNow so a user-requested delivery attempt is never silently deferred.
func (s *SQLiteStore) RetryableOps() []QueuedOp {
	now := time.Now().UTC().Format(time.RFC3339)
	return s.queryOps(`SELECT id, type, status, account, folder, payload, error, attempts, next_attempt_at, created_at, updated_at
		FROM pending_ops
		WHERE status = ?
		   OR (status = ? AND attempts < ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?))
		   OR (status = ? AND (lease_until IS NULL OR lease_until < ?))
		ORDER BY created_at`,
		string(OpPending), string(OpFailed), opMaxAttempts, now, string(OpRunning), now)
}

// RecentOps returns the most recent operations (for the :ops log view).
func (s *SQLiteStore) RecentOps(limit int) []QueuedOp {
	return s.queryOps(`SELECT id, type, status, account, folder, payload, error, attempts, next_attempt_at, created_at, updated_at
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
		var nextAttemptAt sql.NullString
		if err := rows.Scan(&op.ID, &opType, &status, &op.Account, &op.Folder, &payload, &errStr, &op.Attempts, &nextAttemptAt, &createdAt, &updatedAt); err != nil {
			continue
		}
		op.Type = OpType(opType)
		op.Status = OpStatus(status)
		op.Payload = json.RawMessage(payload)
		if errStr.Valid {
			op.Error = errStr.String
		}
		if nextAttemptAt.Valid {
			op.NextAttemptAt, _ = time.Parse(time.RFC3339, nextAttemptAt.String)
		}
		op.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		op.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		ops = append(ops, op)
	}
	return ops
}
