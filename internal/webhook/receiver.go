// Package webhook receives the deliveries of GitLab project hooks. A delivery
// is only a wake-up call: it names the project to sync now, and counts only
// with the project's secret token. Beyond the project, only a note event's
// issue IID is used (Deliveries.RecordNote).
package webhook

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// Path is where GitLab delivers the project hook's events.
const Path = "/hooks/gitlab"

// MaxBody is the largest delivery read; a larger one is refused.
const MaxBody = 1 << 20

// Receiver is the http.Handler for POST Path. It wakes every AgentProject on
// the delivery's GitLab project whose webhook secret matches X-Gitlab-Token,
// and records the delivery in Deliveries (when set) before the wake. A
// heartbeat (the test delivery the project asked for) wakes nothing.
//
// When no AgentProject takes the delivery, it wakes every AgentProjectSet
// that registered a hook on the project (status.projects[].hookID) and whose
// template's webhook secret matches: a new milestone in a dormant project.
type Receiver struct {
	Reader     client.Reader
	Wake       func(types.NamespacedName)
	WakeSet    func(types.NamespacedName)
	Deliveries *Deliveries
	Log        logr.Logger
}

// deliveries counts the hook deliveries by event (the X-Gitlab-Event header,
// normalized) and result: accepted (an AgentProject was woken), heartbeat
// (the test delivery a project asked for), set (a set was woken) or refused.
var deliveries = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "ai_operator_webhook_deliveries_total",
	Help: "GitLab hook deliveries by event and result (accepted, heartbeat, set, refused)",
}, []string{"event", "result"})

func init() {
	metrics.Registry.MustRegister(deliveries)
}

// knownEvents are the X-Gitlab-Event values with a series of their own;
// others count as "other", so a sender cannot grow the cardinality.
var knownEvents = map[string]string{
	"Push Hook": "push", "Tag Push Hook": "tag_push", "Issue Hook": "issue", "Confidential Issue Hook": "issue",
	"Note Hook": "note", "Confidential Note Hook": "note", "Merge Request Hook": "merge_request",
	"Pipeline Hook": "pipeline", "Job Hook": "job", "Milestone Hook": "milestone", "Release Hook": "release",
}

func eventLabel(header string) string {
	if e, ok := knownEvents[header]; ok {
		return e
	}
	return "other"
}

