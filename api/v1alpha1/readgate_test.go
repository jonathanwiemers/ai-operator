package v1alpha1

import (
	"fmt"
	"strings"
	"testing"
)

func TestReadGateLines(t *testing.T) {
	for _, tc := range []struct {
		in   *ReadGate
		want int
	}{{nil, 350}, {&ReadGate{}, 350}, {&ReadGate{MinLines: 100}, 100}, {&ReadGate{MinLines: 500}, 500}} {
		if got := tc.in.Lines(); got != tc.want {
			t.Errorf("%v.Lines() = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestReadGateSchema(t *testing.T) {
	crd := loadCRD(t, "agentprojects")
	role := func(obj map[string]any, where string) map[string]any {
		agents := spec(obj)["agents"].(map[string]any)
		if where == "profile" {
			return agents["profiles"].([]any)[0].(map[string]any)
		}
		return agents[where].(map[string]any)
	}
	admit := func(where string, gate map[string]any) (map[string]any, []string) {
		obj := readSample(t, "agentproject.yaml", func(s string) string { return s })
		if gate != nil {
			role(obj, where)["readGate"] = gate
		}
		out, errs := crd.admit(obj)
		if out == nil {
			return nil, errs
		}
		return role(out, where), errs
	}
	for _, where := range []string{"implement", "profile", "review"} {
		r, errs := admit(where, map[string]any{})
		if len(errs) > 0 {
			t.Fatalf("readGate {} on %s rejected: %v", where, errs)
		}
		gate, _ := r["readGate"].(map[string]any)
		if got := fmt.Sprint(gate["minLines"]); got != "350" {
			t.Errorf("%s: minLines = %v, want default 350", where, got)
		}
		if r, _ := admit(where, nil); r["readGate"] != nil {
			t.Errorf("%s: readGate present without being set: %v", where, r["readGate"])
		}
	}
	if _, errs := admit("profile", map[string]any{"minLines": int64(100)}); len(errs) > 0 {
		t.Errorf("minLines 100 rejected: %v", errs)
	}
	if _, errs := admit("profile", map[string]any{"minLines": int64(99)}); !strings.Contains(strings.Join(errs, "\n"), "readGate.minLines") {
		t.Errorf("minLines 99: errors %v, want one on readGate.minLines", errs)
	}
}
