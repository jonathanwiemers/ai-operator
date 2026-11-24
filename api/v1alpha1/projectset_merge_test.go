package v1alpha1

import (
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
)

// The examples of RFC 7386, Appendix A.
func TestMergePatchRFC7386(t *testing.T) {
	for _, tc := range []struct{ target, patch, want string }{
		{`{"a":"b"}`, `{"a":"c"}`, `{"a":"c"}`},
		{`{"a":"b"}`, `{"b":"c"}`, `{"a":"b","b":"c"}`},
		{`{"a":"b"}`, `{"a":null}`, `{}`},
		{`{"a":"b","b":"c"}`, `{"a":null}`, `{"b":"c"}`},
		{`{"a":["b"]}`, `{"a":"c"}`, `{"a":"c"}`},
		{`{"a":"c"}`, `{"a":["b"]}`, `{"a":["b"]}`},
		{`{"a":{"b":"c"}}`, `{"a":{"b":"d","c":null}}`, `{"a":{"b":"d"}}`},
		{`{"a":[{"b":"c"}]}`, `{"a":[1]}`, `{"a":[1]}`},
		{`["a","b"]`, `["c","d"]`, `["c","d"]`},
		{`{"a":"b"}`, `["c"]`, `["c"]`},
		{`{"a":"foo"}`, `null`, `null`},
		{`{"a":"foo"}`, `"bar"`, `"bar"`},
		{`{"e":null}`, `{"a":1}`, `{"e":null,"a":1}`},
		{`[1,2]`, `{"a":"b","c":null}`, `{"a":"b"}`},
		{`{}`, `{"a":{"bb":{"ccc":null}}}`, `{"a":{"bb":{}}}`},
		// Not in the RFC: an empty target is null, big numbers keep their text.
		{``, `{"a":{"b":null,"c":1}}`, `{"a":{"c":1}}`},
		{`{"n":12345678901234567890}`, `{"m":1}`, `{"m":1,"n":12345678901234567890}`},
	} {
		got, err := MergePatch([]byte(tc.target), []byte(tc.patch))
		if err != nil {
			t.Errorf("%s + %s: %v", tc.target, tc.patch, err)
			continue
		}
		if !jsonEqual(t, got, []byte(tc.want)) {
			t.Errorf("%s + %s = %s, want %s", tc.target, tc.patch, got, tc.want)
		}
	}
	for _, tc := range []struct{ target, patch, want string }{
		{`{`, `{}`, "target: "},
		{`{}`, `{} {}`, "patch: trailing data"},
		{`{}`, ``, "patch: "},
	} {
		if _, err := MergePatch([]byte(tc.target), []byte(tc.patch)); err == nil || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("%q + %q: error %v, want %q", tc.target, tc.patch, err, tc.want)
		}
	}
}

// jsonEqual compares two JSON documents by value, numbers by their text.
func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	va, err := decodeJSON(a)
	if err != nil {
		t.Fatalf("%s: %v", a, err)
	}
	vb, err := decodeJSON(b)
	if err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return reflect.DeepEqual(va, vb)
}

func TestProjectSetEntrySpec(t *testing.T) {
	raw := func(s string) *runtime.RawExtension { return &runtime.RawExtension{Raw: []byte(s)} }
	spec := AgentProjectSetSpec{
		Template: raw(`{"maxConcurrentRuns":2,"agents":{"image":"agent:1","imagePullPolicy":"Always"},` +
			`"webhook":{"enabled":true,"url":"https://h/hooks/gitlab"},"crossRepo":{"targets":[{"project":"g/a"},{"project":"g/b"}]}}`),
		Projects: []ProjectSetEntry{
			{Path: "g/merged", Spec: raw(`{"maxConcurrentRuns":3,"agents":{"imagePullPolicy":null},"crossRepo":{"targets":[{"project":"g/c"}]}}`)},
			{Path: "g/plain"},
			{Path: "g/unknown", Spec: raw(`{"agents":{"imag":"x"}}`)},
			{Path: "g/gitlab", Spec: raw(`{"gitlab":{"project":"x"}}`)},
			{Path: "g/array", Spec: raw(`["x"]`)},
		},
	}
	got, err := spec.EntrySpec(0)
	if err != nil {
		t.Fatal(err)
	}
	// Objects merge, null deletes, arrays and scalars replace.
	if got.MaxConcurrentRuns != 3 || got.Agents.Image != "agent:1" || got.Agents.ImagePullPolicy != "" || !got.Webhook.Enabled ||
		got.CrossRepo == nil || len(got.CrossRepo.Targets) != 1 || got.CrossRepo.Targets[0].Project != "g/c" {
		t.Errorf("merged = %+v", got)
	}
	tmpl, err := spec.DecodeTemplate()
	if err != nil {
		t.Fatal(err)
	}
	// No entry spec: the template.
	if plain, err := spec.EntrySpec(1); err != nil || !reflect.DeepEqual(plain, tmpl) {
		t.Errorf("plain = %+v, %v, want %+v", plain, err, tmpl)
	}
	for i, want := range map[int]string{
		2: `projects[2]: json: unknown field "imag"`,
		3: "projects[3].gitlab is set by the set",
		4: "projects[4]: json: cannot unmarshal array",
	} {
		if _, err := spec.EntrySpec(i); err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("projects[%d]: error %v, want %q", i, err, want)
		}
	}
	// No template: the entry alone.
	alone := AgentProjectSetSpec{Projects: []ProjectSetEntry{{Path: "g/p", Spec: raw(`{"maxConcurrentRuns":1,"budget":null}`)}}}
	if got, err := alone.EntrySpec(0); err != nil || got.MaxConcurrentRuns != 1 {
		t.Errorf("alone = %+v, %v", got, err)
	}
}
