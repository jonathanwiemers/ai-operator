package v1alpha1

import (
	"strings"
	"testing"
)

func TestProjectSetChildName(t *testing.T) {
	cases := []struct{ group, path, want string }{
		{"jo.wiemers", "jo.wiemers/devops/functions/pnpm", "devops-functions-pnpm"},
		{"Jo.Wiemers", "jo.wiemers/Shop_Front.end", "shop-front-end"},
		{"jo.wiemers/", "/JO.WIEMERS/x/", "x"},
		{"other", "jo.wiemers/x", "jo-wiemers-x"},
	}
	for _, tc := range cases {
		if got := ProjectSetChildName(tc.group, tc.path); got != tc.want {
			t.Errorf("ProjectSetChildName(%q, %q) = %q, want %q", tc.group, tc.path, got, tc.want)
		}
	}

	long := "jo.wiemers/" + strings.Repeat("abcdefghi/", 5) + "x-project"
	got := ProjectSetChildName("jo.wiemers", long)
	if len(got) > 57 || got != ProjectSetChildName("jo.wiemers", strings.ToUpper(long)) {
		t.Errorf("long name %q (%d)", got, len(got))
	}
	// The cut lands on a separator: it is trimmed before the hash.
	if !strings.HasPrefix(got, "abcdefghi-abcdefghi-abcdefghi-abcdefghi-abcdefghi-") || strings.Contains(got, "--") {
		t.Errorf("long name %q", got)
	}
	other := ProjectSetChildName("jo.wiemers", long+"2")
	if other == got || len(other) > 57 {
		t.Errorf("hash does not tell %q from %q", other, got)
	}
	if exact := ProjectSetChildName("jo.wiemers", "jo.wiemers/"+strings.Repeat("a", 50)); exact != strings.Repeat("a", 50) {
		t.Errorf("50 characters are kept: %q", exact)
	}
}
