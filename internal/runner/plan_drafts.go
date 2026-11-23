package runner

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

const (
	// maxPlanDrafts mirrors the CRD's MaxItems of status.drafts.
	maxPlanDrafts    = 5
	planDraftKept    = "(Your saved edit of draft %d was kept; this turn's change to it was not applied.)"
	planDraftLarge   = "(This turn's %s is over 64 KiB and was not applied.)"
	planDraftSixth   = "(A sixth draft was not added: a plan holds at most 5.)"
	planDraftUnknown = "(The plan has no draft %d; your change to it was not applied.)"
	planDraftCreated = "(Draft %d is created as %%%d and read-only; your change was not applied.)"
	planDraftTwice   = "(Draft %d came twice in this turn; only its first change was applied.)"
)

// planTurnDraft is one draft of the agent's answer: key 0 adds a new one.
type planTurnDraft struct {
	Key   int64  `json:"key"`
	Title string `json:"title"`
	Draft string `json:"draft"`
}

// applyPlanDrafts applies the drafts the agent changed to s, each on its
// own, after moving a plan's single draft from before status.drafts to key
// 1. Key 0 adds a draft, up to maxPlanDrafts; a known key changes its draft
// unless that one is created or the owner saved a revision other than base,
// the revisions the turn started from. Empty or unchanged text is no change.
// notes are the reply's lines for the changes not applied. refused holds
// every draft changed, by key (0 for a draft not added): true when the change
// was not applied.
func applyPlanDrafts(s *aiv1.AgentPlanStatus, base map[int64]int64, in []planTurnDraft, sha string) (notes []string, refused map[int64]bool) {
	s.MoveDrafts()
	refused = map[int64]bool{}
	refuse := func(key int64, note string) { notes, refused[key] = append(notes, note), true }
	for _, t := range in {
		text, i := strings.TrimSpace(t.Draft), s.DraftIndex(t.Key)
		_, done := refused[t.Key]
		switch {
		case text == "" || i >= 0 && text == s.Drafts[i].Draft:
			continue
		case done && t.Key != 0:
			notes = append(notes, fmt.Sprintf(planDraftTwice, t.Key))
			continue
		case t.Key == 0 && len(s.Drafts) >= maxPlanDrafts:
			refuse(0, planDraftSixth)
			continue
		case t.Key == 0 && len(text) > maxPlanDraft:
			refuse(0, fmt.Sprintf(planDraftLarge, "new draft"))
			continue
		case t.Key == 0:
			s.Drafts = append(s.Drafts, aiv1.PlanDraft{Key: s.NextDraftKey()})
			i = len(s.Drafts) - 1
		case i < 0:
			refuse(t.Key, fmt.Sprintf(planDraftUnknown, t.Key))
			continue
		case s.Drafts[i].CreatedMilestoneIID != 0:
			refuse(t.Key, fmt.Sprintf(planDraftCreated, t.Key, s.Drafts[i].CreatedMilestoneIID))
			continue
		case s.Drafts[i].Revision != base[t.Key]:
			refuse(t.Key, fmt.Sprintf(planDraftKept, t.Key))
			continue
		case len(text) > maxPlanDraft:
			refuse(t.Key, fmt.Sprintf(planDraftLarge, fmt.Sprintf("draft %d", t.Key)))
			continue
		}
		d := &s.Drafts[i]
		d.Draft, d.Revision, d.By, d.SHA, d.Title = text, d.Revision+1, "agent", sha, cmp.Or(planTitleFrom(t.Title), d.Title)
		refused[d.Key] = false
	}
	return notes, refused
}

// allPlanDraftsRefused reports whether the agent changed drafts and none of
// the changes was applied: the decisions list follows the drafts then and is
// not applied either.
func allPlanDraftsRefused(refused map[int64]bool) bool {
	for _, r := range refused {
		if !r {
			return false
		}
	}
	return len(refused) > 0
}

