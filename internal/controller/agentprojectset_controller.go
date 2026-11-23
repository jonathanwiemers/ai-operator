package controller

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
	"github.com/jonathanwiemers/ai-operator/internal/webhook"
)

// maxProjectSetComplexity is the discovery query score above which a set is
// Ready=False, reason TooComplex: past it, the next growth of the group may
// hit GitLab's limit.
const maxProjectSetComplexity = 200

// AgentProjectSetReconciler discovers the projects of a set's GitLab group
// every pollInterval and writes one child AgentProject per active project.
type AgentProjectSetReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// APIReader reads uncached: the live task check before a child is
	// deleted.
	APIReader client.Reader
	// NewDiscoverer creates the GraphQL discovery client; replaced in tests.
	NewDiscoverer func(baseURL, token string) gitlab.Discoverer
	Now           func() time.Time
	// Deliveries and Wake are the webhook receiver's delivery store and
	// waker: a child whose milestones changed gets a delivery and a wake.
	// Nil: the children see the change on their next full sync.
	Deliveries *webhook.Deliveries
	Wake       func(types.NamespacedName)
	// Wakes, when set, reconciles the set of every event at once; SendWake
	// sends to it (the set waker's Wake, which allows one per 30s).
	Wakes    <-chan event.GenericEvent
	SendWake func(types.NamespacedName)

	woken    sync.Map // sets a hook delivery woke: they discover at once
	failures hookFailures
}

// WakeSet makes the set discover at once: a hook it registered on a dormant
// project delivered.
func (r *AgentProjectSetReconciler) WakeSet(n types.NamespacedName) {
	r.woken.Store(n, true)
	if r.SendWake != nil {
		r.SendWake(n)
	}
}

// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agentprojectsets,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agentprojectsets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agentprojectsets/finalizers,verbs=update
// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agentprojects,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ai.wiemers.me,resources=agenttasks,verbs=list
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get

func (r *AgentProjectSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var set aiv1.AgentProjectSet
	if err := r.Get(ctx, req.NamespacedName, &set); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !set.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, &set)
	}
	if err := r.addHookFinalizer(ctx, &set); err != nil {
		return ctrl.Result{}, err
	}
	_, woken := r.woken.LoadAndDelete(req.NamespacedName)
	orig := set.DeepCopy()
	spec := set.Spec.WithDefaults()
	poll := spec.PollInterval.Duration
	now := r.now()

	tmpl, reason, msg := checkProjectSet(spec)
	if reason != "" {
		setProjectSetCondition(&set, aiv1.ConditionReady, false, reason, msg)
		return r.patchStatus(ctx, &set, orig, poll)
	}

	// An idle set sends nothing between polls; a spec change or a delivery
	// to a set hook discovers at once.
	if last := set.Status.LastDiscoveryTime; last != nil && set.Status.ObservedGeneration == set.Generation && !woken {
		if age := now.Sub(last.Time); age >= 0 && age < poll {
			return ctrl.Result{RequeueAfter: poll - age}, nil
		}
	}

	d, err := r.gitlabClient(ctx, &set, spec)
	var disc *gitlab.GroupDiscovery
	if err == nil {
		defer countGitLabRequests(set.Name, d)
		disc, err = r.discover(ctx, d, spec, tmpl)
	}
	if err != nil {
		if gitlab.IsTransient(err) {
			logger.Info("Group discovery failed, trying again on the next poll", "error", err.Error())
		} else {
			logger.Error(err, "Group discovery failed")
		}
		// The children and the last entries stay: an outage reshapes nothing.
		setProjectSetCondition(&set, aiv1.ConditionDiscovered, false, aiv1.ReasonDiscoveryFailed, textutil.Truncate(err.Error(), 1024))
		setProjectSetReady(&set)
		return r.patchStatus(ctx, &set, orig, poll)
	}

	var projects aiv1.AgentProjectList
	if err := r.List(ctx, &projects); err != nil {
		return ctrl.Result{}, err
	}
	var tasks aiv1.AgentTaskList
	if err := r.List(ctx, &tasks, client.InNamespace(set.Namespace), client.HasLabels{LabelProject}); err != nil {
		return ctrl.Result{}, err
	}
	counts := map[string]int{}
	for _, t := range tasks.Items {
		counts[t.Labels[LabelProject]]++
	}
	plan := planProjectSet(projectSetInput{
		Set: &set, Template: tmpl, Discovery: disc, Projects: projects.Items, TaskCounts: counts, Now: now,
	})

	existing := map[string]*aiv1.AgentProject{}
	for i := range projects.Items {
		if p := &projects.Items[i]; p.Namespace == set.Namespace {
			existing[p.Name] = p
		}
	}
	for _, child := range plan.Children {
		taken, err := r.applyChild(ctx, &set, child, existing[child.Name])
		switch {
		case err != nil:
			// The next poll writes it again: its hash still differs.
			logger.Error(err, "Writing child AgentProject failed", "child", child.Name)
		case taken:
			plan.nameTaken(child.Name)
		}
	}
	r.removeChildren(ctx, &set, &plan, existing, counts, now)
	r.syncSetHooks(ctx, &set, d, tmpl, &plan, projects.Items, now)
	changed := milestoneChanges(&set, &plan, disc, projects.Items)

	stamp := metav1.NewTime(now)
	set.Status.Projects = plan.Entries
	set.Status.Counts = plan.Counts
	set.Status.DroppedTargets = plan.Dropped
	set.Status.Complexity = &aiv1.ProjectSetComplexity{Score: clampInt32(disc.Complexity.Score), Limit: clampInt32(disc.Complexity.Limit)}
	set.Status.LastDiscoveryTime = &stamp
	set.Status.ObservedGeneration = set.Generation
	setProjectSetCondition(&set, aiv1.ConditionDiscovered, true, "Discovered",
		fmt.Sprintf("%d projects in %d queries", len(disc.Projects), disc.Posts))
	setProjectSetReady(&set)
	logger.V(1).Info("Group discovered", "projects", len(disc.Projects), "queries", disc.Posts,
		"active", plan.Counts.Active, "complexity", disc.Complexity.Score, "woken", len(changed))
	res, err := r.patchStatus(ctx, &set, orig, poll)
	if err == nil {
		// Once the new hashes are stored: a failed patch wakes them on the
		// next poll instead, not twice.
		r.wakeChildren(changed)
	}
	return res, err
}

