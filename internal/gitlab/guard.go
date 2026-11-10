package gitlab

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// The merge/push guard (docs/specs/cross-repo-changes.spec.md, decision 11).
// The bot's token can reach every project of its group, so the operator and
// the runner refuse, by themselves, a merge outside the home project or a
// companion target with autoMerge, merge request and pipeline writes outside
// the home project and its targets, and the deletion of anything but an
// agent/ branch. Reads, issue and note writes stay unguarded: links and
// comments in other projects are wanted.

// AgentBranchPrefix is the prefix of every branch the operator and the
// runner may push or delete.
const AgentBranchPrefix = "agent/"

// Policy is what the guard allows: the home project and the valid companion
// targets, by normalized path (aiv1.NormalizeProject). Its fields are
// unexported on purpose: NewPolicy, which validates the targets, is the only
// way to build one with targets.
type Policy struct {
	home    string
	targets map[string]TargetPolicy
}

// TargetPolicy is what the guard allows in a companion target.
type TargetPolicy struct {
	// AutoMerge: the operator may merge the target's merge requests.
	AutoMerge bool
}

// NewPolicy builds the policy of an AgentProject: its home project and the
// targets aiv1.ValidateCrossRepo accepts with the cluster's allow-list, so a
// project the cluster does not allow, the home project or one of
// aiv1.NeverTargets never becomes a target.
func NewPolicy(spec aiv1.AgentProjectSpec, allowed []string) Policy {
	p := Policy{home: aiv1.NormalizeProject(spec.GitLab.Project), targets: map[string]TargetPolicy{}}
	valid, _ := aiv1.ValidateCrossRepo(spec, allowed)
	for _, t := range valid {
		p.targets[aiv1.NormalizeProject(t.Project)] = TargetPolicy{AutoMerge: t.AutoMerge}
	}
	return p
}

// ForCompanion narrows p to a task's companion: it stays the only target,
// and merges in it are allowed only when both the task's resolved autoMerge
// and the target's allow it. Without a companion, or one that is no target
// of p, no target is left.
func (p Policy) ForCompanion(c *aiv1.CompanionSpec) Policy {
	out := Policy{home: p.home, targets: map[string]TargetPolicy{}}
	if c == nil {
		return out
	}
	project := aiv1.NormalizeProject(c.Project)
	if t, ok := p.targets[project]; ok {
		out.targets[project] = TargetPolicy{AutoMerge: t.AutoMerge && c.AutoMerge}
	}
	return out
}

// Home is the normalized home project.
func (p Policy) Home() string { return p.home }

// Target returns the policy of a companion target.
func (p Policy) Target(project string) (TargetPolicy, bool) {
	t, ok := p.targets[aiv1.NormalizeProject(project)]
	return t, ok
}

// Targets returns the normalized target projects, for logs.
func (p Policy) Targets() []string {
	out := make([]string, 0, len(p.targets))
	for t := range p.targets {
		out = append(out, t)
	}
	return out
}

// known reports whether project is the home project or a target.
func (p Policy) known(project string) bool {
	n := aiv1.NormalizeProject(project)
	if n == "" {
		return false
	}
	_, ok := p.targets[n]
	return n == p.home || ok
}

// GuardError is a call or push the guard refused. It is not transient
// (IsTransient): trying again gets the same answer.
type GuardError struct {
	// Project is the project the call or push was for.
	Project string
	// Op names the refused operation, e.g. "merge !12" or "push agent/x".
	Op string
	// Reason says why it was refused.
	Reason string
}

func (e *GuardError) Error() string {
	return fmt.Sprintf("refused by the merge/push guard: %s in %s: %s", e.Op, e.Project, e.Reason)
}

// Refuse logs a refusal at info level and returns it as a *GuardError.
func Refuse(log logr.Logger, project, op, reason string) error {
	log.Info("guard refused", "project", project, "op", op, "reason", reason)
	return &GuardError{Project: project, Op: op, Reason: reason}
}

// Guard returns api with the writes the policy does not allow refused (a
// *GuardError, logged); everything else passes through unchanged.
func Guard(api API, p Policy, log logr.Logger) API {
	g := &guarded{API: api, p: p, log: log}
	if s, ok := api.(interface{ Stats() Stats }); ok {
		return &guardedStats{guarded: g, stats: s}
	}
	return g
}

type guarded struct {
	API
	p   Policy
	log logr.Logger
}

