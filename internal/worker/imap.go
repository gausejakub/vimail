package worker

import (
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-sasl"
	"github.com/gausejakub/vimail/internal/auth"
	"github.com/gausejakub/vimail/internal/cache"
	"github.com/gausejakub/vimail/internal/config"
	"github.com/gausejakub/vimail/internal/email"
	"github.com/gausejakub/vimail/internal/pgp"
)

const (
	// maxBodySize limits the total size of a fetched message body to prevent OOM.
	maxBodySize = 50 * 1024 * 1024 // 50 MB

	// opDeadline is the maximum time for a single IMAP command (SELECT, FETCH envelope, etc.).
	opDeadline = 45 * time.Second

	// syncDeadline is the maximum time for a full folder sync (may fetch many messages).
	syncDeadline = 3 * time.Minute

	// fetchBodyDeadline is the maximum time for fetching a single message body.
	fetchBodyDeadline = 60 * time.Second

	// disconnectWait is how long Disconnect waits for an in-flight op to finish
	// after force-closing the connection.
	disconnectWait = 5 * time.Second

	// imapUIDChunkSize bounds generated UID-set commands for servers with
	// conservative command-length limits.
	imapUIDChunkSize = 500
)

// IMAPWorker manages a single IMAP connection for one account.
type IMAPWorker struct {
	acct   config.AccountConfig
	creds  *auth.Credentials
	client *imapclient.Client
	store  *cache.SQLiteStore

	// commandDeadline bounds one batched write command. It defaults to
	// opDeadline and is configurable by loopback tests that exercise timeout
	// behavior without waiting 45 seconds.
	commandDeadline time.Duration

	pgp *pgp.Client // nil when PGP is off

	// Maps display folder name → actual IMAP mailbox name.
	folderMap map[string]string

	// opMu serializes all IMAP operations (SELECT/FETCH/STORE/etc.)
	// to prevent concurrent SELECT from switching the active mailbox.
	opMu sync.Mutex

	// Dedicated second connection for body fetches so they don't block behind sync.
	fetchClient    *imapclient.Client
	fetchMu        sync.Mutex
	wantedFetchUID atomic.Uint32 // only the latest requested UID is fetched; stale ones skip

	// For IDLE notifications.
	mu      sync.Mutex
	newMail bool
}

// NewIMAPWorker creates a new IMAP worker for the given account.
func NewIMAPWorker(acct config.AccountConfig, creds *auth.Credentials, store *cache.SQLiteStore) *IMAPWorker {
	return &IMAPWorker{
		acct:            acct,
		creds:           creds,
		store:           store,
		folderMap:       make(map[string]string),
		commandDeadline: opDeadline,
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
	w.folderMap = make(map[string]string)
	if _, err := w.listMailboxes(); err != nil {
		client.Close()
		w.client = nil
		return fmt.Errorf("discover mailboxes: %w", err)
	}

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
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	tlsMode := w.acct.TLS
	if tlsMode == "" {
		tlsMode = "tls"
	}

	opts := &imapclient.Options{
		TLSConfig: clientTLSConfig(host),
		Dialer: &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second, // Detect dead connections via TCP keepalive.
		},
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
		// A bearer token grants full mailbox access and cannot be scoped to
		// this server; never send one over an unencrypted connection.
		if w.creds != nil && (w.creds.AuthMethod == auth.AuthOAuth2Gmail || w.creds.AuthMethod == auth.AuthOAuth2Outlook) {
			return nil, fmt.Errorf("refusing to send an OAuth2 token to %s without TLS (tls = \"none\")", addr)
		}
		log.Printf("WARNING: IMAP connecting to %s without TLS — credentials sent in plaintext", addr)
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
	case auth.AuthOAuth2Outlook:
		token, err := w.creds.AccessToken()
		if err != nil {
			return err
		}
		return client.Authenticate(newXOAuth2Client(w.creds.Username, token))
	case auth.AuthOAuth2Gmail:
		token, err := w.creds.AccessToken()
		if err != nil {
			return err
		}
		saslClient := sasl.NewOAuthBearerClient(&sasl.OAuthBearerOptions{
			Username: w.creds.Username,
			Token:    token,
		})
		return client.Authenticate(saslClient)
	default:
		cmd := client.Login(w.creds.Username, w.creds.Password)
		return cmd.Wait()
	}
}

