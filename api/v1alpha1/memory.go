package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// MaxOOMRetries bounds how often one run request is started again after the
// agent container ran out of memory.
const MaxOOMRetries = 3

// MemoryCap is the most the agent container's memory limit is raised to, in
// bytes: MaxMemory, else twice the configured limit. ok is false without a
// configured memory limit.
func (a AgentsSpec) MemoryCap() (int64, bool) {
	limit, ok := a.Resources.Limits[corev1.ResourceMemory]
	if !ok {
		return 0, false
	}
	if a.MaxMemory != nil {
		return a.MaxMemory.Value(), true
	}
	return 2 * limit.Value(), true
}

// RaisedMemory returns the agent container's memory limit after an OOM kill
// at current: half as much again (whole MiB), at most MaxMemory. ok is false
// if it cannot be raised: no memory limit configured, or at the cap.
func (a AgentsSpec) RaisedMemory(current resource.Quantity) (resource.Quantity, bool) {
	maxBytes, ok := a.MemoryCap()
	if !ok {
		return resource.Quantity{}, false
	}
	const mi = 1 << 20
	next := (current.Value()*3/2 + mi - 1) / mi * mi
	next = min(next, maxBytes)
	if next <= current.Value() {
		return resource.Quantity{}, false
	}
	return *resource.NewQuantity(next, resource.BinarySI), true
}

// EffectiveMemory is the agent container's memory limit of a task whose limit
// was raised to raised (status.agentMemoryLimit): at most the cap as it is
// now. ok is false where the configured limit applies.
func (a AgentsSpec) EffectiveMemory(raised string) (resource.Quantity, bool) {
	q, err := resource.ParseQuantity(raised)
	limit, hasLimit := a.Resources.Limits[corev1.ResourceMemory]
	if err != nil || !hasLimit {
		return resource.Quantity{}, false
	}
	if maxBytes, _ := a.MemoryCap(); q.Value() > maxBytes {
		q = *resource.NewQuantity(maxBytes, resource.BinarySI)
	}
	if q.Cmp(limit) <= 0 {
		return resource.Quantity{}, false
	}
	return q, true
}
