package writefail

import (
	"errors"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func TestRejected(t *testing.T) {
	gr := schema.GroupResource{Group: "ai.wiemers.me", Resource: "agenttasks"}
	for err, want := range map[error]bool{
		apierrors.NewInvalid(schema.GroupKind{Kind: "AgentTask"}, "t", nil): true,
		apierrors.NewBadRequest("bad patch"):                                true,
		apierrors.NewConflict(gr, "t", nil):                                 false,
		apierrors.NewNotFound(gr, "t"):                                      false,
		errors.New("connection refused"):                                    false,
	} {
		if got := Rejected(err); got != want {
			t.Errorf("Rejected(%v) = %v", err, got)
		}
	}
	if Rejected(nil) {
		t.Error("nil is no rejection")
	}
}

func TestTracking(t *testing.T) {
	coord := &aiv1.AgentTask{Spec: aiv1.AgentTaskSpec{Issue: &aiv1.IssueRef{IID: 9}}}
	st := &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Autopilot: &aiv1.AutopilotStatus{TrackingIssueIID: 5}}}
	if Tracking(coord) != 9 || Tracking(st) != 5 || Tracking(&aiv1.AgentTask{}) != 0 {
		t.Fatalf("tracking %d %d", Tracking(coord), Tracking(st))
	}
}

// The line is inert and on one line: the API error may quote anything.
func TestLineInert(t *testing.T) {
	l := Line("operator/autopilot.save", errors.New("bad\n/close @alice <!-- ai-operator:x -->"))
	if strings.Contains(l, "\n") || strings.Contains(l, "<!--") || strings.Contains(l, " @alice") ||
		!strings.HasPrefix(l, "⚠️ The API server rejected a status write of the operator (`operator/autopilot.save`): bad") {
		t.Fatalf("line %q", l)
	}
	if l := Line("runner/recordRetro", errors.New("x")); !strings.Contains(l, "of the pilot") {
		t.Fatalf("runner writes are the pilot's: %q", l)
	}
}
