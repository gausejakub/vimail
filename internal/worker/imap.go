package worker

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-sasl"
	"github.com/gausejakub/vimail/internal/auth"
	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/config"
)

// IMAPWorker manages a single IMAP connection for one account.
type IMAPWorker struct {
	acct   config.AccountConfig
	creds  *auth.Credentials
	client *imapclient.Client
	store  *cache.SQLiteStore

	// Maps display folder name → actual IMAP mailbox name.
	folderMap map[string]string

	// opMu serializes all IMAP operations (SELECT/FETCH/STORE/etc.)
	// to prevent concurrent SELECT from switching the active mailbox.
	opMu sync.Mutex

	// Dedicated second connection for body fetches so they don't block behind sync.
	fetchClient *imapclient.Client
	fetchMu     sync.Mutex

	// For IDLE notifications.
	mu      sync.Mutex
	newMail bool
}

// NewIMAPWorker creates a new IMAP worker for the given account.
func NewIMAPWorker(acct config.AccountConfig, creds *auth.Credentials, store *cache.SQLiteStore) *IMAPWorker {
	return &IMAPWorker{
		acct:      acct,
		creds:     creds,
		store:     store,
		folderMap: make(map[string]string),
	}
}

// Connect establishes a connection to the IMAP server and authenticates.
// Also opens a second connection dedicated to body fetches.
func (w *IMAPWorker) Connect() error {
	client, err := w.dial(true)
	if err != nil {
		return err
	}
	w.client = client

	// Open a second connection for body fetches (best-effort).
	fetchClient, err := w.dial(false)
	if err != nil {
		log.Printf("IMAP fetch-client connect failed for %s (will fall back): %v", w.acct.Email, err)
	} else {
		w.fetchClient = fetchClient
	}

	return nil
}

// dial opens and authenticates a single IMAP connection.
// If withIDLE is true, the unilateral data handler for new-mail detection is attached.
func (w *IMAPWorker) dial(withIDLE bool) (*imapclient.Client, error) {
	host := w.acct.IMAPHost
	port := w.acct.IMAPPort
	if port == 0 {
		port = 993
	}
	addr := fmt.Sprintf("%s:%d", host, port)

	tlsMode := w.acct.TLS
	if tlsMode == "" {
		tlsMode = "tls"
	}

	opts := &imapclient.Options{
		TLSConfig: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12},
	}
	if withIDLE {
		opts.UnilateralDataHandler = &imapclient.UnilateralDataHandler{
			Mailbox: func(data *imapclient.UnilateralDataMailbox) {
				if data.NumMessages != nil {
					w.mu.Lock()
					w.newMail = true
					w.mu.Unlock()
				}
			},
		}
	}

	var client *imapclient.Client
	var err error

	switch tlsMode {
	case "tls":
		client, err = imapclient.DialTLS(addr, opts)
	case "starttls":
		client, err = imapclient.DialStartTLS(addr, opts)
	case "none":
		client, err = imapclient.DialInsecure(addr, opts)
	default:
		client, err = imapclient.DialTLS(addr, opts)
	}
	if err != nil {
		return nil, fmt.Errorf("IMAP connect to %s: %w", addr, err)
	}

	if err := w.authenticate(client); err != nil {
		client.Close()
		return nil, fmt.Errorf("IMAP auth for %s: %w", w.acct.Email, err)
	}

	return client, nil
}

func (w *IMAPWorker) authenticate(client *imapclient.Client) error {
	switch w.creds.AuthMethod {
	case auth.AuthOAuth2Gmail, auth.AuthOAuth2Outlook:
		saslClient := sasl.NewOAuthBearerClient(&sasl.OAuthBearerOptions{
			Username: w.creds.Username,
			Token:    w.creds.Token,
		})
		return client.Authenticate(saslClient)
	default:
		cmd := client.Login(w.creds.Username, w.creds.Password)
		return cmd.Wait()
	}
}

// Disconnect closes the IMAP connection gracefully.
// It acquires opMu to wait for any in-flight operations to finish
// before tearing down the client.
func (w *IMAPWorker) Disconnect() {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	w.disconnectLocked()
}

