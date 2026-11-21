package runner

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// planRefRe is a milestone reference: %38, not %draft-1 or 50%.
var planRefRe = regexp.MustCompile(`%([0-9]+)\b`)

// planRef is a milestone the owner names in a message: Path is its file in
// the checkout; Missing, the project has none or it could not be read.
type planRef struct {
	IID     int64
	Path    string
	Missing bool
}

// planRefs is the distinct IIDs of the message's %N references, in the
// order they appear.
func planRefs(msg string) []int64 {
	var iids []int64
	for _, m := range planRefRe.FindAllStringSubmatch(msg, -1) {
		if iid, err := strconv.ParseInt(m[1], 10, 64); err == nil && iid > 0 && !slices.Contains(iids, iid) {
			iids = append(iids, iid)
		}
	}
	return iids
}

// planRefList pairs the named IIDs with the milestone files written.
func planRefList(iids []int64, infos []planMilestoneInfo) []planRef {
	refs := make([]planRef, 0, len(iids))
	for _, iid := range iids {
		i := slices.IndexFunc(infos, func(m planMilestoneInfo) bool { return m.IID == iid })
		if i < 0 {
			refs = append(refs, planRef{IID: iid, Missing: true})
		} else {
			refs = append(refs, planRef{IID: iid, Path: infos[i].Path})
		}
	}
	return refs
}

// renderPlanRefs is the prompt section for the milestones the owner names,
// "" for none.
func renderPlanRefs(refs []planRef) string {
	if len(refs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Milestones the owner names\n\n")
	for _, r := range refs {
		if r.Missing {
			fmt.Fprintf(&b, "- The owner names %%%d, which the project does not have or could not be read.\n", r.IID)
		} else {
			fmt.Fprintf(&b, "- The owner names %%%d: read `%s` before you answer.\n", r.IID, r.Path)
		}
	}
	return b.String() + "\n"
}
