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

func TestEntryOf(t *testing.T) {
	mark := metav1.NewTime(t0)
	for _, tc := range []struct {
		name string
		ap   *aiv1.AutopilotStatus
		want Entry
	}{
		{name: "no status: unmarked, not done", want: Entry{IID: 7}},
		{name: "running", ap: &aiv1.AutopilotStatus{Phase: aiv1.AutopilotRunning}, want: Entry{IID: 7}},
		{
			name: "planned: items, not done",
			ap:   &aiv1.AutopilotStatus{Phase: aiv1.AutopilotPaused, Items: []aiv1.AutopilotItem{{IID: 1}}},
			want: Entry{IID: 7, Started: true},
		},
		{
			name: "done with items is not started",
			ap:   &aiv1.AutopilotStatus{Phase: aiv1.AutopilotDone, Items: []aiv1.AutopilotItem{{IID: 1}}},
			want: Entry{IID: 7, Done: true},
		},
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

func TestSeed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []Entry
		want    []int64
	}{
		{name: "no marks: by age", entries: []Entry{{IID: 10}, {IID: 7}, {IID: 9}}, want: []int64{7, 9, 10}},
		{name: "one mark passes older ones", entries: []Entry{{IID: 5}, {IID: 6, MarkedAt: at(0)}, {IID: 7}}, want: []int64{6, 5, 7}},
		{name: "two marks in mark order", entries: []Entry{{IID: 7}, {IID: 9, MarkedAt: at(5)}, {IID: 10, MarkedAt: at(1)}}, want: []int64{10, 9, 7}},
		{name: "a mark tie is broken by iid", entries: []Entry{{IID: 10, MarkedAt: at(1)}, {IID: 7}, {IID: 9, MarkedAt: at(1)}}, want: []int64{9, 10, 7}},
		{name: "done ones left out", entries: []Entry{{IID: 7, Done: true}, {IID: 9}, {IID: 10, MarkedAt: at(0), Done: true}}, want: []int64{9}},
		{name: "no entries", want: []int64{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Seed(tc.entries); !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	for _, tc := range []struct {
		name    string
		order   []int64
		entries []Entry
		want    []int64
	}{
		{name: "an empty list is seeded", entries: []Entry{{IID: 5}, {IID: 6, MarkedAt: at(0)}, {IID: 7}}, want: []int64{6, 5, 7}},
		{name: "a stored list ignores marks", order: []int64{5, 6, 7}, entries: []Entry{{IID: 5}, {IID: 6, MarkedAt: at(0)}, {IID: 7}}, want: []int64{5, 6, 7}},
		{name: "new ones join at the bottom by iid", order: []int64{7, 5}, entries: []Entry{{IID: 9}, {IID: 5}, {IID: 8}, {IID: 7}}, want: []int64{7, 5, 8, 9}},
		{name: "done ones are dropped", order: []int64{6, 5, 7}, entries: []Entry{{IID: 5}, {IID: 6, Done: true}, {IID: 7}}, want: []int64{5, 7}},
		{name: "closed or off-autopilot ones are dropped", order: []int64{6, 5, 7}, entries: []Entry{{IID: 5}, {IID: 7}}, want: []int64{5, 7}},
		{name: "duplicates are dropped", order: []int64{7, 5, 7}, entries: []Entry{{IID: 5}, {IID: 7}}, want: []int64{7, 5}},
		{name: "nothing open", order: []int64{5}, entries: []Entry{{IID: 5, Done: true}}, want: []int64{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := slices.Clone(tc.order)
			if got := Normalize(tc.order, tc.entries); !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			if !slices.Equal(tc.order, before) {
				t.Fatalf("the order changed: %v", tc.order)
			}
		})
	}
}

func TestRankAndAhead(t *testing.T) {
	for _, tc := range []struct {
		name    string
		order   []int64
		entries []Entry
		rank    []int64
		ahead   map[int64][]int64
	}{
		{
			name:    "list order",
			order:   []int64{10, 7, 9},
			entries: []Entry{{IID: 7}, {IID: 9}, {IID: 10}},
			rank:    []int64{10, 7, 9},
			ahead:   map[int64][]int64{10: nil, 7: {10}, 9: {10, 7}},
		},
		{
			name:    "marks do not rank a stored list",
			order:   []int64{7, 9, 10},
			entries: []Entry{{IID: 7}, {IID: 9}, {IID: 10, MarkedAt: at(0)}},
			rank:    []int64{7, 9, 10},
			ahead:   map[int64][]int64{7: nil, 9: {7}, 10: {7, 9}},
		},
		{
			name:    "unlisted ones at the bottom by iid",
			order:   []int64{9},
			entries: []Entry{{IID: 11}, {IID: 7}, {IID: 9}},
			rank:    []int64{9, 7, 11},
			ahead:   map[int64][]int64{9: nil, 7: {9}, 11: {9, 7}},
		},
		{
			name:    "an empty list ranks like the seed",
			entries: []Entry{{IID: 7}, {IID: 9}, {IID: 10, MarkedAt: at(0)}},
			rank:    []int64{10, 7, 9},
			ahead:   map[int64][]int64{10: nil, 7: {10}, 9: {10, 7}},
		},
		{
			name:    "done entries hold back nothing and get the list of where they would rank",
			order:   []int64{10, 7, 9, 11},
			entries: []Entry{{IID: 7, Done: true}, {IID: 9}, {IID: 10, Done: true}, {IID: 11}},
			rank:    []int64{9, 11},
			ahead:   map[int64][]int64{10: nil, 7: nil, 9: nil, 11: {9}},
		},
		{
			name:    "listed iids without an entry are skipped",
			order:   []int64{4, 9, 7},
			entries: []Entry{{IID: 7}, {IID: 9}},
			rank:    []int64{9, 7},
			ahead:   map[int64][]int64{9: nil, 7: {9}},
		},
		{
			name:  "no entries",
			order: []int64{5},
			ahead: map[int64][]int64{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Rank(tc.order, tc.entries); !slices.Equal(got, tc.rank) {
				t.Fatalf("Rank: got %v, want %v", got, tc.rank)
			}
			got := Ahead(tc.order, tc.entries)
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
	order := []int64{8, 9}
	before, orderBefore := slices.Clone(entries), slices.Clone(order)
	Rank(order, entries)
	Ahead(order, entries)
	Seed(entries)
	if !reflect.DeepEqual(entries, before) || !slices.Equal(order, orderBefore) {
		t.Fatalf("the input is sorted in place: %v, %v", entries, order)
	}
}

func TestRunOrder(t *testing.T) {
	for _, tc := range []struct {
		name    string
		order   []int64
		entries []Entry
		want    []int64
	}{
		{
			name:    "started first, then the rest in stored order; done left out",
			order:   []int64{12, 8, 9, 7},
			entries: []Entry{{IID: 7}, {IID: 5, Done: true}, {IID: 12}, {IID: 9, Started: true}, {IID: 8}},
			want:    []int64{9, 12, 8, 7},
		},
		{
			name:    "a started milestone stays ahead of one moved before it",
			order:   []int64{10, 11},
			entries: []Entry{{IID: 10}, {IID: 11, Started: true}},
			want:    []int64{11, 10},
		},
		{
			name:    "several started in stored order",
			order:   []int64{14, 9, 6, 8},
			entries: []Entry{{IID: 14, Started: true}, {IID: 6, Started: true}, {IID: 9}, {IID: 8}},
			want:    []int64{14, 6, 9, 8},
		},
		{name: "no entries", want: []int64{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := slices.Clone(tc.entries)
			if got := RunOrder(tc.order, tc.entries); !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			if !reflect.DeepEqual(tc.entries, before) {
				t.Fatalf("the input changed: %v", tc.entries)
			}
		})
	}
}
