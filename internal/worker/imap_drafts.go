package worker

import (
	"fmt"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// headerBackfillChunk and headerBackfillPerSync bound the header refetch of
// messages cached before full headers were stored, so a large mailbox is
// upgraded over a few syncs instead of stalling the first one.
const (
	headerBackfillChunk   = 500
	headerBackfillPerSync = 5000
)

// BackfillHeaders refetches headers (not bodies) for cached rows older than
// cache.HeadersVersion, filling cc, reply-to, full To and threading headers.
// It returns how many rows were updated.
func (w *IMAPWorker) BackfillHeaders(folder string) (int, error) {
	done := 0
	for done < headerBackfillPerSync {
		uids := w.store.StaleHeaderUIDs(w.acct.Email, folder, headerBackfillChunk)
		if len(uids) == 0 {
			return done, nil
		}
		n, err := w.refetchHeaders(folder, uids)
		done += n
		if err != nil {
			return done, err
		}
		if n == 0 {
			// The server no longer has these UIDs; mark them so the loop
			// ends. Verification removes the stale rows.
			for _, uid := range uids {
				w.store.MarkHeadersCurrent(w.acct.Email, folder, uid)
			}
		}
	}
	return done, nil
}

func (w *IMAPWorker) refetchHeaders(folder string, uids []uint32) (int, error) {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	if w.client == nil {
		return 0, fmt.Errorf("not connected")
	}
	cancel := w.opTimer(syncDeadline)
	defer cancel()
	imapName := w.imapMailboxName(folder)
	if _, err := w.client.Select(imapName, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return 0, fmt.Errorf("SELECT %s: %w", imapName, err)
	}
	var set imap.UIDSet
	for _, uid := range uids {
		set.AddNum(imap.UID(uid))
	}
	fetch := w.client.Fetch(set, headerFetchOptions())
	n := 0
	seen := map[uint32]bool{}
	for {
		data := fetch.Next()
		if data == nil {
			break
		}
		buf, err := data.Collect()
		if err != nil || buf.UID == 0 {
			continue
		}
		if err := w.store.UpdateHeaders(w.acct.Email, folder, parseFetchedHeaders(buf)); err == nil {
			n++
			seen[uint32(buf.UID)] = true
		}
	}
	if err := fetch.Close(); err != nil {
		return n, fmt.Errorf("FETCH headers: %w", err)
	}
	// UIDs the server did not return are gone; stop retrying them.
	if n > 0 {
		for _, uid := range uids {
			if !seen[uid] {
				w.store.MarkHeadersCurrent(w.acct.Email, folder, uid)
			}
		}
	}
	return n, nil
}

// AppendWithUID appends a message and returns the UID the server assigned
// (0 when the server does not support UIDPLUS).
func (w *IMAPWorker) AppendWithUID(folder string, message []byte, flags []imap.Flag) (uint32, error) {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	if w.client == nil {
		return 0, fmt.Errorf("not connected")
	}
	cancel := w.opTimer(syncDeadline)
	defer cancel()
	imapName := w.imapMailboxName(folder)
	appendCmd := w.client.Append(imapName, int64(len(message)), &imap.AppendOptions{Flags: flags})
	if _, err := appendCmd.Write(message); err != nil {
		return 0, fmt.Errorf("APPEND write: %w", err)
	}
	if err := appendCmd.Close(); err != nil {
		return 0, fmt.Errorf("APPEND close: %w", err)
	}
	data, err := appendCmd.Wait()
	if err != nil {
		return 0, fmt.Errorf("APPEND: %w", err)
	}
	return uint32(data.UID), nil
}

// DeleteByMessageIDs permanently removes the messages in folder whose
// Message-ID header matches one of ids. Used for superseded and deleted
// drafts, where a UID may be unknown or stale but the Message-ID is ours.
func (w *IMAPWorker) DeleteByMessageIDs(folder string, ids []string) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	if w.client == nil {
		return fmt.Errorf("not connected")
	}
	if len(ids) == 0 {
		return nil
	}
	cancel := w.opTimer(syncDeadline)
	defer cancel()
	imapName := w.imapMailboxName(folder)
	if _, err := w.client.Select(imapName, nil).Wait(); err != nil {
		return fmt.Errorf("SELECT %s: %w", imapName, err)
	}
	var set imap.UIDSet
	for _, id := range ids {
		if id == "" {
			continue
		}
		data, err := w.client.UIDSearch(&imap.SearchCriteria{
			Header: []imap.SearchCriteriaHeaderField{{Key: "Message-ID", Value: "<" + id + ">"}},
		}, nil).Wait()
		if err != nil {
			return fmt.Errorf("UID SEARCH Message-ID: %w", err)
		}
		for _, uid := range data.AllUIDs() {
			set.AddNum(uid)
		}
	}
	if len(set) == 0 {
		return nil // already gone
	}
	if err := w.client.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
		return fmt.Errorf("STORE \\Deleted: %w", err)
	}
	return w.expunge(set)
}