// checkProjectSet decodes the template, or says why the set cannot use its
// spec: the condition reason and message.
func checkProjectSet(spec aiv1.AgentProjectSetSpec) (tmpl aiv1.AgentProjectSpec, reason, msg string) {
	if problems := spec.Validate(); len(problems) > 0 {
		return tmpl, aiv1.ReasonInvalidSpec, strings.Join(problems, "; ")
	}
	tmpl, err := spec.DecodeTemplate()
	if err != nil {
		return tmpl, aiv1.ReasonInvalidTemplate, textutil.Truncate(err.Error(), 1024)
	}
	if problem := spec.WebhookProblem(tmpl); problem != "" {
		return tmpl, aiv1.ReasonWebhookRequired, "set children need webhooks: " + problem
	}
	return tmpl, "", ""
}

// gitlabClient reads the set's token and creates its GitLab client.
func (r *AgentProjectSetReconciler) gitlabClient(ctx context.Context, set *aiv1.AgentProjectSet, spec aiv1.AgentProjectSetSpec) (gitlab.Discoverer, error) {
	ref := spec.GitLab.TokenSecretRef
	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: set.Namespace, Name: ref.Name}, &secret); err != nil {
		return nil, fmt.Errorf("reading GitLab token secret: %w", err)
	}
	token := strings.TrimSpace(string(secret.Data[ref.Key]))
	if token == "" {
		return nil, fmt.Errorf("secret %s has no key %q", ref.Name, ref.Key)
	}
	return r.NewDiscoverer(strings.TrimRight(spec.GitLab.URL, "/"), token), nil
}

// discover reads the group.
func (r *AgentProjectSetReconciler) discover(ctx context.Context, d gitlab.Discoverer, spec aiv1.AgentProjectSetSpec, tmpl aiv1.AgentProjectSpec) (*gitlab.GroupDiscovery, error) {
	// Like a project's poll: the client's retries stop at this deadline.
	dctx, cancel := context.WithTimeout(ctx, max(spec.PollInterval.Duration, syncTimeout))
	defer cancel()
	disc, err := d.DiscoverGroup(dctx, spec.GitLab.Group, tmpl.Workflow.WithDefaults().ReadyLabel)
	if err != nil {
		return nil, fmt.Errorf("discovering group %s: %w", spec.GitLab.Group, err)
	}
	return disc, nil
}

