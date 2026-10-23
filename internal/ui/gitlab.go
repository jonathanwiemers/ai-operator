package ui

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/oauth2"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
	"github.com/jonathanwiemers/ai-operator/internal/naming"
)

// Paths of the GitLab connection.
const (
	pathGitLabLogin    = "/oauth2/gitlab/login"
	pathGitLabCallback = "/oauth2/gitlab/callback"
	pathGitLabLogout   = "/oauth2/gitlab/logout"
)

// gitlabTokenTTL is how long a GitLab connection is kept: the refresh token
// renews the access token (GitLab's last two hours) until then.
const gitlabTokenTTL = 30 * 24 * time.Hour

// GitLabToken is the content of the GitLab connection cookie. It belongs to
// the UI user who connected it and is never sent to the app's JavaScript.
type GitLabToken struct {
	Owner    string `json:"owner"`
	Access   string `json:"at"`
	Refresh  string `json:"rt"`
	Expires  int64  `json:"exp"`
	Username string `json:"user"`
	UserID   int64  `json:"uid"`
	// Until ends the connection, however often the token is refreshed.
	Until int64 `json:"until"`
}

// gitlabConn runs the OAuth flow against the configured GitLab and keeps the
// users' tokens in encrypted cookies.
type gitlabConn struct {
	cfg      *Config
	sessions *Sessions
	// newClient makes the API client of a user's token; replaced in tests.
	newClient func(baseURL, token string) gitlab.API
}

func (g *gitlabConn) cookie() string      { return g.sessions.cookieName("ai-operator-gitlab") }
func (g *gitlabConn) loginCookie() string { return g.sessions.cookieName("ai-operator-gitlab-login") }

func (g *gitlabConn) oauth2Config() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     g.cfg.GitLab.ClientID,
		ClientSecret: g.cfg.GitLabClientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  g.cfg.GitLab.URL + "/oauth/authorize",
			TokenURL: g.cfg.GitLab.URL + "/oauth/token",
		},
		RedirectURL: g.cfg.ExternalURL + pathGitLabCallback,
		// GitLab has no narrower scope that may post comments.
		Scopes: []string{"api"},
	}
}

