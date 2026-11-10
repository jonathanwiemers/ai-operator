package v1alpha1

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/pruning"
	"k8s.io/apimachinery/pkg/runtime"
	openapispec "k8s.io/kube-openapi/pkg/validation/spec"
	"k8s.io/kube-openapi/pkg/validation/strfmt"
	"k8s.io/kube-openapi/pkg/validation/validate"
	"sigs.k8s.io/yaml"
)

// crdSchema checks objects against a generated CRD the way the API server
// does: unknown fields, defaults, then the OpenAPI validation.
type crdSchema struct {
	structural *structuralschema.Structural
	openapi    *openapispec.Schema
}

func loadCRD(t *testing.T, name string) crdSchema {
	t.Helper()
	return loadCRDFile(t, "../../config/crd/bases/ai.wiemers.me_"+name+".yaml")
}

// loadCRDFile is loadCRD of the CRD at path, e.g. the chart's copy.
func loadCRDFile(t *testing.T, path string) crdSchema {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(data, &crd); err != nil {
		t.Fatal(err)
	}
	var props apiextensions.JSONSchemaProps
	if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(crd.Spec.Versions[0].Schema.OpenAPIV3Schema, &props, nil); err != nil {
		t.Fatal(err)
	}
	s, err := structuralschema.NewStructural(&props)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(crd.Spec.Versions[0].Schema.OpenAPIV3Schema)
	if err != nil {
		t.Fatal(err)
	}
	var o openapispec.Schema
	if err := json.Unmarshal(raw, &o); err != nil {
		t.Fatal(err)
	}
	return crdSchema{structural: s, openapi: &o}
}

// applyDefaults sets the schema defaults of missing fields, like the API
// server (without the cel-based checks of its defaulting package).
func applyDefaults(x any, s *structuralschema.Structural) {
	switch x := x.(type) {
	case map[string]any:
		for k, p := range s.Properties {
			if _, ok := x[k]; !ok && p.Default.Object != nil {
				x[k] = runtime.DeepCopyJSONValue(p.Default.Object)
			}
			if v, ok := x[k]; ok {
				applyDefaults(v, &p)
			}
		}
	case []any:
		if s.Items != nil {
			for _, v := range x {
				applyDefaults(v, s.Items)
			}
		}
	}
}

// admit returns the object as stored, or the errors that reject it.
func (c crdSchema) admit(obj map[string]any) (map[string]any, []string) {
	unknown := pruning.PruneWithOptions(obj, c.structural, true, structuralschema.UnknownFieldPathOptions{TrackUnknownFieldPaths: true})
	if len(unknown) > 0 {
		return nil, unknown
	}
	applyDefaults(obj, c.structural)
	if err := validate.AgainstSchema(c.openapi, obj, strfmt.Default); err != nil {
		return obj, []string{err.Error()}
	}
	return obj, nil
}

func readSample(t *testing.T, name string, edit func(string) string) map[string]any {
	t.Helper()
	data, err := os.ReadFile("../../config/samples/" + name)
	if err != nil {
		t.Fatal(err)
	}
	obj := map[string]any{}
	if err := yaml.Unmarshal([]byte(edit(string(data))), &obj); err != nil {
		t.Fatal(err)
	}
	return obj
}

func spec(obj map[string]any) map[string]any { return obj["spec"].(map[string]any) }

func TestResearchSampleAdmitted(t *testing.T) {
	crd := loadCRD(t, "agentresearches")
	obj, errs := crd.admit(readSample(t, "agentresearch.yaml", func(s string) string { return s }))
	if len(errs) > 0 {
		t.Fatalf("sample rejected: %v", errs)
	}
	if s := spec(obj); s["view"] != "newsflash" || s["depth"] != "quick" || s["schedule"] != "0 7 * * *" {
		t.Errorf("sample spec %v", s)
	}

	// Without the optional fields the defaults apply.
	obj, errs = crd.admit(map[string]any{"spec": map[string]any{"projectRef": "shop", "prompt": "What changed in Go 1.26?"}})
	if len(errs) > 0 {
		t.Fatalf("minimal research rejected: %v", errs)
	}
	if s := spec(obj); s["view"] != "report" || s["depth"] != "quick" || s["retention"] != "720h" || s["agent"] != nil {
		t.Errorf("defaults %v", s)
	}
}

