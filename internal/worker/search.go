package worker

import (
	"fmt"
	"sort"
	"strings"

	"github.com/emersion/go-imap/v2"

	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/logging"
)

// serverSearchPerFolder bounds how many matches are taken from one folder
// (the newest), so one huge folder cannot crowd out the rest.
const serverSearchPerFolder = 200

// SearchFolder asks the server which messages in a folder contain the text
// anywhere (headers or body) and returns their UIDs, newest first.
func (w *IMAPWorker) SearchFolder(folder, query string) ([]uint32, error) {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	if w.client == nil {
		return nil, fmt.Errorf("not connected")
	}
	cancel := w.opTimer(syncDeadline)
	defer cancel()
	imapName := w.imapMailboxName(folder)
	if _, err := w.client.Select(imapName, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return nil, fmt.Errorf("SELECT %s: %w", imapName, err)
	}
	data, err := w.client.UIDSearch(&imap.SearchCriteria{Text: []string{query}}, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("UID SEARCH: %w", err)
	}
	uids := make([]uint32, 0, len(data.AllUIDs()))
	for _, uid := range data.AllUIDs() {
		uids = append(uids, uint32(uid))
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] > uids[j] })
	if len(uids) > serverSearchPerFolder {
		uids = uids[:serverSearchPerFolder]
	}
	return uids, nil
}

// FetchHeaders caches the headers of the given UIDs (used for search hits
// the cache does not hold yet).
func (w *IMAPWorker) FetchHeaders(folder string, uids []uint32) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	if w.client == nil {
		return fmt.Errorf("not connected")
	}
	if len(uids) == 0 {
		return nil
	}
	cancel := w.opTimer(syncDeadline)
	defer cancel()
	imapName := w.imapMailboxName(folder)
	if _, err := w.client.Select(imapName, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return fmt.Errorf("SELECT %s: %w", imapName, err)
	}
	var set imap.UIDSet
	for _, uid := range uids {
		set.AddNum(imap.UID(uid))
	}
	fetch := w.client.Fetch(set, headerFetchOptions())
	for {
		data := fetch.Next()
		if data == nil {
			break
		}
		buf, err := data.Collect()
		if err != nil || buf.UID == 0 {
			continue
		}
		if err := w.store.UpsertMessage(w.acct.Email, folder, parseFetchedHeaders(buf)); err != nil {
			return err
		}
	}
	return fetch.Close()
}

// ServerSearchResult is the outcome of searching an account on the server.
type ServerSearchResult struct {
	Messages []email.Message
	// Errors holds per-folder failures; the other folders' hits are kept.
	Errors []error
}

// SearchServerNow searches an account's folders on the server (headers and
// bodies, including mail whose body was never downloaded) and returns the
// matching messages from the cache, fetching headers for hits it does not
// hold. Label copies of one message are returned once. Trash, Spam and
// Drafts are not searched.
func (c *Coordinator) SearchServerNow(acctEmail, query string, limit int) ServerSearchResult {
	var out ServerSearchResult
	query = strings.TrimSpace(query)
	acct, ok := c.accountByEmail(acctEmail)
	if !ok || query == "" || limit <= 0 {
		return out
	}
	w, err := c.ensureIMAPWorker(acct)
	if err != nil {
		out.Errors = append(out.Errors, err)
		return out
	}
	seen := map[string]bool{}
	for _, folder := range c.store.FoldersFor(acctEmail) {
		switch folder.Name {
		case "Drafts", "Trash", "Spam":
			continue
		}
		uids, err := w.SearchFolder(folder.Name, query)
		if err != nil {
			out.Errors = append(out.Errors, fmt.Errorf("%s: %w", folder.Name, err))
			continue
		}
		missing := c.uncached(acctEmail, folder.Name, uids)
		if len(missing) > 0 {
			// New mail the cache has not seen: a normal sync brings it in
			// and keeps the incremental-sync watermark honest. Whatever is
			// still missing after that is a hole below the watermark.
			if _, err := w.SyncFolder(folder.Name); err != nil {
				out.Errors = append(out.Errors, fmt.Errorf("%s: %w", folder.Name, err))
			}
			missing = c.uncached(acctEmail, folder.Name, uids)
			if highest, _ := c.store.HighestUID(acctEmail, folder.Name); len(missing) > 0 {
				var holes []uint32
				for _, uid := range missing {
					if uid <= highest {
						holes = append(holes, uid)
					}
				}
				if err := w.FetchHeaders(folder.Name, holes); err != nil {
					out.Errors = append(out.Errors, fmt.Errorf("%s: %w", folder.Name, err))
				}
			}
		}
		for _, uid := range uids {
			m, _, cached := c.store.MessageByUID(acctEmail, folder.Name, uid)
			if !cached {
				continue
			}
			key := m.MessageID
			if key == "" {
				key = fmt.Sprintf("%s\x00%d", folder.Name, uid)
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			out.Messages = append(out.Messages, m)
		}
	}
	sort.SliceStable(out.Messages, func(i, j int) bool { return out.Messages[i].Date.After(out.Messages[j].Date) })
	if len(out.Messages) > limit {
		out.Messages = out.Messages[:limit]
	}
	logging.Info("search", "server search", logging.Acct(acctEmail), logging.KV("hits", len(out.Messages)), logging.KV("folder_errors", len(out.Errors)))
	return out
}

// uncached returns the UIDs of a folder that are not in the cache.
func (c *Coordinator) uncached(acctEmail, folder string, uids []uint32) []uint32 {
	var missing []uint32
	for _, uid := range uids {
		if _, _, cached := c.store.MessageByUID(acctEmail, folder, uid); !cached {
			missing = append(missing, uid)
		}
	}
	return missing
}
