// Package e2e runs the operator's rules and the runner together, through
// their exported APIs only, against a fake Kubernetes API, a fake GitLab
// and local bare git repositories. Nothing sleeps: the tests advance a fake
// clock and step every reconcile and every run themselves.
package e2e

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
	"github.com/jonathanwiemers/ai-operator/internal/ui"
)

// The branches of issue #2's run: the home branch and the companion's
// agent/<home-slug>-<iid>-<slug>.
const (
	homeBranch      = "agent/2-turn-on-the-companion-feature"
	companionBranch = "agent/ai-operator-2-turn-on-the-companion-feature"
)

// TestCompanionMilestoneMerge is the milestone's first acceptance bullet:
// an autopilot milestone with "/agent repo example/infra merge"
// and a target with autoMerge. One issue's run pushes both trees and opens
// two linked merge requests; both go green; autopilot merges the home one;
// once the operator runs the release that contains it, the companion is
// rebased, goes green, is marked ready and merged, its default branch goes
// green, and the milestone is done and closes. No human step.
func TestCompanionMilestoneMerge(t *testing.T) {
	e := newEnv(t, scenario{merge: true})
	impl := e.planAndImplement()
	run := impl.Status.Run
	e.greenAndHomeMerged(impl)

	// The companion waits for a release that contains the home merge.
	merge := e.home.MRs[1].MergeCommitSHA
	if c := e.companion(); c.State != aiv1.LandingWaitingRelease || c.HomeMergeSHA != merge || !c.AutoMerge ||
		!strings.Contains(c.Reason, "no release contains the merge") {
		t.Fatalf("the companion waits for the release: %+v", c)
	}
	// Meanwhile the companion's default branch moved on.
	e.infra.MRs[1].DetailedMergeStatus = "need_rebase"
	e.reconcile()
	// The milestone's final release: the operator tags the merge 2026.11.5
	// through the API (main has no pipelines, the merge is its head).
	if rs := e.home.Releases; len(rs) != 1 || rs[0].Tag != "2026.11.5" || rs[0].Ref != merge {
		t.Fatalf("the operator creates the release 2026.11.5 at the merge: %+v", rs)
	}
	if c := e.companion(); c.State != aiv1.LandingWaitingRelease || !strings.Contains(c.Reason, "the operator runs 2026.11.4") ||
		!strings.Contains(c.Reason, "is first in 2026.11.5") {
		t.Fatalf("2026.11.4 does not run the merge yet: %+v", c)
	}
	e.reconcile()
	if len(e.infra.Rebased) != 0 || len(e.infra.Merged) != 0 {
		t.Fatalf("nothing happens to the companion before the release runs: rebased %v, merged %v", e.infra.Rebased, e.infra.Merged)
	}

	// The tag pipeline builds it, and the operator runs the release: the
	// companion is rebased once.
	e.home.SetPipelineStatus(e.home.Releases[0].PipelineID, "success")
	e.r.Version = "2026.11.5"
	e.reconcile()
	if c := e.companion(); c.State != aiv1.LandingRebasing || c.Release != "2026.11.5" || !slices.Equal(e.infra.Rebased, []int64{1}) {
		t.Fatalf("live in 2026.11.5, the companion is rebased: %+v, rebased %v", c, e.infra.Rebased)
	}
	rebased := e.infra.MRs[1].SHA
	e.reconcile()
	if c := e.companion(); c.State != aiv1.LandingWaitingCI || c.HeadSHA != rebased {
		t.Fatalf("the rebased head, with the same change, waits for its pipeline: %+v (head %s)", c, rebased)
	}
	e.infra.AddPipeline(rebased, companionBranch, "success")
	e.reconcile()
	if c := e.companion(); c.State != aiv1.LandingMerging || c.ReadyAt == nil || e.infra.MRs[1].Draft {
		t.Fatalf("green and mergeable: autopilot marks it ready to merge it: %+v, draft %v", c, e.infra.MRs[1].Draft)
	}
	e.reconcile()
	if !slices.Equal(e.infra.Merged, []int64{1}) || e.infra.MergeOpts[1].SHA != rebased {
		t.Fatalf("autopilot merges the checked head %s: merged %v, %+v", rebased, e.infra.Merged, e.infra.MergeOpts[1])
	}
	if c := e.companion(); c.State != aiv1.LandingMerging || c.MergeSHA != e.infra.MRs[1].MergeCommitSHA || e.autopilot().Phase == aiv1.AutopilotDone {
		t.Fatalf("it follows the default branch's pipeline of the merge, the milestone is not done yet: %+v, %s", c, e.autopilot().Phase)
	}

	// The default branch's pipeline of the merge commit is green: landed.
	e.infra.AddPipeline(e.infra.MRs[1].MergeCommitSHA, "main", "success")
	e.reconcile()
	if c := e.companion(); c.State != aiv1.LandingMerged || c.EndedAt == nil {
		t.Fatalf("the companion has landed: %+v", c)
	}
	if ap := e.autopilot(); ap.Phase != aiv1.AutopilotDone {
		t.Fatalf("the milestone is done once its companion has landed: %+v", ap)
	}

	// The pilot writes the retrospective, which lists the companion, and
	// the milestone closes.
	pilot := e.task(aiv1.TaskTypeCoordinate, 0)
	e.run(pilot)
	if p := e.engine.lastPrompt("retrospective"); !strings.Contains(p, infraProject+"!1") {
		t.Fatalf("the retrospective lists the companion:\n%s", p)
	}
	e.pollUntil("the milestone closes", func() bool { return e.home.Milestones[0].State == "closed" })
	if tracking := e.home.Issues[1]; tracking.State != "closed" {
		t.Fatalf("the tracking issue closes with the milestone: %+v", tracking)
	}

	// The GitLab calls that move code, in order: two merge requests from
	// the one run, the home merge, the release, the one rebase and the
	// companion merge, each with the head the operator checked. Nothing
	// else, and nothing in another project.
	want := []string{
		homeProject + " create !1",
		infraProject + " create !1",
		homeProject + " merge !1@" + run.PushedSHA,
		homeProject + " release 2026.11.5@" + merge,
		infraProject + " rebase !1",
		infraProject + " merge !1@" + rebased,
	}
	if got := e.api.list(); !slices.Equal(got, want) {
		t.Fatalf("GitLab writes:\n got %q\nwant %q", got, want)
	}
	e.nothingElseWritten()
	if heads := e.heads(e.infraOrig); heads["main"] != e.infraMain {
		t.Fatalf("no git push reached the companion's default branch: %v", heads)
	}
}