func TestResearchSchemaRejects(t *testing.T) {
	crd := loadCRD(t, "agentresearches")
	for name, tc := range map[string]struct {
		key   string
		value any
		want  string
	}{
		"view":          {"view", "digest", "spec.view"},
		"depth":         {"depth", "medium", "spec.depth"},
		"empty prompt":  {"prompt", "", "spec.prompt"},
		"long prompt":   {"prompt", strings.Repeat("x", 16385), "spec.prompt"},
		"no project":    {"projectRef", "", "spec.projectRef"},
		"too many":      {"topics", repeat("t", 21), "spec.topics"},
		"long topic":    {"topics", []any{strings.Repeat("t", 101)}, "spec.topics[0]"},
		"engine":        {"agent", map[string]any{"engine": "gpt", "model": "x"}, "spec.agent.engine"},
		"no model":      {"agent", map[string]any{"engine": "claude"}, "spec.agent.model"},
		"effort":        {"agent", map[string]any{"engine": "claude", "model": "opus", "effort": "lots"}, "spec.agent.effort"},
		"unknown field": {"agents", "auto", "spec.agents"},
	} {
		s := map[string]any{"projectRef": "shop", "prompt": "news", tc.key: tc.value}
		if _, errs := crd.admit(map[string]any{"spec": s}); !strings.Contains(strings.Join(errs, "\n"), tc.want) {
			t.Errorf("%s: errors %v, want one on %s", name, errs, tc.want)
		}
	}
	if _, errs := crd.admit(map[string]any{"metadata": map[string]any{"name": "x"}}); !strings.Contains(strings.Join(errs, "\n"), "spec in body is required") {
		t.Errorf("research without spec: errors %v", errs)
	}
}

