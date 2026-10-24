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

// progressEditTimeout bounds one edit of the progress comment.
const progressEditTimeout = 10 * time.Second

// progressMarker marks the bot's progress comments, so they are kept out of
// the agent's prompt.
const progressMarker = "<!-- ai-operator:progress -->"

// liveHeadline is the headline of a progress comment while its run goes on.
const liveHeadline = "⏳ Working on it."

// isProgress reports whether a note is one of the bot's progress comments.
// Without the bot's ID the marker alone decides.
func isProgress(n gitlab.Note, botID int64) bool {
	return strings.Contains(n.Body, progressMarker) && (botID == 0 || n.Author.ID == botID)
}

// progress keeps one comment on the merge request up to date while a run is
// going on: the run log so far, with the step in progress. When the run ends
// it is deleted (the report follows as a new comment), or turned into a
// short note if the run failed or was deferred.
//
// Edits are sent by a timer goroutine, so a slow GitLab never holds up the
// run; the body is rendered by the run, so the timer never reads the run log
// while it changes.
type progress struct {
	gl      gitlab.API
	project string
	mr      int64
	note    int64
	run     *runLog
	log     logr.Logger

	mu      sync.Mutex // the fields below
	pending string     // the latest body, not sent yet if dirty
	dirty   bool
	last    time.Time
	timer   *time.Timer
	closed  bool

	send sync.Mutex // one request at a time
}

// startProgress posts the progress comment and has every step update it.
// Progress comments a run left behind (the pod was killed) are removed
// first. A failure only costs the live view, so it is logged, not returned.
func (r *Runner) startProgress(ctx context.Context, gl gitlab.API, project string, mr, botID int64) {
	removeLiveProgress(ctx, gl, project, mr, botID)
	p := &progress{gl: gl, project: project, mr: mr, run: r.run, log: r.log()}
	note, err := gl.CreateMergeRequestNote(ctx, project, mr, p.body(liveHeadline, true))
	if err != nil {
		r.log().Info("could not post the progress comment", "error", err.Error())
		return
	}
	p.note, p.last = note.ID, time.Now()
	r.progress = p
	r.run.onChange = func() { p.update(ctx) }
}

// removeLiveProgress deletes the "working" progress comments that runs
// killed with their pod left on the merge request.
func removeLiveProgress(ctx context.Context, gl gitlab.API, project string, mr, botID int64) {
	notes, err := gl.ListMergeRequestNotes(ctx, project, mr)
	if err != nil {
		return
	}
	for _, n := range notes {
		if isProgress(n, botID) && strings.Contains(n.Body, liveHeadline) {
			_ = gl.DeleteMergeRequestNote(ctx, project, mr, n.ID)
		}
	}
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

// update queues an edit: at once if the last one is older than
// progressInterval, else when the interval is over, so the comment never
// lags behind for long (a step such as the implementer can run for minutes).
func (p *progress) update(ctx context.Context) {
	body := p.body(liveHeadline, true)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.pending, p.dirty = body, true
	if p.timer == nil {
		p.timer = time.AfterFunc(max(0, progressInterval-time.Since(p.last)), func() { p.flush(ctx) })
	}
}

// flush sends the pending body; it runs on the timer goroutine.
func (p *progress) flush(ctx context.Context) {
	p.send.Lock()
	defer p.send.Unlock()
	p.mu.Lock()
	p.timer = nil
	if p.closed || !p.dirty {
		p.mu.Unlock()
		return
	}
	body := p.pending
	p.dirty, p.last = false, time.Now()
	p.mu.Unlock()
	// The live view is best effort: an edit does not wait out a long rate
	// limit, which would hold up close.
	ctx, cancel := context.WithTimeout(ctx, progressEditTimeout)
	defer cancel()
	if _, err := p.gl.UpdateMergeRequestNote(ctx, p.project, p.mr, p.note, body); err != nil {
		p.log.V(1).Info("could not update the progress comment", "error", err.Error())
	}
}

// close ends the live view: without a headline the comment is deleted, with
// one it keeps the final run log under that headline. It waits for an edit
// in flight, so that edit cannot overwrite the final state.
func (p *progress) close(ctx context.Context, headline string) error {
	body := p.body(headline, false)
	p.mu.Lock()
	p.closed = true
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	p.mu.Unlock()
	p.send.Lock()
	defer p.send.Unlock()
	if headline == "" {
		return p.gl.DeleteMergeRequestNote(ctx, p.project, p.mr, p.note)
	}
	_, err := p.gl.UpdateMergeRequestNote(ctx, p.project, p.mr, p.note, body)
	return err
}
