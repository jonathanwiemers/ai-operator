package desklink

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func holder(t aiv1.TaskType) *aiv1.AgentTask {
	return &aiv1.AgentTask{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "impl-7"},
		Spec: aiv1.AgentTaskSpec{ProjectRef: "proj", Type: t,
			Milestone: &aiv1.MilestoneRef{IID: 4}},
	}
}

func TestLinks(t *testing.T) {
	const base = "https://ai.example.com"
	for name, c := range map[string]struct{ got, want string }{
		"milestone":       {Milestone(base, "ns", "proj", 4), base + "/projects/ns/proj/milestones/4"},
		"trailing slash":  {Milestone(base+"/", "ns", "proj", 4), base + "/projects/ns/proj/milestones/4"},
		"task":            {Task(base, "ns", "proj", "impl-7"), base + "/projects/ns/proj/tasks/impl-7"},
		"escaped":         {Task(base, "n s", "p/q", "t?#"), base + "/projects/n%20s/p%2Fq/tasks/t%3F%23"},
		"spec prompt":     {Prompt(base, holder(aiv1.TaskTypeSpec), "Q2"), base + "/projects/ns/proj/milestones/4#prompt-Q2"},
		"implement":       {Prompt(base, holder(aiv1.TaskTypeImplement), "Q1"), base + "/projects/ns/proj/tasks/impl-7#prompt-Q1"},
		"markdown":        {Markdown("https://x/y"), "[Answer in the Desk](https://x/y)"},
		"markdown no url": {Markdown(""), "in the Desk"},
	} {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", name, c.got, c.want)
		}
	}
}

func TestEmptyBase(t *testing.T) {
	for name, got := range map[string]string{
		"milestone": Milestone("", "ns", "proj", 4),
		"task":      Task("", "ns", "proj", "t"),
		"prompt":    Prompt("", holder(aiv1.TaskTypeSpec), "Q1"),
		"no holder": Prompt("https://x", nil, "Q1"),
	} {
		if got != "" {
			t.Errorf("%s: want no link, got %q", name, got)
		}
	}
}
