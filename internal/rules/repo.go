package rules

import (
	"net/url"
	"regexp"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// The repo lines "<prefix> repo <project> [merge]" of a milestone name the
// companion repositories its issues may change (docs/specs/
// cross-repo-changes.spec.md). Unlike the owners, rules and budget lines
// they stay in the spec: changing one re-plans the milestone.

// RepoLine is one "<prefix> repo <path-or-url> [merge]" line.
type RepoLine struct {
	// Project is the companion's full path, normalized (aiv1.NormalizeProject);
	// "" for a malformed line.
	Project string
	// Merge: the line ends in "merge", autopilot may merge the companion's
	// merge request (if the milestone is on autopilot and the target allows
	// it).
	Merge bool
	// Raw is the line as written, trimmed.
	Raw string
	// Problem says why the line is malformed or repeated; "" for a line
	// that names a project. Whether the project is a valid target is the
	// operator's check.
	Problem string
}

func repoLine(prefix string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(prefix) + `[ \t]+repo(?:[ \t]+(.*?))?[ \t]*\r?(?:\n|\z)`)
}

// Repos returns the repo lines of a milestone description, in order. A
// line for a project an earlier line names already has a Problem.
func Repos(description, prefix string) []RepoLine {
	var out []RepoLine
	for _, m := range repoLine(prefix).FindAllStringSubmatch(description, -1) {
		l := RepoLine{Raw: strings.TrimSpace(m[0])}
		fields := strings.Fields(m[1])
		switch {
		case len(fields) == 0 || len(fields) > 2:
			l.Problem = "write `" + prefix + " repo <project path or URL> [merge]`"
		case len(fields) == 2 && !strings.EqualFold(fields[1], "merge"):
			l.Problem = "only `merge` may follow the project: write `" + prefix + " repo <project path or URL> [merge]`"
		default:
			p, ok := ProjectPath(fields[0])
			if !ok {
				l.Problem = "`" + fields[0] + "` is no project path (`group/project`) or GitLab URL"
				break
			}
			l.Project, l.Merge = p, len(fields) == 2
			for _, o := range out {
				if o.Project == p {
					l.Problem = "repeats the line for `" + p + "`"
				}
			}
		}
		if l.Problem != "" {
			l.Project, l.Merge = "", false
		}
		out = append(out, l)
	}
	return out
}

var projectPathRE = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_.-]*(/[A-Za-z0-9_.][A-Za-z0-9_.-]*)+$`)

// ProjectPath reads a project's full path, "group/sub/project", or a GitLab
// URL of it ("https://gitlab.com/group/sub/project", also with "/-/…" or
// ".git"), and returns the path normalized (aiv1.NormalizeProject).
func ProjectPath(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return "", false
		}
		s = u.Path
		if i := strings.Index(s+"/", "/-/"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimSuffix(strings.TrimRight(s, "/"), ".git")
	}
	s = strings.Trim(s, "/")
	if !projectPathRE.MatchString(s) {
		return "", false
	}
	for _, seg := range strings.Split(s, "/") {
		if seg == "." || seg == ".." {
			return "", false
		}
	}
	return aiv1.NormalizeProject(s), true
}
