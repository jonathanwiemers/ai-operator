package milestoneorder

import (
	"reflect"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

var t0 = time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)

func at(minutes int) *time.Time {
	t := t0.Add(time.Duration(minutes) * time.Minute)
	return &t
}

func TestRankAndAhead(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []Entry
		rank    []int64
		ahead   map[int64][]int64
	}{
		{
			name:    "no marks: by age",
			entries: []Entry{{IID: 10}, {IID: 7}, {IID: 9}},
			rank:    []int64{7, 9, 10},
			ahead:   map[int64][]int64{7: nil, 9: {7}, 10: {7, 9}},
		},
		{
			name:    "one mark passes older ones",
			entries: []Entry{{IID: 7}, {IID: 9}, {IID: 10, MarkedAt: at(0)}},
			rank:    []int64{10, 7, 9},
			ahead:   map[int64][]int64{10: nil, 7: {10}, 9: {10, 7}},
		},
		{
			name:    "two marks in mark order",
			entries: []Entry{{IID: 7}, {IID: 9, MarkedAt: at(5)}, {IID: 10, MarkedAt: at(1)}},
			rank:    []int64{10, 9, 7},
			ahead:   map[int64][]int64{10: nil, 9: {10}, 7: {10, 9}},
		},
		{
			name:    "a mark tie is broken by iid",
			entries: []Entry{{IID: 10, MarkedAt: at(1)}, {IID: 7}, {IID: 9, MarkedAt: at(1)}},
			rank:    []int64{9, 10, 7},
			ahead:   map[int64][]int64{9: nil, 10: {9}, 7: {9, 10}},
		},
		{
			name:    "done entries hold back nothing and get the list of where they would rank",
			entries: []Entry{{IID: 7, Done: true}, {IID: 9}, {IID: 10, MarkedAt: at(0), Done: true}, {IID: 11}},
			rank:    []int64{9, 11},
			ahead:   map[int64][]int64{10: nil, 7: nil, 9: nil, 11: {9}},
		},
		{
			name:    "a cleared mark restores age order",
			entries: []Entry{{IID: 7}, {IID: 9}, {IID: 10}},
			rank:    []int64{7, 9, 10},
			ahead:   map[int64][]int64{7: nil, 9: {7}, 10: {7, 9}},
		},
		{
			name:  "no entries",
			ahead: map[int64][]int64{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Rank(tc.entries); !slices.Equal(got, tc.rank) {
				t.Fatalf("Rank: got %v, want %v", got, tc.rank)
			}
			got := Ahead(tc.entries)
			if len(got) != len(tc.ahead) {
				t.Fatalf("Ahead: got %v, want %v", got, tc.ahead)
			}
			for iid, want := range tc.ahead {
				if g, ok := got[iid]; !ok || !slices.Equal(g, want) {
					t.Fatalf("Ahead %%%d: got %v, want %v (all: %v)", iid, g, want, got)
				}
			}
		})
	}
}

func TestRankLeavesInputAlone(t *testing.T) {
	entries := []Entry{{IID: 9}, {IID: 7, MarkedAt: at(0)}, {IID: 8}}
	before := slices.Clone(entries)
	Rank(entries)
	Ahead(entries)
	if !reflect.DeepEqual(entries, before) {
		t.Fatalf("the input is sorted in place: %v", entries)
	}
}

func TestEntryOf(t *testing.T) {
	mark := metav1.NewTime(t0)
	for _, tc := range []struct {
		name string
		ap   *aiv1.AutopilotStatus
		want Entry
	}{
		{name: "no status: unmarked, not done", want: Entry{IID: 7}},
		{name: "running", ap: &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning}, want: Entry{IID: 7}},
		{name: "done", ap: &aiv1.AutopilotStatus{Phase: aiv1.AutopilotDone}, want: Entry{IID: 7, Done: true}},
		{
			name: "marked",
			ap:   &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning, StartRequestedBy: "@alice", StartRequestedAt: &mark},
			want: Entry{IID: 7, MarkedAt: &t0},
		},
		{name: "a legacy start without a time is no mark", ap: &aiv1.AutopilotStatus{StartRequestedBy: "@alice"}, want: Entry{IID: 7}},
		{name: "a time without an owner is no mark", ap: &aiv1.AutopilotStatus{StartRequestedAt: &mark}, want: Entry{IID: 7}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := EntryOf(7, &aiv1.AgentTask{Status: aiv1.AgentTaskStatus{Autopilot: tc.ap}}); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
	if got := EntryOf(7, nil); !reflect.DeepEqual(got, Entry{IID: 7}) {
		t.Fatalf("no spec task: got %+v", got)
	}
}
