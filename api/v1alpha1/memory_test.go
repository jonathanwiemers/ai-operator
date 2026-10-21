package v1alpha1

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestRaisedMemory(t *testing.T) {
	a := AgentsSpec{Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("4Gi")}}}
	for _, tc := range []struct{ current, want string }{
		{"4Gi", "6Gi"},
		{"6Gi", "8Gi"}, // capped at twice the limit
		{"8Gi", ""},
		{"1000M", "1431Mi"},
	} {
		got, ok := a.RaisedMemory(resource.MustParse(tc.current))
		if (tc.want == "") == ok || ok && got.String() != tc.want {
			t.Errorf("%s: got %s %v, want %q", tc.current, got.String(), ok, tc.want)
		}
	}
	maxMem := resource.MustParse("16Gi")
	a.MaxMemory = &maxMem
	if got, ok := a.RaisedMemory(resource.MustParse("8Gi")); !ok || got.String() != "12Gi" {
		t.Errorf("with maxMemory: got %s %v", got.String(), ok)
	}
	if _, ok := (AgentsSpec{}).RaisedMemory(resource.MustParse("4Gi")); ok {
		t.Error("without a configured limit nothing is raised")
	}
}