// disconnectLocked closes the IMAP connection. Caller must hold opMu.
// Uses a short timeout for graceful logout to avoid hanging on dead connections.
func (w *IMAPWorker) disconnectLocked() {
	if w.client == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		cmd := w.client.Logout()
		cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		log.Printf("IMAP logout timed out for %s, forcing close", w.acct.Email)
		w.client.Close()
	}
	w.client = nil

	// Also close the fetch client.
	if w.fetchClient != nil {
		done2 := make(chan struct{})
		go func() {
			cmd := w.fetchClient.Logout()
			cmd.Wait()
			close(done2)
		}()
		select {
		case <-done2:
		case <-time.After(3 * time.Second):
			w.fetchClient.Close()
		}
		w.fetchClient = nil
	}
}

// IsConnected returns true if the IMAP client exists.
func (w *IMAPWorker) IsConnected() bool {
	return w.client != nil
}

// ListMailboxes fetches the list of mailboxes and syncs them to the cache.
func (w *IMAPWorker) ListMailboxes() ([]string, error) {
	w.opMu.Lock()
	defer w.opMu.Unlock()

	if w.client == nil {
		return nil, fmt.Errorf("not connected")
	}

	listCmd := w.client.List("", "*", nil)
	mailboxes, err := listCmd.Collect()
	if err != nil {
		return nil, fmt.Errorf("LIST: %w", err)
	}

	var names []string
	for _, mbox := range mailboxes {
		// Skip IMAP internal noselect mailboxes.
		noSelect := false
		for _, attr := range mbox.Attrs {
			if attr == imap.MailboxAttrNoSelect {
				noSelect = true
				break
			}
		}
		if noSelect {
			continue
		}
		displayName := FolderName(mbox.Mailbox)
		w.folderMap[displayName] = mbox.Mailbox
		names = append(names, displayName)
		w.store.EnsureFolder(w.acct.Email, displayName)
	}

	return names, nil
}

// FolderStatus returns UIDNEXT and UIDVALIDITY for a folder via STATUS (no SELECT needed).
func (w *IMAPWorker) FolderStatus(folder string) (uidNext uint32, uidValidity uint32, err error) {
	w.opMu.Lock()
	defer w.opMu.Unlock()

	if w.client == nil {
		return 0, 0, fmt.Errorf("not connected")
	}

	imapName := w.imapMailboxName(folder)
	statusCmd := w.client.Status(imapName, &imap.StatusOptions{
		UIDNext:     true,
		UIDValidity: true,
	})
	data, err := statusCmd.Wait()
	if err != nil {
		return 0, 0, fmt.Errorf("STATUS %s: %w", imapName, err)
	}
	return uint32(data.UIDNext), data.UIDValidity, nil
}

// SyncFolder syncs a single folder via IMAP FETCH.
// If onProgress is non-nil, it is called periodically with the number of new messages fetched so far.
func (w *IMAPWorker) SyncFolder(folder string, onProgress ...func(fetched int)) (int, error) {
	w.opMu.Lock()
	defer w.opMu.Unlock()

	if w.client == nil {
		return 0, fmt.Errorf("not connected")
	}

	imapName := w.imapMailboxName(folder)

	selCmd := w.client.Select(imapName, nil)
	selData, err := selCmd.Wait()
	if err != nil {
		return 0, fmt.Errorf("SELECT %s: %w", imapName, err)
	}

	// Check UIDVALIDITY.
	storedUV, _ := w.store.GetUIDValidity(w.acct.Email, folder)
	if storedUV != 0 && storedUV != selData.UIDValidity {
		w.store.PurgeFolder(w.acct.Email, folder)
	}
	w.store.SetUIDValidity(w.acct.Email, folder, selData.UIDValidity)

	if selData.NumMessages == 0 {
		return 0, nil
	}

	// Find highest stored UID for incremental sync.
	highUID, _ := w.store.HighestUID(w.acct.Email, folder)

	var seqSet imap.UIDSet
	if highUID > 0 {
		seqSet.AddRange(imap.UID(highUID+1), imap.UID(math.MaxUint32))
	} else {
		seqSet.AddRange(1, imap.UID(math.MaxUint32))
	}

	fetchOptions := &imap.FetchOptions{
		Envelope: true,
		Flags:    true,
		UID:      true,
	}

	fetchCmd := w.client.Fetch(seqSet, fetchOptions)
	newCount := 0

	for {
		msgData := fetchCmd.Next()
		if msgData == nil {
			break
		}

		buf, err := msgData.Collect()
		if err != nil {
			log.Printf("collect fetch data: %v", err)
			continue
		}

		if buf.UID == 0 {
			continue
		}

		msg := ParseEnvelope(uint32(buf.UID), buf.Envelope, buf.Flags)
		if err := w.store.UpsertMessage(w.acct.Email, folder, msg); err != nil {
			log.Printf("upsert message uid=%d folder=%s: %v", buf.UID, folder, err)
			continue
		}
		newCount++
		if len(onProgress) > 0 && onProgress[0] != nil && newCount%100 == 0 {
			onProgress[0](newCount)
		}
	}

	if err := fetchCmd.Close(); err != nil {
		return newCount, fmt.Errorf("FETCH: %w", err)
	}

	return newCount, nil
}