// TestCompanionMilestoneOwnersMerge is the same milestone without "merge"
// on its line: once the release runs, the companion is rebased and green,
// is marked ready, and the owners get one comment and an Inbox item.
// Nothing merges it.
func TestCompanionMilestoneOwnersMerge(t *testing.T) {
	e := newEnv(t, scenario{merge: false})
	impl := e.planAndImplement()
	run := impl.Status.Run
	e.greenAndHomeMerged(impl)
	if c := e.companion(); c.AutoMerge {
		t.Fatalf("without the merge line the companion is not autopilot's: %+v", c)
	}

	merge := e.home.MRs[1].MergeCommitSHA
	e.infra.MRs[1].DetailedMergeStatus = "need_rebase"
	e.reconcile() // the operator creates the release 2026.11.5
	e.home.SetPipelineStatus(e.home.Releases[0].PipelineID, "success")
	e.r.Version = "2026.11.5"
	e.reconcile() // rebased
	rebased := e.infra.MRs[1].SHA
	e.reconcile() // waits for the pipeline of the rebased head
	e.infra.AddPipeline(rebased, companionBranch, "success")
	e.reconcile()
	c := e.companion()
	if c.State != aiv1.LandingReady || c.NotifiedAt == nil || c.Release != "2026.11.5" || e.infra.MRs[1].Draft {
		t.Fatalf("the companion is ready, marked ready and the owners are told: %+v, draft %v", c, e.infra.MRs[1].Draft)
	}
	// More polls: still one comment, and nothing merged.
	for range 3 {
		e.reconcile()
	}
	// Of all comments anywhere, exactly one mentions the owners: the one on
	// the companion asking them to merge it, naming the release.
	var mentions []string
	for _, f := range []*gitlabfake.Fake{e.home, e.infra} {
		for _, notes := range [](map[int64][]gitlab.Note){f.MRNotes, f.IssueNotes} {
			for _, ns := range notes {
				for _, n := range ns {
					if strings.Contains(n.Body, "@"+owner.Username) {
						mentions = append(mentions, n.Body)
					}
				}
			}
		}
	}
	if len(mentions) != 1 || !slices.ContainsFunc(e.infra.MRNotes[1], func(n gitlab.Note) bool { return n.Body == mentions[0] }) ||
		!strings.Contains(mentions[0], "please merge it") || !strings.Contains(mentions[0], "live in release 2026.11.5") {
		t.Fatalf("exactly one comment for the owners, on the companion, naming the release: %q", mentions)
	}
	if c := e.companion(); c.State != aiv1.LandingReady || c.RemindedAt != nil {
		t.Fatalf("it stays ready for the owners: %+v", c)
	}

	// The Inbox has the companion for the owners (no task: #2 closed with
	// its merge, and its task went).
	items := e.inbox()
	i := slices.IndexFunc(items, func(it ui.InboxItem) bool { return it.Kind == "companionReady" })
	if i < 0 {
		t.Fatalf("no companionReady item in the Inbox: %+v", items)
	}
	if it := items[i]; it.IssueIID != 2 || it.MilestoneIID != milestoneIID || it.Title != infraProject+"!1" ||
		it.URL != e.infra.MRs[1].WebURL || it.Reason != "ready to merge: release 2026.11.5 runs" || it.Task != "" {
		t.Fatalf("unexpected Inbox item %+v", it)
	}

	// No merge call on the companion; the milestone waits for it.
	want := []string{
		homeProject + " create !1",
		infraProject + " create !1",
		homeProject + " merge !1@" + run.PushedSHA,
		homeProject + " release 2026.11.5@" + merge,
		infraProject + " rebase !1",
	}
	if got := e.api.list(); !slices.Equal(got, want) {
		t.Fatalf("GitLab writes:\n got %q\nwant %q", got, want)
	}
	if len(e.infra.Merged) != 0 || e.infra.MRs[1].State != "opened" {
		t.Fatalf("nothing merges the companion: %v, %s", e.infra.Merged, e.infra.MRs[1].State)
	}
	if ap := e.autopilot(); ap.Phase == aiv1.AutopilotDone || e.home.Milestones[0].State == "closed" {
		t.Fatalf("the milestone is not done while its companion is open: %+v", ap)
	}
	e.nothingElseWritten()
}