// login sends a logged-in user to GitLab to connect their account.
func (g *gitlabConn) login(w http.ResponseWriter, r *http.Request) {
	st := LoginState{State: rand.Text(), Verifier: oauth2.GenerateVerifier(), Return: safeReturn(r.URL.Query().Get("return"))}
	exp := g.sessions.now().Add(loginStateTTL)
	st.Expires = exp.Unix()
	if err := g.sessions.write(w, g.loginCookie(), st, exp); err != nil {
		http.Error(w, "cannot start the GitLab connection", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, g.oauth2Config().AuthCodeURL(st.State, oauth2.S256ChallengeOption(st.Verifier)), http.StatusFound)
}

// callback redeems GitLab's code and keeps the token.
func (g *gitlabConn) callback(s *Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var st LoginState
		ok := g.sessions.read(r, g.loginCookie(), &st) && g.sessions.now().Unix() < st.Expires
		g.sessions.Clear(w, g.loginCookie())
		q := r.URL.Query()
		back := func(code string) {
			ret := st.Return
			if ret == "" {
				ret = "/"
			}
			if code != "" {
				sep := "?"
				if strings.Contains(ret, "?") {
					sep = "&"
				}
				ret += sep + "gitlab_error=" + url.QueryEscape(code)
			}
			http.Redirect(w, r, ret, http.StatusFound)
		}
		switch {
		case !ok:
			back("expired")
			return
		case q.Get("state") != st.State:
			back("state")
			return
		case q.Get("error") != "":
			s.log.Info("GitLab refused the connection", "error", q.Get("error"))
			back("refused")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		tok, err := g.oauth2Config().Exchange(ctx, q.Get("code"), oauth2.VerifierOption(st.Verifier))
		if err != nil {
			s.log.Error(err, "GitLab code exchange failed")
			back("token")
			return
		}
		u, err := g.newClient(g.cfg.GitLab.URL, tok.AccessToken).CurrentUser(ctx)
		if err != nil {
			s.log.Error(err, "reading the GitLab user failed")
			back("token")
			return
		}
		gt := GitLabToken{Owner: principalOf(r).Username, Access: tok.AccessToken, Refresh: tok.RefreshToken,
			Expires: tok.Expiry.Unix(), Username: u.Username, UserID: u.ID, Until: g.sessions.now().Add(gitlabTokenTTL).Unix()}
		if err := g.store(w, gt); err != nil {
			s.log.Error(err, "cannot keep the GitLab token")
			back("token")
			return
		}
		s.log.Info("GitLab connected", "user", gt.Owner, "gitlabUser", gt.Username)
		back("")
	}
}

func (g *gitlabConn) store(w http.ResponseWriter, t GitLabToken) error {
	return g.sessions.write(w, g.cookie(), t, time.Unix(t.Until, 0))
}

// logout forgets the user's GitLab token.
func (g *gitlabConn) logout(w http.ResponseWriter, _ *http.Request) {
	g.sessions.Clear(w, g.cookie())
	writeJSON(w, http.StatusOK, map[string]string{"status": "disconnected"})
}

// errNotConnected: the user has no (usable) GitLab connection.
var errNotConnected = errors.New("GitLab is not connected")

// errGitLabUnavailable: refreshing the token failed for a passing reason.
var errGitLabUnavailable = errors.New("GitLab is not reachable")

// token returns the request user's GitLab token, refreshed if it is about to
// expire. A token of another UI user (a shared browser) does not count.
func (g *gitlabConn) token(w http.ResponseWriter, r *http.Request) (*GitLabToken, error) {
	var t GitLabToken
	if !g.sessions.read(r, g.cookie(), &t) || t.Owner != principalOf(r).Username || t.Access == "" || g.sessions.now().Unix() >= t.Until {
		return nil, errNotConnected
	}
	if g.sessions.now().Add(time.Minute).Unix() < t.Expires {
		return &t, nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	tok, err := g.oauth2Config().TokenSource(ctx, &oauth2.Token{RefreshToken: t.Refresh, Expiry: time.Unix(1, 0)}).Token()
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && re.ErrorCode == "invalid_grant" {
			// Revoked, or another tab refreshed it just now: that tab's
			// cookie wins; without it, connect again.
			return nil, errNotConnected
		}
		// GitLab is not reachable: keep the connection.
		return nil, errGitLabUnavailable
	}
	t.Access, t.Expires = tok.AccessToken, tok.Expiry.Unix()
	if tok.RefreshToken != "" {
		t.Refresh = tok.RefreshToken
	}
	if err := g.store(w, t); err != nil {
		return nil, err
	}
	return &t, nil
}

// serves reports whether a project lives on the connected GitLab.
func (g *gitlabConn) serves(ap *aiv1.AgentProject) bool {
	u := ap.Spec.GitLab.URL
	if u == "" {
		u = "https://gitlab.com"
	}
	return strings.TrimSuffix(u, "/") == g.cfg.GitLab.URL
}

// GitLabStatusView says whether GitLab is configured and connected.
type GitLabStatusView struct {
	Enabled   bool   `json:"enabled"`
	URL       string `json:"url,omitempty"`
	Connected bool   `json:"connected"`
	Username  string `json:"username,omitempty"`
	Login     string `json:"login,omitempty"`
}

// gitlabStatus says whether GitLab is configured and connected.
func (s *Server) gitlabStatus(w http.ResponseWriter, r *http.Request) {
	out := GitLabStatusView{}
	if s.gitlab != nil {
		out = GitLabStatusView{Enabled: true, URL: s.cfg.GitLab.URL, Login: pathGitLabLogin}
		if t, err := s.gitlab.token(w, r); err == nil {
			out.Connected, out.Username = true, t.Username
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// NoteView is a GitLab comment as the app shows it.
type NoteView struct {
	ID        int64      `json:"id"`
	Author    string     `json:"author"`
	Body      string     `json:"body"`
	System    bool       `json:"system,omitempty"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
}

// ThreadPart is the comments of one issue or merge request.
type ThreadPart struct {
	Target string     `json:"target"` // issue, mr, tracking
	IID    int64      `json:"iid"`
	WebURL string     `json:"webURL"`
	Notes  []NoteView `json:"notes"`
}

// targets are the issue, merge request and tracking issue of a task, which
// the thread shows and a comment may go to.
func targets(ap *aiv1.AgentProject, t *aiv1.AgentTask) []ThreadPart {
	web := strings.TrimSuffix(ap.Spec.GitLab.URL, "/")
	if web == "" {
		web = "https://gitlab.com"
	}
	web += "/" + ap.Spec.GitLab.Project + "/-/"
	var out []ThreadPart
	if t.Spec.Issue != nil {
		out = append(out, ThreadPart{Target: "issue", IID: t.Spec.Issue.IID, WebURL: fmt.Sprintf("%sissues/%d", web, t.Spec.Issue.IID)})
	}
	if iid := t.Status.Run.MergeRequestIID; iid != 0 {
		out = append(out, ThreadPart{Target: "mr", IID: iid, WebURL: fmt.Sprintf("%smerge_requests/%d", web, iid)})
	}
	if ap := t.Status.Autopilot; ap != nil && ap.TrackingIssueIID != 0 {
		out = append(out, ThreadPart{Target: "tracking", IID: ap.TrackingIssueIID, WebURL: fmt.Sprintf("%sissues/%d", web, ap.TrackingIssueIID)})
	}
	return out
}

// steeringTask loads the task of the request and the user's GitLab client.
// It answers the request itself when that is not possible.
func (s *Server) steeringTask(w http.ResponseWriter, r *http.Request) (*aiv1.AgentProject, *aiv1.AgentTask, gitlab.API, *GitLabToken, bool) {
	ap, tasks, ok := s.loadProject(w, r)
	if !ok {
		return nil, nil, nil, nil, false
	}
	var task *aiv1.AgentTask
	for i := range tasks {
		if tasks[i].Name == r.PathValue("task") {
			task = &tasks[i]
		}
	}
	switch {
	case task == nil:
		notFound(w)
		return nil, nil, nil, nil, false
	case s.gitlab == nil || !s.gitlab.serves(ap):
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "this project's GitLab is not connected to the UI"})
		return nil, nil, nil, nil, false
	}
	tok, err := s.gitlab.token(w, r)
	switch {
	case errors.Is(err, errGitLabUnavailable):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "GitLab is not reachable right now"})
		return nil, nil, nil, nil, false
	case err != nil:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "connect your GitLab account first", "login": pathGitLabLogin})
		return nil, nil, nil, nil, false
	}
	return ap, task, s.gitlab.newClient(s.cfg.GitLab.URL, tok.Access), tok, true
}

// thread returns the comments of the task's issue, merge request and
// tracking issue, read with the user's own GitLab token.
func (s *Server) thread(w http.ResponseWriter, r *http.Request) {
	ap, task, gl, _, ok := s.steeringTask(w, r)
	if !ok {
		return
	}
	project := ap.Spec.GitLab.Project
	parts := targets(ap, task)
	for i := range parts {
		var notes []gitlab.Note
		var err error
		if parts[i].Target == "mr" {
			notes, err = gl.ListMergeRequestNotes(r.Context(), project, parts[i].IID)
		} else {
			notes, err = gl.ListIssueNotes(r.Context(), project, parts[i].IID)
		}
		if err != nil {
			if gitlab.StatusCode(err) == http.StatusUnauthorized {
				s.gitlab.sessions.Clear(w, s.gitlab.cookie())
				writeJSON(w, http.StatusConflict, map[string]string{"error": "connect your GitLab account again", "login": pathGitLabLogin})
				return
			}
			// What the user may not read stays empty.
			s.log.V(1).Info("cannot read comments", "target", parts[i].Target, "iid", parts[i].IID, "error", err.Error())
			continue
		}
		parts[i].Notes = make([]NoteView, 0, len(notes))
		for _, n := range notes {
			parts[i].Notes = append(parts[i].Notes, NoteView{ID: n.ID, Author: n.Author.Username, Body: n.Body, System: n.System, CreatedAt: n.CreatedAt})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"parts": parts})
}

// maxNoteBody bounds a comment posted from the UI.
const maxNoteBody = 10000

// Rate limit of posted comments per user.
const (
	notesPerWindow = 10
	notesWindow    = time.Minute
)

// noteLimiter counts the comments each user posted recently.
type noteLimiter struct {
	mu    sync.Mutex
	posts map[string][]time.Time
}

func (l *noteLimiter) allow(user string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.posts == nil {
		l.posts = map[string][]time.Time{}
	}
	recent := l.posts[user][:0]
	for _, t := range l.posts[user] {
		if now.Sub(t) < notesWindow {
			recent = append(recent, t)
		}
	}
	if len(recent) >= notesPerWindow {
		l.posts[user] = recent
		return false
	}
	l.posts[user] = append(recent, now)
	return true
}

// postNote writes a comment on the task's issue, merge request or tracking
// issue, in the user's GitLab name. The UI does not interpret it: the
// operator handles it like any comment written in GitLab.
func (s *Server) postNote(w http.ResponseWriter, r *http.Request) {
	p := principalOf(r)
	if !s.authz.CanSteer(p, r.PathValue("namespace"), r.PathValue("name")) {
		// The project itself may still be invisible: loadProject decides.
		if !s.authz.CanView(p, r.PathValue("namespace"), r.PathValue("name")) {
			notFound(w)
			return
		}
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "no access to steering"})
		return
	}
	var req struct {
		Target string `json:"target"`
		Body   string `json:"body"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*maxNoteBody)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	req.Body = strings.TrimSpace(req.Body)
	if req.Body == "" || utf8.RuneCountInString(req.Body) > maxNoteBody {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("the comment must have 1 to %d characters", maxNoteBody)})
		return
	}
	ap, task, gl, tok, ok := s.steeringTask(w, r)
	if !ok {
		return
	}
	var target *ThreadPart
	for _, t := range targets(ap, task) {
		if t.Target == req.Target {
			target = &t
		}
	}
	if target == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the task has no " + req.Target})
		return
	}
	// The MR and tracking issue numbers come from the task's status, which
	// the agent pods may write: check that they are the task's before a
	// user's comment goes there.
	if err := s.checkTarget(r.Context(), gl, ap, task, target); err != nil {
		s.log.Info("refusing a comment target", "user", p.Username, "target", target.Target, "iid", target.IID, "reason", err.Error())
		writeJSON(w, http.StatusConflict, map[string]string{"error": "the " + target.Target + " could not be confirmed as this task's"})
		return
	}
	if !s.notes.allow(p.Username, time.Now()) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many comments, try again in a minute"})
		return
	}
	var n *gitlab.Note
	var err error
	if target.Target == "mr" {
		n, err = gl.CreateMergeRequestNote(r.Context(), ap.Spec.GitLab.Project, target.IID, req.Body)
	} else {
		n, err = gl.CreateIssueNote(r.Context(), ap.Spec.GitLab.Project, target.IID, req.Body)
	}
	if err != nil {
		code := gitlab.StatusCode(err)
		s.log.Info("posting a comment failed", "user", p.Username, "gitlabUser", tok.Username, "target", target.Target, "iid", target.IID, "status", code)
		if code == http.StatusUnauthorized {
			s.gitlab.sessions.Clear(w, s.gitlab.cookie())
			writeJSON(w, http.StatusConflict, map[string]string{"error": "connect your GitLab account again", "login": pathGitLabLogin})
			return
		}
		if code == http.StatusForbidden || code == http.StatusNotFound {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "GitLab did not let you comment there"})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "GitLab did not take the comment"})
		return
	}
	// Who posted what where, never the text.
	s.log.Info("comment posted", "user", p.Username, "gitlabUser", tok.Username, "project", ap.Namespace+"/"+ap.Name,
		"target", target.Target, "iid", target.IID, "note", n.ID)
	writeJSON(w, http.StatusOK, NoteView{ID: n.ID, Author: tok.Username, Body: n.Body, CreatedAt: n.CreatedAt})
}

// checkTarget confirms that the MR or tracking issue a comment goes to
// belongs to the task: the MR's source branch is the task issue's work
// branch, the tracking issue carries the operator's marker. The issue
// itself comes from the task's spec, which only the operator writes.
func (s *Server) checkTarget(ctx context.Context, gl gitlab.API, ap *aiv1.AgentProject, task *aiv1.AgentTask, t *ThreadPart) error {
	project := ap.Spec.GitLab.Project
	switch t.Target {
	case "mr":
		mr, err := gl.GetMergeRequest(ctx, project, t.IID)
		if err != nil {
			return err
		}
		if task.Spec.Issue == nil || !naming.IsIssueBranch(mr.SourceBranch, task.Spec.Issue.IID) {
			return fmt.Errorf("!%d is not on the work branch of the task's issue", t.IID)
		}
	case "tracking":
		is, err := gl.GetIssue(ctx, project, t.IID)
		if err != nil {
			return err
		}
		if !marker.IsTrackingIssue(is.Description) || is.Milestone == nil || task.Spec.Milestone == nil || is.Milestone.ID != task.Spec.Milestone.ID {
			return fmt.Errorf("#%d is not the tracking issue of the task's milestone", t.IID)
		}
	}
	return nil
}
