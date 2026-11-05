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

// gitlabConn runs the OAuth flow against the configured GitLab and keeps the
// users' tokens in encrypted cookies.
type gitlabConn struct {
	cfg      *Config
	sessions *Sessions
	// newClient makes the API client of a user's OAuth token, newFixed the
	// one of the fixed token; replaced in tests.
	newClient func(baseURL, token string) gitlab.API
	newFixed  func(baseURL, token string) gitlab.API

	// fixedUser is the GitLab user of the fixed token, read once.
	fixedMu   sync.Mutex
	fixedUser string
}

// oauth reports whether users can connect their own account.
func (g *gitlabConn) oauth() bool { return g.cfg.GitLab.ClientID != "" }

// client returns the API client of a token.
func (g *gitlabConn) client(t *GitLabToken) gitlab.API {
	if t.Fixed {
		return g.newFixed(g.cfg.GitLab.URL, t.Access)
	}
	return g.newClient(g.cfg.GitLab.URL, t.Access)
}

// fixed returns the configured token for its UI user.
func (g *gitlabConn) fixed(ctx context.Context, user string) (*GitLabToken, error) {
	if g.cfg.GitLab.TokenUser == "" || user != g.cfg.GitLab.TokenUser || g.cfg.GitLabToken == "" {
		return nil, errNotConnected
	}
	t := &GitLabToken{Owner: user, Access: g.cfg.GitLabToken, Fixed: true}
	g.fixedMu.Lock()
	defer g.fixedMu.Unlock()
	if g.fixedUser == "" {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		u, err := g.client(t).CurrentUser(ctx)
		switch {
		case gitlab.StatusCode(err) == http.StatusUnauthorized:
			return nil, errNotConnected
		case err != nil:
			return nil, errGitLabUnavailable
		}
		g.fixedUser = u.Username
	}
	t.Username = g.fixedUser
	return t, nil
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

// errNotTasks: a comment target is not the task's.
var errNotTasks = errors.New("not the task's")

// errGitLabUnavailable: refreshing the token failed for a passing reason.
var errGitLabUnavailable = errors.New("GitLab is not reachable")

// token returns the request user's GitLab token, refreshed if it is about to
// expire. A token of another UI user (a shared browser) does not count.
func (g *gitlabConn) token(w http.ResponseWriter, r *http.Request) (*GitLabToken, error) {
	if t, err := g.fixed(r.Context(), principalOf(r).Username); err == nil || errors.Is(err, errGitLabUnavailable) {
		return t, err
	}
	if !g.oauth() {
		return nil, errNotConnected
	}
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
	// Login is where to connect an account; empty without an OAuth
	// application.
	Login string `json:"login,omitempty"`
	// Fixed: connected with the configured token (no disconnect).
	Fixed bool `json:"fixed,omitempty"`
}

// gitlabStatus says whether GitLab is configured and connected.
func (s *Server) gitlabStatus(w http.ResponseWriter, r *http.Request) {
	out := GitLabStatusView{}
	if s.gitlab != nil {
		out = GitLabStatusView{Enabled: true, URL: s.cfg.GitLab.URL}
		if s.gitlab.oauth() {
			out.Login = pathGitLabLogin
		}
		if t, err := s.gitlab.token(w, r); err == nil {
			out.Connected, out.Username, out.Fixed = true, t.Username, t.Fixed
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
	Target string     `json:"target" tstype:"'issue' | 'mr' | 'tracking'"` // issue, mr, tracking
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
	if task == nil {
		notFound(w)
		return nil, nil, nil, nil, false
	}
	gl, tok, ok := s.userGitLab(w, r, ap)
	return ap, task, gl, tok, ok
}

// userGitLab returns the request user's GitLab client for the project. It
// answers the request itself when there is none.
func (s *Server) userGitLab(w http.ResponseWriter, r *http.Request, ap *aiv1.AgentProject) (gitlab.API, *GitLabToken, bool) {
	if s.gitlab == nil || !s.gitlab.serves(ap) {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "this project's GitLab is not connected to the UI"})
		return nil, nil, false
	}
	tok, err := s.gitlab.token(w, r)
	switch {
	case errors.Is(err, errGitLabUnavailable):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "GitLab is not reachable right now"})
		return nil, nil, false
	case err != nil:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "connect your GitLab account first", "login": pathGitLabLogin})
		return nil, nil, false
	}
	return s.gitlab.client(tok), tok, true
}

