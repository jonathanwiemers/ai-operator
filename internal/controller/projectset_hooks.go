package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

const (
	// setHookFinalizer keeps a set until the project hooks it registered are
	// deleted.
	setHookFinalizer = "ai.wiemers.me/project-hooks"
	// setHookBudget is how many hook requests one reconcile of a set sends:
	// a group's worth of dormant projects is hooked over several polls.
	setHookBudget = 6
	// setHookMaxFailures is how often deleting a set hook may fail before the
	// set forgets it.
	setHookMaxFailures = 3
	// setHookRetry is when a deleted set with hooks left tries again.
	setHookRetry = 30 * time.Second
)

// projectHooks is the part of the GitLab client that manages project hooks.
type projectHooks interface {
	ListProjectHooks(ctx context.Context, project string) ([]gitlab.ProjectHook, error)
	AddProjectHook(ctx context.Context, project string, opts gitlab.ProjectHookOptions) (*gitlab.ProjectHook, error)
	EditProjectHook(ctx context.Context, project string, id int64, opts gitlab.ProjectHookOptions) (*gitlab.ProjectHook, error)
	DeleteProjectHook(ctx context.Context, project string, id int64) error
}

// hookFailures counts failed deletes of set hooks by set, path and hook.
type hookFailures struct {
	mu sync.Mutex
	n  map[string]int
}

// fail counts a failure and reports whether the hook is given up.
func (f *hookFailures) fail(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.n == nil {
		f.n = map[string]int{}
	}
	f.n[key]++
	if f.n[key] >= setHookMaxFailures {
		delete(f.n, key)
		return true
	}
	return false
}

func (f *hookFailures) clear(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.n, key)
}

// setHooks sends one reconcile's hook requests within the budget.
type setHooks struct {
	r      *AgentProjectSetReconciler
	set    *aiv1.AgentProjectSet
	gl     projectHooks // nil: no client, every delete fails
	budget int
}

// remove deletes the hook and reports whether the set is done with it:
// deleted, already gone, or failed setHookMaxFailures times.
func (s *setHooks) remove(ctx context.Context, path string, id int64) bool {
	if s.budget <= 0 {
		return false
	}
	key := fmt.Sprintf("%s/%s|%s|%d", s.set.Namespace, s.set.Name, strings.ToLower(path), id)
	err := fmt.Errorf("no GitLab client")
	if s.gl != nil {
		s.budget--
		err = s.gl.DeleteProjectHook(ctx, path, id)
	}
	if err == nil || gitlab.IsNotFound(err) {
		s.r.failures.clear(key)
		return true
	}
	given := s.r.failures.fail(key)
	log.FromContext(ctx).Info("Deleting a set's project hook failed", "project", path, "hook", id, "givenUp", given, "error", err.Error())
	return given
}

// ensure looks the entry's hook up and creates or updates it. Any outcome
// stamps HookCheckedAt, so a failing project waits for the next recheck.
func (s *setHooks) ensure(ctx context.Context, e *aiv1.ProjectSetEntry, opts gitlab.ProjectHookOptions, now time.Time) {
	if s.budget < 2 {
		return
	}
	logger := log.FromContext(ctx).WithValues("project", e.Path)
	e.HookCheckedAt = &metav1.Time{Time: now}
	s.budget--
	hooks, err := s.gl.ListProjectHooks(ctx, e.Path)
	if err != nil {
		logger.Info("Listing a dormant project's hooks failed", "error", err.Error())
		return
	}
	// Ours by the recorded ID, else the one for the URL (status lost).
	var found *gitlab.ProjectHook
	for i := range hooks {
		if e.HookID != 0 && hooks[i].ID == e.HookID {
			found = &hooks[i]
			break
		}
		if found == nil && hooks[i].URL == opts.URL {
			found = &hooks[i]
		}
	}
	switch {
	case found == nil:
		s.budget--
		h, err := s.gl.AddProjectHook(ctx, e.Path, opts)
		if err != nil {
			logger.Info("Creating a dormant project's hook failed", "error", err.Error())
			e.HookID = 0
			return
		}
		e.HookID = h.ID
	case found.ID != e.HookID || !hookMatches(*found, opts):
		// The token cannot be read back: one found by URL is set anew.
		s.budget--
		if _, err := s.gl.EditProjectHook(ctx, e.Path, found.ID, opts); err != nil {
			logger.Info("Updating a dormant project's hook failed", "error", err.Error())
			return
		}
		e.HookID = found.ID
	}
}

// setHookDue: the entry's hook was never checked, or not for an hour plus a
// jitter by path, so the projects are not all checked at once.
func setHookDue(e *aiv1.ProjectSetEntry, now time.Time) bool {
	return e.HookCheckedAt == nil || now.Sub(e.HookCheckedAt.Time) >= webhookRecheck+jitter(e.Path, webhookRecheck/4)
}