func (r *Receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	log := r.Log.WithValues("event", textutil.Truncate(req.Header.Get("X-Gitlab-Event"), 256), "remote", req.RemoteAddr)
	sw := &statusWriter{ResponseWriter: w}
	w = sw
	defer func() {
		result := sw.result
		if result == "" {
			result = "refused"
		}
		deliveries.WithLabelValues(eventLabel(req.Header.Get("X-Gitlab-Event")), result).Inc()
	}()
	if req.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := req.Header.Get("X-Gitlab-Token")
	projects, sets, err := r.owners(req.Context(), token, log)
	if err != nil {
		log.Error(err, "Webhook delivery failed: listing AgentProjects")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// The token is checked before the body is read, so a sender without a
	// valid one cannot make the operator buffer a body.
	if len(projects) == 0 && len(sets) == 0 {
		log.Info("Webhook delivery refused: the token matches no webhook secret", "tokenSent", token != "")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, MaxBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			log.Info("Webhook delivery refused: body too large")
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return
		}
		log.Info("Webhook delivery refused: reading the body failed", "error", err.Error())
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var payload struct {
		Project struct {
			PathWithNamespace string `json:"path_with_namespace"`
		} `json:"project"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.Project.PathWithNamespace == "" {
		log.Info("Webhook delivery refused: no project in the body")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	path := payload.Project.PathWithNamespace
	noteIID := noteIssue(body)
	log = log.WithValues("project", textutil.Truncate(path, 256))

	var woken []string
	heartbeats := 0
	for _, p := range projects {
		if !strings.EqualFold(p.Spec.GitLab.Project, path) {
			continue
		}
		name := types.NamespacedName{Namespace: p.Namespace, Name: p.Name}
		// Recorded first, so the sync the wake starts sees it.
		if r.Deliveries != nil {
			if r.Deliveries.RecordHook(name) {
				heartbeats++
				continue
			}
			if noteIID > 0 {
				r.Deliveries.RecordNote(name, noteIID)
			}
		}
		r.Wake(name)
		woken = append(woken, name.String())
	}
	switch {
	case len(woken) > 0:
		sw.result = "accepted"
		log.Info("Webhook delivery accepted", "woken", woken)
		w.WriteHeader(http.StatusAccepted)
		return
	case heartbeats > 0:
		sw.result = "heartbeat"
		log.V(1).Info("Webhook heartbeat arrived")
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if sets := r.wakeSets(sets, path); len(sets) > 0 {
		sw.result = "set"
		log.Info("Webhook delivery accepted for project sets", "woken", sets)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	// The token belongs to another project. Unknown projects get the same
	// answer as bad tokens, so nobody can probe which projects are served.
	log.Info("Webhook delivery refused: unknown project or wrong token")
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

// owners returns the AgentProjects, and the AgentProjectSets when WakeSet is
// set, whose webhook secret matches token. A delivery may only wake these,
// and only those of them on the project its body names.
func (r *Receiver) owners(ctx context.Context, token string, log logr.Logger) ([]*aiv1.AgentProject, []*aiv1.AgentProjectSet, error) {
	if token == "" {
		return nil, nil, nil
	}
	var list aiv1.AgentProjectList
	if err := r.Reader.List(ctx, &list); err != nil {
		return nil, nil, err
	}
	var projects []*aiv1.AgentProject
	for i := range list.Items {
		p := &list.Items[i]
		if r.tokenMatches(ctx, p, p.Namespace, p.Spec.Webhook.SecretRef, token, log) {
			projects = append(projects, p)
		}
	}
	if r.WakeSet == nil {
		return projects, nil, nil
	}
	var setList aiv1.AgentProjectSetList
	if err := r.Reader.List(ctx, &setList); err != nil {
		log.Error(err, "Listing AgentProjectSets failed")
		return projects, nil, nil
	}
	var sets []*aiv1.AgentProjectSet
	for i := range setList.Items {
		set := &setList.Items[i]
		tmpl, err := set.Spec.DecodeTemplate()
		if err == nil && r.tokenMatches(ctx, set, set.Namespace, tmpl.Webhook.SecretRef, token, log) {
			sets = append(sets, set)
		}
	}
	return projects, sets, nil
}

// wakeSets wakes every set of sets that registered a hook on path, and
// returns their names.
func (r *Receiver) wakeSets(sets []*aiv1.AgentProjectSet, path string) []string {
	var woken []string
	for _, set := range sets {
		hooked := slices.ContainsFunc(set.Status.Projects, func(e aiv1.ProjectSetProjectStatus) bool {
			return e.HookID != 0 && strings.EqualFold(e.Path, path)
		})
		if !hooked {
			continue
		}
		name := types.NamespacedName{Namespace: set.Namespace, Name: set.Name}
		r.WakeSet(name)
		woken = append(woken, name.String())
	}
	return woken
}

// statusWriter remembers the delivery's result for the metric.
type statusWriter struct {
	http.ResponseWriter
	result string
}

// noteIssue returns the IID of the issue a note event is on, and 0 for any
// other event. Its fields are decoded apart, so an odd one changes no answer.
func noteIssue(body []byte) int64 {
	var ev struct {
		ObjectKind       string `json:"object_kind"`
		ObjectAttributes struct {
			NoteableType string `json:"noteable_type"`
		} `json:"object_attributes"`
		Issue struct {
			IID int64 `json:"iid"`
		} `json:"issue"`
	}
	if json.Unmarshal(body, &ev) != nil || ev.ObjectKind != "note" || ev.ObjectAttributes.NoteableType != "Issue" {
		return 0
	}
	return max(ev.Issue.IID, 0)
}

// tokenMatches compares the token with the webhook secret ref in namespace
// in constant time. No secret, or an empty one, matches no token. It runs for
// every owner on every delivery, so an unreadable secret logs at V(1) only.
func (r *Receiver) tokenMatches(ctx context.Context, owner client.Object, namespace string, ref *corev1.SecretKeySelector, token string, log logr.Logger) bool {
	if ref == nil || token == "" {
		return false
	}
	var secret corev1.Secret
	if err := r.Reader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Name}, &secret); err != nil {
		log.V(1).Info("Webhook secret unreadable", "owner", namespace+"/"+owner.GetName(), "error", err.Error())
		return false
	}
	want := strings.TrimSpace(string(secret.Data[ref.Key]))
	if want == "" {
		return false
	}
	// Hashing first gives both sides the same length, which
	// ConstantTimeCompare would otherwise reveal.
	got, exp := sha256.Sum256([]byte(token)), sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(got[:], exp[:]) == 1
}
