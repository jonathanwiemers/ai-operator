package runner

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// progressInterval bounds how often the progress comment is edited; steps
// that end faster are shown with the next edit.
var progressInterval = 3 * time.Second

// progress keeps one comment on the merge request up to date while a run is
// going on: the run log so far, with the step in progress. When the run ends
// it is deleted (the report follows as a new comment), or turned into a
// short note if the run failed or was deferred.
type progress struct {
	gl      gitlab.API
	project string
	mr      int64
	note    int64
	run     *runLog
	log     logr.Logger

	// The body is rendered by the run, so the timer never reads the run
	// log while it changes.
	mu      sync.Mutex
	pending string
	last    time.Time
	timer   *time.Timer // a delayed edit for changes inside the interval
	closed  bool
}

// startProgress posts the progress comment and has every step update it.
// A failure only costs the live view, so it is logged, not returned.
func (r *Runner) startProgress(ctx context.Context, gl gitlab.API, project string, mr int64) {
	p := &progress{gl: gl, project: project, mr: mr, run: r.run, log: r.log()}
	note, err := gl.CreateMergeRequestNote(ctx, project, mr, p.body("⏳ Working on it.", true))
	if err != nil {
		r.log().Info("could not post the progress comment", "error", err.Error())
		return
	}
	p.note, p.last = note.ID, time.Now()
	r.progress = p
	r.run.onChange = func() { p.update(ctx) }
}

// progressMarker marks the bot's progress comments, so they are kept out of
// the agent's prompt.
const progressMarker = "<!-- ai-operator:progress -->"

// isProgress reports whether a note is one of the bot's progress comments.
func isProgress(n gitlab.Note, botID int64) bool {
	return botID != 0 && n.Author.ID == botID && strings.Contains(n.Body, progressMarker)
}

func (p *progress) body(headline string, live bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n🤖 %s Run `%s`", progressMarker, headline, p.run.id)
	if live {
		b.WriteString(", the steps so far. This comment is replaced by the report when the run ends.")
	}
	b.WriteString(p.run.table(live, nil))
	return b.String()
}

// update edits the comment, at most every progressInterval; a change inside
// the interval is shown by a delayed edit, so the comment never lags behind
// for long (a step such as the implementer can run for many minutes).
func (p *progress) update(ctx context.Context) {
	body := p.body("⏳ Working on it.", true)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.pending = body
	if wait := progressInterval - time.Since(p.last); wait > 0 {
		if p.timer == nil {
			p.timer = time.AfterFunc(wait, func() {
				p.mu.Lock()
				defer p.mu.Unlock()
				p.timer = nil
				p.edit(ctx)
			})
		}
		return
	}
	p.edit(ctx)
}

// edit writes the current run log; p.mu is held.
func (p *progress) edit(ctx context.Context) {
	if p.closed {
		return
	}
	p.last = time.Now()
	if _, err := p.gl.UpdateMergeRequestNote(ctx, p.project, p.mr, p.note, p.pending); err != nil {
		p.log.V(1).Info("could not update the progress comment", "error", err.Error())
	}
}

// close ends the live view: without a headline the comment is deleted, with
// one it keeps the final run log under that headline.
func (p *progress) close(ctx context.Context, headline string) error {
	body := p.body(headline, false)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	if headline == "" {
		return p.gl.DeleteMergeRequestNote(ctx, p.project, p.mr, p.note)
	}
	_, err := p.gl.UpdateMergeRequestNote(ctx, p.project, p.mr, p.note, body)
	return err
}