// repeat returns a list of n times v.
func repeat(v any, n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// uncommentResearch enables the commented spec.research example of the
// AgentProject sample.
func uncommentResearch(s string) string {
	before, after, ok := strings.Cut(s, "  # research:")
	if !ok {
		return s
	}
	lines := strings.Split("  # research:"+after, "\n")
	for i, l := range lines {
		lines[i] = strings.Replace(l, "# ", "", 1)
	}
	return before + strings.Join(lines, "\n")
}

func TestProjectSampleResearch(t *testing.T) {
	crd := loadCRD(t, "agentprojects")
	if _, errs := crd.admit(readSample(t, "agentproject.yaml", func(s string) string { return s })); len(errs) > 0 {
		t.Fatalf("sample rejected: %v", errs)
	}
	obj, errs := crd.admit(readSample(t, "agentproject.yaml", uncommentResearch))
	if len(errs) > 0 {
		t.Fatalf("sample with research rejected: %v", errs)
	}
	research, _ := spec(obj)["research"].(map[string]any)
	writer, _ := research["writerSecret"].(map[string]any)
	if writer["name"] != "agents.research-writer.ai-operator-postgres.credentials.postgresql.acid.zalan.do" ||
		writer["usernameKey"] != "username" || writer["passwordKey"] != "password" {
		t.Errorf("writerSecret %v", writer)
	}
	if quick, _ := research["candidates"].(map[string]any)["quick"].([]any); len(quick) != 2 {
		t.Errorf("candidates %v", research["candidates"])
	}

	for name, tc := range map[string]struct {
		research map[string]any
		want     string
	}{
		"writer without name": {map[string]any{"writerSecret": map[string]any{"usernameKey": "user"}}, "spec.research.writerSecret.name"},
		"candidate engine":    {map[string]any{"candidates": map[string]any{"deep": []any{map[string]any{"engine": "gpt", "model": "x"}}}}, "spec.research.candidates.deep[0].engine"},
		"too many candidates": {map[string]any{"candidates": map[string]any{"quick": repeat(map[string]any{"engine": "claude", "model": "sonnet"}, 6)}}, "spec.research.candidates.quick"},
	} {
		obj := readSample(t, "agentproject.yaml", func(s string) string { return s })
		spec(obj)["research"] = tc.research
		if _, errs := crd.admit(obj); !strings.Contains(strings.Join(errs, "\n"), tc.want) {
			t.Errorf("%s: errors %v, want one on %s", name, errs, tc.want)
		}
	}
}

// The operator copies every chat kind onto the task (spec.source.chatMessages);
// a kind the schema rejects keeps the message queued for good.
func TestChatMessageKinds(t *testing.T) {
	crd := loadCRD(t, "agenttasks")
	chatErrs := func(kind string) string {
		msg := map[string]any{"id": int64(1), "kind": kind, "text": "hi", "author": "u", "at": "2026-11-03T12:00:00Z"}
		obj := map[string]any{"spec": map[string]any{"source": map[string]any{"chatMessages": []any{msg}}}}
		_, errs := crd.admit(obj)
		var out []string
		for _, e := range errs {
			for _, line := range strings.Split(e, "\n") {
				if strings.Contains(line, "chatMessages") {
					out = append(out, line)
				}
			}
		}
		return strings.Join(out, "\n")
	}
	for _, kind := range []string{"message", "interrupt", "command"} {
		if errs := chatErrs(kind); errs != "" {
			t.Errorf("kind %s rejected: %s", kind, errs)
		}
	}
	if errs := chatErrs("shout"); !strings.Contains(errs, "chatMessages[0].kind") {
		t.Errorf("kind shout: errors %q, want one on chatMessages[0].kind", errs)
	}
}

// The operator writes prompts into the task's status; the schema takes every
// field of a valid one and rejects kinds, states and sources it does not know.
func TestPromptSchema(t *testing.T) {
	crd := loadCRD(t, "agenttasks")
	prompt := func(edit func(p map[string]any)) map[string]any {
		p := map[string]any{
			"id": "Q3", "question": "Main is red. What now?", "context": "`rust:test` failed",
			"source":        map[string]any{"kind": "mainHealth", "issueIID": int64(340), "mrIID": int64(12), "task": "t", "key": "main:abc"},
			"options":       []any{map[string]any{"id": "A", "label": "Fix it", "detail": "d", "recommended": true, "actions": []any{map[string]any{"tool": "fixFlaky", "issueIID": int64(340)}}}, map[string]any{"id": "B", "label": "Do nothing", "fromAnswer": false}},
			"allowFreeText": true, "state": "answered", "mirroredState": "open",
			"answer":   map[string]any{"optionID": "A", "text": "go", "by": "jo", "at": "2026-11-04T12:00:00Z", "via": "desk", "outcome": "done"},
			"postedAt": "2026-11-04T11:00:00Z", "closedAt": "2026-11-04T12:00:00Z", "postedNoteID": int64(4160687951), "postedNoteURL": "https://gitlab.com/x",
		}
		edit(p)
		return map[string]any{"status": map[string]any{"lastPrompt": int64(3), "prompts": []any{p}}}
	}
	if _, errs := crd.admit(prompt(func(map[string]any) {})); len(errs) > 0 {
		t.Errorf("valid prompt rejected: %v", errs)
	}
	for name, tc := range map[string]struct {
		edit func(p map[string]any)
		want string
	}{
		"kind":          {func(p map[string]any) { p["source"].(map[string]any)["kind"] = "chat" }, "status.prompts[0].source.kind"},
		"state":         {func(p map[string]any) { p["state"] = "done" }, "status.prompts[0].state"},
		"mirroredState": {func(p map[string]any) { p["mirroredState"] = "done" }, "status.prompts[0].mirroredState"},
		"via":           {func(p map[string]any) { p["answer"].(map[string]any)["via"] = "slack" }, "status.prompts[0].answer.via"},
	} {
		if _, errs := crd.admit(prompt(tc.edit)); !strings.Contains(strings.Join(errs, "\n"), tc.want) {
			t.Errorf("%s: errors %v, want one on %s", name, errs, tc.want)
		}
	}
	many := map[string]any{"status": map[string]any{"prompts": repeat(map[string]any{"id": "Q1"}, MaxPrompts+1)}}
	if _, errs := crd.admit(many); !strings.Contains(strings.Join(errs, "\n"), "status.prompts") {
		t.Errorf("%d prompts: errors %v", MaxPrompts+1, errs)
	}
}

// The operator hands free text to the pilot in spec.source.promptRefines and
// the runner answers in status.pilot.refined: the schema keeps every field
// (the runner matches an answer to its refine by prompt, message and
// request) and bounds both lists.
func TestPromptRefineSchema(t *testing.T) {
	crd := loadCRD(t, "agenttasks")
	option := map[string]any{"id": "C", "label": "From your answer: retry #1", "detail": "d", "fromAnswer": true,
		"actions": []any{map[string]any{"tool": "retry", "issueIID": int64(1)}}}
	refine := map[string]any{"prompt": "Q3", "question": "q", "context": "c", "options": []any{option}, "text": "retry it",
		"by": "alice", "request": int64(4), "messageID": int64(77)}
	refined := map[string]any{"prompt": "Q3", "messageID": int64(77), "request": int64(4), "option": option, "followUp": "f", "error": "e"}
	obj := map[string]any{
		"spec":   map[string]any{"projectRef": "shop", "type": "Coordinate", "source": map[string]any{"promptRefines": []any{refine}}},
		"status": map[string]any{"pilot": map[string]any{"refined": []any{refined}}},
	}
	if _, errs := crd.admit(obj); len(errs) > 0 {
		t.Errorf("valid refine and refinement rejected: %v", errs)
	}
	many := map[string]any{
		"spec":   map[string]any{"source": map[string]any{"promptRefines": repeat(map[string]any{"prompt": "Q1", "text": "t", "request": int64(1)}, MaxRefines+1)}},
		"status": map[string]any{"pilot": map[string]any{"refined": repeat(map[string]any{"prompt": "Q1"}, MaxRefines+1)}},
	}
	_, errs := crd.admit(many)
	for _, want := range []string{"spec.source.promptRefines", "status.pilot.refined"} {
		if !strings.Contains(strings.Join(errs, "\n"), want) {
			t.Errorf("%d entries: errors %v, want one on %s", MaxRefines+1, errs, want)
		}
	}
}

// A pilot assessment proposes options only, with no actions of its own. The
// runner records it with nil Actions; the schema must store it, or the whole
// status patch fails and the owners never get the prompt.
func TestPilotProposalWithOptionsOnlyAdmitted(t *testing.T) {
	crd := loadCRD(t, "agenttasks")
	task := AgentTask{Status: AgentTaskStatus{Pilot: &PilotStatus{Proposed: []PilotProposal{{
		Kind: PromptAssessment, Summary: "Autopilot paused", Source: "pause:stuck", State: "s1",
		Question: "Autopilot paused. What now?",
		Options: []PromptOption{
			{ID: "A", Label: "Resume", Recommended: true, Actions: []PilotAction{{Tool: "resume"}}},
			{ID: "B", Label: "Do nothing"},
		},
	}}}}}
	data, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatal(err)
	}
	if _, errs := crd.admit(map[string]any{"status": obj["status"]}); len(errs) > 0 {
		t.Fatalf("options-only proposal rejected: %v", errs)
	}
}

