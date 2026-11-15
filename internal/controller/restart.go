package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

const (
	nothingToRestart = "Nothing to restart: no run is in progress."
	notOwnerRestart  = "Only the milestone's owners can restart a run."
)

// restartRun restarts the run in progress of task (issue iid) by deleting
// its pod: the AgentTask reconciler makes a new one and the runner starts
// the active request again as an interruption, counted against
// MaxInterruptions. ActiveRunRequest is kept (resetting it is hibernation).
// The note on the issue comes first, so a failed note deletes nothing. by
// says who restarted it; it is made inert.
//
// key names the request (a chat command, a pilot action) and makes it
// idempotent: the note records it with the pod it restarts, and a request
// applied again (its answer was not saved, the delete failed) finds the
// bot's note and deletes only that pod, if it is still there.
func (r *AgentProjectReconciler) restartRun(ctx context.Context, gl gitlab.API, spec aiv1.AgentProjectSpec,
	task *aiv1.AgentTask, iid int64, by, key string, bot int64) (string, error) {
	run := task.Status.Run
	if run.ActiveRunRequest <= run.ObservedRunRequest {
		return nothingToRestart, nil
	}
	var pod corev1.Pod
	if err := r.Get(ctx, types.NamespacedName{Namespace: task.Namespace, Name: task.Name}, &pod); apierrors.IsNotFound(err) || err == nil && !pod.DeletionTimestamp.IsZero() {
		pod = corev1.Pod{}
	} else if err != nil {
		return "", err
	}
	notes, err := gl.ListIssueNotes(ctx, spec.GitLab.Project, iid)
	if err != nil {
		return "", fmt.Errorf("reading the notes of issue #%d: %w", iid, err)
	}
	tag := fmt.Sprintf("<!-- ai-operator:restart=%s pod=", key)
	for _, n := range notes {
		if _, rest, ok := strings.Cut(n.Body, tag); ok && bot != 0 && n.Author.ID == bot {
			if uid, _, _ := strings.Cut(rest, " "); pod.UID != "" && uid == string(pod.UID) {
				return "🔄 The run is restarted.", r.deletePod(ctx, &pod)
			}
			return "🔄 The run is restarted.", nil
		}
	}
	if task.Status.PodName == "" || pod.Name == "" {
		return nothingToRestart, nil
	}
	step := "before its first step"
	if run.CurrentStep != "" {
		step = "in step `" + inertLine(strings.ReplaceAll(run.CurrentStep, "`", "'"), 80) + "`"
	}
	if run.CurrentStepSince != nil {
		age := minutes(r.now().Sub(run.CurrentStepSince.Time))
		if age == "" || strings.HasPrefix(age, "-") {
			age = "less than a minute"
		}
		step += " after " + age
	}
	what := fmt.Sprintf("restart %d of at most %d", run.Interruptions+1, aiv1.MaxInterruptions)
	body := fmt.Sprintf("🔄 Run restarted by %s %s (%s).\n\n%s%s -->", inertLine(by, 100), step, what, tag, pod.UID)
	if _, err := gl.CreateIssueNote(ctx, spec.GitLab.Project, iid, body); err != nil {
		return "", fmt.Errorf("posting the restart note on issue #%d: %w", iid, err)
	}
	log.FromContext(ctx).Info("restarting run", "task", task.Name, "pod", pod.Name, "by", by, "step", run.CurrentStep)
	return fmt.Sprintf("🔄 The run is restarted (%s).", what), r.deletePod(ctx, &pod)
}

// restartUntracked answers a restart by author on a Spec task whose
// autopilot is off: no tracking issue takes a note. The pod it returns is
// deleted once the answer is saved, so a lost save restarts nothing and a
// replay nothing twice.
func (r *AgentProjectReconciler) restartUntracked(ctx context.Context, task *aiv1.AgentTask, author string) (string, *corev1.Pod, error) {
	if !slices.Contains(task.Spec.OwnerSubjects, author) {
		return notOwnerRestart, nil, nil
	}
	run, pod := task.Status.Run, &corev1.Pod{}
	if run.ActiveRunRequest <= run.ObservedRunRequest || task.Status.PodName == "" {
		return nothingToRestart, nil, nil
	}
	if err := r.Get(ctx, types.NamespacedName{Namespace: task.Namespace, Name: task.Name}, pod); apierrors.IsNotFound(err) || err == nil && !pod.DeletionTimestamp.IsZero() {
		return nothingToRestart, nil, nil
	} else if err != nil {
		return "", nil, err
	}
	log.FromContext(ctx).Info("restarting run", "task", task.Name, "pod", pod.Name, "by", author, "step", run.CurrentStep)
	return fmt.Sprintf("🔄 The run is restarted (restart %d of at most %d).", run.Interruptions+1, aiv1.MaxInterruptions), pod, nil
}

func (r *AgentProjectReconciler) deletePod(ctx context.Context, pod *corev1.Pod) error {
	return client.IgnoreNotFound(r.Delete(ctx, pod))
}