// TestCompanionViolationPushesNothing: a companion change outside the
// target's paths fails the run before either tree is pushed, so nothing is
// opened in the companion project and nothing is merged.
func TestCompanionViolationPushesNothing(t *testing.T) {
	for _, tc := range []struct {
		name, path, reason string
	}{
		{"outside the paths", "clusters/my-cluster/applications/orbit/values.yaml", "outside the allowed paths"},
		{"CI configuration", ".gitlab-ci.yml", "CI configuration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, scenario{merge: true, edit: func(dir string) {
				writeFile(t, dir+"/"+valuesFile, "replicas: 1\ncompanion:\n  enabled: true\n")
				writeFile(t, dir+"/"+tc.path, "changed: true\n")
			}})
			homeBefore, infraBefore := e.heads(e.homeOrigin), e.heads(e.infraOrig)
			impl := e.planAndImplement()
			run := impl.Status.Run
			if run.LastResult != aiv1.RunResultFailed || run.PushedSHA != "" || run.Companion != nil {
				t.Fatalf("the run fails and pushes nothing: %+v", run)
			}
			// The home branch has only the start commit of the draft merge
			// request (pushed before the implementer ran), not the run's
			// work; the companion remote has nothing new.
			homeAfter := e.heads(e.homeOrigin)
			if homeAfter["main"] != homeBefore["main"] || len(homeAfter) != 2 {
				t.Fatalf("home remote: before %v, after %v", homeBefore, homeAfter)
			}
			if log := e.git(e.tmp, "--git-dir", e.homeOrigin, "log", "--format=%s", "main.."+homeBranch); !strings.HasPrefix(log, "Start work on #2") || strings.Contains(log, "\n") {
				t.Fatalf("the home branch must not get the run's commit:\n%s", log)
			}
			if files := e.git(e.tmp, "--git-dir", e.homeOrigin, "ls-tree", "-r", "--name-only", homeBranch); strings.Contains(files, homeFile) {
				t.Fatalf("the home change was pushed:\n%s", files)
			}
			if infraAfter := e.heads(e.infraOrig); !maps.Equal(infraAfter, infraBefore) {
				t.Fatalf("companion remote: before %v, after %v", infraBefore, infraAfter)
			}
			var failure string
			for _, n := range e.home.IssueNotes[2] {
				if strings.Contains(n.Body, "The agent run failed") {
					failure = n.Body
				}
			}
			if !strings.Contains(failure, tc.path) || !strings.Contains(failure, tc.reason) || !strings.Contains(failure, "Neither repository was pushed") {
				t.Fatalf("the failure names the path and the reason:\n%s", failure)
			}

			// The operator goes on polling: nothing is merged, and the
			// companion project has no merge request.
			for range 3 {
				e.reconcile()
			}
			if got, want := e.api.list(), []string{homeProject + " create !1"}; !slices.Equal(got, want) {
				t.Fatalf("GitLab writes:\n got %q\nwant %q", got, want)
			}
			if len(e.home.Merged) != 0 || len(e.infra.MRs) != 0 || len(e.project().Status.Companions) != 0 {
				t.Fatalf("nothing merged or opened in the companion: home %v, infra %d MRs, %+v", e.home.Merged, len(e.infra.MRs), e.project().Status.Companions)
			}
			e.nothingElseWritten()
		})
	}
}

