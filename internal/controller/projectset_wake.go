package controller

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/types"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// milestonesHash identifies a project's active milestones by iid, title and
// updatedAt, in iid order: any edit of a milestone changes it.
func milestonesHash(ms []gitlab.DiscoveredMilestone) string {
	ms = slices.Clone(ms)
	slices.SortFunc(ms, func(a, b gitlab.DiscoveredMilestone) int { return cmp.Compare(a.IID, b.IID) })
	var b strings.Builder
	for _, m := range ms {
		fmt.Fprintf(&b, "%d|%s|%s\n", m.IID, m.Title, m.UpdatedAt.UTC().Format(time.RFC3339Nano))
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// milestoneChanges stores the milestones' hash in every planned entry with a
// child, and returns the children that existed before this poll and whose
// milestones changed since the stored hash. An entry without a stored hash
// (first discovery, status lost) wakes nothing: a restart must not sync
// every child at once.
func milestoneChanges(set *aiv1.AgentProjectSet, plan *projectSetPlan, disc *gitlab.GroupDiscovery, existing map[string]*aiv1.AgentProject) []types.NamespacedName {
	prev := map[string]string{}
	for _, e := range set.Status.Projects {
		prev[aiv1.NormalizeProject(e.Path)] = e.Milestones
	}
	found := map[string][]gitlab.DiscoveredMilestone{}
	for _, dp := range disc.Projects {
		found[aiv1.NormalizeProject(dp.FullPath)] = dp.Milestones
	}
	var wake []types.NamespacedName
	for i := range plan.Entries {
		e := &plan.Entries[i]
		path := aiv1.NormalizeProject(e.Path)
		ms, ok := found[path]
		if e.Child == "" || !ok {
			continue
		}
		e.Milestones = milestonesHash(ms)
		if old := prev[path]; old != "" && old != e.Milestones && existing[e.Child] != nil {
			wake = append(wake, types.NamespacedName{Namespace: set.Namespace, Name: e.Child})
		}
	}
	return wake
}

// wakeChildren records a delivery for each child and wakes it, in the
// receiver's order, so the woken sync sees the delivery and syncs fully.
func (r *AgentProjectSetReconciler) wakeChildren(children []types.NamespacedName) {
	for _, n := range children {
		if r.Deliveries != nil {
			r.Deliveries.Record(n)
		}
		if r.Wake != nil {
			r.Wake(n)
		}
	}
}
