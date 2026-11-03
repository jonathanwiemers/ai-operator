package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// heartbeat keeps event streams open through proxies that close idle
// connections.
const heartbeat = 25 * time.Second

// maxStreamsPerUser limits the open event streams of one user (a browser
// tab holds one), so a user cannot exhaust the UI's memory with them.
const maxStreamsPerUser = 20

// me returns the logged-in user.
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	p := principalOf(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"username": p.Username,
		"roles":    p.Roles,
		"authMode": s.cfg.Auth.Mode,
	})
}

// projects lists the projects the user may see.
func (s *Server) projects(w http.ResponseWriter, r *http.Request) {
	p := principalOf(r)
	var projects aiv1.AgentProjectList
	if err := s.reader.List(r.Context(), &projects); err != nil {
		s.internalError(w, err)
		return
	}
	var tasks aiv1.AgentTaskList
	if err := s.reader.List(r.Context(), &tasks); err != nil {
		s.internalError(w, err)
		return
	}
	byProject := map[string][]aiv1.AgentTask{}
	for _, t := range tasks.Items {
		key := t.Namespace + "/" + t.Spec.ProjectRef
		byProject[key] = append(byProject[key], t)
	}
	out := []ProjectSummary{}
	now := time.Now()
	for i := range projects.Items {
		ap := &projects.Items[i]
		if !s.authz.CanView(p, ap.Namespace, ap.Name) {
			continue
		}
		out = append(out, summarize(ap, byProject[ap.Namespace+"/"+ap.Name], now))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	writeJSON(w, http.StatusOK, out)
}

// project returns one project with its tasks by milestone.
func (s *Server) project(w http.ResponseWriter, r *http.Request) {
	ap, tasks, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, detail(ap, tasks, time.Now()))
}

// task returns one task of a project.
func (s *Server) task(w http.ResponseWriter, r *http.Request) {
	ap, tasks, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	name := r.PathValue("task")
	now := time.Now()
	for i := range tasks {
		if tasks[i].Name == name {
			writeJSON(w, http.StatusOK, map[string]any{
				"project": summarize(ap, tasks, now),
				"task":    taskView(&tasks[i], ciWindow(ap), now),
				// What the app may offer; the endpoints check again.
				"permissions": map[string]bool{
					"transcript": s.logs != nil && s.authz.CanSeeTranscript(principalOf(r), ap.Namespace, ap.Name),
					// gitlab: the thread can be shown (after connecting).
					"gitlab": s.gitlab != nil && s.gitlab.serves(ap),
					"steer":  s.gitlab != nil && s.gitlab.serves(ap) && s.authz.CanSteer(principalOf(r), ap.Namespace, ap.Name),
					"chat":   s.cfg.ChatEnabled() && s.authz.CanChat(principalOf(r), ap.Namespace, ap.Name),
				},
			})
			return
		}
	}
	notFound(w)
}

// loadProject reads the project of the request path and its tasks. A
// project the user may not see is reported as not found, so the API does
// not reveal which projects exist.
func (s *Server) loadProject(w http.ResponseWriter, r *http.Request) (*aiv1.AgentProject, []aiv1.AgentTask, bool) {
	ns, name := r.PathValue("namespace"), r.PathValue("name")
	if !s.authz.CanView(principalOf(r), ns, name) {
		notFound(w)
		return nil, nil, false
	}
	var ap aiv1.AgentProject
	if err := s.reader.Get(r.Context(), client.ObjectKey{Namespace: ns, Name: name}, &ap); err != nil {
		if apierrors.IsNotFound(err) {
			notFound(w)
		} else {
			s.internalError(w, err)
		}
		return nil, nil, false
	}
	var list aiv1.AgentTaskList
	if err := s.reader.List(r.Context(), &list, client.InNamespace(ns)); err != nil {
		s.internalError(w, err)
		return nil, nil, false
	}
	var tasks []aiv1.AgentTask
	for _, t := range list.Items {
		if t.Spec.ProjectRef == name {
			tasks = append(tasks, t)
		}
	}
	return &ap, tasks, true
}

// events streams change events (server-sent events) of the projects and
// researches the user may see, until the session expires.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	p := principalOf(r)
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.internalError(w, fmt.Errorf("streaming not supported"))
		return
	}
	if s.sessions != nil {
		release, ok := s.acquireStream(p.Username)
		if !ok {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many open event streams"})
			return
		}
		defer release()
	}
	events, cancel := s.hub.Subscribe(func(e Event) bool {
		if e.Kind == EventResearch {
			return s.authz.CanReadResearch(p, e.Namespace, e.Name)
		}
		return s.authz.CanView(p, e.Namespace, e.Project)
	})
	defer cancel()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	// Ask buffering proxies (nginx) to pass events on at once.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "retry: 5000\n: connected\n\n")
	flusher.Flush()

	var expired <-chan time.Time
	if exp := sessionExpiry(r); !exp.IsZero() {
		t := time.NewTimer(time.Until(exp))
		defer t.Stop()
		expired = t.C
	}
	tick := time.NewTicker(heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-expired:
			// The browser reconnects, gets a 401 and asks for a login.
			return
		case <-tick.C:
			fmt.Fprint(w, ": ping\n\n")
		case e := <-events:
			b, _ := json.Marshal(e)
			fmt.Fprintf(w, "event: change\ndata: %s\n\n", b)
		}
		flusher.Flush()
	}
}

// acquireStream counts an event stream of user; release ends it.
func (s *Server) acquireStream(user string) (release func(), ok bool) {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	if s.streams[user] >= maxStreamsPerUser {
		return nil, false
	}
	s.streams[user]++
	return func() {
		s.streamsMu.Lock()
		defer s.streamsMu.Unlock()
		if s.streams[user]--; s.streams[user] <= 0 {
			delete(s.streams, user)
		}
	}, true
}

func (s *Server) internalError(w http.ResponseWriter, err error) {
	s.log.Error(err, "request failed")
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
}

func notFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