// The operator records the main commit a BaseFailing MR was rebased onto in
// the queue item (healedOn); the schema must keep it.
func TestAutopilotItemHealedOnAdmitted(t *testing.T) {
	crd := loadCRD(t, "agenttasks")
	item := map[string]any{"iid": int64(1), "state": "Active", "rebasedFrom": "sha-api", "rebases": int64(1), "healedOn": "main-y"}
	obj, errs := crd.admit(map[string]any{"status": map[string]any{"autopilot": map[string]any{"phase": "Running", "items": []any{item}}}})
	if len(errs) > 0 {
		t.Fatalf("item with healedOn rejected: %v", errs)
	}
	items := obj["status"].(map[string]any)["autopilot"].(map[string]any)["items"].([]any)
	if got := items[0].(map[string]any)["healedOn"]; got != "main-y" {
		t.Fatalf("healedOn not stored: %v", items[0])
	}
}

// An owner's startAnyway is recorded on the waiting queue item; the schema
// must keep it.
func TestAutopilotItemStartAnywayAdmitted(t *testing.T) {
	crd := loadCRD(t, "agenttasks")
	item := map[string]any{"iid": int64(88), "state": "Pending", "startAnyway": true}
	obj, errs := crd.admit(map[string]any{"status": map[string]any{"autopilot": map[string]any{"phase": "Paused", "items": []any{item}}}})
	if len(errs) > 0 {
		t.Fatalf("item with startAnyway rejected: %v", errs)
	}
	items := obj["status"].(map[string]any)["autopilot"].(map[string]any)["items"].([]any)
	if got := items[0].(map[string]any)["startAnyway"]; got != true {
		t.Fatalf("startAnyway not stored: %v", items[0])
	}
}