// FetchBody fetches the full body of a specific message by UID.
func (w *IMAPWorker) FetchBody(folder string, uid uint32) (BodyResult, error) {
	w.opMu.Lock()
	defer w.opMu.Unlock()

	if w.client == nil {
		return BodyResult{}, fmt.Errorf("not connected")
	}

	imapName := w.imapMailboxName(folder)

	selCmd := w.client.Select(imapName, nil)
	if _, err := selCmd.Wait(); err != nil {
		return BodyResult{}, fmt.Errorf("SELECT %s: %w", imapName, err)
	}

	var seqSet imap.UIDSet
	seqSet.AddNum(imap.UID(uid))

	fetchOptions := &imap.FetchOptions{
		BodySection: []*imap.FetchItemBodySection{
			{}, // BODY[] — full RFC 5322 message including headers.
		},
	}

	fetchCmd := w.client.Fetch(seqSet, fetchOptions)

	var result BodyResult
	msgCount := 0
	for {
		msgData := fetchCmd.Next()
		if msgData == nil {
			break
		}
		msgCount++

		for {
			item := msgData.Next()
			if item == nil {
				break
			}
			if bs, ok := item.(imapclient.FetchItemDataBodySection); ok {
				data, err := io.ReadAll(bs.Literal)
				if err != nil {
					continue
				}
				parsed, err := ParseBody(data)
				if err != nil {
					result = BodyResult{Text: string(data)}
				} else {
					result = parsed
				}
			}
		}
	}

	if err := fetchCmd.Close(); err != nil {
		return result, fmt.Errorf("FETCH body: %w", err)
	}

	if msgCount == 0 {
		return result, fmt.Errorf("UID %d not found in %s", uid, imapName)
	}

	// Cache text + HTML body + attachment metadata.
	w.store.UpdateMessageBody(w.acct.Email, folder, uid, result.Text, result.HTML, result.Attachments)

	return result, nil
}

