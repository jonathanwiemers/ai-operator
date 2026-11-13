package naming

import (
	"strings"
	"testing"
)

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Add PayPal button":       "add-paypal-button",
		"  --Checkout v2!! ":      "checkout-v2",
		"Über straße / API (v1)":  "ber-stra-e-api-v1",
		"":                        "",
		"___":                     "",
		"Fix #12: crash on login": "fix-12-crash-on-login",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTaskNames(t *testing.T) {
	if got := SpecTask("shop", 3, "Checkout v2"); got != "shop-spec-m3-checkout-v2" {
		t.Errorf("SpecTask = %q", got)
	}
	if got := ImplementTask("Shop", 42, "Add PayPal button"); got != "shop-42-add-paypal-button" {
		t.Errorf("ImplementTask = %q", got)
	}
	long := ImplementTask("shop", 1234, strings.Repeat("implement the very long feature title ", 5))
	if len(long) > MaxNameLength {
		t.Errorf("name too long: %d %q", len(long), long)
	}
	if strings.HasSuffix(long, "-") {
		t.Errorf("name ends with dash: %q", long)
	}
	if !strings.HasPrefix(long, "shop-1234-implement-the-very-long") {
		t.Errorf("unexpected long name %q", long)
	}
	if got := ImplementTask("shop", 7, "!!!"); got != "shop-7" {
		t.Errorf("ImplementTask without slug = %q", got)
	}
}

func TestWorkspaceAndBranch(t *testing.T) {
	ws := Workspace(strings.Repeat("a", 63))
	if len(ws) > MaxNameLength {
		t.Errorf("workspace name too long: %q", ws)
	}
	if got := Branch(42, "Add PayPal button"); got != "agent/42-add-paypal-button" {
		t.Errorf("Branch = %q", got)
	}
}

func TestResearchPod(t *testing.T) {
	if got := ResearchPod("morning-news", "1a2b3c4d-0000-4000-8000-000000000000"); got != "morning-news-1a2b3c4d" {
		t.Errorf("got %q", got)
	}
	long := strings.Repeat("research.", 10)
	got := ResearchPod(long, "1a2b3c4d-0000-4000-8000-000000000000")
	if len(got) > MaxNameLength || !strings.HasSuffix(got, "-1a2b3c4d") || strings.Contains(got, ".-") {
		t.Errorf("got %q (%d)", got, len(got))
	}
	if v := LabelValue(long); len(v) > MaxNameLength || LabelValue("short") != "short" {
		t.Errorf("label value %q", v)
	}
}

func TestProjectSlug(t *testing.T) {
	for in, want := range map[string]string{
		"example/infra":         "example-infra",
		"/Group/Sub_Group/App/": "group-sub_group-app",
		"group/ä-app":           "group--app",
		"../etc/passwd":         "etc-passwd", // leading "." and "-" are trimmed
		"":                      "",
		"..":                    "",
	} {
		got := ProjectSlug(in)
		if got != want {
			t.Errorf("ProjectSlug(%q) = %q, want %q", in, got, want)
		}
		if got != "" && !ValidProjectSlug(got) {
			t.Errorf("ProjectSlug(%q) = %q is not valid", in, got)
		}
	}
	if got := ProjectSlug(strings.Repeat("a/", 300)); len(got) != 200 || !ValidProjectSlug(got) {
		t.Errorf("a long slug is cut to 200: %d", len(got))
	}
}

func TestValidProjectSlug(t *testing.T) {
	for s, want := range map[string]bool{
		"example-infra":          true,
		"a":                      true,
		"0a._-":                  true,
		"":                       false,
		".":                      false,
		"..":                     false,
		".git":                   false,
		"-a":                     false,
		"_a":                     false,
		"a/b":                    false,
		"A":                      false,
		"a b":                    false,
		"a\x00":                  false,
		strings.Repeat("a", 201): false,
	} {
		if got := ValidProjectSlug(s); got != want {
			t.Errorf("ValidProjectSlug(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestCompanionBranch(t *testing.T) {
	for _, c := range []struct {
		home  string
		iid   int64
		title string
		want  string
	}{
		{"example/ai-operator", 42, "Add PayPal button", "agent/ai-operator-42-add-paypal-button"},
		{"Group/Shop.App", 7, "", "agent/shop-app-7"},
		{"app", 3, "Fix it", "agent/app-3-fix-it"},
		{"group/app", 9, strings.Repeat("word ", 20), "agent/app-9-word-word-word-word-word-word-word-word"},
	} {
		if got := CompanionBranch(c.home, c.iid, c.title); got != c.want {
			t.Errorf("CompanionBranch(%q, %d, %q) = %q, want %q", c.home, c.iid, c.title, got, c.want)
		}
		if IsIssueBranch(CompanionBranch(c.home, c.iid, c.title), c.iid) {
			t.Errorf("a companion branch is no issue branch of the home project")
		}
	}
}
