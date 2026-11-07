package ui

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/rules"
)

// Bounds of a follow-up milestone created from the Desk: GitLab's title
// limit, and the owner's description without the operator's lines.
const (
	maxFollowUpTitle       = 255
	maxFollowUpDescription = 64 << 10
)

// FollowUpMilestoneView is the follow-up milestone created from the Desk.
type FollowUpMilestoneView struct {
	IID    int64  `json:"iid"`
	WebURL string `json:"webURL"`
}

// followUpRequest asks for a follow-up milestone of the follow-ups
// FollowUps, with the owner's title and description.
type followUpRequest struct {
	FollowUps   []int  `json:"followUps"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// followUpSource is what a follow-up milestone takes from its source: its
// owners (GitLab usernames), owner subjects and follow-ups, by number
// whether they are open.
type followUpSource struct {
	owners, subjects []string
	open             map[int]bool
}

// addFollowUps adds the follow-ups of r: one is open only while every
// source that has it says so.
func (f *followUpSource) addFollowUps(r *RetrospectiveView) {
	if r == nil {
		return
	}
	for _, fu := range r.FollowUps {
		open, seen := f.open[fu.Number]
		f.open[fu.Number] = fu.State == store.FollowUpOpen && (open || !seen)
	}
}

// createFollowUp creates the follow-up milestone of some of a milestone's
// follow-ups with the user's GitLab token: the owner's text, then the
// operator's lines (spec and autopilot markers, the source's owners and
// owner subjects, followup-of). Only the source's owner subjects with the
// steer grant may; follow-ups not open are refused. The operator settles
// them once it reads the followup-of line; the UI writes nothing.
func (s *Server) createFollowUp(w http.ResponseWriter, r *http.Request) {
	if !s.mayPost(w, r) {
		return
	}
	req, ok := readFollowUp(w, r)
	if !ok {
		return
	}
	ap, tasks, iid, ok := s.loadMilestoneIID(w, r)
	if !ok {
		return
	}
	src, ok := s.followUpSource(w, r, ap, tasks, iid)
	if !ok {
		return
	}
	p := principalOf(r)
	if !slices.Contains(src.subjects, p.Username) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only the milestone's owners may create a follow-up milestone"})
		return
	}
	var unknown, taken []int
	for _, n := range req.FollowUps {
		switch open, ok := src.open[n]; {
		case !ok:
			unknown = append(unknown, n)
		case !open:
			taken = append(taken, n)
		}
	}
	if len(unknown) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "the retrospective has no such follow-up", "unknown": unknown})
		return
	}
	if len(taken) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "follow-ups already opened, taken or dismissed", "taken": taken})
		return
	}
	gl, tok, ok := s.userGitLab(w, r, ap)
	if !ok {
		return
	}
	if ok, _ := s.notes.allow(p.Username, time.Now()); !ok {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many requests, try again in a minute"})
		return
	}
	desc := followUpDescription(ap.Spec, req.Description, iid, req.FollowUps, src)
	m, err := gl.CreateMilestone(r.Context(), ap.Spec.GitLab.Project, gitlab.CreateMilestoneOptions{Title: req.Title, Description: desc})
	if err != nil {
		code := gitlab.StatusCode(err)
		s.log.Info("creating a follow-up milestone failed", "user", p.Username, "gitlabUser", tok.Username,
			"project", ap.Namespace+"/"+ap.Name, "milestone", iid, "status", code)
		switch code {
		case http.StatusUnauthorized:
			s.gitlabRejected(w, tok)
		case http.StatusForbidden, http.StatusNotFound:
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "GitLab did not let you create a milestone"})
		case http.StatusBadRequest, http.StatusConflict, http.StatusUnprocessableEntity:
			writeJSON(w, http.StatusConflict, map[string]string{"error": "GitLab refused the milestone; is the title taken?"})
		default:
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "GitLab did not create the milestone"})
		}
		return
	}
	// Who created what from where, never the text.
	s.log.Info("audit", "action", "milestone.followup", "user", p.Username, "gitlabUser", tok.Username,
		"namespace", ap.Namespace, "project", ap.Name, "milestone", iid, "followUps", req.FollowUps, "created", m.IID)
	writeJSON(w, http.StatusCreated, FollowUpMilestoneView{IID: m.IID,
		WebURL: cmp.Or(m.WebURL, fmt.Sprintf("%s/-/milestones/%d", gitLabRef(ap).WebURL, m.IID))})
}

// readFollowUp reads a follow-up request: a title of 1 to 255 characters
// on one line, a description of 1 byte to 64 KiB (both trimmed), at least
// one follow-up (deduplicated and sorted). It answers 400 itself.
func readFollowUp(w http.ResponseWriter, r *http.Request) (followUpRequest, bool) {
	var req followUpRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*maxFollowUpDescription))
	dec.DisallowUnknownFields()
	bad := func(msg string) (followUpRequest, bool) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
		return req, false
	}
	if err := dec.Decode(&req); err != nil {
		return bad("invalid request")
	}
	req.Title, req.Description = strings.TrimSpace(req.Title), strings.TrimSpace(req.Description)
	if n := utf8.RuneCountInString(req.Title); n == 0 || n > maxFollowUpTitle || strings.ContainsAny(req.Title, "\r\n") {
		return bad(fmt.Sprintf("the title must have 1 to %d characters on one line", maxFollowUpTitle))
	}
	if req.Description == "" || len(req.Description) > maxFollowUpDescription {
		return bad(fmt.Sprintf("the description must have 1 byte to %d KiB", maxFollowUpDescription>>10))
	}
	slices.Sort(req.FollowUps)
	if req.FollowUps = slices.Compact(req.FollowUps); len(req.FollowUps) == 0 {
		return bad("name at least one follow-up")
	}
	return req, true
}

// followUpSource reads the owners and follow-ups of the project's milestone
// iid: from its live tasks, else from its snapshot. A live milestone's
// snapshot adds what the operator settled there. It answers the request
// itself (404 without either, 500 when the database fails for a milestone
// without tasks).
func (s *Server) followUpSource(w http.ResponseWriter, r *http.Request, ap *aiv1.AgentProject, tasks []aiv1.AgentTask, iid int64) (followUpSource, bool) {
	src := followUpSource{open: map[int]bool{}}
	web := gitLabRef(ap).WebURL
	st, coord := milestoneTasks(tasks, iid)
	if st != nil {
		src.owners, src.subjects = st.Spec.Owners, st.Spec.OwnerSubjects
		src.addFollowUps(liveRetrospective(web, nil, st, coord))
	}
	var d store.MilestoneDetail
	err := store.ErrNotFound
	if ds := s.milestoneDetailStore(); ds != nil {
		d, err = ds.Milestone(r.Context(), store.ProjectKey{Namespace: ap.Namespace, Name: ap.Name}, iid, time.Now())
	}
	switch {
	case err == nil:
		if st == nil {
			src.owners, src.subjects = d.Owners, d.OwnerSubjects
		}
		src.addFollowUps(storedRetrospective(web, nil, d))
	case st != nil:
		if !errors.Is(err, store.ErrNotFound) {
			s.log.Error(err, "cannot read the milestone's snapshot", "namespace", ap.Namespace, "project", ap.Name, "milestone", iid)
		}
	case errors.Is(err, store.ErrNotFound):
		notFound(w)
		return src, false
	default:
		s.internalError(w, err)
		return src, false
	}
	return src, true
}

// followUpDescription is the description of a follow-up milestone of the
// source's follow-ups numbers: the owner's text without the lines the
// operator reads from it here (owners, owner subjects, followup-of), a
// blank line, then the spec and autopilot markers, the source's owners
// and owner subjects and the followup-of line, each on its own line.
func followUpDescription(spec aiv1.AgentProjectSpec, text string, iid int64, numbers []int, src followUpSource) string {
	prefix := spec.Workflow.WithDefaults().CommandPrefix
	text = notify.WithoutOwnerSubjects(notify.WithoutOwners(text, prefix), prefix)
	text = strings.TrimSpace(rules.WithoutFollowUpOf(text, prefix))
	var lines []string
	if m := spec.Milestones.Marker(); m != "" && m != spec.Milestones.Autopilot() {
		lines = append(lines, m)
	}
	if m := spec.Milestones.Autopilot(); m != "" {
		lines = append(lines, m)
	}
	if owners := notify.Usernames(src.owners); len(owners) > 0 {
		lines = append(lines, prefix+" owners @"+strings.Join(owners, " @"))
	}
	if len(src.subjects) > 0 {
		lines = append(lines, prefix+" owner-subjects "+strings.Join(src.subjects, " "))
	}
	lines = append(lines, rules.FollowUpOfLine(prefix, iid, numbers))
	if text == "" {
		return strings.Join(lines, "\n")
	}
	return text + "\n\n" + strings.Join(lines, "\n")
}