// AddFlag adds a flag to messages, after checking (when messageID is given)
// that the UIDs still hold that message.
func (w *IMAPWorker) AddFlag(folder string, uids []uint32, flag, messageID string) error {
	if messageID != "" {
		ok, err := w.uidHoldsMessage(folder, uids, messageID)
		if err != nil {
			return err
		}
		if !ok {
			return nil // the original moved or was deleted; nothing to flag
		}
	}
	return w.setFlagBatch(folder, uids, imap.Flag(flag), true)
}

func (w *IMAPWorker) uidHoldsMessage(folder string, uids []uint32, messageID string) (bool, error) {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	if w.client == nil {
		return false, fmt.Errorf("not connected")
	}
	cancel := w.opTimer(syncDeadline)
	defer cancel()
	imapName := w.imapMailboxName(folder)
	if _, err := w.client.Select(imapName, nil).Wait(); err != nil {
		return false, fmt.Errorf("SELECT %s: %w", imapName, err)
	}
	var set imap.UIDSet
	for _, uid := range uids {
		set.AddNum(imap.UID(uid))
	}
	fetch := w.client.Fetch(set, &imap.FetchOptions{UID: true, Envelope: true})
	found := false
	for {
		data := fetch.Next()
		if data == nil {
			break
		}
		buf, err := data.Collect()
		if err == nil && buf.Envelope != nil && buf.Envelope.MessageID == messageID {
			found = true
		}
	}
	if err := fetch.Close(); err != nil {
		return false, fmt.Errorf("FETCH: %w", err)
	}
	return found, nil
}

// PeekRawMessage fetches a whole message without setting \Seen.
func (w *IMAPWorker) PeekRawMessage(folder string, uid uint32) ([]byte, error) {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	if w.client == nil {
		return nil, fmt.Errorf("not connected")
	}
	cancel := w.opTimer(fetchBodyDeadline)
	defer cancel()
	imapName := w.imapMailboxName(folder)
	if _, err := w.client.Select(imapName, nil).Wait(); err != nil {
		return nil, fmt.Errorf("SELECT %s: %w", imapName, err)
	}
	var set imap.UIDSet
	set.AddNum(imap.UID(uid))
	fetch := w.client.Fetch(set, &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{{Peek: true}}})
	var raw []byte
	for {
		msgData := fetch.Next()
		if msgData == nil {
			break
		}
		for {
			item := msgData.Next()
			if item == nil {
				break
			}
			if bs, ok := item.(imapclient.FetchItemDataBodySection); ok {
				if data, err := readBodyLiteral(bs.Literal); err == nil {
					raw = data
				}
			}
		}
	}
	if err := fetch.Close(); err != nil {
		return raw, fmt.Errorf("FETCH: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("message %d not found in %s", uid, folder)
	}
	return raw, nil
}
