package worker

import (
	"fmt"

	"github.com/gausejakub/vimail/internal/cache"
)

// archiveTarget returns where archiving moves mail for an account: its
// Archive folder, or for Gmail "All Mail" (archiving there means removing
// the message from the folder it is in). All Mail is not cached, so that
// move skips the cache.
func (c *Coordinator) archiveTarget(acctEmail string) (folder string, skipCache bool, err error) {
	for _, f := range c.store.FoldersFor(acctEmail) {
		if f.Name == "Archive" {
			return "Archive", false, nil
		}
	}
	// Gmail (and any account set up the same way) has an All Mail folder that
	// holds everything and is left out of sync.
	if acct, ok := c.accountByEmail(acctEmail); ok && acct.ShouldSkipFolder("All Mail") {
		return "All Mail", true, nil
	}
	return "", false, fmt.Errorf("%s has no Archive folder", acctEmail)
}

// ArchiveMessagesNow archives messages: the cache is updated at once and
// the server move goes through the operation queue, so it works offline.
func (c *Coordinator) ArchiveMessagesNow(acctEmail, source string, uids []uint32) OperationResult {
	dest, skipCache, err := c.archiveTarget(acctEmail)
	if err != nil {
		return OperationResult{Err: err}
	}
	switch source {
	case dest:
		return OperationResult{Err: fmt.Errorf("already in %s", dest)}
	case "Drafts", "Trash", "Spam":
		return OperationResult{Err: fmt.Errorf("mail in %s is not archived", source)}
	}
	if !skipCache {
		return c.MoveMessagesNow(acctEmail, source, dest, uids)
	}
	if err := c.store.RemoveMessagesByUID(acctEmail, source, uids); err != nil {
		return OperationResult{Err: err}
	}
	payload := cache.MovePayload{UIDs: uids, Destination: dest, SkipCache: true}
	return c.executeStateOp(cache.OpMove, acctEmail, source, payload, func(w *IMAPWorker) error {
		return c.executeMove(w, acctEmail, source, payload)
	})
}