// applyChild creates a missing child and updates an owned one whose template
// hash differs. taken reports a create that lost a race for the name.
func (r *AgentProjectSetReconciler) applyChild(ctx context.Context, set *aiv1.AgentProjectSet, c plannedChild, cur *aiv1.AgentProject) (taken bool, err error) {
	if cur == nil {
		p := &aiv1.AgentProject{
			ObjectMeta: metav1.ObjectMeta{
				Name: c.Name, Namespace: set.Namespace,
				Labels:      map[string]string{aiv1.LabelProjectSet: set.Name},
				Annotations: map[string]string{aiv1.AnnotationTemplateHash: c.Hash},
			},
			Spec: c.Spec,
		}
		if err := controllerutil.SetControllerReference(set, p, r.Scheme); err != nil {
			return false, err
		}
		err := r.Create(ctx, p)
		if apierrors.IsAlreadyExists(err) {
			return true, nil
		}
		return false, err
	}
	if !metav1.IsControlledBy(cur, set) {
		return true, nil // the planner skips these; a guard all the same
	}
	// A removal in progress leaves the hash but switches the webhook off: the
	// planned child is revived.
	if cur.Annotations[aiv1.AnnotationTemplateHash] == c.Hash && cur.Labels[aiv1.LabelProjectSet] == set.Name &&
		cur.Annotations[aiv1.AnnotationRemoving] == "" && cur.Spec.Webhook.Enabled == c.Spec.Webhook.Enabled {
		return false, nil
	}
	cur.Spec = c.Spec
	delete(cur.Annotations, aiv1.AnnotationRemoving)
	metav1.SetMetaDataAnnotation(&cur.ObjectMeta, aiv1.AnnotationTemplateHash, c.Hash)
	metav1.SetMetaDataLabel(&cur.ObjectMeta, aiv1.LabelProjectSet, set.Name)
	return false, r.Update(ctx, cur)
}

// nameTaken marks the entry of a child whose create found the name taken.
func (p *projectSetPlan) nameTaken(name string) {
	for i := range p.Entries {
		if e := &p.Entries[i]; e.Child == name {
			e.State, e.Child = aiv1.ProjectSetNameTaken, ""
			e.Reason = fmt.Sprintf("name %s is taken by another AgentProject", name)
		}
	}
	p.Counts = projectSetCounts(p.Entries)
}

// clampInt32 converts an int GitLab sent into the int32 of a status field.
// It saturates instead of wrapping, so a score past the range still reads
// as too complex.
func clampInt32(v int) int32 {
	switch {
	case v > math.MaxInt32:
		return math.MaxInt32
	case v < math.MinInt32:
		return math.MinInt32
	}
	return int32(v)
}

// setProjectSetReady sets Ready from the last discovery's complexity.
func setProjectSetReady(set *aiv1.AgentProjectSet) {
	if c := set.Status.Complexity; c != nil && c.Score > maxProjectSetComplexity {
		setProjectSetCondition(set, aiv1.ConditionReady, false, aiv1.ReasonTooComplex, fmt.Sprintf(
			"discovery query complexity %d passes %d of the limit %d: split the set", c.Score, maxProjectSetComplexity, c.Limit))
		return
	}
	setProjectSetCondition(set, aiv1.ConditionReady, true, aiv1.ReasonReady, "the set discovers its group")
}

func setProjectSetCondition(set *aiv1.AgentProjectSet, typ string, ok bool, reason, msg string) {
	meta.SetStatusCondition(&set.Status.Conditions, metav1.Condition{
		Type: typ, Status: boolCondition(ok), Reason: reason, Message: msg, ObservedGeneration: set.Generation,
	})
}

func (r *AgentProjectSetReconciler) patchStatus(ctx context.Context, set, orig *aiv1.AgentProjectSet, poll time.Duration) (ctrl.Result, error) {
	if err := r.Status().Patch(ctx, set, client.MergeFrom(orig)); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: poll}, nil
}

func (r *AgentProjectSetReconciler) now() time.Time { return nowOr(r.Now) }

// SetupWithManager registers the controller. It does not watch the children:
// their status changes on every poll and must not re-run discovery.
func (r *AgentProjectSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	b := ctrl.NewControllerManagedBy(mgr).
		For(&aiv1.AgentProjectSet{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("agentprojectset")
	if r.Wakes != nil {
		b = b.WatchesRawSource(source.Channel(r.Wakes, &handler.EnqueueRequestForObject{}))
	}
	return b.Complete(r)
}