// FetchBodyDirect fetches a message body using the dedicated fetch connection,
// bypassing the main opMu so it doesn't block behind sync operations.
// Falls back to the main connection if the fetch client is unavailable.
func (w *IMAPWorker) FetchBodyDirect(folder string, uid uint32) (BodyResult, error) {
	w.fetchMu.Lock()
	defer w.fetchMu.Unlock()

	client := w.fetchClient
	if client == nil {
		newClient, err := w.dial(false)
		if err != nil {
			return BodyResult{}, fmt.Errorf("no fetch connection available: %w", err)
		}
		w.fetchClient = newClient
		client = newClient
	}

	imapName := w.imapMailboxName(folder)

	selCmd := client.Select(imapName, nil)
	if _, err := selCmd.Wait(); err != nil {
		// Connection may be dead — try to reconnect the fetch client.
		newClient, dialErr := w.dial(false)
		if dialErr != nil {
			return BodyResult{}, fmt.Errorf("SELECT %s: %w (reconnect failed: %v)", imapName, err, dialErr)
		}
		w.fetchClient = newClient
		client = newClient
		selCmd = client.Select(imapName, nil)
		if _, err := selCmd.Wait(); err != nil {
			return BodyResult{}, fmt.Errorf("SELECT %s after reconnect: %w", imapName, err)
		}
	}
	var seqSet imap.UIDSet
	seqSet.AddNum(imap.UID(uid))

	fetchCmd := client.Fetch(seqSet, &imap.FetchOptions{
		BodySection: []*imap.FetchItemBodySection{{}},
	})

	var result BodyResult
	msgCount := 0
	for {
		msgData := fetchCmd.Next()
		if msgData == nil {
			break
		}
		msgCount++
		for {
			item := msgData.Next()
			if item == nil {
				break
			}
			if bs, ok := item.(imapclient.FetchItemDataBodySection); ok {
				data, err := io.ReadAll(bs.Literal)
				if err != nil {
					continue
				}
				parsed, err := ParseBody(data)
				if err != nil {
					result = BodyResult{Text: string(data)}
				} else {
					result = parsed
				}
			}
		}
	}

	if err := fetchCmd.Close(); err != nil {
		return result, fmt.Errorf("FETCH body: %w", err)
	}

	// UID not found on fetch client — try the main connection as fallback.
	if msgCount == 0 {
		if w.opMu.TryLock() {
			defer w.opMu.Unlock()
			if w.client != nil {
				selCmd2 := w.client.Select(imapName, nil)
				if _, err := selCmd2.Wait(); err == nil {
					var seqSet2 imap.UIDSet
					seqSet2.AddNum(imap.UID(uid))
					fetchCmd2 := w.client.Fetch(seqSet2, &imap.FetchOptions{
						BodySection: []*imap.FetchItemBodySection{{}},
					})
					for {
						msgData := fetchCmd2.Next()
						if msgData == nil {
							break
						}
						msgCount++
						for {
							item := msgData.Next()
							if item == nil {
								break
							}
							if bs, ok := item.(imapclient.FetchItemDataBodySection); ok {
								data, err := io.ReadAll(bs.Literal)
								if err != nil {
									continue
								}
								parsed, err := ParseBody(data)
								if err != nil {
									result = BodyResult{Text: string(data)}
								} else {
									result = parsed
								}
							}
						}
					}
					fetchCmd2.Close()
				}
			}
		}
		if msgCount == 0 {
			return result, fmt.Errorf("UID %d not found in %s (message may have been moved or deleted)", uid, imapName)
		}
	}

	// Cache the result.
	w.store.UpdateMessageBody(w.acct.Email, folder, uid, result.Text, result.HTML, result.Attachments)

	return result, nil
}

// FetchRawMessage fetches the full RFC 5322 message bytes for a UID.
func (w *IMAPWorker) FetchRawMessage(folder string, uid uint32) ([]byte, error) {
	w.opMu.Lock()
	defer w.opMu.Unlock()

	if w.client == nil {
		return nil, fmt.Errorf("not connected")
	}

	imapName := w.imapMailboxName(folder)
	selCmd := w.client.Select(imapName, nil)
	if _, err := selCmd.Wait(); err != nil {
		return nil, fmt.Errorf("SELECT %s: %w", imapName, err)
	}

	var seqSet imap.UIDSet
	seqSet.AddNum(imap.UID(uid))

	fetchCmd := w.client.Fetch(seqSet, &imap.FetchOptions{
		BodySection: []*imap.FetchItemBodySection{{}},
	})

	var raw []byte
	for {
		msgData := fetchCmd.Next()
		if msgData == nil {
			break
		}
		for {
			item := msgData.Next()
			if item == nil {
				break
			}
			if bs, ok := item.(imapclient.FetchItemDataBodySection); ok {
				data, err := io.ReadAll(bs.Literal)
				if err == nil {
					raw = data
				}
			}
		}
	}
	if err := fetchCmd.Close(); err != nil {
		return raw, fmt.Errorf("FETCH: %w", err)
	}
	return raw, nil
}

// MarkRead sets the \Seen flag on a message by UID.
func (w *IMAPWorker) MarkRead(folder string, uid uint32) error {
	return w.MarkReadBatch(folder, []uint32{uid})
}

