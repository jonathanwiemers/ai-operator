package ui

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// Limits of the one-off researches users start from the UI.
const (
	maxCreateBody    = 32 << 10
	maxPromptLength  = 16384 // characters, like the CRD
	maxTopics        = 20
	maxTopicLength   = 100
	createsPerWindow = 10
	createsWindow    = time.Hour
)

// SetResearchWriter sets the client that creates and deletes one-off
// researches (research.allowCreate).
func (s *Server) SetResearchWriter(c client.Client) { s.writer = c }

// ResearchForm is what the one-off form offers.
type ResearchForm struct {
	Namespace string               `json:"namespace"`
	Projects  []FormProject        `json:"projects"`
	Depths    []aiv1.ResearchDepth `json:"depths"`
	Views     []aiv1.ResearchView  `json:"views"`
	Efforts   []string             `json:"efforts"`
}

// FormProject is an AgentProject a one-off can run with. One without a
// writer login is listed as unavailable: its runs could not store results.
type FormProject struct {
	Name      string        `json:"name"`
	Available bool          `json:"available"`
	Reason    string        `json:"reason,omitempty"`
	Models    []ModelChoice `json:"models"`
}

// ModelChoice is an allowed model; Label is "engine/name" (the alias if it
// has one).
type ModelChoice struct {
	Engine aiv1.Engine `json:"engine"`
	Model  string      `json:"model"`
	Label  string      `json:"label"`
}

// createResearchRequest is a one-off research started from the UI. Agent
// nil lets the operator choose.
type createResearchRequest struct {
	Project string              `json:"project"`
	Prompt  string              `json:"prompt"`
	Depth   aiv1.ResearchDepth  `json:"depth"`
	View    aiv1.ResearchView   `json:"view"`
	Topics  []string            `json:"topics"`
	Agent   *aiv1.ResearchAgent `json:"agent"`
}

const noWriterSecret = "the project has no spec.research.writerSecret, so its runs cannot store results"

// writeHandler runs h for logged-in users with the research write grant:
// 404 without research.allowCreate or any research grant (or for a research
// of the path the user may not read), 403 with read only.
func (s *Server) writeHandler(h http.HandlerFunc) http.Handler {
	return s.authed(func(w http.ResponseWriter, r *http.Request) {
		p := principalOf(r)
		read, write := s.authz.ResearchAccess(p)
		ns := r.PathValue("namespace")
		switch {
		case s.writer == nil || !s.cfg.Research.Enabled() || !s.cfg.Research.AllowCreate || !read,
			ns != "" && !s.authz.CanReadResearch(p, ns, r.PathValue("name")):
			notFound(w)
		case !write:
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "no access to starting researches"})
		default:
			h(w, r)
		}
	})
}

// researchForm lists the projects, models, depths and views of the form.
func (s *Server) researchForm(w http.ResponseWriter, r *http.Request) {
	ns := s.cfg.Research.Namespace
	// A name like the one a create would get: the write rule must match it.
	if !s.authz.CanWriteResearch(principalOf(r), ns, oneOffName(time.Now())) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "no access to starting researches in " + ns})
		return
	}
	var list aiv1.AgentProjectList
	if err := s.reader.List(r.Context(), &list, client.InNamespace(ns)); err != nil {
		s.internalError(w, err)
		return
	}
	out := ResearchForm{Namespace: ns, Projects: []FormProject{}, Efforts: aiv1.Efforts,
		Depths: []aiv1.ResearchDepth{aiv1.ResearchDepthQuick, aiv1.ResearchDepthDeep},
		Views:  []aiv1.ResearchView{aiv1.ResearchViewReport, aiv1.ResearchViewNewsflash}}
	for _, ap := range list.Items {
		spec := ap.Spec.WithDefaults()
		p := FormProject{Name: ap.Name, Available: hasWriterSecret(spec), Models: []ModelChoice{}}
		if !p.Available {
			p.Reason = noWriterSecret
		}
		for _, o := range spec.AllowedModels() {
			p.Models = append(p.Models, ModelChoice{Engine: o.Engine, Model: o.Model, Label: o.String()})
		}
		out.Projects = append(out.Projects, p)
	}
	slices.SortFunc(out.Projects, func(a, b FormProject) int { return strings.Compare(a.Name, b.Name) })
	writeJSON(w, http.StatusOK, out)
}

func hasWriterSecret(spec aiv1.AgentProjectSpec) bool {
	return spec.Research != nil && spec.Research.WriterSecret != nil && spec.Research.WriterSecret.Name != ""
}

