package email

import (
	"strings"
	"testing"
)

func TestGroupThreads(t *testing.T) {
	// Newest first, as the cache returns a folder.
	in := []Message{
		{ID: "7", Subject: "Re: Digifest", ThreadID: "root-a"},
		{ID: "6", Subject: "Lunch", ThreadID: "root-b"},
		{ID: "5", Subject: "no thread info"},
		{ID: "4", Subject: "Re: Digifest", ThreadID: "root-a"},
		{ID: "3", Subject: "also no thread info"},
		{ID: "2", Subject: "Re: Lunch", ThreadID: "root-b"},
		{ID: "1", Subject: "Digifest", ThreadID: "root-a"},
	}
	out, pos := GroupThreads(in)
	var ids []string
	for _, m := range out {
		ids = append(ids, m.ID)
	}
	if got := strings.Join(ids, ","); got != "7,4,1,6,2,5,3" {
		t.Fatalf("order = %s", got)
	}
	want := []ThreadPos{{Count: 3}, {Member: true}, {Member: true}, {Count: 2}, {Member: true}, {}, {}}
	for i := range want {
		if pos[i] != want[i] {
			t.Fatalf("pos[%d] = %+v, want %+v", i, pos[i], want[i])
		}
	}
	if len(out) != len(in) || len(pos) != len(in) {
		t.Fatalf("lengths: %d %d", len(out), len(pos))
	}
	if out, pos := GroupThreads(nil); len(out) != 0 || len(pos) != 0 {
		t.Fatal("empty input")
	}
}