// MarkReadBatch marks multiple messages as read in a single IMAP operation.
func (w *IMAPWorker) MarkReadBatch(folder string, uids []uint32) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()

	if w.client == nil {
		return fmt.Errorf("not connected")
	}
	if len(uids) == 0 {
		return nil
	}

	imapName := w.imapMailboxName(folder)
	selCmd := w.client.Select(imapName, nil)
	if _, err := selCmd.Wait(); err != nil {
		return fmt.Errorf("SELECT %s: %w", imapName, err)
	}

	var seqSet imap.UIDSet
	for _, uid := range uids {
		seqSet.AddNum(imap.UID(uid))
	}

	storeCmd := w.client.Store(seqSet, &imap.StoreFlags{
		Op:    imap.StoreFlagsAdd,
		Flags: []imap.Flag{imap.FlagSeen},
	}, nil)
	if err := storeCmd.Close(); err != nil {
		return fmt.Errorf("STORE +FLAGS \\Seen %d uids: %w", len(uids), err)
	}
	return nil
}

// Idle starts IMAP IDLE on the given folder and blocks until
// new mail arrives or the timeout is reached. Returns true if new mail arrived.
func (w *IMAPWorker) Idle(folder string, timeout time.Duration) (bool, error) {
	w.opMu.Lock()
	defer w.opMu.Unlock()

	if w.client == nil {
		return false, fmt.Errorf("not connected")
	}

	imapName := w.imapMailboxName(folder)
	selCmd := w.client.Select(imapName, nil)
	if _, err := selCmd.Wait(); err != nil {
		return false, fmt.Errorf("SELECT %s for IDLE: %w", imapName, err)
	}

	w.mu.Lock()
	w.newMail = false
	w.mu.Unlock()

	idleCmd, err := w.client.Idle()
	if err != nil {
		return false, fmt.Errorf("IDLE: %w", err)
	}

	// Wait for new mail notification or timeout.
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timer.C:
			idleCmd.Close()
			w.mu.Lock()
			got := w.newMail
			w.mu.Unlock()
			return got, nil
		case <-ticker.C:
			w.mu.Lock()
			got := w.newMail
			w.mu.Unlock()
			if got {
				idleCmd.Close()
				return true, nil
			}
		}
	}
}

// AppendToFolder appends a message to a server folder (e.g. Sent).
func (w *IMAPWorker) AppendToFolder(folder string, message []byte, flags []imap.Flag) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()

	if w.client == nil {
		return fmt.Errorf("not connected")
	}

	imapName := w.imapMailboxName(folder)
	size := int64(len(message))
	appendCmd := w.client.Append(imapName, size, &imap.AppendOptions{Flags: flags})
	if _, err := appendCmd.Write(message); err != nil {
		return fmt.Errorf("APPEND write: %w", err)
	}
	if err := appendCmd.Close(); err != nil {
		return fmt.Errorf("APPEND close: %w", err)
	}
	return nil
}

// MoveToTrash moves a message to the Trash folder via IMAP (COPY + STORE \Deleted + EXPUNGE).
func (w *IMAPWorker) MoveToTrash(folder string, uid uint32) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()

	if w.client == nil {
		return fmt.Errorf("not connected")
	}

	imapName := w.imapMailboxName(folder)
	trashName := w.trashMailboxName()

	// SELECT source folder.
	selCmd := w.client.Select(imapName, nil)
	if _, err := selCmd.Wait(); err != nil {
		return fmt.Errorf("SELECT %s: %w", imapName, err)
	}

	var seqSet imap.UIDSet
	seqSet.AddNum(imap.UID(uid))

	// COPY to Trash.
	copyCmd := w.client.Copy(seqSet, trashName)
	if _, err := copyCmd.Wait(); err != nil {
		return fmt.Errorf("COPY to %s: %w", trashName, err)
	}

	// STORE +FLAGS \Deleted.
	storeCmd := w.client.Store(seqSet, &imap.StoreFlags{
		Op:    imap.StoreFlagsAdd,
		Flags: []imap.Flag{imap.FlagDeleted},
	}, nil)
	if err := storeCmd.Close(); err != nil {
		return fmt.Errorf("STORE +FLAGS \\Deleted uid=%d: %w", uid, err)
	}

	// EXPUNGE.
	expungeCmd := w.client.Expunge()
	if err := expungeCmd.Close(); err != nil {
		return fmt.Errorf("EXPUNGE: %w", err)
	}

	return nil
}

