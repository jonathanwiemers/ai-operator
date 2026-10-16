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