// Rejected status writes are kept in status.writeFailures (writefail); the
// schema must keep them and bound them.
func TestWriteFailuresAdmitted(t *testing.T) {
	crd := loadCRD(t, "agenttasks")
	entry := func(i int) any {
		return map[string]any{"source": "runner/recordProposal", "error": strings.Repeat("e", MaxWriteFailureError), "key": strings.Repeat("k", 16),
			"at": "2026-11-05T10:12:00Z", "count": int64(i + 1)}
	}
	list := func(n int) []any {
		var out []any
		for i := range n {
			out = append(out, entry(i))
		}
		return out
	}
	obj, errs := crd.admit(map[string]any{"status": map[string]any{"writeFailures": list(MaxWriteFailures)}})
	if len(errs) > 0 {
		t.Fatalf("write failures rejected: %v", errs)
	}
	if got := obj["status"].(map[string]any)["writeFailures"].([]any); len(got) != MaxWriteFailures || got[0].(map[string]any)["key"] == nil {
		t.Fatalf("write failures not stored: %v", got)
	}
	if _, errs := crd.admit(map[string]any{"status": map[string]any{"writeFailures": list(MaxWriteFailures + 1)}}); len(errs) == 0 {
		t.Fatal("more than MaxWriteFailures admitted")
	}
	long := entry(0).(map[string]any)
	long["error"] = strings.Repeat("e", MaxWriteFailureError+1)
	if _, errs := crd.admit(map[string]any{"status": map[string]any{"writeFailures": []any{long}}}); len(errs) == 0 {
		t.Fatal("an error over MaxWriteFailureError admitted")
	}
}

// pilot.stuckAfter is optional and defaults to 30m; the Go fallback covers
// objects stored before the field existed.
func TestPilotStuckAfterAdmitted(t *testing.T) {
	crd := loadCRD(t, "agentprojects")
	pilot := func(obj map[string]any) map[string]any { return spec(obj)["pilot"].(map[string]any) }
	base := readSample(t, "agentproject.yaml", func(s string) string { return s })
	spec(base)["pilot"] = map[string]any{}
	obj, errs := crd.admit(base)
	if len(errs) > 0 || pilot(obj)["stuckAfter"] != "30m" {
		t.Fatalf("the default applies: %v %v", errs, pilot(obj))
	}
	base = readSample(t, "agentproject.yaml", func(s string) string { return s })
	spec(base)["pilot"] = map[string]any{"stuckAfter": "1h30m"}
	if obj, errs = crd.admit(base); len(errs) > 0 || pilot(obj)["stuckAfter"] != "1h30m" {
		t.Fatalf("a custom stuckAfter is kept: %v %v", errs, pilot(obj))
	}
	if got := (PilotSpec{}).StuckAfterOf(); got != 30*time.Minute {
		t.Errorf("Go fallback %v", got)
	}
}

// The stuck detector's stamps (stateHash, stateChangedAt, pausedAt) are
// stored with the autopilot status.
func TestAutopilotStateStampsAdmitted(t *testing.T) {
	crd := loadCRD(t, "agenttasks")
	ap := map[string]any{"phase": "Paused", "stateHash": "c4c47baf0bed24e8", "stateChangedAt": "2026-11-05T10:12:00Z", "pausedAt": "2026-11-05T10:00:00Z"}
	obj, errs := crd.admit(map[string]any{"status": map[string]any{"autopilot": ap}})
	if len(errs) > 0 {
		t.Fatalf("stamps rejected: %v", errs)
	}
	got := obj["status"].(map[string]any)["autopilot"].(map[string]any)
	for _, k := range []string{"stateHash", "stateChangedAt", "pausedAt"} {
		if got[k] != ap[k] {
			t.Errorf("%s not stored: %v", k, got)
		}
	}
}

