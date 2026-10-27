// Package webhook receives the deliveries of GitLab project hooks. A delivery
// is only a wake-up call: it names the project to sync now, and counts only
// with the project's secret token. Nothing else in the payload is used.
package webhook

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// Path is where GitLab delivers the project hook's events.
const Path = "/hooks/gitlab"

// MaxBody is the largest delivery read; a larger one is refused.
const MaxBody = 1 << 20

// Receiver is the http.Handler for POST Path. It wakes every AgentProject on
// the delivery's GitLab project whose webhook secret matches X-Gitlab-Token.
type Receiver struct {
	Reader client.Reader
	Wake   func(types.NamespacedName)
	Log    logr.Logger
}

func (r *Receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	log := r.Log.WithValues("event", clip(req.Header.Get("X-Gitlab-Event")), "remote", req.RemoteAddr)
	if req.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
	log = log.WithValues("project", clip(path))

	var projects aiv1.AgentProjectList
	if err := r.Reader.List(req.Context(), &projects); err != nil {
		log.Error(err, "Webhook delivery failed: listing AgentProjects")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	token := req.Header.Get("X-Gitlab-Token")
	var woken []string
	known := false
	for i := range projects.Items {
		p := &projects.Items[i]
		if !strings.EqualFold(p.Spec.GitLab.Project, path) {
			continue
		}
		known = true
		if r.tokenMatches(req, p, token, log) {
			name := types.NamespacedName{Namespace: p.Namespace, Name: p.Name}
			r.Wake(name)
			woken = append(woken, name.String())
		}
	}
	if len(woken) == 0 {
		// Unknown projects get the same answer as bad tokens, so nobody can
		// probe which projects are served.
		log.Info("Webhook delivery refused: unknown project or wrong token", "known", known, "tokenSent", token != "")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	log.Info("Webhook delivery accepted", "woken", woken)
	w.WriteHeader(http.StatusAccepted)
}

// tokenMatches compares the token with the project's webhook secret in
// constant time. A project without a secret, or with an empty one, matches
// no token.
func (r *Receiver) tokenMatches(req *http.Request, p *aiv1.AgentProject, token string, log logr.Logger) bool {
	ref := p.Spec.Webhook.SecretRef
	if ref == nil || token == "" {
		return false
	}
	var secret corev1.Secret
	if err := r.Reader.Get(req.Context(), types.NamespacedName{Namespace: p.Namespace, Name: ref.Name}, &secret); err != nil {
		log.Info("Webhook secret unreadable", "agentProject", p.Namespace+"/"+p.Name, "error", err.Error())
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

// clip bounds a value taken from the request before it is logged.
func clip(s string) string {
	const limit = 256
	if len(s) > limit {
		return s[:limit] + "…"
	}
	return s
}