// MoveToTrashBatch moves multiple messages to the Trash folder via IMAP.
// Processes UIDs in chunks to avoid server-side limits and timeouts.
// If onProgress is non-nil, it is called after each chunk with (done, total).
func (w *IMAPWorker) MoveToTrashBatch(folder string, uids []uint32, onProgress func(done, total int)) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()

	if w.client == nil {
		return fmt.Errorf("not connected")
	}
	if len(uids) == 0 {
		return nil
	}

	imapName := w.imapMailboxName(folder)
	trashName := w.trashMailboxName()

	log.Printf("IMAP delete: %d UIDs from %s (%s) → %s", len(uids), folder, imapName, trashName)

	// SELECT once for all chunks.
	selCmd := w.client.Select(imapName, nil)
	if _, err := selCmd.Wait(); err != nil {
		return fmt.Errorf("SELECT %s: %w", imapName, err)
	}

	const chunkSize = 500
	for i := 0; i < len(uids); i += chunkSize {
		end := i + chunkSize
		if end > len(uids) {
			end = len(uids)
		}
		chunk := uids[i:end]

		if err := w.moveChunkToTrash(trashName, chunk); err != nil {
			return fmt.Errorf("chunk %d-%d: %w", i, end-1, err)
		}
		log.Printf("IMAP delete: processed %d/%d UIDs", end, len(uids))
		if onProgress != nil {
			log.Printf("IMAP delete: sending progress %d/%d", end, len(uids))
			onProgress(end, len(uids))
		}
	}

	// Single EXPUNGE after all chunks.
	expungeCmd := w.client.Expunge()
	if err := expungeCmd.Close(); err != nil {
		return fmt.Errorf("EXPUNGE: %w", err)
	}

	return nil
}

// moveChunkToTrash moves a chunk of UIDs to trash (COPY + STORE \Deleted).
// Caller must hold opMu and have the mailbox already SELECTed.
func (w *IMAPWorker) moveChunkToTrash(trashName string, uids []uint32) error {
	var seqSet imap.UIDSet
	for _, uid := range uids {
		seqSet.AddNum(imap.UID(uid))
	}

	copyCmd := w.client.Copy(seqSet, trashName)
	if _, err := copyCmd.Wait(); err != nil {
		return fmt.Errorf("COPY to %s: %w", trashName, err)
	}

	storeCmd := w.client.Store(seqSet, &imap.StoreFlags{
		Op:    imap.StoreFlagsAdd,
		Flags: []imap.Flag{imap.FlagDeleted},
	}, nil)
	if err := storeCmd.Close(); err != nil {
		return fmt.Errorf("STORE +FLAGS \\Deleted: %w", err)
	}

	return nil
}

// MoveToFolder moves a message from one folder to another via IMAP (COPY + STORE \Deleted + EXPUNGE).
func (w *IMAPWorker) MoveToFolder(srcFolder string, uid uint32, dstFolder string) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()

	if w.client == nil {
		return fmt.Errorf("not connected")
	}

	srcName := w.imapMailboxName(srcFolder)
	dstName := w.imapMailboxName(dstFolder)

	selCmd := w.client.Select(srcName, nil)
	if _, err := selCmd.Wait(); err != nil {
		return fmt.Errorf("SELECT %s: %w", srcName, err)
	}

	var seqSet imap.UIDSet
	seqSet.AddNum(imap.UID(uid))

	copyCmd := w.client.Copy(seqSet, dstName)
	if _, err := copyCmd.Wait(); err != nil {
		return fmt.Errorf("COPY to %s: %w", dstName, err)
	}

	storeCmd := w.client.Store(seqSet, &imap.StoreFlags{
		Op:    imap.StoreFlagsAdd,
		Flags: []imap.Flag{imap.FlagDeleted},
	}, nil)
	if err := storeCmd.Close(); err != nil {
		return fmt.Errorf("STORE +FLAGS \\Deleted uid=%d: %w", uid, err)
	}

	expungeCmd := w.client.Expunge()
	if err := expungeCmd.Close(); err != nil {
		return fmt.Errorf("EXPUNGE: %w", err)
	}

	return nil
}

