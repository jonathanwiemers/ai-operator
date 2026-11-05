package v1alpha1

import (
	"os"
	"slices"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

// Every PauseKind has a way out, and the table is well formed.
func TestPauseExits(t *testing.T) {
	for _, kind := range PauseKinds {
		t.Run(string(kind), func(t *testing.T) {
			exits := PauseExits(kind)
			if len(exits) == 0 {
				t.Fatal("no exit")
			}
			ids := map[string]bool{}
			remedies := false
			for i, e := range exits {
				if e.ID == "" || ids[e.ID] {
					t.Errorf("exit %d: ID %q is empty or not unique", i, e.ID)
				}
				ids[e.ID] = true
				if e.Does == "" {
					t.Errorf("%s: Does is empty", e.ID)
				}
				if e.Auto {
					if remedies {
						t.Errorf("%s: automatic exits come first", e.ID)
					}
					if e.When == "" || e.Command != "" || e.Label != "" || e.GitLab || e.Prompt || e.Target != "" {
						t.Errorf("%s: an automatic exit has a condition and no remedy: %+v", e.ID, e)
					}
					continue
				}
				remedies = true
				if e.Command == "" && e.Label == "" && !e.GitLab && !e.Prompt {
					t.Errorf("%s: a remedy needs a command, a label, a GitLab action or the pilot's prompt", e.ID)
				}
				if e.Prompt && (e.When == "" || e.Command != "" || e.Label != "" || e.GitLab) {
					t.Errorf("%s: a prompt remedy names its tools in When and nothing else: %+v", e.ID, e)
				}
				if e.GitLab && e.When == "" {
					t.Errorf("%s: a GitLab action says which in When", e.ID)
				}
				if (e.Command != "" && !e.GitLab || e.Label != "") && e.Target != ExitTargetSpec && e.Target != ExitTargetImplement {
					t.Errorf("%s: target %q", e.ID, e.Target)
				}
				if e.Label != "" && (WorkflowSpec{DoneLabel: "d", ReadyLabel: "r"}).RoleLabel(e.Label) == "" {
					t.Errorf("%s: unknown label role %q", e.ID, e.Label)
				}
			}
		})
	}
}

// PauseExits returns a copy: a caller cannot change the table.
func TestPauseExitsCopy(t *testing.T) {
	PauseExits(PauseOwner)[0].Does = "changed"
	if PauseExits(PauseOwner)[0].Does == "changed" {
		t.Fatal("the table was changed")
	}
}

// PauseKinds is the pauseKind enum of the generated CRD, so a kind added to
// the marker but not to the list (and so without exits) fails.
func TestPauseKindsMatchCRD(t *testing.T) {
	data, err := os.ReadFile("../../config/crd/bases/ai.wiemers.me_agenttasks.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(data, &crd); err != nil {
		t.Fatal(err)
	}
	ap := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["status"].Properties["autopilot"]
	var enum []PauseKind
	for _, v := range ap.Properties["pauseKind"].Enum {
		var k PauseKind
		if err := yaml.Unmarshal(v.Raw, &k); err != nil {
			t.Fatal(err)
		}
		enum = append(enum, k)
	}
	if len(enum) == 0 {
		t.Fatal("no pauseKind enum in the CRD")
	}
	if !slices.Equal(enum, PauseKinds) {
		t.Fatalf("PauseKinds = %v, the CRD's enum is %v", PauseKinds, enum)
	}
}