// Who ended the last pause (resumedBy) is stored with the autopilot status,
// one of the resolvers only.
func TestAutopilotResumedByAdmitted(t *testing.T) {
	crd := loadCRD(t, "agenttasks")
	for _, by := range []PauseResolver{ResolvedByOperator, ResolvedByPilot, ResolvedByOwner, ResolvedBySelfHealed} {
		obj, errs := crd.admit(map[string]any{"status": map[string]any{"autopilot": map[string]any{"phase": "Running", "resumedBy": string(by)}}})
		if len(errs) > 0 {
			t.Fatalf("resumedBy %s rejected: %v", by, errs)
		}
		if got := obj["status"].(map[string]any)["autopilot"].(map[string]any)["resumedBy"]; got != string(by) {
			t.Errorf("resumedBy %s not stored: %v", by, got)
		}
	}
	if _, errs := crd.admit(map[string]any{"status": map[string]any{"autopilot": map[string]any{"phase": "Running", "resumedBy": "someone"}}}); len(errs) == 0 {
		t.Error("an unknown resolver admitted")
	}
}

// How the last pause ended (resumedVia) and who asked for a run during it
// (pauseRunBy, pauseRunHead) are stored; resumedVia is one of the ways only.
func TestAutopilotResumedViaAdmitted(t *testing.T) {
	crd := loadCRD(t, "agenttasks")
	for _, via := range []PauseResumeVia{ResumedViaReReview, ResumedViaTakeover, ResumedViaPush, ResumedViaMergedByHand,
		ResumedViaClosedByHand, ResumedViaCommand, ResumedViaCauseGone, ResumedViaSelfHealed} {
		ap := map[string]any{"phase": "Paused", "resumedVia": string(via), "pauseRunBy": "@alice", "pauseRunHead": "abc123"}
		obj, errs := crd.admit(map[string]any{"status": map[string]any{"autopilot": ap}})
		if len(errs) > 0 {
			t.Fatalf("resumedVia %s rejected: %v", via, errs)
		}
		got := obj["status"].(map[string]any)["autopilot"].(map[string]any)
		for _, k := range []string{"resumedVia", "pauseRunBy", "pauseRunHead"} {
			if got[k] != ap[k] {
				t.Errorf("%s not stored: %v", k, got)
			}
		}
	}
	if _, errs := crd.admit(map[string]any{"status": map[string]any{"autopilot": map[string]any{"phase": "Running", "resumedVia": "magic"}}}); len(errs) == 0 {
		t.Error("an unknown way to resume admitted")
	}
}

func TestProjectCrossRepoSchema(t *testing.T) {
	crd := loadCRD(t, "agentprojects")
	withCrossRepo := func(crossRepo any) map[string]any {
		obj := readSample(t, "agentproject.yaml", func(s string) string { return s })
		spec(obj)["crossRepo"] = crossRepo
		return obj
	}
	obj, errs := crd.admit(withCrossRepo(map[string]any{"targets": []any{
		map[string]any{"project": "jo.wiemers/infra/cluster", "paths": []any{"clusters/netcup01/applications/ai-operator/"}},
		map[string]any{"project": "jo.wiemers/bloop", "paths": []any{"apps/"}, "autoMerge": true,
			"tokenSecretRef": map[string]any{"name": "bloop-token", "key": "token"}},
	}}))
	if len(errs) > 0 {
		t.Fatalf("crossRepo rejected: %v", errs)
	}
	targets := spec(obj)["crossRepo"].(map[string]any)["targets"].([]any)
	if am, ok := targets[0].(map[string]any)["autoMerge"]; !ok || am != false {
		t.Errorf("autoMerge default %v (set %v), want false", am, ok)
	}
	if am := targets[1].(map[string]any)["autoMerge"]; am != true {
		t.Errorf("autoMerge %v, want true", am)
	}

	for name, tc := range map[string]struct {
		crossRepo any
		want      string
	}{
		"no targets":    {map[string]any{}, "spec.crossRepo.targets"},
		"empty targets": {map[string]any{"targets": []any{}}, "spec.crossRepo.targets"},
		"no project":    {map[string]any{"targets": []any{map[string]any{"paths": []any{"a/"}}}}, "spec.crossRepo.targets[0].project"},
		"empty project": {map[string]any{"targets": []any{map[string]any{"project": "", "paths": []any{"a/"}}}}, "spec.crossRepo.targets[0].project"},
		"no paths":      {map[string]any{"targets": []any{map[string]any{"project": "a/b"}}}, "spec.crossRepo.targets[0].paths"},
		"empty paths":   {map[string]any{"targets": []any{map[string]any{"project": "a/b", "paths": []any{}}}}, "spec.crossRepo.targets[0].paths"},
		"no token key":  {map[string]any{"targets": []any{map[string]any{"project": "a/b", "paths": []any{"a/"}, "tokenSecretRef": map[string]any{"name": "x"}}}}, "spec.crossRepo.targets[0].tokenSecretRef.key"},
		"unknown field": {map[string]any{"targets": []any{map[string]any{"project": "a/b", "paths": []any{"a/"}, "merge": true}}}, "spec.crossRepo.targets[0].merge"},
	} {
		if _, errs := crd.admit(withCrossRepo(tc.crossRepo)); !strings.Contains(strings.Join(errs, "\n"), tc.want) {
			t.Errorf("%s: errors %v, want one on %s", name, errs, tc.want)
		}
	}
}