// Disconnect closes the IMAP connection gracefully if idle, or forcefully
// if an operation is in flight. This never blocks indefinitely, preventing
// deadlocks when the network is down and a cmd.Wait() is stuck.
func (w *IMAPWorker) Disconnect() {
	if w.opMu.TryLock() {
		// No operation in flight — graceful shutdown.
		defer w.opMu.Unlock()
		w.disconnectLocked()
		return
	}

	// An operation is in flight holding opMu. Force-close connections to
	// unblock any stuck cmd.Wait() calls, then wait briefly for opMu.
	log.Printf("IMAP force-closing %s (operation in flight)", w.acct.Email)
	if w.client != nil {
		w.client.Close()
	}
	if w.fetchClient != nil {
		w.fetchClient.Close()
	}

	done := make(chan struct{})
	go func() {
		w.opMu.Lock()
		w.client = nil
		w.fetchClient = nil
		w.opMu.Unlock()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(disconnectWait):
		log.Printf("IMAP disconnect: timed out waiting for in-flight op for %s", w.acct.Email)
	}
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

// opTimer starts a background timer that force-closes the main IMAP client
// when the deadline fires, unblocking any stuck cmd.Wait(). The returned
// cancel function MUST be deferred by the caller.
func (w *IMAPWorker) opTimer(d time.Duration) func() {
	t := time.AfterFunc(d, func() {
		log.Printf("IMAP op timeout (%v) for %s — force-closing connection", d, w.acct.Email)
		if w.client != nil {
			w.client.Close()
		}
	})
	return func() { t.Stop() }
}

// fetchOpTimer is like opTimer but for the dedicated fetch client. timedOut
// reports whether the deadline fired, so callers can surface a timeout instead
// of the protocol error the forced close leaves behind.
func (w *IMAPWorker) fetchOpTimer(d time.Duration) (cancel func(), timedOut func() bool) {
	var fired atomic.Bool
	t := time.AfterFunc(d, func() {
		fired.Store(true)
		log.Printf("IMAP fetch op timeout (%v) for %s — force-closing fetch connection", d, w.acct.Email)
		if w.fetchClient != nil {
			w.fetchClient.Close()
		}
	})
	return func() { t.Stop() }, fired.Load
}

// ErrFetchSuperseded reports that a body fetch was skipped because the user
// selected another message first. It is expected while scrolling, not a failure.
var ErrFetchSuperseded = errors.New("fetch superseded")

// readBodyLiteral reads a FETCH body literal up to maxBodySize and always
// consumes the rest, so an oversized body or a read error cannot leave the
// literal open and desynchronize the connection for every later command.
func readBodyLiteral(r io.Reader) ([]byte, error) {
	return readLimitedDrain(r, maxBodySize)
}

func readLimitedDrain(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit))
	if _, drainErr := io.Copy(io.Discard, r); err == nil {
		err = drainErr
	}
	return data, err
}

// IsConnected returns true if the IMAP client exists.
func (w *IMAPWorker) IsConnected() bool {
	return w.client != nil
}

// Ping checks if the IMAP connection is alive by issuing a NOOP command.
// Returns true if the connection is healthy.
func (w *IMAPWorker) Ping() bool {
	if w.client == nil {
		return false
	}
	w.opMu.Lock()
	defer w.opMu.Unlock()
	cancel := w.opTimer(10 * time.Second)
	defer cancel()

	if err := w.client.Noop().Wait(); err != nil {
		log.Printf("IMAP ping failed for %s: %v", w.acct.Email, err)
		return false
	}
	return true
}

// ListMailboxes fetches the list of mailboxes and syncs them to the cache.
func (w *IMAPWorker) ListMailboxes() ([]string, error) {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	return w.listMailboxes()
}

