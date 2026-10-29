package ui

import (
	"context"
	"sync"

	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// Event tells the browser that a resource changed. It carries no content:
// the browser fetches what it shows again, through the access checks.
type Event struct {
	Kind      string `json:"kind"` // project, task or research
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// Project is the project of a project or task, empty for a research.
	Project string `json:"project"`
}

// EventResearch is the kind of an AgentResearch's events.
const EventResearch = "research"

// Hub fans out change events to the open event streams.
type Hub struct {
	mu   sync.Mutex
	subs map[*subscription]struct{}
}

type subscription struct {
	ch    chan Event
	allow func(Event) bool
}

// NewHub returns an empty hub.
func NewHub() *Hub {
	return &Hub{subs: map[*subscription]struct{}{}}
}

// Subscribe returns the events allow lets through, until cancel is called.
func (h *Hub) Subscribe(allow func(Event) bool) (events <-chan Event, cancel func()) {
	s := &subscription{ch: make(chan Event, 32), allow: allow}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s.ch, func() {
		h.mu.Lock()
		delete(h.subs, s)
		h.mu.Unlock()
	}
}

// Publish sends e to every subscriber that may see it. A subscriber whose
// buffer is full misses it: the events still queued make its browser
// fetch again after this change.
func (h *Hub) Publish(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		if !s.allow(e) {
			continue
		}
		select {
		case s.ch <- e:
		default:
		}
	}
}

// Watch publishes every change of AgentProjects and AgentTasks in the cache,
// and with research of AgentResearches (without, the UI may not read them).
func Watch(ctx context.Context, informers cache.Informers, hub *Hub, research bool) error {
	objs := []client.Object{&aiv1.AgentProject{}, &aiv1.AgentTask{}}
	if research {
		objs = append(objs, &aiv1.AgentResearch{})
	}
	for _, obj := range objs {
		inf, err := informers.GetInformer(ctx, obj)
		if err != nil {
			return err
		}
		publish := func(o any) {
			if e, ok := eventOf(o); ok {
				hub.Publish(e)
			}
		}
		if _, err := inf.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
			AddFunc:    publish,
			UpdateFunc: func(_, o any) { publish(o) },
			DeleteFunc: publish,
		}); err != nil {
			return err
		}
	}
	return nil
}

func eventOf(o any) (Event, bool) {
	if d, ok := o.(toolscache.DeletedFinalStateUnknown); ok {
		o = d.Obj
	}
	switch v := o.(type) {
	case *aiv1.AgentProject:
		return Event{Kind: "project", Namespace: v.Namespace, Name: v.Name, Project: v.Name}, true
	case *aiv1.AgentTask:
		return Event{Kind: "task", Namespace: v.Namespace, Name: v.Name, Project: v.Spec.ProjectRef}, true
	case *aiv1.AgentResearch:
		return Event{Kind: EventResearch, Namespace: v.Namespace, Name: v.Name}, true
	}
	return Event{}, false
}
