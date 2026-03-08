package email

// Store is the data access interface used by the TUI layer.
// Implementations include MockStore (hardcoded data) and SQLiteStore (persistent cache).
type Store interface {
	Accounts() []Account
	FoldersFor(email string) []Folder
	MessagesFor(email, folder string) []Message
	MessagesForPage(email, folder string, offset, limit int) []Message
	MessageCount(email, folder string) int
	SaveDraft(email string, msg Message)
	DeleteDraft(email, id string)
	NextDraftID() string
	MarkRead(email, folder, id string)
	DeleteMessage(email, folder, id string)
	DeleteMessages(email, folder string, ids []string)
	SearchMessages(acctEmail, query string, limit int) []Message
}
