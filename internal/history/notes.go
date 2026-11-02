package history

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// maxSeen bounds the note IDs kept in memory; past it the set starts over
// (the database's unique key still keeps a note to one row).
const maxSeen = 50000

type seenNotes struct {
	mu  sync.Mutex
	ids map[int64]struct{}
}

func newSeenNotes() *seenNotes { return &seenNotes{ids: map[int64]struct{}{}} }

// add reports whether id is new.
func (s *seenNotes) add(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.ids[id]; ok {
		return false
	}
	if len(s.ids) >= maxSeen {
		s.ids = map[int64]struct{}{}
	}
	s.ids[id] = struct{}{}
	return true
}

// Project is the AgentProject a GitLab client works for.
type Project struct {
	Namespace, Name string
	// WebURL is the GitLab project's web URL, for the comments' links.
	WebURL string
	// Bot is the username the operator comments as, if known.
	Bot string
}

// WrapGitLab returns api, recording the comments of every issue and merge
// request note listing of p. Without a recorder it returns api unchanged.
func (r *Recorder) WrapGitLab(api gitlab.API, p Project) gitlab.API {
	if r == nil {
		return api
	}
	return &notesAPI{API: api, r: r, p: p}
}

type notesAPI struct {
	gitlab.API
	r *Recorder
	p Project
}

func (n *notesAPI) ListIssueNotes(ctx context.Context, project string, iid int64) ([]gitlab.Note, error) {
	notes, err := n.API.ListIssueNotes(ctx, project, iid)
	if err == nil {
		n.record("issue", iid, notes)
	}
	return notes, err
}

func (n *notesAPI) ListMergeRequestNotes(ctx context.Context, project string, iid int64) ([]gitlab.Note, error) {
	notes, err := n.API.ListMergeRequestNotes(ctx, project, iid)
	if err == nil {
		n.record("mr", iid, notes)
	}
	return notes, err
}

func (n *notesAPI) record(noteable string, iid int64, notes []gitlab.Note) {
	if !n.r.started.Load() {
		return
	}
	for _, note := range notes {
		if note.System || note.ID == 0 || !n.r.notes.add(note.ID) {
			continue
		}
		e := store.Event{
			Key:       fmt.Sprintf("note/%d", note.ID),
			Namespace: n.p.Namespace,
			Project:   n.p.Name,
			Kind:      KindNote,
			Actor:     note.Author.Username,
			Data:      map[string]any{"body": note.Body, "noteable": noteable},
		}
		if note.CreatedAt != nil {
			e.At = *note.CreatedAt
		}
		path := "issues"
		if noteable == "mr" {
			e.MergeRequestIID, path = iid, "merge_requests"
			e.Summary = fmt.Sprintf("%s commented on !%d", note.Author.Username, iid)
		} else {
			e.IssueIID = iid
			e.Summary = fmt.Sprintf("%s commented on #%d", note.Author.Username, iid)
		}
		if n.p.WebURL != "" {
			e.Data["url"] = fmt.Sprintf("%s/-/%s/%d#note_%d", strings.TrimRight(n.p.WebURL, "/"), path, iid, note.ID)
		}
		if n.p.Bot != "" && note.Author.Username == n.p.Bot {
			e.Data["bot"] = true
		}
		n.r.Record(e)
	}
}