// MoveToFolderBatch moves multiple messages from one folder to another via IMAP.
func (w *IMAPWorker) MoveToFolderBatch(srcFolder string, uids []uint32, dstFolder string) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()

	if w.client == nil {
		return fmt.Errorf("not connected")
	}
	if len(uids) == 0 {
		return nil
	}

	srcName := w.imapMailboxName(srcFolder)
	dstName := w.imapMailboxName(dstFolder)

	selCmd := w.client.Select(srcName, nil)
	if _, err := selCmd.Wait(); err != nil {
		return fmt.Errorf("SELECT %s: %w", srcName, err)
	}

	const chunkSize = 500
	for i := 0; i < len(uids); i += chunkSize {
		end := i + chunkSize
		if end > len(uids) {
			end = len(uids)
		}

		var seqSet imap.UIDSet
		for _, uid := range uids[i:end] {
			seqSet.AddNum(imap.UID(uid))
		}

		copyCmd := w.client.Copy(seqSet, dstName)
		if _, err := copyCmd.Wait(); err != nil {
			return fmt.Errorf("COPY to %s: %w", dstName, err)
		}

		storeCmd := w.client.Store(seqSet, &imap.StoreFlags{
			Op:    imap.StoreFlagsAdd,
			Flags: []imap.Flag{imap.FlagDeleted},
		}, nil)
		if err := storeCmd.Close(); err != nil {
			return fmt.Errorf("STORE +FLAGS \\Deleted: %w", err)
		}
	}

	expungeCmd := w.client.Expunge()
	if err := expungeCmd.Close(); err != nil {
		return fmt.Errorf("EXPUNGE: %w", err)
	}

	return nil
}

// DeleteMailbox deletes a mailbox on the IMAP server.
func (w *IMAPWorker) DeleteMailbox(folder string) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()

	if w.client == nil {
		return fmt.Errorf("not connected")
	}

	imapName := w.imapMailboxName(folder)
	if err := w.client.Delete(imapName).Wait(); err != nil {
		return fmt.Errorf("DELETE %s: %w", imapName, err)
	}

	delete(w.folderMap, folder)
	return nil
}

// imapMailboxName maps a display folder name back to an IMAP mailbox name.
func (w *IMAPWorker) imapMailboxName(folder string) string {
	if name, ok := w.folderMap[folder]; ok {
		return name
	}
	return folder
}

// trashMailboxName returns the IMAP name of the trash folder.
// Falls back to "Trash" if no trash folder was discovered.
func (w *IMAPWorker) trashMailboxName() string {
	if name, ok := w.folderMap["Trash"]; ok {
		return name
	}
	// Search folderMap values for common trash names.
	for _, imapName := range w.folderMap {
		lower := strings.ToLower(imapName)
		if lower == "[gmail]/trash" || lower == "[gmail]/bin" ||
			lower == "trash" || lower == "deleted items" || lower == "deleted messages" {
			return imapName
		}
	}
	return "Trash"
}

// reconnect attempts to re-establish the IMAP connection.
// Caller must hold opMu.
func (w *IMAPWorker) reconnect() error {
	w.disconnectLocked()
	return w.Connect()
}

// withReconnect wraps an operation with a single reconnect retry.
func (w *IMAPWorker) withReconnect(op func() error) error {
	err := op()
	if err == nil {
		return nil
	}

	if isConnectionError(err) {
		log.Printf("IMAP connection error for %s, reconnecting: %v", w.acct.Email, err)
		if reconnErr := w.reconnect(); reconnErr != nil {
			return fmt.Errorf("reconnect failed: %w (original: %v)", reconnErr, err)
		}
		return op()
	}
	return err
}

func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "connection refused") {
		return true
	}
	_, ok := err.(*net.OpError)
	return ok
}
