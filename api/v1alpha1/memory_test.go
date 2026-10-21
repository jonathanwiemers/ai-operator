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

func TestEffectiveMemory(t *testing.T) {
	a := AgentsSpec{Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("4Gi")}}}
	if got, ok := a.EffectiveMemory("6Gi"); !ok || got.String() != "6Gi" {
		t.Errorf("raised: %s %v", got.String(), ok)
	}
	if _, ok := a.EffectiveMemory(""); ok {
		t.Error("nothing raised")
	}
	low := resource.MustParse("5Gi")
	a.MaxMemory = &low
	if got, ok := a.EffectiveMemory("6Gi"); !ok || got.String() != "5Gi" {
		t.Errorf("a lowered cap applies: %s %v", got.String(), ok)
	}
	a.Resources.Limits[corev1.ResourceMemory] = resource.MustParse("8Gi")
	if _, ok := a.EffectiveMemory("6Gi"); ok {
		t.Error("a configured limit above the raise wins")
	}
}
