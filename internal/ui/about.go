package ui

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// About is the running release and the self project's newest release.
type About struct {
	Version string `json:"version"`
	// ReleasesURL is the self project's GitLab releases page, empty without
	// a self project.
	ReleasesURL string        `json:"releasesURL"`
	Release     *AboutRelease `json:"release" tstype:"AboutRelease | null,required"`
}

// AboutRelease is the self project's newest release that is not dropped.
type AboutRelease struct {
	Version string            `json:"version"`
	State   aiv1.ReleaseState `json:"state"`
	// URL is the GitLab release, empty until released.
	URL string `json:"url"`
}

// about answers any logged-in user: the version is not project data.
func (s *Server) about(w http.ResponseWriter, r *http.Request) {
	out := About{Version: s.version}
	if s.selfProject == "" {
		writeJSON(w, http.StatusOK, out)
		return
	}
	var projects aiv1.AgentProjectList
	if err := s.reader.List(r.Context(), &projects); err != nil {
		s.internalError(w, err)
		return
	}
	for i := range projects.Items {
		ap := &projects.Items[i]
		if aiv1.NormalizeProject(ap.Spec.GitLab.Project) != s.selfProject {
			continue
		}
		out.ReleasesURL = gitLabRef(ap).WebURL + "/-/releases"
		var tasks aiv1.AgentTaskList
		if err := s.reader.List(r.Context(), &tasks, client.InNamespace(ap.Namespace)); err != nil {
			s.internalError(w, err)
			return
		}
		recs := slices.Clone(ap.Status.Releases)
		for _, t := range tasks.Items {
			if t.Spec.ProjectRef == ap.Name && t.Status.Autopilot != nil {
				recs = append(recs, t.Status.Autopilot.Releases...)
			}
		}
		var best *aiv1.ReleaseRecord
		for j := range recs {
			rec := &recs[j]
			if rec.State == aiv1.ReleaseDropped || rec.Version == "" {
				continue
			}
			if best == nil {
				best = rec
			} else if c := compareCalver(rec.Version, best.Version); c > 0 || c == 0 && rec.DecidedAt.After(best.DecidedAt.Time) {
				best = rec
			}
		}
		if best != nil {
			out.Release = &AboutRelease{Version: best.Version, State: best.State, URL: best.ReleaseURL}
		}
		break
	}
	writeJSON(w, http.StatusOK, out)
}

// compareCalver compares versions YEAR.MONTH.N numerically per segment; a
// segment that is not a number counts as 0, a missing one sorts first.
func compareCalver(a, b string) int {
	parts := func(v string) (out []int) {
		for _, s := range strings.Split(v, ".") {
			n, _ := strconv.Atoi(s)
			out = append(out, n)
		}
		return out
	}
	return slices.Compare(parts(a), parts(b))
}
