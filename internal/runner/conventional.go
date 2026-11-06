package runner

import (
	"encoding/json"
	"fmt"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/conventional"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// commitTypes are the Conventional Commits types of the project's commits,
// nil while workflow.conventionalCommits is off: nil keeps every message,
// schema and prompt as it is without the option.
func commitTypes(wf aiv1.WorkflowSpec) []string {
	if wf.ConventionalCommits == nil || !*wf.ConventionalCommits {
		return nil
	}
	if len(wf.CommitTypes) == 0 {
		return aiv1.WorkflowSpec{}.WithDefaults().CommitTypes
	}
	return wf.CommitTypes
}

// plainCommitMessage is the commitMessage property of the implementer's
// schemas without Conventional Commits.
const plainCommitMessage = `"commitMessage": {"type": "string", "description": "Imperative subject line (max 72 chars), blank line, body."}`

// implementSchemaFor is implementSchema; with types its commitMessage must
// follow Conventional Commits.
func implementSchemaFor(types []string) string {
	return withCommitPattern(implementSchema, types)
}

// implementStepSchemaFor is implementStepSchema; with types its
// commitMessage must follow Conventional Commits.
func implementStepSchemaFor(types []string) string {
	return withCommitPattern(implementStepSchema, types)
}

func withCommitPattern(schema string, types []string) string {
	if types == nil {
		return schema
	}
	pattern, _ := json.Marshal(conventional.Pattern(types))
	desc, _ := json.Marshal("Conventional Commits: type(scope): subject (imperative, lowercase, max 72 chars), blank line, body. Types: " +
		strings.Join(types, ", ") + ".")
	return strings.Replace(schema, plainCommitMessage,
		fmt.Sprintf(`"commitMessage": {"type": "string", "pattern": %s, "description": %s}`, pattern, desc), 1)
}

// typeHints say when to pick a commit type.
var typeHints = map[string]string{
	"feat":     "new behaviour",
	"fix":      "a bug fix",
	"refactor": "a change that keeps the behaviour",
	"perf":     "faster or leaner",
	"test":     "tests only",
	"docs":     "docs only",
	"build":    "build, dependencies",
	"ci":       "the CI pipeline",
	"chore":    "anything else",
	"style":    "formatting only",
	"revert":   "undoing a commit",
}

// conventionalNote is added to the implementer's system prompt while
// workflow.conventionalCommits is on.
func conventionalNote(types []string) string {
	hints := make([]string, len(types))
	for i, t := range types {
		hints[i] = t
		if h := typeHints[t]; h != "" {
			hints[i] += " (" + h + ")"
		}
	}
	return `

Every commitMessage follows Conventional Commits: "type(scope): subject", a
blank line, then the body. Pick the type that fits the change best: ` + strings.Join(hints, ", ") + `. The scope is the package, directory or
component the change touches, in lowercase. The subject is imperative and
lowercase, at most 72 characters, e.g. "fix(runner): keep the lock file".`
}

// conventionalPointer ends the step note and the fix, CI and conflict
// prompts while workflow.conventionalCommits is on ("" while it is off).
func conventionalPointer(types []string) string {
	if types == nil {
		return ""
	}
	return "\nWrite the commitMessage in the Conventional Commits format of your instructions (`type(scope): subject`)."
}

// agentMessage is the commit message of the agent's work. rewritten says
// the agent's message did not follow Conventional Commits and fell back to
// chore(agent): …; original is what it was.
type agentMessage struct {
	text, original string
	rewritten      bool
}

// commitMessage is the message of a commit of the agent's work: msg, or the
// issue title when msg is empty, with "Refs #N". With types, a message that
// does not follow Conventional Commits becomes conventional.Fallback's.
func commitMessage(msg string, issue *gitlab.Issue, types []string) agentMessage {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		msg = issue.Title
	}
	m := agentMessage{original: msg}
	if types != nil && !conventional.Valid(msg, types) {
		msg, m.rewritten = conventional.Fallback(msg), true
	}
	m.text = fmt.Sprintf("%s\n\nRefs #%d", msg, issue.IID)
	return m
}

// committed logs and counts a rewritten message once its commit landed.
func (r *Runner) committed(m agentMessage) {
	if !m.rewritten {
		return
	}
	first, _, _ := strings.Cut(m.original, "\n")
	subject, _, _ := strings.Cut(m.text, "\n")
	r.log().Info("commit message did not follow Conventional Commits, rewritten", "message", first, "as", subject)
	r.run.noteRewrite()
}

// startMessage is the message of the empty commit a new branch starts with.
// One line, no "[skip ci]": merge commit templates may copy it to the
// default branch. The push itself skips CI.
func startMessage(iid int64, types []string) string {
	if types != nil {
		return fmt.Sprintf("chore: start work on #%d", iid)
	}
	return fmt.Sprintf("Start work on #%d", iid)
}

// mergeMessage is the message of the merge of base into branch in a
// conflict run. A conventional subject that would be too long leaves out
// the branch.
func mergeMessage(base, branch string, types []string) string {
	if types != nil {
		subject := fmt.Sprintf("merge %s into %s", base, branch)
		if len(subject) > conventional.MaxSubject {
			subject = fmt.Sprintf("merge %s into the branch", base)
		}
		return "chore: " + subject
	}
	return fmt.Sprintf("Merge branch '%s' into %s", base, branch)
}

func (rl *runLog) noteRewrite() {
	if rl == nil {
		return
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.rewritten++
}

// rewriteNotice is the report's notice about rewritten commit messages, ""
// when there were none.
func (rl *runLog) rewriteNotice() string {
	if rl == nil {
		return ""
	}
	rl.mu.Lock()
	n := rl.rewritten
	rl.mu.Unlock()
	switch n {
	case 0:
		return ""
	case 1:
		return "1 commit message did not follow Conventional Commits and was rewritten as `chore(agent): …`."
	}
	return fmt.Sprintf("%d commit messages did not follow Conventional Commits and were rewritten as `chore(agent): …`.", n)
}