// syncSetHooks keeps one project hook, issues and milestone events only, on
// every dormant project without an AgentProject, so a new milestone or ready
// issue wakes the set at once. A hook goes when its project gets an
// AgentProject with a registered hook of its own (one that took the set's
// hook over by URL keeps it), or when the project is excluded or gone.
func (r *AgentProjectSetReconciler) syncSetHooks(ctx context.Context, set *aiv1.AgentProjectSet, d gitlab.Discoverer, tmpl aiv1.AgentProjectSpec,
	plan *projectSetPlan, projects []aiv1.AgentProject, now time.Time) {
	gl, _ := d.(projectHooks)
	if gl == nil {
		return
	}
	s := &setHooks{r: r, set: set, gl: gl, budget: setHookBudget}
	prev := map[string]aiv1.ProjectSetEntry{}
	for _, e := range set.Status.Projects {
		if e.HookID != 0 || e.HookCheckedAt != nil {
			prev[aiv1.NormalizeProject(e.Path)] = e
		}
	}
	watchers := map[string][]*aiv1.AgentProject{}
	for i := range projects {
		p := &projects[i]
		path := aiv1.NormalizeProject(p.Spec.GitLab.Project)
		watchers[path] = append(watchers[path], p)
	}
	opts, err := r.setHookOptions(ctx, set, tmpl)
	if err != nil {
		log.FromContext(ctx).Info("Set hooks are not registered", "error", err.Error())
	}
	for i := range plan.Entries {
		e := &plan.Entries[i]
		path := aiv1.NormalizeProject(e.Path)
		if p, ok := prev[path]; ok {
			e.HookID, e.HookCheckedAt = p.HookID, p.HookCheckedAt
			delete(prev, path)
		}
		want := e.State == aiv1.ProjectSetDormant && e.Child == ""
		switch {
		case want:
			if err == nil && setHookDue(e, now) {
				s.ensure(ctx, e, opts, now)
			}
		case e.HookID == 0:
			e.HookCheckedAt = nil
		default:
			// A child just created is not listed yet: it keeps the hook too.
			taken, registered := hookTakenOver(watchers[path], e.HookID)
			if taken || ((registered || (len(watchers[path]) == 0 && e.Child == "")) && s.remove(ctx, e.Path, e.HookID)) {
				e.HookID, e.HookCheckedAt = 0, nil
			}
		}
	}
	// Projects the include no longer matches: kept as an entry until the
	// hook is deleted.
	for _, p := range prev {
		if p.HookID != 0 && !s.remove(ctx, p.Path, p.HookID) {
			plan.Entries = append(plan.Entries, aiv1.ProjectSetEntry{Path: p.Path, State: aiv1.ProjectSetExcluded,
				Reason: "not found in the group; deleting the set's project hook", HookID: p.HookID, HookCheckedAt: p.HookCheckedAt})
		}
	}
}

// hookTakenOver reports whether an AgentProject of the path took the set's
// hook over (its own hook has the same ID), or registered one of its own.
func hookTakenOver(watchers []*aiv1.AgentProject, id int64) (taken, registered bool) {
	for _, p := range watchers {
		if st := p.Status.Webhook; st != nil && st.HookID == id {
			return true, true
		}
		if meta.IsStatusConditionTrue(p.Status.Conditions, aiv1.ConditionWebhookRegistered) {
			registered = true
		}
	}
	return false, registered
}

// setHookOptions are the set hook's settings: the template's URL and secret,
// issues and milestone events only.
func (r *AgentProjectSetReconciler) setHookOptions(ctx context.Context, set *aiv1.AgentProjectSet, tmpl aiv1.AgentProjectSpec) (gitlab.ProjectHookOptions, error) {
	wh := tmpl.Webhook
	if !wh.Enabled || wh.SecretRef == nil || wh.URL == "" {
		return gitlab.ProjectHookOptions{}, fmt.Errorf("the template has no webhook")
	}
	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: set.Namespace, Name: wh.SecretRef.Name}, &secret); err != nil {
		return gitlab.ProjectHookOptions{}, fmt.Errorf("reading webhook secret: %w", err)
	}
	token := strings.TrimSpace(string(secret.Data[wh.SecretRef.Key]))
	if token == "" {
		return gitlab.ProjectHookOptions{}, fmt.Errorf("secret %s has no key %q", wh.SecretRef.Name, wh.SecretRef.Key)
	}
	return gitlab.ProjectHookOptions{URL: wh.URL, Token: token, IssuesEvents: true, MilestoneEvents: true, EnableSSLVerification: true}, nil
}

// addHookFinalizer adds the finalizer before the set registers a hook.
func (r *AgentProjectSetReconciler) addHookFinalizer(ctx context.Context, set *aiv1.AgentProjectSet) error {
	if controllerutil.ContainsFinalizer(set, setHookFinalizer) {
		return nil
	}
	before := set.DeepCopy()
	controllerutil.AddFinalizer(set, setHookFinalizer)
	return r.Patch(ctx, set, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

// finalize deletes the set's hooks, within the budget per attempt, and lets
// the set go once none is left.
func (r *AgentProjectSetReconciler) finalize(ctx context.Context, set *aiv1.AgentProjectSet) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(set, setHookFinalizer) {
		return ctrl.Result{}, nil
	}
	orig := set.DeepCopy()
	s := &setHooks{r: r, set: set, budget: setHookBudget}
	if slices.ContainsFunc(set.Status.Projects, func(e aiv1.ProjectSetEntry) bool { return e.HookID != 0 }) {
		d, err := r.gitlabClient(ctx, set, set.Spec.WithDefaults())
		if err != nil {
			log.FromContext(ctx).Info("No GitLab client to delete the set's hooks", "error", err.Error())
		} else {
			defer countGitLabRequests(set.Name, d)
			s.gl, _ = d.(projectHooks)
		}
	}
	left := false
	for i := range set.Status.Projects {
		e := &set.Status.Projects[i]
		if e.HookID == 0 {
			continue
		}
		if s.remove(ctx, e.Path, e.HookID) {
			e.HookID, e.HookCheckedAt = 0, nil
		} else {
			left = true
		}
	}
	if err := r.Status().Patch(ctx, set, client.MergeFrom(orig)); err != nil {
		return ctrl.Result{}, err
	}
	if left {
		return ctrl.Result{RequeueAfter: setHookRetry}, nil
	}
	before := set.DeepCopy()
	controllerutil.RemoveFinalizer(set, setHookFinalizer)
	return ctrl.Result{}, r.Patch(ctx, set, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}
