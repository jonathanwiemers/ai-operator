// Package writefail records status writes of the pilot (runner) and the
// operator that the API server rejected: on the task as
// status.writeFailures and once per source and error as a line on the
// tracking issue, so a broken write is no longer only logged.
package writefail

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

// Rejected reports whether the API server refused the write itself
// (Invalid or BadRequest). Conflicts, not-found and transport errors are
// retried or transient and are not reported.
func Rejected(err error) bool {
	return err != nil && (apierrors.IsInvalid(err) || apierrors.IsBadRequest(err))
}

// Tracking is the issue a failure on task is posted on: the autopilot's
// tracking issue of a spec task, else the task's issue (the tracking
// issue of a Coordinate task). 0: none.
func Tracking(task *aiv1.AgentTask) int64 {
	if ap := task.Status.Autopilot; ap != nil && ap.TrackingIssueIID != 0 {
		return ap.TrackingIssueIID
	}
	if task.Spec.Issue != nil {
		return task.Spec.Issue.IID
	}
	return 0
}

// Report records the rejected write err of source on task: it re-reads
// the task and adds or bumps the entry with a patch of only
// status.writeFailures, so the rejected data is not sent again. Only a new
// entry posts a line on the tracking issue (gl may be nil, tracking 0:
// none). A failure that could not be recorded posts nothing either, so a
// broken status cannot flood the issue on every poll.
func Report(ctx context.Context, c client.Client, gl gitlab.API, glProject string, task *aiv1.AgentTask, tracking int64, source string, err error) error {
	text := textutil.Truncate(err.Error(), aiv1.MaxWriteFailureError-len("…"))
	sum := sha256.Sum256([]byte(source + "\x00" + err.Error()))
	key := hex.EncodeToString(sum[:8])
	var added bool
	if rerr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var t aiv1.AgentTask
		if gerr := c.Get(ctx, client.ObjectKeyFromObject(task), &t); gerr != nil {
			return gerr
		}
		orig := t.DeepCopy()
		list := slices.Clone(t.Status.WriteFailures)
		now := metav1.Now()
		f := aiv1.WriteFailure{Source: source, Error: text, Key: key, At: &now, Count: 1}
		i := slices.IndexFunc(list, func(x aiv1.WriteFailure) bool { return x.Key == key })
		if added = i < 0; !added {
			f.Count = list[i].Count + 1
			list = slices.Delete(list, i, i+1) // the newest goes last
		}
		list = append(list, f)
		if n := len(list); n > aiv1.MaxWriteFailures {
			list = list[n-aiv1.MaxWriteFailures:]
		}
		t.Status.WriteFailures = list
		return c.Status().Patch(ctx, &t, client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{}))
	}); rerr != nil {
		return fmt.Errorf("recording the rejected status write of %s: %w", source, rerr)
	}
	if !added || gl == nil || tracking == 0 {
		return nil
	}
	if _, nerr := gl.CreateIssueNote(ctx, glProject, tracking, Line(source, err)); nerr != nil {
		return fmt.Errorf("posting the rejected status write of %s on #%d: %w", source, tracking, nerr)
	}
	return nil
}

// Line is the tracking issue's line for a rejected write, inert and on
// one line.
func Line(source string, err error) string {
	who := "operator"
	if strings.HasPrefix(source, "runner/") {
		who = "pilot"
	}
	text := textutil.Truncate(strings.Join(strings.Fields(err.Error()), " "), aiv1.MaxWriteFailureError-len("…"))
	text = notify.Defuse(strings.ReplaceAll(text, "<!--", "&lt;!--"))
	return fmt.Sprintf("⚠️ The API server rejected a status write of the %s (`%s`): %s", who, source, text)
}
