package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// webhookRecheck is how often a registered project hook whose settings did
// not change is looked up in GitLab anyway, to recreate one deleted by hand.
const webhookRecheck = time.Hour

// syncWebhook registers the project hook of spec.webhook, or deletes the one
// it registered once switched off. It never fails the sync: the outcome is
// the WebhookRegistered condition, polling covers the gap, and a failed step
// is tried again on the next poll.
func (r *AgentProjectReconciler) syncWebhook(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec) {
	wh, st := spec.Webhook, project.Status.Webhook
	if !wh.Enabled {
		if st != nil && st.HookID != 0 {
			if err := gl.DeleteProjectHook(ctx, spec.GitLab.Project, st.HookID); err != nil && !gitlab.IsNotFound(err) {
				webhookFailed(ctx, project, "deleting the project hook", err)
				return
			}
		}
		project.Status.Webhook = nil
		meta.RemoveStatusCondition(&project.Status.Conditions, aiv1.ConditionWebhookRegistered)
		return
	}
	if wh.URL == "" || wh.SecretRef == nil {
		setWebhookCondition(project, metav1.ConditionFalse, "Invalid", "spec.webhook.enabled needs spec.webhook.url and spec.webhook.secretRef")
		return
	}
	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: project.Namespace, Name: wh.SecretRef.Name}, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			setWebhookCondition(project, metav1.ConditionFalse, "Invalid", fmt.Sprintf("secret %s not found", wh.SecretRef.Name))
		} else {
			webhookFailed(ctx, project, "reading the webhook secret", err)
		}
		return
	}
	// Trimmed like the receiver does, so the token GitLab sends matches.
	token := strings.TrimSpace(string(secret.Data[wh.SecretRef.Key]))
	if token == "" {
		setWebhookCondition(project, metav1.ConditionFalse, "Invalid", fmt.Sprintf("secret %s has no key %q", wh.SecretRef.Name, wh.SecretRef.Key))
		return
	}
	opts := gitlab.ProjectHookOptions{URL: wh.URL, Token: token, IssuesEvents: true, NoteEvents: true,
		MergeRequestsEvents: true, PipelineEvents: true, PushEvents: true, EnableSSLVerification: true}
	hash := webhookHash(opts)
	now := r.now()
	if st != nil && st.HookID != 0 && st.ConfigHash == hash && st.CheckedAt != nil && now.Sub(st.CheckedAt.Time) < webhookRecheck {
		setWebhookCondition(project, metav1.ConditionTrue, "Registered", fmt.Sprintf("project hook %d delivers to %s", st.HookID, wh.URL))
		return
	}

	hooks, err := gl.ListProjectHooks(ctx, spec.GitLab.Project)
	if err != nil {
		webhookFailed(ctx, project, "listing the project hooks", err)
		return
	}
	// Ours by the recorded ID, else the one for the URL (status lost).
	var found *gitlab.ProjectHook
	for i := range hooks {
		if st != nil && st.HookID != 0 && hooks[i].ID == st.HookID {
			found = &hooks[i]
			break
		}
		if found == nil && hooks[i].URL == wh.URL {
			found = &hooks[i]
		}
	}
	id := int64(0)
	switch {
	case found == nil:
		h, err := gl.AddProjectHook(ctx, spec.GitLab.Project, opts)
		if err != nil {
			webhookFailed(ctx, project, "creating the project hook", err)
			return
		}
		id = h.ID
	case st == nil || st.HookID != found.ID || st.ConfigHash != hash || !hookMatches(*found, opts):
		// The token cannot be read back: one found by URL is set anew.
		if _, err := gl.EditProjectHook(ctx, spec.GitLab.Project, found.ID, opts); err != nil {
			webhookFailed(ctx, project, "updating the project hook", err)
			return
		}
		id = found.ID
	default:
		id = found.ID
	}
	project.Status.Webhook = &aiv1.WebhookStatus{HookID: id, ConfigHash: hash, CheckedAt: &metav1.Time{Time: now}}
	setWebhookCondition(project, metav1.ConditionTrue, "Registered", fmt.Sprintf("project hook %d delivers to %s", id, wh.URL))
}

// webhookHash identifies the hook's settings; the token enters only as its
// own hash, so the status reveals nothing about it.
func webhookHash(opts gitlab.ProjectHookOptions) string {
	t := sha256.Sum256([]byte(opts.Token))
	opts.Token = hex.EncodeToString(t[:])
	data, _ := json.Marshal(opts)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// hookMatches reports whether GitLab shows the hook with the settings it can
// show (all but the token).
func hookMatches(h gitlab.ProjectHook, o gitlab.ProjectHookOptions) bool {
	return h == gitlab.ProjectHook{ID: h.ID, URL: o.URL, IssuesEvents: o.IssuesEvents, NoteEvents: o.NoteEvents,
		MergeRequestsEvents: o.MergeRequestsEvents, PipelineEvents: o.PipelineEvents, PushEvents: o.PushEvents,
		EnableSSLVerification: o.EnableSSLVerification}
}

func webhookFailed(ctx context.Context, project *aiv1.AgentProject, what string, err error) {
	log.FromContext(ctx).Info("Project hook not registered, trying again on the next poll", "step", what, "error", err.Error())
	if c := gitlab.StatusCode(err); c == http.StatusForbidden || c == http.StatusNotFound {
		setWebhookCondition(project, metav1.ConditionFalse, "Forbidden", "the bot needs the Maintainer role to manage project webhooks")
		return
	}
	setWebhookCondition(project, metav1.ConditionFalse, "Failed", truncateMsg(what+": "+err.Error()))
}

func setWebhookCondition(project *aiv1.AgentProject, status metav1.ConditionStatus, reason, msg string) {
	meta.SetStatusCondition(&project.Status.Conditions, metav1.Condition{
		Type: aiv1.ConditionWebhookRegistered, Status: status, Reason: reason, Message: msg,
	})
}
