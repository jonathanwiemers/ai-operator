package command

import "testing"

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