// guardedStats keeps the inner client's request counts visible.
type guardedStats struct {
	*guarded
	stats interface{ Stats() Stats }
}

func (g *guardedStats) Stats() Stats { return g.stats.Stats() }

// mrWrite refuses a merge request or pipeline write outside the home
// project and its targets.
func (g *guarded) mrWrite(project, op string) error {
	if g.p.known(project) {
		return nil
	}
	return Refuse(g.log, project, op, "neither the home project nor a companion target")
}

func (g *guarded) MergeMergeRequest(ctx context.Context, project string, iid int64, opts MergeOptions) (*MergeRequest, error) {
	op := fmt.Sprintf("merge !%d", iid)
	n := aiv1.NormalizeProject(project)
	switch t, ok := g.p.targets[n]; {
	case n != "" && n == g.p.home:
	case ok && t.AutoMerge:
	case ok:
		return nil, Refuse(g.log, project, op, "a companion target without autoMerge")
	default:
		return nil, Refuse(g.log, project, op, "neither the home project nor a companion target")
	}
	return g.API.MergeMergeRequest(ctx, project, iid, opts)
}

func (g *guarded) CreateMergeRequest(ctx context.Context, project string, opts CreateMergeRequestOptions) (*MergeRequest, error) {
	if err := g.mrWrite(project, "create merge request from "+opts.SourceBranch); err != nil {
		return nil, err
	}
	return g.API.CreateMergeRequest(ctx, project, opts)
}

func (g *guarded) UpdateMergeRequest(ctx context.Context, project string, iid int64, opts UpdateMergeRequestOptions) (*MergeRequest, error) {
	if err := g.mrWrite(project, fmt.Sprintf("update !%d", iid)); err != nil {
		return nil, err
	}
	return g.API.UpdateMergeRequest(ctx, project, iid, opts)
}

func (g *guarded) RebaseMergeRequest(ctx context.Context, project string, iid int64) error {
	if err := g.mrWrite(project, fmt.Sprintf("rebase !%d", iid)); err != nil {
		return err
	}
	return g.API.RebaseMergeRequest(ctx, project, iid)
}

func (g *guarded) CreatePipeline(ctx context.Context, project, ref string, vars ...PipelineVariable) (*Pipeline, error) {
	if err := g.mrWrite(project, "create pipeline for "+ref); err != nil {
		return nil, err
	}
	return g.API.CreatePipeline(ctx, project, ref, vars...)
}

func (g *guarded) CreateMergeRequestPipeline(ctx context.Context, project string, iid int64) error {
	if err := g.mrWrite(project, fmt.Sprintf("create pipeline for !%d", iid)); err != nil {
		return err
	}
	return g.API.CreateMergeRequestPipeline(ctx, project, iid)
}

// DeleteBranch deletes only agent/ branches of the home project and its
// targets, and never the project's default branch (read from GitLab; when
// that read fails, nothing is deleted).
func (g *guarded) DeleteBranch(ctx context.Context, project, branch string) error {
	op := "delete branch " + branch
	if err := g.mrWrite(project, op); err != nil {
		return err
	}
	if !IsAgentBranch(branch) {
		return Refuse(g.log, project, op, "not an "+AgentBranchPrefix+" branch")
	}
	proj, err := g.API.GetProject(ctx, project)
	if err != nil {
		return fmt.Errorf("reading the default branch before deleting %s: %w", branch, err)
	}
	if proj.DefaultBranch == "" || proj.DefaultBranch == branch {
		return Refuse(g.log, project, op, "the project's default branch")
	}
	return g.API.DeleteBranch(ctx, project, branch)
}

// IsAgentBranch reports whether branch is a well-formed agent/ branch: the
// prefix, a non-empty rest and nothing that would make it a pattern, a
// refspec or a path out of refs/heads/.
func IsAgentBranch(branch string) bool {
	rest, ok := strings.CutPrefix(branch, AgentBranchPrefix)
	if !ok || rest == "" || strings.HasSuffix(rest, "/") || strings.HasSuffix(rest, ".lock") {
		return false
	}
	if strings.ContainsAny(branch, " :*?[\\^~\t\n") || strings.Contains(branch, "..") ||
		strings.Contains(branch, "//") || strings.Contains(branch, "@{") {
		return false
	}
	for _, seg := range strings.Split(rest, "/") {
		if seg == "" || strings.HasPrefix(seg, ".") {
			return false
		}
	}
	return true
}
