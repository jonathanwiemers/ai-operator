// Package naming builds the human readable, DNS-safe names of the objects the
// operator creates, e.g. "shop-spec-m3-checkout-v2" or "shop-42-add-paypal".
package naming

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// MaxNameLength keeps names usable as pod hostnames and label values.
const MaxNameLength = 63

// Slug lowercases s and reduces it to [a-z0-9-], collapsing separators.
func Slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// join concatenates the prefix with an optional slug and truncates the slug
// part so the result fits into MaxNameLength.
func join(prefix, title string) string {
	prefix = strings.Trim(prefix, "-")
	slug := Slug(title)
	if slug == "" {
		return truncate(prefix)
	}
	room := MaxNameLength - len(prefix) - 1
	if room <= 0 {
		return truncate(prefix)
	}
	if len(slug) > room {
		slug = strings.TrimRight(slug[:room], "-")
		// Avoid cutting the last word in half when there is enough left.
		if i := strings.LastIndexByte(slug, '-'); i > room/2 {
			slug = slug[:i]
		}
	}
	return prefix + "-" + slug
}

func truncate(s string) string {
	if len(s) <= MaxNameLength {
		return s
	}
	sum := sha256.Sum256([]byte(s))
	h := hex.EncodeToString(sum[:])[:8]
	return strings.TrimRight(s[:MaxNameLength-9], "-") + "-" + h
}

// SpecTask names the spec task (and pod) of a milestone.
func SpecTask(project string, milestoneIID int64, milestoneTitle string) string {
	return join(fmt.Sprintf("%s-spec-m%d", Slug(project), milestoneIID), milestoneTitle)
}

// CoordinateTask names the pilot task (and pod) of a milestone.
func CoordinateTask(project string, milestoneIID int64, milestoneTitle string) string {
	return join(fmt.Sprintf("%s-pilot-m%d", Slug(project), milestoneIID), milestoneTitle)
}

// ImplementTask names the implementation task (and pod) of an issue.
func ImplementTask(project string, issueIID int64, issueTitle string) string {
	return join(fmt.Sprintf("%s-%d", Slug(project), issueIID), issueTitle)
}

// Workspace names the workspace PVC of a task.
func Workspace(task string) string {
	return truncate(task + "-ws")
}

// IsIssueBranch reports whether branch is a work branch of the issue, as
// Branch names them.
func IsIssueBranch(branch string, issueIID int64) bool {
	p := fmt.Sprintf("agent/%d", issueIID)
	return branch == p || strings.HasPrefix(branch, p+"-")
}

// Branch names the git branch of an issue, e.g. "agent/42-add-paypal-button".
func Branch(issueIID int64, issueTitle string) string {
	slug := Slug(issueTitle)
	if len(slug) > 40 {
		slug = strings.TrimRight(slug[:40], "-")
	}
	if slug == "" {
		return fmt.Sprintf("agent/%d", issueIID)
	}
	return fmt.Sprintf("agent/%d-%s", issueIID, slug)
}

// ResearchPod names the pod of a research run, e.g. "morning-news-1a2b3c4d":
// the research's name, cut to fit, and the first 8 characters of the run ID.
func ResearchPod(research, runID string) string {
	id := runID[:min(len(runID), 8)]
	name := truncate(research)
	if room := MaxNameLength - len(id) - 1; len(name) > room {
		name = strings.TrimRight(name[:room], "-.")
	}
	return name + "-" + id
}

// LabelValue fits a name into a label value (at most 63 characters).
func LabelValue(name string) string {
	return truncate(name)
}

// ProjectSlug names a GitLab project's directories in the runner (its
// mirror "/runner/mirrors/<slug>.git", its checkout
// "/workspace/repos/<slug>"): the full path lowercased, "/" as "-", other
// characters outside [a-z0-9._-] dropped, e.g. "example-infra".
// Characters a slug may not start with are trimmed, so a slug is either
// valid (ValidProjectSlug) or "".
func ProjectSlug(path string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.Trim(path, "/")) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		case r == '/':
			b.WriteByte('-')
		}
	}
	s := strings.TrimLeft(b.String(), "._-")
	if len(s) > maxProjectSlug {
		s = s[:maxProjectSlug]
	}
	return s
}

// maxProjectSlug bounds a slug, well below a file name's 255 bytes.
const maxProjectSlug = 200

// ValidProjectSlug reports whether s can be a ProjectSlug: 1 to 200
// characters of [a-z0-9._-], starting with a letter or digit. It is safe as
// one path segment: never "", "." or "..", and without "/".
func ValidProjectSlug(s string) bool {
	if s == "" || len(s) > maxProjectSlug || !(s[0] >= 'a' && s[0] <= 'z' || s[0] >= '0' && s[0] <= '9') {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// CompanionBranch names the branch of an issue's change in its companion
// repository: "agent/<home-slug>-<iid>-<title slug>", where home-slug is the
// last segment of the home project's path, e.g.
// "agent/ai-operator-42-add-paypal-button".
func CompanionBranch(homeProject string, issueIID int64, issueTitle string) string {
	home := strings.Trim(homeProject, "/")
	if i := strings.LastIndexByte(home, '/'); i >= 0 {
		home = home[i+1:]
	}
	prefix := fmt.Sprintf("agent/%d", issueIID)
	if h := Slug(home); h != "" {
		prefix = fmt.Sprintf("agent/%s-%d", h, issueIID)
	}
	slug := Slug(issueTitle)
	if len(slug) > 40 {
		slug = strings.TrimRight(slug[:40], "-")
	}
	if slug == "" {
		return prefix
	}
	return prefix + "-" + slug
}
