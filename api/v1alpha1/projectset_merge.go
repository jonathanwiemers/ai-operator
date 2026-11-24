package v1alpha1

import (
	"bytes"
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
)

// MergePatch applies patch to target as a JSON Merge Patch (RFC 7386):
// objects merge recursively, null deletes a key, arrays and scalars replace.
// An empty target is null. Numbers keep their text.
func MergePatch(target, patch []byte) ([]byte, error) {
	var t any
	if len(bytes.TrimSpace(target)) > 0 {
		var err error
		if t, err = decodeJSON(target); err != nil {
			return nil, fmt.Errorf("target: %w", err)
		}
	}
	p, err := decodeJSON(patch)
	if err != nil {
		return nil, fmt.Errorf("patch: %w", err)
	}
	return json.Marshal(mergeValue(t, p))
}

// decodeJSON decodes one JSON value, numbers as json.Number.
func decodeJSON(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing data after the JSON value")
	}
	return v, nil
}

// mergeValue is the MergePatch function of RFC 7386, section 2.
func mergeValue(target, patch any) any {
	p, ok := patch.(map[string]any)
	if !ok {
		return patch
	}
	t, ok := target.(map[string]any)
	if !ok {
		t = map[string]any{}
	}
	for k, v := range p {
		if v == nil {
			delete(t, k)
		} else {
			t[k] = mergeValue(t[k], v)
		}
	}
	return t
}

// EntrySpec is the spec of projects[i]'s child, without the set's GitLab:
// the template with the entry's spec merged on top (MergePatch), decoded as
// strictly as DecodeTemplate. The error starts with "projects[i]".
func (s AgentProjectSetSpec) EntrySpec(i int) (AgentProjectSpec, error) {
	field := fmt.Sprintf("projects[%d]", i)
	tmpl := []byte("{}")
	if s.Template != nil && len(bytes.TrimSpace(s.Template.Raw)) > 0 {
		tmpl = s.Template.Raw
	}
	merged := tmpl
	if e := s.Projects[i].Spec; e != nil && len(bytes.TrimSpace(e.Raw)) > 0 {
		var err error
		if merged, err = MergePatch(tmpl, e.Raw); err != nil {
			return AgentProjectSpec{}, fmt.Errorf("%s: %w", field, err)
		}
	}
	return decodeStrict(field, &runtime.RawExtension{Raw: merged})
}
