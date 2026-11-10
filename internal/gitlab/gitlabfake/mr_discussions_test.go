package gitlabfake

import (
	"context"
	"fmt"
	"testing"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

func TestMergeRequestDiscussions(t *testing.T) {
	ctx := context.Background()
	f := New()
	root := f.AddMRNote(3, gitlab.User{ID: 9, Username: "human"}, "question")
	other := f.AddMRNote(3, gitlab.User{ID: 9, Username: "human"}, "other")

	n, err := f.ReplyMergeRequestDiscussion(ctx, "g/p", 3, "99999", "x")
	if !gitlab.IsNotFound(err) || n != nil {
		t.Fatalf("unknown discussion: %+v %v", n, err)
	}
	bot, err := f.ReplyMergeRequestDiscussion(ctx, "g/p", 3, idOf(root), "bot reply")
	if err != nil {
		t.Fatal(err)
	}
	human := f.AddMRReply(3, root.ID, gitlab.User{ID: 9}, "human reply")

	ds, err := f.ListMergeRequestDiscussions(ctx, "g/p", 3)
	if err != nil || len(ds) != 2 {
		t.Fatalf("threads %+v err %v", ds, err)
	}
	if ds[0].ID != idOf(root) || len(ds[0].Notes) != 3 || ds[0].Notes[1].ID != bot.ID || ds[0].Notes[2].ID != human.ID {
		t.Fatalf("thread 0: %+v", ds[0])
	}
	if ds[1].ID != idOf(other) || len(ds[1].Notes) != 1 {
		t.Fatalf("thread 1: %+v", ds[1])
	}
	if len(f.MRNotes[3]) != 4 {
		t.Fatalf("MRNotes %d", len(f.MRNotes[3]))
	}
	// A reply is not a thread root.
	if _, err := f.ReplyMergeRequestDiscussion(ctx, "g/p", 3, idOf(*bot), "x"); !gitlab.IsNotFound(err) {
		t.Fatalf("reply as root: %v", err)
	}
}

func idOf(n gitlab.Note) string { return fmt.Sprint(n.ID) }