// planAndImplement plans the milestone and runs the implement task of its
// one issue, #2 (#1 is the tracking issue). Unless the run failed, it
// checks the two pushes and the two linked merge requests. It returns the
// task after the run.
func (e *env) planAndImplement() *aiv1.AgentTask {
	t := e.t
	t.Helper()
	e.reconcile()
	st := e.specTask()
	if !st.Spec.Autopilot || len(st.Spec.Repos) != 1 || st.Spec.Repos[0].Project != infraProject || st.Spec.Repos[0].Merge != e.mergeLine ||
		st.Spec.Repos[0].Problem != "" {
		t.Fatalf("the milestone line names the target: %+v", st.Spec)
	}
	e.run(st)
	if p := e.engine.lastPrompt("spec"); !strings.Contains(p, infraProject) || !strings.Contains(p, valuesFile) {
		t.Fatalf("the planner sees the target and the tree of its paths:\n%s", p)
	}
	issue := e.home.Issues[2]
	if issue == nil || !slices.Contains(issue.Labels, "agent-repo::"+infraProject) {
		t.Fatalf("the plan writes #2 with the companion's label: %+v", issue)
	}
	e.reconcile() // the queue hands #2 over
	e.reconcile() // its task
	impl := e.task(aiv1.TaskTypeImplement, 2)
	if impl == nil || impl.Spec.Companion == nil {
		t.Fatalf("#2 gets an implement task with its companion: %+v", impl)
	}
	want := aiv1.CompanionSpec{Project: infraProject, Paths: []string{allowedPaths}, Branch: companionBranch, AutoMerge: e.mergeLine}
	if c := impl.Spec.Companion; c.Project != want.Project || !slices.Equal(c.Paths, want.Paths) || c.Branch != want.Branch || c.AutoMerge != want.AutoMerge {
		t.Fatalf("spec.companion = %+v, want %+v", *c, want)
	}
	e.run(impl)
	impl = e.task(aiv1.TaskTypeImplement, 2)
	run := impl.Status.Run
	if run.LastResult != aiv1.RunResultSucceeded {
		return impl
	}

	// Two pushes: each tree's branch is at the head the run recorded.
	c := run.Companion
	if c == nil || run.Branch != homeBranch || c.Branch != companionBranch || run.MergeRequestIID != 1 || c.MRIID != 1 {
		t.Fatalf("the run records both branches and merge requests: %+v, companion %+v", run, c)
	}
	if heads := e.heads(e.homeOrigin); heads[homeBranch] != run.PushedSHA || run.ReviewedSHA != run.PushedSHA {
		t.Fatalf("home branch %s pushed, reviewed %s, remote %v", run.PushedSHA, run.ReviewedSHA, heads)
	}
	if heads := e.heads(e.infraOrig); heads[companionBranch] != c.PushedSHA || c.ReviewedSHA != c.PushedSHA || heads["main"] != e.infraMain {
		t.Fatalf("companion branch %s pushed, reviewed %s, remote %v", c.PushedSHA, c.ReviewedSHA, heads)
	}
	if files := e.git(e.tmp, "--git-dir", e.infraOrig, "diff", "--name-only", "main", companionBranch); files != valuesFile {
		t.Fatalf("the companion branch changes only %s: %q", valuesFile, files)
	}
	if files := e.git(e.tmp, "--git-dir", e.homeOrigin, "diff", "--name-only", "main", homeBranch); files != homeFile {
		t.Fatalf("the home branch changes only %s: %q", homeFile, files)
	}
	// Two linked merge requests: the companion a draft with its label and
	// the "Lands after" line, the home one with the "Companion:" line.
	cmr, hmr := e.infra.MRs[1], e.home.MRs[1]
	if !cmr.Draft || !slices.Contains(cmr.Labels, "agent::companion") || cmr.SourceBranch != companionBranch ||
		!strings.Contains(cmr.Title, "(companion of "+homeProject+"#2)") || !strings.Contains(cmr.Description, "Lands after: "+homeProject+"!1") {
		t.Fatalf("unexpected companion merge request %+v", cmr)
	}
	if hmr.SourceBranch != homeBranch || !strings.Contains(hmr.Description, "Companion: "+infraProject+"!1") {
		t.Fatalf("the home merge request links its companion: %+v", hmr)
	}
	// The reviewers saw both trees.
	if p := e.engine.lastPrompt("bugbot"); !strings.Contains(p, homeFile) || !strings.Contains(p, valuesFile) {
		t.Fatalf("the reviewer's diff has both trees:\n%s", p)
	}
	return impl
}