// createResearch starts a one-off research in research.namespace, named by
// the server and stamped with its creator.
func (s *Server) createResearch(w http.ResponseWriter, r *http.Request) {
	p := principalOf(r)
	var req createResearchRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCreateBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "the request is larger than 32 KiB"})
			return
		}
		badRequest(w, "invalid request")
		return
	}
	ns, name := s.cfg.Research.Namespace, oneOffName(time.Now())
	if !s.authz.CanWriteResearch(p, ns, name) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "no access to starting researches in " + ns})
		return
	}
	if field, msg := validateResearch(&req); msg != "" {
		fieldError(w, field, msg)
		return
	}
	var ap aiv1.AgentProject
	if err := s.reader.Get(r.Context(), client.ObjectKey{Namespace: ns, Name: req.Project}, &ap); apierrors.IsNotFound(err) {
		fieldError(w, "project", fmt.Sprintf("there is no project %q in %s", req.Project, ns))
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	spec := ap.Spec.WithDefaults()
	if !hasWriterSecret(spec) {
		fieldError(w, "project", noWriterSecret)
		return
	}
	if req.Agent != nil {
		a, err := spec.ResolveResearchAgent(*req.Agent)
		if err != nil {
			fieldError(w, "agent", err.Error())
			return
		}
		req.Agent = &a
	}
	if ok, wait := s.creates.allow(p.Username, time.Now()); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int((wait+time.Second-1)/time.Second)))
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": fmt.Sprintf("at most %d researches per hour, try again later", createsPerWindow)})
		return
	}
	ar := &aiv1.AgentResearch{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Annotations: map[string]string{aiv1.AnnotationCreatedBy: p.Username}},
		Spec:       aiv1.AgentResearchSpec{ProjectRef: ap.Name, Prompt: req.Prompt, Topics: req.Topics, View: req.View, Depth: req.Depth, Agent: req.Agent},
	}
	if err := s.writer.Create(r.Context(), ar); err != nil {
		s.internalError(w, err)
		return
	}
	s.log.Info("audit", "action", "research.create", "user", p.Username, "namespace", ns, "name", ar.Name,
		"project", ap.Name, "depth", ar.Spec.Depth, "agent", agentName(ar.Spec.Agent))
	writeJSON(w, http.StatusCreated, map[string]string{"namespace": ns, "name": ar.Name})
}

// validateResearch trims and checks a request and fills in the defaults;
// it returns the problem, if any, and the field it is about.
func validateResearch(req *createResearchRequest) (field, msg string) {
	req.Prompt = strings.TrimSpace(req.Prompt)
	if n := utf8.RuneCountInString(req.Prompt); n < 1 || n > maxPromptLength {
		return "prompt", fmt.Sprintf("the question must have 1 to %d characters", maxPromptLength)
	}
	if req.Project == "" {
		return "project", "project is required"
	}
	if len(req.Topics) > maxTopics {
		return "topics", fmt.Sprintf("at most %d topics", maxTopics)
	}
	for i, t := range req.Topics {
		req.Topics[i] = strings.TrimSpace(t)
		if n := utf8.RuneCountInString(req.Topics[i]); n < 1 || n > maxTopicLength {
			return "topics", fmt.Sprintf("a topic must have 1 to %d characters", maxTopicLength)
		}
	}
	switch req.Depth {
	case "":
		req.Depth = aiv1.ResearchDepthQuick
	case aiv1.ResearchDepthQuick, aiv1.ResearchDepthDeep:
	default:
		return "depth", "depth must be quick or deep"
	}
	switch req.View {
	case "":
		req.View = aiv1.ResearchViewReport
	case aiv1.ResearchViewReport, aiv1.ResearchViewNewsflash:
	default:
		return "view", "view must be report or newsflash"
	}
	return "", ""
}

// oneOffName is q-<yyyymmdd>-<6 random characters>.
func oneOffName(now time.Time) string {
	return "q-" + now.UTC().Format("20060102") + "-" + strings.ToLower(rand.Text()[:6])
}

func agentName(a *aiv1.ResearchAgent) string {
	switch {
	case a == nil:
		return "auto"
	case a.Effort != "":
		return string(a.Engine) + "/" + a.Model + "@" + a.Effort
	}
	return string(a.Engine) + "/" + a.Model
}

// deleteResearch deletes a one-off research the user created in
// research.namespace. Its stored results stay until they expire.
// writeHandler has checked that the user may read it.
func (s *Server) deleteResearch(w http.ResponseWriter, r *http.Request) {
	p := principalOf(r)
	ns, name := r.PathValue("namespace"), r.PathValue("name")
	forbidden := func(msg string) { writeJSON(w, http.StatusForbidden, map[string]string{"error": msg}) }
	if ns != s.cfg.Research.Namespace || !s.authz.CanWriteResearch(p, ns, name) {
		forbidden("only your one-off researches in " + s.cfg.Research.Namespace + " can be deleted here")
		return
	}
	// Read directly: a research created a moment ago may not be cached yet.
	var ar aiv1.AgentResearch
	if err := s.writer.Get(r.Context(), client.ObjectKey{Namespace: ns, Name: name}, &ar); apierrors.IsNotFound(err) {
		notFound(w)
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	if !s.canDelete(p, &ar) {
		forbidden("only your one-off researches in " + s.cfg.Research.Namespace + " can be deleted here")
		return
	}
	err := s.writer.Delete(r.Context(), &ar, client.Preconditions{UID: &ar.UID})
	switch {
	case apierrors.IsNotFound(err) || apierrors.IsConflict(err):
		notFound(w)
		return
	case err != nil:
		s.internalError(w, err)
		return
	}
	s.log.Info("audit", "action", "research.delete", "user", p.Username, "namespace", ns, "name", name,
		"depth", ar.Spec.Depth, "agent", agentName(ar.Spec.Agent))
	w.WriteHeader(http.StatusNoContent)
}

// canDelete reports whether p may delete ar from the UI: a one-off in
// research.namespace that p created, with the write grant.
func (s *Server) canDelete(p Principal, ar *aiv1.AgentResearch) bool {
	return s.writer != nil && ar.Namespace == s.cfg.Research.Namespace && ar.OneOff() && p.Username != "" &&
		ar.Annotations[aiv1.AnnotationCreatedBy] == p.Username && s.authz.CanWriteResearch(p, ar.Namespace, ar.Name)
}

func badRequest(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
}

// fieldError is a 400 about one field of the request, which the form shows
// next to it.
func fieldError(w http.ResponseWriter, field, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg, "field": field})
}
