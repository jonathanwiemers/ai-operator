package ui

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

func TestCompareCalver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2026.11.3", "2026.11.3", 0},
		{"2026.11.4", "2026.11.3", 1},
		{"2026.10.12", "2026.11.2", -1},
		{"2026.9.1", "2026.10.1", -1},
		{"2027.1.1", "2026.12.40", 1},
		{"2026.11", "2026.11.1", -1},
	}
	for _, tc := range cases {
		if got := compareCalver(tc.a, tc.b); got != tc.want {
			t.Errorf("compareCalver(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// setReleases stores the releases of the shop project (testObjects) and of
// its milestone spec task.
func setReleases(t *testing.T, env *testEnv, project, milestone []aiv1.ReleaseRecord) {
	t.Helper()
	ctx := context.Background()
	var ap aiv1.AgentProject
	if err := env.reader.Get(ctx, client.ObjectKey{Namespace: "team-a", Name: "shop"}, &ap); err != nil {
		t.Fatal(err)
	}
	ap.Status.Releases = project
	if err := env.reader.Update(ctx, &ap); err != nil {
		t.Fatal(err)
	}
	var task aiv1.AgentTask
	if err := env.reader.Get(ctx, client.ObjectKey{Namespace: "team-a", Name: "shop-spec-m3-checkout"}, &task); err != nil {
		t.Fatal(err)
	}
	task.Status.Autopilot = &aiv1.AutopilotStatus{Releases: milestone}
	if err := env.reader.Update(ctx, &task); err != nil {
		t.Fatal(err)
	}
}

func rel(version string, state aiv1.ReleaseState, decided time.Time) aiv1.ReleaseRecord {
	return aiv1.ReleaseRecord{Version: version, State: state, DecidedAt: metav1.NewTime(decided)}
}

func TestAbout(t *testing.T) {
	day := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	released := rel("2026.10.12", aiv1.ReleaseReleased, day.Add(3*time.Hour))
	released.ReleaseURL = "https://gitlab.example.com/acme/shop/-/releases/2026.10.12"
	cases := []struct {
		name               string
		self               string
		project, milestone []aiv1.ReleaseRecord
		want               About
	}{
		{"no self project", "", []aiv1.ReleaseRecord{released}, nil,
			About{Version: "2026.11.3"}},
		{"no matching project", "acme/other", []aiv1.ReleaseRecord{released}, nil,
			About{Version: "2026.11.3"}},
		{"no records", "acme/shop", nil, nil,
			About{Version: "2026.11.3", ReleasesURL: "https://gitlab.example.com/acme/shop/-/releases"}},
		{"newer running on the milestone", "acme/shop", nil,
			[]aiv1.ReleaseRecord{rel("2026.11.3", aiv1.ReleaseReleased, day), rel("2026.11.4", aiv1.ReleaseRunning, day.Add(time.Hour))},
			About{Version: "2026.11.3", ReleasesURL: "https://gitlab.example.com/acme/shop/-/releases",
				Release: &AboutRelease{Version: "2026.11.4", State: aiv1.ReleaseRunning}}},
		{"dropped and versionless skipped", "acme/shop", nil,
			[]aiv1.ReleaseRecord{
				rel("2026.11.3", aiv1.ReleaseReleased, day),
				rel("2026.11.4", aiv1.ReleaseDropped, day.Add(time.Hour)),
				rel("", aiv1.ReleaseWaiting, day.Add(2*time.Hour)),
			},
			About{Version: "2026.11.3", ReleasesURL: "https://gitlab.example.com/acme/shop/-/releases",
				Release: &AboutRelease{Version: "2026.11.3", State: aiv1.ReleaseReleased}}},
		{"compared by version, not position", "acme/shop", []aiv1.ReleaseRecord{released},
			[]aiv1.ReleaseRecord{rel("2026.11.2", aiv1.ReleaseRunning, day)},
			About{Version: "2026.11.3", ReleasesURL: "https://gitlab.example.com/acme/shop/-/releases",
				Release: &AboutRelease{Version: "2026.11.2", State: aiv1.ReleaseRunning}}},
		{"tie goes to the later decision", "acme/shop",
			[]aiv1.ReleaseRecord{rel("2026.11.5", aiv1.ReleaseFailed, day)},
			[]aiv1.ReleaseRecord{rel("2026.11.5", aiv1.ReleaseRunning, day.Add(time.Hour))},
			About{Version: "2026.11.3", ReleasesURL: "https://gitlab.example.com/acme/shop/-/releases",
				Release: &AboutRelease{Version: "2026.11.5", State: aiv1.ReleaseRunning}}},
		{"older project record keeps its URL", "acme/shop", []aiv1.ReleaseRecord{released}, nil,
			About{Version: "2026.11.3", ReleasesURL: "https://gitlab.example.com/acme/shop/-/releases",
				Release: &AboutRelease{Version: "2026.10.12", State: aiv1.ReleaseReleased, URL: released.ReleaseURL}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, &Config{Auth: AuthConfig{Mode: AuthNone}})
			env.server.SetAbout("2026.11.3", tc.self)
			setReleases(t, env, tc.project, tc.milestone)
			var got About
			if code := env.get(t, "/api/v1/about", &got); code != http.StatusOK {
				t.Fatalf("status %d", code)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("about = %+v (release %+v), want %+v (release %+v)", got, got.Release, tc.want, tc.want.Release)
			}
		})
	}
}

// Any logged-in user sees the release, without a role on the self project;
// without a session the endpoint answers 401.
func TestAboutWithoutViewRole(t *testing.T) {
	env := newTestEnv(t, oidcConfig(Rule{Roles: []string{"admin"}}))
	env.server.SetAbout("2026.11.3", "acme/shop")
	setReleases(t, env, nil, []aiv1.ReleaseRecord{rel("2026.11.4", aiv1.ReleaseRunning, time.Now())})
	if code := env.get(t, "/api/v1/about", nil); code != http.StatusUnauthorized {
		t.Fatalf("before login: status %d", code)
	}
	env.idp.user = map[string]any{"sub": "u2", "email": "bob@example.com"}
	env.login(t, "/")
	var got About
	if code := env.get(t, "/api/v1/about", &got); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if got.Release == nil || got.Release.Version != "2026.11.4" || got.Release.State != aiv1.ReleaseRunning {
		t.Errorf("about = %+v (release %+v)", got, got.Release)
	}
}