// greenAndHomeMerged sets both pipelines of the run green and polls until
// autopilot merged the home merge request. The companion stays a draft,
// unmerged, and is recorded to land.
func (e *env) greenAndHomeMerged(impl *aiv1.AgentTask) {
	t := e.t
	t.Helper()
	run := impl.Status.Run
	e.home.AddPipeline(run.PushedSHA, run.Branch, "success")
	e.infra.AddPipeline(run.Companion.PushedSHA, run.Companion.Branch, "success")
	e.pollUntil("autopilot merges the home merge request", func() bool { return len(e.home.Merged) > 0 })
	if !slices.Equal(e.home.Merged, []int64{1}) || e.home.MergeOpts[1].SHA != run.PushedSHA {
		t.Fatalf("autopilot merges the home head %s: %v, %+v", run.PushedSHA, e.home.Merged, e.home.MergeOpts[1])
	}
	if cmr := e.infra.MRs[1]; len(e.infra.Merged) != 0 || !cmr.Draft || cmr.State != "opened" {
		t.Fatalf("the companion stays an open draft: merged %v, %+v", e.infra.Merged, cmr)
	}
	e.pollUntil("the companion is recorded", func() bool { return len(e.project().Status.Companions) == 1 })
}

// companion is the one entry of status.companions.
func (e *env) companion() aiv1.CompanionStatus {
	e.t.Helper()
	cs := e.project().Status.Companions
	if len(cs) != 1 {
		e.t.Fatalf("expected one companion entry, got %+v", cs)
	}
	c := cs[0]
	if c.Project != infraProject || c.MRIID != 1 || c.HomeIssueIID != 2 || c.HomeMRIID != 1 || c.MilestoneIID != milestoneIID {
		e.t.Fatalf("unexpected companion entry %+v", c)
	}
	return c
}

// nothingElseWritten checks that nothing reached the project the bot can
// reach but nothing may write, that the merges happened only at home and
// in the companion, and that the merge/push guard refused nothing (the
// rules never asked it to).
func (e *env) nothingElseWritten() {
	e.t.Helper()
	for _, c := range e.api.list() {
		if !strings.HasPrefix(c, homeProject+" ") && !strings.HasPrefix(c, infraProject+" ") {
			e.t.Fatalf("a write outside the home project and the companion: %s", c)
		}
	}
	if s := e.secrets; len(s.MRs) != 0 || len(s.Merged) != 0 || len(s.IssueNotes) != 0 || len(s.Rebased) != 0 || len(s.DeletedBranches) != 0 {
		e.t.Fatalf("%s was written: %+v", secretsProject, s)
	}
	if refused := e.logs.containing("guard refused"); len(refused) != 0 {
		e.t.Fatalf("the merge/push guard refused: %v", refused)
	}
}

// inbox is the Desk's Inbox, read through the UI server like the browser
// does (auth mode none, so the request comes from localhost).
func (e *env) inbox() []ui.InboxItem {
	e.t.Helper()
	cfg := &ui.Config{Auth: ui.AuthConfig{Mode: ui.AuthNone}}
	cfg.Default()
	if err := cfg.Validate("127.0.0.1:0"); err != nil {
		e.t.Fatal(err)
	}
	s, err := ui.NewServer(cfg, e.k8s, ui.NewHub(), fstest.MapFS{}, e.logs.logger().WithName("ui"))
	if err != nil {
		e.t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://localhost/api/v1/inbox", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		e.t.Fatalf("inbox: %d %s", rec.Code, rec.Body)
	}
	var body struct {
		Items []ui.InboxItem `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		e.t.Fatal(err)
	}
	return body.Items
}

// pollUntil polls until done, at most ten times.
func (e *env) pollUntil(what string, done func() bool) {
	e.t.Helper()
	for range 10 {
		e.reconcile()
		if done() {
			return
		}
	}
	e.t.Fatalf("after ten polls, still not: %s", what)
}