// planDraftPath is draft key's file, relative to the checkout.
func planDraftPath(key int64) string { return fmt.Sprintf("%s/drafts/%d.md", planContextDir, key) }

// planDraftState says whether a draft is created: "created as %41
// (read-only)" or "not created".
func planDraftState(iid int64) string {
	if iid != 0 {
		return fmt.Sprintf("created as %%%d (read-only)", iid)
	}
	return "not created"
}

// writePlanDrafts writes each draft to <root>/drafts/<key>.md: a first line
// naming its key, title and whether it is created, then its text.
func writePlanDrafts(root string, drafts []aiv1.PlanDraft) error {
	if err := os.MkdirAll(filepath.Join(root, "drafts"), 0o755); err != nil { // #nosec G301 -- draft text for the agent in the runner's exchange dir; the pod runs every container as uid/gid 1000
		return err
	}
	var errs []error
	for _, d := range drafts {
		body := fmt.Sprintf("> Draft %d %q: %s. Its text starts below this line.\n\n%s\n", d.Key, d.Title, planDraftState(d.CreatedMilestoneIID), d.Draft)
		errs = append(errs, os.WriteFile(filepath.Join(root, "drafts", fmt.Sprintf("%d.md", d.Key)), []byte(body), 0o644)) // #nosec G306 -- draft text the agent reads, no secret; the pod runs every container as uid/gid 1000
	}
	return errors.Join(errs...)
}

// planDraftLine is one draft in a plan turn's prompt.
type planDraftLine struct {
	Key        int64
	Title      string
	CreatedIID int64 // its milestone, 0 while not created
	Edited     bool  // the owner edited it since the agent saw it
}

// renderPlanDrafts renders the prompt's ## Drafts section: one line per
// draft in list order, then the drafts the owner edited; none without drafts.
func renderPlanDrafts(drafts []planDraftLine) string {
	var b strings.Builder
	if len(drafts) == 0 {
		return "" // the system prompt says key 0 adds one
	}
	b.WriteString("## Drafts\n\nThe plan's drafts, in the order their milestones are created:\n\n")
	var edited []string
	for _, d := range drafts {
		title := cmp.Or(strings.TrimSpace(d.Title), "untitled")
		fmt.Fprintf(&b, "- draft %d %q: %s, %s\n", d.Key, title, planDraftState(d.CreatedIID), planDraftPath(d.Key))
		if d.Edited {
			edited = append(edited, fmt.Sprintf("- draft %d: %s", d.Key, planDraftPath(d.Key)))
		}
	}
	if len(edited) > 0 {
		fmt.Fprintf(&b, "\nThe owner edited these drafts since you last saw them. Read their files before you answer, build on their edits and keep their changes:\n\n%s\n",
			strings.Join(edited, "\n"))
	}
	return b.String() + "\n"
}

// planDraftLines are the prompt's lines of drafts, with Edited for each draft
// the owner saved after seen, the revisions sent by key; revs are the
// revisions this turn sends.
func planDraftLines(drafts []aiv1.PlanDraft, seen map[int64]int64) (lines []planDraftLine, revs map[int64]int64) {
	revs = map[int64]int64{}
	for _, d := range drafts {
		lines = append(lines, planDraftLine{Key: d.Key, Title: d.Title, CreatedIID: d.CreatedMilestoneIID,
			Edited: d.By == "owner" && d.Revision > seen[d.Key]})
		revs[d.Key] = d.Revision
	}
	return lines, revs
}

// planSeen is the newest revision of each draft a plan turn sent, by key. A
// state from before several drafts has draft 1's alone, so a pod on the new
// image does not send it again.
func (st *state) planSeen() map[int64]int64 {
	if len(st.PlanSeen) == 0 && st.PlanSeenRevision != 0 {
		return map[int64]int64{1: st.PlanSeenRevision}
	}
	return st.PlanSeen
}