// listMailboxes fetches the server's mailbox names and refreshes folderMap.
// The caller must have exclusive access to the main client: either hold opMu
// or call this during Connect before the worker is published.
func (w *IMAPWorker) listMailboxes() ([]string, error) {
	if w.client == nil {
		return nil, fmt.Errorf("not connected")
	}
	cancel := w.opTimer(opDeadline)
	defer cancel()

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
		displayName := email.SanitizeLine(FolderName(mbox.Mailbox))
		// Always populate folderMap (needed for IMAP operations like restore).
		w.folderMap[displayName] = mbox.Mailbox
		// Skip redundant folders from sync and sidebar.
		if w.acct.ShouldSkipFolder(displayName) {
			continue
		}
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
	cancel := w.opTimer(opDeadline)
	defer cancel()

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
	return w.syncFolder(folder, false, onProgress...)
}

// SyncFolderFull fetches an authoritative folder snapshot and only then
// replaces the cached headers. This reconciles server-side moves and deletes
// that incremental UID fetching cannot observe.
func (w *IMAPWorker) SyncFolderFull(folder string, onProgress ...func(fetched int)) (int, error) {
	return w.syncFolder(folder, true, onProgress...)
}

// VerifyFolder reconciles cached UIDs and mutable flags with the server while
// preserving UIDs covered by queued optimistic writes.
func (w *IMAPWorker) VerifyFolder(folder string) (VerifyResult, error) {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	result := VerifyResult{Account: w.acct.Email, Folder: folder}
	if w.client == nil {
		return result, fmt.Errorf("not connected")
	}
	started := time.Now()
	cancel := w.opTimer(syncDeadline)
	defer cancel()
	sel, err := w.client.Select(w.imapMailboxName(folder), nil).Wait()
	if err != nil {
		return result, fmt.Errorf("SELECT %s: %w", folder, err)
	}
	storedValidity, _ := w.store.GetUIDValidity(w.acct.Email, folder)
	if storedValidity != 0 && storedValidity != sel.UIDValidity {
		if err := w.store.PurgeFolder(w.acct.Email, folder); err != nil {
			return result, err
		}
		result.UIDValidityReset = true
	}
	_ = w.store.SetUIDValidity(w.acct.Email, folder, sel.UIDValidity)
	server := make(map[uint32]cache.MessageState)
	// Existence comes from UID SEARCH, not FETCH: a message whose FETCH
	// response fails to parse must not be treated as deleted.
	exists := make(map[uint32]bool)
	if sel.NumMessages > 0 {
		search, err := w.client.UIDSearch(&imap.SearchCriteria{}, nil).Wait()
		if err != nil {
			return result, fmt.Errorf("UID SEARCH ALL: %w", err)
		}
		uids := search.AllUIDs()
		for _, uid := range uids {
			exists[uint32(uid)] = true
		}
		for start := 0; start < len(uids); start += imapUIDChunkSize {
			end := start + imapUIDChunkSize
			if end > len(uids) {
				end = len(uids)
			}
			var chunk imap.UIDSet
			for _, uid := range uids[start:end] {
				chunk.AddNum(uid)
			}
			fetch := w.client.Fetch(chunk, &imap.FetchOptions{UID: true, Flags: true})
			for {
				data := fetch.Next()
				if data == nil {
					break
				}
				buf, collectErr := data.Collect()
				if collectErr != nil {
					continue
				}
				state := cache.MessageState{UID: uint32(buf.UID), Unread: true}
				for _, flag := range buf.Flags {
					if flag == imap.FlagSeen {
						state.Unread = false
					}
					if flag == imap.FlagFlagged {
						state.Flagged = true
					}
					if flag == imap.FlagAnswered {
						state.Answered = true
					}
				}
				if state.UID != 0 {
					server[state.UID] = state
				}
			}
			if err := fetch.Close(); err != nil {
				return result, fmt.Errorf("FETCH UID FLAGS chunk %d-%d: %w", start, end-1, err)
			}
		}
	}
	result.Messages = len(exists)
	cached, err := w.store.MessageStates(w.acct.Email, folder)
	if err != nil {
		return result, err
	}
	pending := w.store.PendingUIDsSince(w.acct.Email, folder, started)
	var stale, missing []uint32
	for uid, local := range cached {
		if pending[uid] {
			continue
		}
		if !exists[uid] {
			stale = append(stale, uid)
			continue
		}
		remote, ok := server[uid]
		if !ok {
			continue // exists, but its flags could not be read this pass
		}
		if local.Unread != remote.Unread {
			if err := w.store.SetUnreadUIDs(w.acct.Email, folder, []uint32{uid}, remote.Unread); err != nil {
				return result, err
			}
			result.FlagsCorrected++
		}
		if local.Flagged != remote.Flagged {
			if err := w.store.SetFlaggedUIDs(w.acct.Email, folder, []uint32{uid}, remote.Flagged); err != nil {
				return result, err
			}
			result.FlagsCorrected++
		}
		if local.Answered != remote.Answered {
			if err := w.store.SetAnsweredUIDs(w.acct.Email, folder, []uint32{uid}, remote.Answered); err != nil {
				return result, err
			}
			result.FlagsCorrected++
		}
	}
	for uid := range exists {
		if _, ok := cached[uid]; !ok && !pending[uid] {
			missing = append(missing, uid)
		}
	}
	if err := w.store.RemoveMessagesByUID(w.acct.Email, folder, stale); err != nil {
		return result, err
	}
	result.StaleRemoved = len(stale)
	for start := 0; start < len(missing); start += imapUIDChunkSize {
		end := start + imapUIDChunkSize
		if end > len(missing) {
			end = len(missing)
		}
		var set imap.UIDSet
		for _, uid := range missing[start:end] {
			set.AddNum(imap.UID(uid))
		}
		fetch := w.client.Fetch(set, headerFetchOptions())
		for {
			data := fetch.Next()
			if data == nil {
				break
			}
			buf, collectErr := data.Collect()
			if collectErr != nil {
				continue
			}
			if err := w.store.UpsertMessage(w.acct.Email, folder, parseFetchedHeaders(buf)); err != nil {
				return result, err
			}
			result.MissingAdded++
		}
		if err := fetch.Close(); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (w *IMAPWorker) syncFolder(folder string, full bool, onProgress ...func(fetched int)) (int, error) {
	w.opMu.Lock()
	defer w.opMu.Unlock()

	if w.client == nil {
		return 0, fmt.Errorf("not connected")
	}
	cancel := w.opTimer(syncDeadline)
	defer cancel()

	imapName := w.imapMailboxName(folder)

	selCmd := w.client.Select(imapName, nil)
	selData, err := selCmd.Wait()
	if err != nil {
		return 0, fmt.Errorf("SELECT %s: %w", imapName, err)
	}

	// Check UIDVALIDITY.
	storedUV, _ := w.store.GetUIDValidity(w.acct.Email, folder)
	if !full {
		if storedUV != 0 && storedUV != selData.UIDValidity {
			w.store.PurgeFolder(w.acct.Email, folder)
		}
		w.store.SetUIDValidity(w.acct.Email, folder, selData.UIDValidity)
	}

	if selData.NumMessages == 0 {
		if full {
			if err := w.store.ReplaceFolderHeaders(w.acct.Email, folder, nil, selData.UIDValidity); err != nil {
				return 0, fmt.Errorf("reset empty folder cache: %w", err)
			}
		}
		return 0, nil
	}

	// Find highest stored UID for incremental sync.
	highUID, _ := w.store.HighestUID(w.acct.Email, folder)
	if full {
		highUID = 0
	}

	var seqSet imap.UIDSet
	if highUID > 0 {
		seqSet.AddRange(imap.UID(highUID+1), imap.UID(math.MaxUint32))
	} else {
		seqSet.AddRange(1, imap.UID(math.MaxUint32))
	}

	fetchOptions := headerFetchOptions()

	fetchCmd := w.client.Fetch(seqSet, fetchOptions)
	newCount := 0
	var snapshot []email.Message

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

		msg := parseFetchedHeaders(buf)
		if full {
			snapshot = append(snapshot, msg)
		} else {
			if err := w.store.UpsertMessage(w.acct.Email, folder, msg); err != nil {
				log.Printf("upsert message uid=%d folder=%s: %v", buf.UID, folder, err)
				continue
			}
		}
		newCount++
		if len(onProgress) > 0 && onProgress[0] != nil && newCount%100 == 0 {
			onProgress[0](newCount)
		}
	}

	if err := fetchCmd.Close(); err != nil {
		return newCount, fmt.Errorf("FETCH: %w", err)
	}
	if full {
		if err := w.store.ReplaceFolderHeaders(w.acct.Email, folder, snapshot, selData.UIDValidity); err != nil {
			return 0, fmt.Errorf("replace folder cache: %w", err)
		}
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
	cancel := w.opTimer(fetchBodyDeadline)
	defer cancel()

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
				data, err := readBodyLiteral(bs.Literal)
				if err != nil {
					continue
				}
				parsed, err := w.parseBody(data)
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
	w.cacheBody(folder, uid, result)

	return result, nil
}

// SetWantedFetchUID records which UID the user currently wants fetched.
// Stale fetches (for a different UID) will skip after acquiring the lock.
func (w *IMAPWorker) SetWantedFetchUID(uid uint32) {
	w.wantedFetchUID.Store(uid)
}

// FetchBodyDirect fetches a message body using the dedicated fetch connection,
// bypassing the main opMu so it doesn't block behind sync operations.
// Falls back to the main connection if the fetch client is unavailable.
// If a newer fetch has been requested (wantedFetchUID changed), this fetch
// returns early to avoid blocking the one the user actually wants.
func (w *IMAPWorker) FetchBodyDirect(folder string, uid uint32) (BodyResult, error) {
	return w.fetchBodyDirect(folder, uid, false)
}

// PeekBodyDirect fetches and caches a message body without setting the IMAP
// \Seen flag. It is used for background/MCP review where reading must not
// change mailbox state.
func (w *IMAPWorker) PeekBodyDirect(folder string, uid uint32) (BodyResult, error) {
	return w.fetchBodyDirect(folder, uid, true)
}

func (w *IMAPWorker) fetchBodyDirect(folder string, uid uint32, peek bool) (BodyResult, error) {
	// Skip if the user has already moved on — before queueing for the lock,
	// and again after, since the wait can be long.
	if wanted := w.wantedFetchUID.Load(); wanted != 0 && wanted != uid {
		return BodyResult{}, fmt.Errorf("%w: UID %d superseded by UID %d", ErrFetchSuperseded, uid, wanted)
	}
	w.fetchMu.Lock()
	defer w.fetchMu.Unlock()
	if wanted := w.wantedFetchUID.Load(); wanted != 0 && wanted != uid {
		return BodyResult{}, fmt.Errorf("%w: UID %d superseded by UID %d", ErrFetchSuperseded, uid, wanted)
	}

	cancel, timedOut := w.fetchOpTimer(fetchBodyDeadline)
	defer cancel()

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
		BodySection: []*imap.FetchItemBodySection{{Peek: peek}},
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
				data, err := readBodyLiteral(bs.Literal)
				if err != nil {
					continue
				}
				parsed, err := w.parseBody(data)
				if err != nil {
					result = BodyResult{Text: string(data)}
				} else {
					result = parsed
				}
			}
		}
	}

	if err := fetchCmd.Close(); err != nil {
		if timedOut() {
			return result, fmt.Errorf("FETCH body: timed out after %s", fetchBodyDeadline)
		}
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
						BodySection: []*imap.FetchItemBodySection{{Peek: peek}},
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
								data, err := readBodyLiteral(bs.Literal)
								if err != nil {
									continue
								}
								parsed, err := w.parseBody(data)
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
	w.cacheBody(folder, uid, result)

	return result, nil
}