// thread returns the comments of the task's issue, merge request and
// tracking issue, read with the user's own GitLab token.
func (s *Server) thread(w http.ResponseWriter, r *http.Request) {
	ap, task, gl, tok, ok := s.steeringTask(w, r)
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
				s.gitlabRejected(w, tok)
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

// postNote writes a comment on the task's issue, merge request or tracking
// issue, in the user's GitLab name. The UI does not interpret it: the
// operator handles it like any comment written in GitLab.
func (s *Server) postNote(w http.ResponseWriter, r *http.Request) {
	if !s.mayPost(w, r) {
		return
	}
	req, ok := readNote(w, r)
	if !ok {
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
		s.log.Info("refusing a comment target", "user", principalOf(r).Username, "target", target.Target, "iid", target.IID, "reason", err.Error())
		s.targetRefused(w, tok, "the "+target.Target+" could not be confirmed as this task's", err)
		return
	}
	s.createNote(w, r, ap, gl, tok, target, req.Body)
}

// noteRequest is a comment posted from the UI.
type noteRequest struct {
	Target string `json:"target"`
	Body   string `json:"body"`
}

// mayPost answers a request of a user without the steer grant: 403, or 404
// when the project is not even visible.
func (s *Server) mayPost(w http.ResponseWriter, r *http.Request) bool {
	p := principalOf(r)
	if s.authz.CanSteer(p, r.PathValue("namespace"), r.PathValue("name")) {
		return true
	}
	// The project itself may still be invisible: loadProject decides.
	if !s.authz.CanView(p, r.PathValue("namespace"), r.PathValue("name")) {
		notFound(w)
		return false
	}
	writeJSON(w, http.StatusForbidden, map[string]string{"error": "no access to steering"})
	return false
}

// readNote reads a posted comment, its body trimmed and within the limits.
// It answers 400 itself.
func readNote(w http.ResponseWriter, r *http.Request) (noteRequest, bool) {
	var req noteRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*maxNoteBody)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return req, false
	}
	req.Body = strings.TrimSpace(req.Body)
	if req.Body == "" || utf8.RuneCountInString(req.Body) > maxNoteBody {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("the comment must have 1 to %d characters", maxNoteBody)})
		return req, false
	}
	return req, true
}

// createNote posts body on a confirmed target in the user's name, within
// the user's rate limit, and answers with the note.
func (s *Server) createNote(w http.ResponseWriter, r *http.Request, ap *aiv1.AgentProject, gl gitlab.API, tok *GitLabToken, target *ThreadPart, body string) {
	p := principalOf(r)
	if ok, _ := s.notes.allow(p.Username, time.Now()); !ok {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many comments, try again in a minute"})
		return
	}
	var n *gitlab.Note
	var err error
	if target.Target == "mr" {
		n, err = gl.CreateMergeRequestNote(r.Context(), ap.Spec.GitLab.Project, target.IID, body)
	} else {
		n, err = gl.CreateIssueNote(r.Context(), ap.Spec.GitLab.Project, target.IID, body)
	}
	if err != nil {
		code := gitlab.StatusCode(err)
		s.log.Info("posting a comment failed", "user", p.Username, "gitlabUser", tok.Username, "target", target.Target, "iid", target.IID, "status", code)
		if code == http.StatusUnauthorized {
			s.gitlabRejected(w, tok)
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
		if task.Spec.Issue == nil || !naming.IsIssueBranch(mr.SourceBranch, task.Spec.Issue.IID) || mr.SourceProjectID != mr.ProjectID {
			return fmt.Errorf("%w: !%d is not on the work branch of the task's issue", errNotTasks, t.IID)
		}
	case "tracking":
		is, err := gl.GetIssue(ctx, project, t.IID)
		if err != nil {
			return err
		}
		bot := ap.Status.BotUserID
		if !marker.IsTrackingIssue(is.Description) || bot == 0 || is.Author.ID != bot ||
			is.Milestone == nil || task.Spec.Milestone == nil || is.Milestone.ID != task.Spec.Milestone.ID {
			return fmt.Errorf("%w: #%d is not the tracking issue of the task's milestone", errNotTasks, t.IID)
		}
	}
	return nil
}

// targetRefused answers a request whose target checkTarget refused: 409
// with msg when it is not the task's (or the user cannot read it).
func (s *Server) targetRefused(w http.ResponseWriter, tok *GitLabToken, msg string, err error) {
	switch code := gitlab.StatusCode(err); {
	case errors.Is(err, errNotTasks), code == http.StatusNotFound, code == http.StatusForbidden:
		writeJSON(w, http.StatusConflict, map[string]string{"error": msg})
	case code == http.StatusUnauthorized:
		s.gitlabRejected(w, tok)
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "GitLab is not reachable right now"})
	}
}

// gitlabRejected answers a request GitLab refused the token of: an OAuth
// connection is dropped (connect again), a fixed token must be renewed.
func (s *Server) gitlabRejected(w http.ResponseWriter, tok *GitLabToken) {
	if tok != nil && tok.Fixed {
		s.log.Info("GitLab rejected the fixed token of gitlab.tokenUser; renew it")
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "GitLab rejected the configured token; it must be renewed"})
		return
	}
	s.gitlab.sessions.Clear(w, s.gitlab.cookie())
	writeJSON(w, http.StatusConflict, map[string]string{"error": "connect your GitLab account again", "login": pathGitLabLogin})
}