func TestProjectMaxStepLinesSchema(t *testing.T) {
	crd := loadCRD(t, "agentprojects")
	withLines := func(implement, profile any) map[string]any {
		obj := readSample(t, "agentproject.yaml", func(s string) string { return s })
		agents := spec(obj)["agents"].(map[string]any)
		if implement != nil {
			agents["implement"].(map[string]any)["maxStepLines"] = implement
		}
		if profile != nil {
			agents["profiles"].([]any)[0].(map[string]any)["maxStepLines"] = profile
		}
		return obj
	}
	if _, errs := crd.admit(withLines(nil, nil)); len(errs) > 0 {
		t.Fatalf("unset rejected: %v", errs)
	}
	obj, errs := crd.admit(withLines(int64(400), int64(150)))
	if len(errs) > 0 {
		t.Fatalf("maxStepLines rejected: %v", errs)
	}
	if got := spec(obj)["agents"].(map[string]any)["implement"].(map[string]any)["maxStepLines"]; got != int64(400) {
		t.Fatalf("implement.maxStepLines %v", got)
	}
	for name, tc := range map[string]struct {
		implement, profile any
		want               string
	}{
		"implement zero":     {int64(0), nil, "spec.agents.implement.maxStepLines"},
		"profile negative":   {nil, int64(-1), "spec.agents.profiles[0].maxStepLines"},
		"implement a string": {"400", nil, "spec.agents.implement.maxStepLines"},
	} {
		if _, errs := crd.admit(withLines(tc.implement, tc.profile)); !strings.Contains(strings.Join(errs, "\n"), tc.want) {
			t.Errorf("%s: errors %v, want one on %s", name, errs, tc.want)
		}
	}
}

func TestProjectConflictRoleSchema(t *testing.T) {
	crd := loadCRD(t, "agentprojects")
	withConflict := func(conflict any) map[string]any {
		obj := readSample(t, "agentproject.yaml", func(s string) string { return s })
		spec(obj)["agents"].(map[string]any)["implement"].(map[string]any)["conflict"] = conflict
		return obj
	}
	obj, errs := crd.admit(withConflict(map[string]any{"model": "claude-sonnet-5-5"}))
	if len(errs) > 0 {
		t.Fatalf("conflict role rejected: %v", errs)
	}
	c := spec(obj)["agents"].(map[string]any)["implement"].(map[string]any)["conflict"].(map[string]any)
	if c["freshSession"] != true || c["engine"] != "claude" || c["model"] != "claude-sonnet-5-5" {
		t.Fatalf("expected freshSession and engine defaulted: %v", c)
	}
	if _, errs := crd.admit(withConflict(map[string]any{"model": "claude-sonnet-5-5", "freshSession": false,
		"fallback": map[string]any{"engine": "cursor", "model": "auto"}})); len(errs) > 0 {
		t.Fatalf("freshSession false and a fallback rejected: %v", errs)
	}
	for name, tc := range map[string]struct {
		conflict any
		want     string
	}{
		"bad effort":    {map[string]any{"effort": "huge"}, "spec.agents.implement.conflict.effort"},
		"bad engine":    {map[string]any{"engine": "gpt"}, "spec.agents.implement.conflict.engine"},
		"unknown field": {map[string]any{"session": "fresh"}, "spec.agents.implement.conflict.session"},
	} {
		if _, errs := crd.admit(withConflict(tc.conflict)); !strings.Contains(strings.Join(errs, "\n"), tc.want) {
			t.Errorf("%s: errors %v, want one on %s", name, errs, tc.want)
		}
	}
}

