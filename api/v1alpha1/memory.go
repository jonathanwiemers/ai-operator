package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// MaxOOMRetries bounds how often one run request is started again after the
// agent container ran out of memory.
const MaxOOMRetries = 3

// RaisedMemory returns the agent container's memory limit after an OOM kill
// at current: half as much again (whole MiB), at most MaxMemory. ok is false
// if it cannot be raised: no memory limit configured, or at the cap.
func (a AgentsSpec) RaisedMemory(current resource.Quantity) (resource.Quantity, bool) {
	limit, ok := a.Resources.Limits[corev1.ResourceMemory]
	if !ok {
		return resource.Quantity{}, false
	}
	maxBytes := 2 * limit.Value()
	if a.MaxMemory != nil {
		maxBytes = a.MaxMemory.Value()
	}
	const mi = 1 << 20
	next := (current.Value()*3/2 + mi - 1) / mi * mi
	next = min(next, maxBytes)
	if next <= current.Value() {
		return resource.Quantity{}, false
	}
	return *resource.NewQuantity(next, resource.BinarySI), true
}
