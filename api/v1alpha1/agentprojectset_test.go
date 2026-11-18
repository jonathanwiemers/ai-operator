package v1alpha1

import (
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestProjectSetWithDefaults(t *testing.T) {
	for _, tc := range []struct {
		name          string
		in            AgentProjectSetSpec
		url           string
		poll, dormant time.Duration
	}{
		{"empty", AgentProjectSetSpec{}, "https://gitlab.com", 2 * time.Minute, 168 * time.Hour},
		{"set", AgentProjectSetSpec{
			GitLab:       ProjectSetGitLab{URL: "https://gitlab.example.com"},
			PollInterval: metav1.Duration{Duration: time.Minute},
			DormantAfter: metav1.Duration{Duration: time.Hour},
		}, "https://gitlab.example.com", time.Minute, time.Hour},
	} {
		got := tc.in.WithDefaults()
		if got.GitLab.URL != tc.url || got.PollInterval.Duration != tc.poll || got.DormantAfter.Duration != tc.dormant {
			t.Errorf("%s: %+v", tc.name, got)
		}
	}
}

func TestProjectSetValidate(t *testing.T) {
	valid := AgentProjectSetSpec{
		GitLab:  ProjectSetGitLab{Group: "my-group"},
		Include: []string{"my-group/**"},
		Exclude: []string{"my-group/archived/**"},
	}
	for _, tc := range []struct {
		name string
		edit func(*AgentProjectSetSpec)
		want []string
	}{
		{"valid", func(*AgentProjectSetSpec) {}, nil},
		{"empty group", func(s *AgentProjectSetSpec) { s.GitLab.Group = " " }, []string{"gitlab.group is empty"}},
		{"empty include", func(s *AgentProjectSetSpec) { s.Include = nil }, []string{"include is empty"}},
		{"bad include glob", func(s *AgentProjectSetSpec) { s.Include = []string{"my-group/**", "my-group/a**b"} }, []string{"include[1]", "a**b"}},
		{"bad exclude glob", func(s *AgentProjectSetSpec) { s.Exclude = []string{"my-group/?"} }, []string{"exclude[0]", "my-group/?"}},
		{"several", func(s *AgentProjectSetSpec) { s.GitLab.Group = ""; s.Include = nil; s.Exclude = []string{""} }, []string{"gitlab.group is empty", "include is empty", "exclude[0]"}},
	} {
		s := valid
		tc.edit(&s)
		problems := s.Validate()
		joined := strings.Join(problems, "\n")
		if len(tc.want) == 0 && len(problems) > 0 {
			t.Errorf("%s: problems %v", tc.name, problems)
		}
		if len(tc.want) > 0 && len(problems) == 0 {
			t.Errorf("%s: no problems", tc.name)
		}
		for _, w := range tc.want {
			if !strings.Contains(joined, w) {
				t.Errorf("%s: problems %q lack %q", tc.name, joined, w)
			}
		}
	}
}

func TestProjectSetDecodeTemplate(t *testing.T) {
	tmpl := func(raw string) AgentProjectSetSpec {
		return AgentProjectSetSpec{Template: &runtime.RawExtension{Raw: []byte(raw)}}
	}
	t.Run("valid", func(t *testing.T) {
		got, err := tmpl(`{"workflow":{"readyLabel":"go"},"maxConcurrentRuns":1,` +
			`"webhook":{"enabled":true,"url":"https://h/hooks/gitlab","secretRef":{"name":"wh","key":"token"}}}`).DecodeTemplate()
		if err != nil {
			t.Fatal(err)
		}
		if got.Workflow.ReadyLabel != "go" || got.MaxConcurrentRuns != 1 || !got.Webhook.Enabled || got.Webhook.SecretRef == nil || got.Webhook.SecretRef.Name != "wh" {
			t.Errorf("decoded %+v", got)
		}
		// No AgentProject defaults: the API server sets them on the child.
		if got.Workflow.DoneLabel != "" || got.PollInterval.Duration != 0 || got.GitLab != (GitLabSpec{}) {
			t.Errorf("defaults applied: %+v", got)
		}
	})
	for _, tc := range []struct {
		name string
		spec AgentProjectSetSpec
		want string
	}{
		{"unknown field", tmpl(`{"workflw":{"readyLabel":"go"}}`), `unknown field "workflw"`},
		{"nested unknown field", tmpl(`{"workflow":{"readyLable":"go"}}`), `unknown field "readyLable"`},
		{"gitlab", tmpl(`{"gitlab":{"project":"x"}}`), "template.gitlab is set by the set"},
		{"invalid json", tmpl(`{"workflow":`), "template:"},
		{"not an object", tmpl(`[1]`), "template:"},
		{"trailing data", tmpl(`{} {}`), "template:"},
	} {
		if _, err := tc.spec.DecodeTemplate(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %v, want %q", tc.name, err, tc.want)
		}
	}
	for name, s := range map[string]AgentProjectSetSpec{"nil": {}, "empty raw": tmpl(""), "empty object": tmpl("{}")} {
		got, err := s.DecodeTemplate()
		if err != nil || got.MaxConcurrentRuns != 0 || got.Webhook.Enabled {
			t.Errorf("%s: %+v, %v", name, got, err)
		}
	}
}

func TestProjectSetWebhookProblem(t *testing.T) {
	ok := WebhookSpec{Enabled: true, URL: "https://h/hooks/gitlab", SecretRef: &corev1.SecretKeySelector{Key: "token"}}
	for _, tc := range []struct {
		name string
		edit func(*WebhookSpec)
		want string
	}{
		{"complete", func(*WebhookSpec) {}, ""},
		{"disabled", func(w *WebhookSpec) { w.Enabled = false }, "template.webhook.enabled is not true"},
		{"no secret", func(w *WebhookSpec) { w.SecretRef = nil }, "template.webhook.secretRef is not set"},
		{"no url", func(w *WebhookSpec) { w.URL = "" }, "template.webhook.url is not set"},
		{"nothing", func(w *WebhookSpec) { *w = WebhookSpec{} }, "template.webhook.enabled is not true; template.webhook.secretRef is not set; template.webhook.url is not set"},
	} {
		w := ok
		tc.edit(&w)
		if got := (AgentProjectSetSpec{}).WebhookProblem(AgentProjectSpec{Webhook: w}); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
