package email

// ThreadPos describes a message's place in a threaded list.
type ThreadPos struct {
	// Count is the number of messages of the conversation in the list, set
	// on its first row only (0 on the others and for single messages).
	Count int
	// Member marks every row of a conversation after its first.
	Member bool
}

// GroupThreads reorders a newest-first message list so the messages of one
// conversation (same ThreadID) are adjacent, at the position of the
// conversation's newest message, and newest first within it. Messages
// without a ThreadID stay where they are. The second result describes each
// row of the returned list.
func GroupThreads(msgs []Message) ([]Message, []ThreadPos) {
	order := make([]string, 0, len(msgs)) // conversation keys, by newest message
	groups := make(map[string][]Message, len(msgs))
	for i, m := range msgs {
		key := m.ThreadID
		if key == "" {
			// No thread information: a conversation of its own.
			key = "\x00" + m.ID + "\x00" + string(rune(i))
		}
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], m)
	}
	out := make([]Message, 0, len(msgs))
	pos := make([]ThreadPos, 0, len(msgs))
	for _, key := range order {
		group := groups[key]
		for i, m := range group {
			out = append(out, m)
			switch {
			case len(group) == 1:
				pos = append(pos, ThreadPos{})
			case i == 0:
				pos = append(pos, ThreadPos{Count: len(group)})
			default:
				pos = append(pos, ThreadPos{Member: true})
			}
		}
	}
	return out, pos
}