// FetchRawMessage fetches the full RFC 5322 message bytes for a UID.
func (w *IMAPWorker) FetchRawMessage(folder string, uid uint32) ([]byte, error) {
	w.opMu.Lock()
	defer w.opMu.Unlock()

	if w.client == nil {
		return nil, fmt.Errorf("not connected")
	}
	cancel := w.opTimer(fetchBodyDeadline)
	defer cancel()

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
				data, err := readBodyLiteral(bs.Literal)
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
	return w.setFlagBatch(folder, uids, imap.FlagSeen, true)
}

func (w *IMAPWorker) MarkUnreadBatch(folder string, uids []uint32) error {
	return w.setFlagBatch(folder, uids, imap.FlagSeen, false)
}
func (w *IMAPWorker) SetFlaggedBatch(folder string, uids []uint32, flagged bool) error {
	return w.setFlagBatch(folder, uids, imap.FlagFlagged, flagged)
}

func (w *IMAPWorker) setFlagBatch(folder string, uids []uint32, flag imap.Flag, add bool) error {
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
	selCmd := w.client.Select(imapName, nil)
	if _, err := selCmd.Wait(); err != nil {
		return fmt.Errorf("SELECT %s: %w", imapName, err)
	}

	for i := 0; i < len(uids); i += imapUIDChunkSize {
		end := i + imapUIDChunkSize
		if end > len(uids) {
			end = len(uids)
		}
		var seqSet imap.UIDSet
		for _, uid := range uids[i:end] {
			seqSet.AddNum(imap.UID(uid))
		}

		op := imap.StoreFlagsDel
		if add {
			op = imap.StoreFlagsAdd
		}
		storeCmd := w.client.Store(seqSet, &imap.StoreFlags{
			Op:    op,
			Flags: []imap.Flag{flag},
		}, nil)
		if err := storeCmd.Close(); err != nil {
			return fmt.Errorf("STORE flag %s chunk %d-%d of %d: %w", flag, i, end-1, len(uids), err)
		}
	}
	return nil
}

