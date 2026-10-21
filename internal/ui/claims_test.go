package ui

import (
	"reflect"
	"strings"
	"testing"
)

func TestClaimMapper(t *testing.T) {
	const zitadelRoles = `claims["urn:zitadel:iam:org:project:roles"]`
	cases := []struct {
		name      string
		claims    Claims
		in        map[string]any
		wantUser  string
		wantRoles []string
		wantErr   string
	}{
		{
			name:      "defaults with groups",
			claims:    Claims{Username: DefaultUsernameExpr, Roles: DefaultRolesExpr},
			in:        map[string]any{"sub": "1", "email": "ada@example.com", "groups": []any{"dev", "ops", "dev"}},
			wantUser:  "ada@example.com",
			wantRoles: []string{"dev", "ops"},
		},
		{
			name:      "defaults without email and groups",
			claims:    Claims{Username: DefaultUsernameExpr, Roles: DefaultRolesExpr},
			in:        map[string]any{"sub": "1234"},
			wantUser:  "1234",
			wantRoles: []string{},
		},
		{
			name:   "zitadel role map",
			claims: Claims{Username: "claims.preferred_username", Roles: zitadelRoles},
			in: map[string]any{
				"preferred_username": "ada",
				"urn:zitadel:iam:org:project:roles": map[string]any{
					"ai-operator-admin": map[string]any{"123": "acme.zitadel.cloud"},
					"viewer":            map[string]any{"123": "acme.zitadel.cloud"},
				},
			},
			wantUser:  "ada",
			wantRoles: []string{"ai-operator-admin", "viewer"},
		},
		{
			name:      "nested list (Keycloak)",
			claims:    Claims{Username: "claims.sub", Roles: "claims.realm_access.roles"},
			in:        map[string]any{"sub": "1", "realm_access": map[string]any{"roles": []any{"admin"}}},
			wantUser:  "1",
			wantRoles: []string{"admin"},
		},
		{
			name:      "single string",
			claims:    Claims{Username: "claims.sub", Roles: "claims.role"},
			in:        map[string]any{"sub": "1", "role": "admin"},
			wantUser:  "1",
			wantRoles: []string{"admin"},
		},
		{
			name:    "missing claim",
			claims:  Claims{Username: "claims.email", Roles: DefaultRolesExpr},
			in:      map[string]any{"sub": "1"},
			wantErr: "username",
		},
		{
			name:      "no roles claim",
			claims:    Claims{Username: "claims.sub", Roles: zitadelRoles},
			in:        map[string]any{"sub": "1"},
			wantUser:  "1",
			wantRoles: []string{},
		},
		{
			name:    "roles of the wrong type",
			claims:  Claims{Username: "claims.sub", Roles: "claims.n"},
			in:      map[string]any{"sub": "1", "n": 3.0},
			wantErr: "roles",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := NewClaimMapper(tc.claims)
			if err != nil {
				t.Fatal(err)
			}
			user, roles, err := m.Map(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if user != tc.wantUser || !reflect.DeepEqual(roles, tc.wantRoles) {
				t.Errorf("got %q %v, want %q %v", user, roles, tc.wantUser, tc.wantRoles)
			}
		})
	}
}

func TestClaimMapperCompileError(t *testing.T) {
	if _, err := NewClaimMapper(Claims{Username: "claims.", Roles: DefaultRolesExpr}); err == nil || !strings.Contains(err.Error(), "oidc.claims.username") {
		t.Fatalf("error = %v", err)
	}
}

func TestAuthorizer(t *testing.T) {
	cfg := &Config{
		Auth: AuthConfig{Mode: AuthOIDC},
		Authorization: AuthorizationConfig{Rules: []Rule{
			{Roles: []string{"admin"}},
			{Roles: []string{"shop-devs"}, Projects: []string{"team-a/shop"}},
			{Roles: []string{"*"}, Projects: []string{"public/*"}},
		}},
	}
	a := NewAuthorizer(cfg)
	cases := []struct {
		roles   []string
		ns, prj string
		want    bool
	}{
		{[]string{"admin"}, "team-a", "shop", true},
		{[]string{"admin"}, "team-b", "x", true},
		{[]string{"shop-devs"}, "team-a", "shop", true},
		{[]string{"shop-devs"}, "team-a", "billing", false},
		{nil, "team-a", "shop", false},
		{nil, "public", "docs", true},
		{[]string{"other"}, "team-b", "x", false},
	}
	for _, tc := range cases {
		if got := a.CanView(Principal{Roles: tc.roles}, tc.ns, tc.prj); got != tc.want {
			t.Errorf("CanView(%v, %s/%s) = %v, want %v", tc.roles, tc.ns, tc.prj, got, tc.want)
		}
	}
	if !NewAuthorizer(&Config{Auth: AuthConfig{Mode: AuthNone}}).CanView(Principal{}, "x", "y") {
		t.Error("auth mode none must show everything")
	}
	if NewAuthorizer(&Config{Auth: AuthConfig{Mode: AuthOIDC}}).CanView(Principal{Roles: []string{"admin"}}, "x", "y") {
		t.Error("no rules must show nothing")
	}
}