// #274: the operator writes spec.source.retroSpend with Update, and the API
// server prunes what the schema lacks. The unmetered count and the per-row
// flag survive in both CRD copies (the chart's is what the cluster runs).
func TestRetroSpendKeepsUnmetered(t *testing.T) {
	s := SpendSummary{CostUSD: "0.5000", CostKnown: true, UnmeteredCalls: 1, Tokens: SpendTokens{Input: 10},
		TopKinds:  []SpendDriver{{Key: "review", Label: "review", Unmetered: true}},
		TopModels: []SpendDriver{{Key: "claude-opus-5-5", Label: "claude-opus-5-5", CostUSD: "0.5000", Tokens: 10}, {Key: "auto", Label: CursorAutoLabel, Unmetered: true}}}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../../config/crd/bases/ai.wiemers.me_agenttasks.yaml", "../../charts/ai-operator/files/crds/ai.wiemers.me_agenttasks.yaml"} {
		crd := loadCRDFile(t, path)
		var spend map[string]any
		if err := json.Unmarshal(b, &spend); err != nil {
			t.Fatal(err)
		}
		obj, errs := crd.admit(map[string]any{"spec": map[string]any{"source": map[string]any{"retroSpend": spend}}})
		if obj == nil {
			t.Fatalf("%s prunes %v", path, errs)
		}
		for _, e := range errs {
			if strings.Contains(e, "retroSpend") {
				t.Errorf("%s rejects the spend: %s", path, e)
			}
		}
		got, err := json.Marshal(spec(obj)["source"].(map[string]any)["retroSpend"])
		if err != nil {
			t.Fatal(err)
		}
		var back SpendSummary
		if err := json.Unmarshal(got, &back); err != nil {
			t.Fatal(err)
		}
		if back.UnmeteredCalls != 1 || len(back.TopModels) != 2 || !back.TopModels[1].Unmetered || len(back.TopKinds) != 1 || !back.TopKinds[0].Unmetered {
			t.Errorf("%s: stored %s", path, got)
		}
	}
}

func TestProjectReleaseSchema(t *testing.T) {
	crd := loadCRD(t, "agentprojects")
	withRelease := func(release any) map[string]any {
		obj := readSample(t, "agentproject.yaml", func(s string) string { return s })
		spec(obj)["workflow"] = map[string]any{"release": release}
		return obj
	}
	obj, errs := crd.admit(withRelease(map[string]any{}))
	if len(errs) > 0 {
		t.Fatalf("empty release rejected: %v", errs)
	}
	r := spec(obj)["workflow"].(map[string]any)["release"].(map[string]any)
	if r["mode"] != "Auto" || r["minInterval"] != "15m" || r["maxUnreleasedAge"] != "24h" {
		t.Fatalf("defaults: %v", r)
	}
	obj, errs = crd.admit(withRelease(map[string]any{"mode": "Off", "minInterval": "1h"}))
	if len(errs) > 0 {
		t.Fatalf("Off rejected: %v", errs)
	}
	r = spec(obj)["workflow"].(map[string]any)["release"].(map[string]any)
	if r["mode"] != "Off" || r["minInterval"] != "1h" {
		t.Fatalf("explicit values: %v", r)
	}
	if _, errs := crd.admit(withRelease(map[string]any{"mode": "Sometimes"})); !strings.Contains(strings.Join(errs, "\n"), "spec.workflow.release.mode") {
		t.Fatalf("bad mode admitted: %v", errs)
	}
}