// MarkAllRead marks every message in a folder read without enumerating UIDs.
// The UID range also covers messages that the local cache has not seen yet.
func (w *IMAPWorker) MarkAllRead(folder string) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()
	if w.client == nil {
		return fmt.Errorf("not connected")
	}
	cancel := w.opTimer(w.commandDeadline)
	defer cancel()
	imapName := w.imapMailboxName(folder)
	if _, err := w.client.Select(imapName, nil).Wait(); err != nil {
		return fmt.Errorf("SELECT %s: %w", imapName, err)
	}
	var all imap.UIDSet
	all.AddRange(1, imap.UID(math.MaxUint32))
	cmd := w.client.Store(all, &imap.StoreFlags{
		Op:    imap.StoreFlagsAdd,
		Flags: []imap.Flag{imap.FlagSeen},
	}, nil)
	if err := cmd.Close(); err != nil {
		return fmt.Errorf("STORE +FLAGS \\Seen for all messages: %w", err)
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
	if _, err := appendCmd.Wait(); err != nil {
		return fmt.Errorf("APPEND %s: %w", imapName, err)
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
	cancel := w.opTimer(opDeadline)
	defer cancel()

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

	if err := w.expunge(seqSet); err != nil {
		return err
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
	cancel := w.opTimer(w.commandDeadline)
	selCmd := w.client.Select(imapName, nil)
	_, err := selCmd.Wait()
	cancel()
	if err != nil {
		return fmt.Errorf("SELECT %s: %w", imapName, err)
	}

	for i := 0; i < len(uids); i += imapUIDChunkSize {
		end := i + imapUIDChunkSize
		if end > len(uids) {
			end = len(uids)
		}
		chunk := uids[i:end]

		cancel = w.opTimer(w.commandDeadline)
		err = w.moveChunkToTrash(trashName, chunk)
		cancel()
		if err != nil {
			return fmt.Errorf("chunk %d-%d: %w", i, end-1, err)
		}
		log.Printf("IMAP delete: processed %d/%d UIDs", end, len(uids))
		if onProgress != nil {
			log.Printf("IMAP delete: sending progress %d/%d", end, len(uids))
			onProgress(end, len(uids))
		}
	}

	// Single EXPUNGE after all chunks.
	var all imap.UIDSet
	for _, uid := range uids {
		all.AddNum(imap.UID(uid))
	}
	cancel = w.opTimer(w.commandDeadline)
	err = w.expunge(all)
	cancel()
	if err != nil {
		return err
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
	cancel := w.opTimer(opDeadline)
	defer cancel()

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

	if err := w.expunge(seqSet); err != nil {
		return err
	}

	return nil
}

// MoveToFolderBatch moves multiple messages from one folder to another via IMAP.
func (w *IMAPWorker) MoveToFolderBatch(srcFolder string, uids []uint32, dstFolder string) error {
	_, err := w.MoveToFolderBatchWithUIDs(srcFolder, uids, dstFolder)
	return err
}

// MoveToFolderBatchWithUIDs moves messages with native IMAP MOVE (or the
// library's UID-safe fallback) and returns destination UID mappings when the
// server advertises UIDPLUS/IMAP4rev2.
func (w *IMAPWorker) MoveToFolderBatchWithUIDs(srcFolder string, uids []uint32, dstFolder string) ([]UIDMove, error) {
	w.opMu.Lock()
	defer w.opMu.Unlock()

	if w.client == nil {
		return nil, fmt.Errorf("not connected")
	}
	if len(uids) == 0 {
		return nil, nil
	}

	srcName := w.imapMailboxName(srcFolder)
	dstName := w.imapMailboxName(dstFolder)

	cancel := w.opTimer(w.commandDeadline)
	selCmd := w.client.Select(srcName, nil)
	_, err := selCmd.Wait()
	cancel()
	if err != nil {
		return nil, fmt.Errorf("SELECT %s: %w", srcName, err)
	}

	moves := make([]UIDMove, 0, len(uids))
	for i := 0; i < len(uids); i += imapUIDChunkSize {
		end := i + imapUIDChunkSize
		if end > len(uids) {
			end = len(uids)
		}

		var seqSet imap.UIDSet
		for _, uid := range uids[i:end] {
			seqSet.AddNum(imap.UID(uid))
		}

		cancel = w.opTimer(w.commandDeadline)
		moveData, moveErr := w.client.Move(seqSet, dstName).Wait()
		cancel()
		if moveErr != nil {
			return moves, fmt.Errorf("MOVE to %s chunk %d-%d: %w", dstName, i, end-1, moveErr)
		}

		destinations := make(map[uint32]uint32)
		if moveData != nil {
			sourceSet, sourceOK := moveData.SourceUIDs.(imap.UIDSet)
			destinationSet, destinationOK := moveData.DestUIDs.(imap.UIDSet)
			if sourceOK && destinationOK {
				sources, sourcesOK := sourceSet.Nums()
				dests, destsOK := destinationSet.Nums()
				if sourcesOK && destsOK && len(sources) == len(dests) {
					for index := range sources {
						destinations[uint32(sources[index])] = uint32(dests[index])
					}
				}
			}
		}
		for _, source := range uids[i:end] {
			moves = append(moves, UIDMove{Source: source, Destination: destinations[source]})
		}
	}
	return moves, nil
}

// DeleteMailbox deletes a mailbox on the IMAP server.
func (w *IMAPWorker) DeleteMailbox(folder string) error {
	w.opMu.Lock()
	defer w.opMu.Unlock()

	if w.client == nil {
		return fmt.Errorf("not connected")
	}
	cancel := w.opTimer(opDeadline)
	defer cancel()

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

// expunge permanently removes the given just-deleted UIDs from the selected
// mailbox. With UIDPLUS it uses UID EXPUNGE, so messages another client has
// flagged \Deleted but not yet expunged are left alone; a plain EXPUNGE would
// purge those too.
func (w *IMAPWorker) expunge(uids imap.UIDSet) error {
	caps := w.client.Caps()
	if caps.Has(imap.CapUIDPlus) || caps.Has(imap.CapIMAP4rev2) {
		if err := w.client.UIDExpunge(uids).Close(); err != nil {
			return fmt.Errorf("UID EXPUNGE: %w", err)
		}
		return nil
	}
	if err := w.client.Expunge().Close(); err != nil {
		return fmt.Errorf("EXPUNGE: %w", err)
	}
	return nil
}
