package command

import (
	"slices"
	"testing"
)

func TestPilotAddressOnlyAtTheStart(t *testing.T) {
	for _, tc := range []struct {
		body, msg string
		ok        bool
	}{
		{"/pilot why are we paused?", "why are we paused?", true},
		{"\n  \n/pilot: skip #3?\nwhat breaks", "skip #3?\nwhat breaks", true},
		{"@ai-bot why are we paused?", "why are we paused?", true},
		{"@AI-Bot, status?", "status?", true},
		{"@ai-bot. status?", "status?", true},
		{"/pilot", "", true},
		{"/pilots are great", "", false},
		{"/pilot/x", "", false},
		{"please /pilot why?", "", false},
		{"thanks @ai-bot, why?", "", false},
		{"> /pilot why are we paused?", "", false},
		{"> @ai-bot why?", "", false},
		{"`/pilot` is the command", "", false},
		{"```\n/pilot why?\n```", "", false},
		{"    /pilot why?", "", false},
		{"\t@ai-bot why?", "", false},
		{"@ai-bot2 why?", "", false},
		{"@ai-bot.x why?", "", false},
		{"@ai-bo why?", "", false},
		{"/agent why?", "", false},
	} {
		msg, ok := Pilot(tc.body, "/pilot", "ai-bot")
		if ok != tc.ok || msg != tc.msg {
			t.Errorf("%q: got %q %v, want %q %v", tc.body, msg, ok, tc.msg, tc.ok)
		}
	}
	if _, ok := Pilot("@ai-bot why?", "/pilot", ""); ok {
		t.Error("without a bot name the mention is off")
	}
}

func TestPilotAnswerIsTheWholeText(t *testing.T) {
	for _, tc := range []struct {
		s       string
		yes, ok bool
		id      int64
	}{
		{"yes", true, true, 0},
		{" Yes! ", true, true, 0},
		{"no", false, true, 0},
		{"yes P3", true, true, 3},
		{"NO p12.", false, true, 12},
		{"> yes", false, false, 0},
		{"yes, but later", false, false, 0},
		{"yes P", false, false, 0},
		{"yesterday", false, false, 0},
	} {
		yes, id, ok := PilotAnswer(tc.s)
		if yes != tc.yes || id != tc.id || ok != tc.ok {
			t.Errorf("%q: got %v %d %v", tc.s, yes, id, ok)
		}
	}
}

func TestPilotTell(t *testing.T) {
	if iid, text, ok := PilotTell("tell #331: install the toolchain\nthen run it"); !ok || iid != 331 || text != "install the toolchain\nthen run it" {
		t.Fatalf("got %d %q %v", iid, text, ok)
	}
	for _, s := range []string{"tell #3", "tell 3: x", "please tell #3: x", "tell #3:   "} {
		if _, _, ok := PilotTell(s); ok {
			t.Errorf("%q is no tell", s)
		}
	}
}

func TestRetroCreateIsTheWholeComment(t *testing.T) {
	for _, tc := range []struct {
		s    string
		nums []int
		ok   bool
	}{
		{"create 1 2", []int{1, 2}, true},
		{" Create 1, 3. ", []int{1, 3}, true},
		{"CREATE 2,1,2", []int{2, 1}, true},
		{"create 7", []int{7}, true},
		{"create all", nil, true},
		{"Create ALL!", nil, true},
		{"create", nil, false},
		{"create 1 and 2", nil, false},
		{"please create 1", nil, false},
		{"> create 1", nil, false},
		{"create 1\nthanks", nil, false},
		{"create #1", nil, false},
		{"create 12345", nil, false},
		{"/pilot create 1", nil, false},
		{"created 1", nil, false},
		{"create none", nil, false},
	} {
		nums, ok := RetroCreate(tc.s)
		if ok != tc.ok || !slices.Equal(nums, tc.nums) || (tc.ok && tc.nums == nil) != (ok && nums == nil) {
			t.Errorf("%q: got %v %v", tc.s, nums, ok)
		}
	}
}

func TestRetroNoneIsTheWholeComment(t *testing.T) {
	for s, want := range map[string]bool{
		"create none":         true,
		" Create NONE. ":      true,
		"create  none!":       true,
		"create none 1":       false,
		"create nothing":      false,
		"please create none":  false,
		"> create none":       false,
		"create none\nthanks": false,
		"/pilot create none":  false,
		"none":                false,
		"create all":          false,
	} {
		if got := RetroNone(s); got != want {
			t.Errorf("%q: got %v", s, got)
		}
	}
}
