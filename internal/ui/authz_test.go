package ui

import "testing"

func TestAuthorizerResearch(t *testing.T) {
	rules := []Rule{
		{Roles: []string{"admin"}, Transcript: true, Steer: true},
		{Roles: []string{"shop-devs"}, Projects: []string{"team-a/shop"}},
		{Roles: []string{"readers"}, Research: ResearchRead},
		{Roles: []string{"news"}, Research: ResearchWrite, Researches: []string{"agents/news-*"}},
	}
	config := func(mode AuthMode, research, allowCreate bool) *Config {
		c := &Config{Auth: AuthConfig{Mode: mode}, Authorization: AuthorizationConfig{Rules: rules}}
		if research {
			c.Research = ResearchConfig{Database: DatabaseConfig{Host: "db"}, Namespace: "agents", AllowCreate: allowCreate}
		}
		return c
	}
	type want struct{ factory, project, read, write, anyRead, anyWrite bool }
	cases := []struct {
		name                  string
		mode                  AuthMode
		research, allowCreate bool
		roles                 []string
		want                  want
	}{
		// Factory rules grant no research, research rules no projects.
		{"admin", AuthOIDC, true, true, []string{"admin"}, want{factory: true, project: true}},
		{"shop dev", AuthOIDC, true, true, []string{"shop-devs"}, want{factory: true}},
		{"reader", AuthOIDC, true, true, []string{"readers"}, want{read: true, anyRead: true}},
		{"news writer", AuthOIDC, true, true, []string{"news"}, want{read: true, write: true, anyRead: true, anyWrite: true}},
		{"writer without allowCreate", AuthOIDC, true, false, []string{"news"}, want{read: true, anyRead: true}},
		{"research off", AuthOIDC, false, false, []string{"readers", "news"}, want{}},
		{"nobody", AuthOIDC, true, true, nil, want{}},
		{"admin and reader", AuthOIDC, true, true, []string{"admin", "readers"}, want{factory: true, project: true, read: true, anyRead: true}},
		{"none", AuthNone, true, false, nil, want{factory: true, project: true, read: true, anyRead: true}},
		{"none with allowCreate", AuthNone, true, true, nil, want{factory: true, project: true, read: true, write: true, anyRead: true, anyWrite: true}},
		{"none without research", AuthNone, false, false, nil, want{factory: true, project: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := NewAuthorizer(config(tc.mode, tc.research, tc.allowCreate))
			p := Principal{Roles: tc.roles}
			anyRead, anyWrite := a.ResearchAccess(p)
			got := want{
				factory:  a.SeesFactory(p),
				project:  a.CanView(p, "team-b", "billing"),
				read:     a.CanReadResearch(p, "agents", "news-morning"),
				write:    a.CanWriteResearch(p, "agents", "news-morning"),
				anyRead:  anyRead,
				anyWrite: anyWrite,
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}

	a := NewAuthorizer(config(AuthOIDC, true, true))
	news := Principal{Roles: []string{"news"}}
	if a.CanReadResearch(news, "agents", "weekly-report") || a.CanReadResearch(news, "other", "news-morning") {
		t.Error("researches patterns must limit the rule")
	}
	if !a.CanReadResearch(Principal{Roles: []string{"readers"}}, "other", "anything") {
		t.Error("a rule without researches must match every research")
	}
	if a.CanView(Principal{Roles: []string{"readers"}}, "team-a", "shop") || a.CanSeeTranscript(news, "team-a", "shop") || a.CanSteer(news, "team-a", "shop") || a.CanChat(news, "team-a", "shop") {
		t.Error("research rules must grant no projects")
	}
}
