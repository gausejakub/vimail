package cache

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"time"
)

// newProcID builds an identifier that is unique per store instance so op
// claims and sync locks can distinguish owners across processes (and across
// two stores opened by one test process): host + pid + random suffix.
func newProcID() string {
	b := make([]byte, 4)
	rand.Read(b)
	host, _ := os.Hostname()
	return fmt.Sprintf("%s-%d-%s", host, os.Getpid(), hex.EncodeToString(b))
}

// syncLockTTL bounds how long a sync lock can outlive a crashed holder.
// Account syncs are capped at 5 minutes by the coordinator, so an expired
// lock always means the holder is gone.
const syncLockTTL = 10 * time.Minute

// TryAcquireSyncLock attempts to take the cross-process advisory lock that
// serializes per-account sync. It returns a release func and true when the
// lock was acquired, or (nil, false) when another holder (any process, or a
// concurrent sync in this one) currently owns it. An expired lock — a crashed
// holder — is stolen. Each acquisition uses its own token, so releasing
// never unlocks someone else's later acquisition.
func (s *SQLiteStore) TryAcquireSyncLock(account string) (func(), bool) {
	token := fmt.Sprintf("%s#%d", s.procID, s.lockSeq.Add(1))
	now := time.Now().UTC()
	res, err := s.db.Exec(`INSERT INTO sync_locks (account, owner, expires_at) VALUES (?, ?, ?)
		ON CONFLICT(account) DO UPDATE SET owner = excluded.owner, expires_at = excluded.expires_at
		WHERE sync_locks.expires_at < ?`,
		account, token, now.Add(syncLockTTL).Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		return nil, false
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return nil, false
	}
	release := func() {
		s.db.Exec(`DELETE FROM sync_locks WHERE account = ? AND owner = ?`, account, token)
	}
	return release, true
}
