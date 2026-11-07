package controller

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// draftRequest checks an owner's Desk "draft" of a follow-up milestone from
// the retrospective's follow-ups nums and returns the answer and the
// request, nil when it is refused: without a retrospective with open
// follow-ups, while a draft is written (pending is one asked for in this
// poll), or for numbers that are unknown, opened or taken over. The
// retrospective is the spec task's (st), where the follow-ups are settled.
func draftRequest(st, coord *aiv1.AgentTask, pending *aiv1.FollowUpDraftRequest, by string, nums []int, now time.Time) (string, *aiv1.FollowUpDraftRequest) {
	var retro *aiv1.PilotRetrospective
	if st.Status.Pilot != nil {
		retro = st.Status.Pilot.Retrospective
	}
	switch d := coord.Spec.Source.FollowUpDraft; {
	case retro == nil || retro.Failed != "" || len(retro.FollowUps) == 0:
		return "This milestone has no retrospective with follow-ups to draft from.", nil
	case followUpsSettled(retro, now):
		return "The follow-ups of this milestone are settled: there is nothing left to draft from.", nil
	case pending != nil || d != nil && d.Request > coord.Status.Run.ObservedRunRequest:
		return "The pilot is writing a draft already; ask again once it shows in the Retrospective tab.", nil
	}
	var problems, names []string
	for _, k := range nums {
		key := strconv.Itoa(k)
		names = append(names, key)
		if iid, ok := retro.Created[key]; ok {
			problems = append(problems, fmt.Sprintf("%d was opened as #%d", k, iid))
		} else if iid, ok := retro.Taken[key]; ok {
			problems = append(problems, fmt.Sprintf("%d was taken over by the milestone %%%d", k, iid))
		} else if k < 1 || k > len(retro.FollowUps) {
			problems = append(problems, fmt.Sprintf("%d is unknown, the retrospective has %d", k, len(retro.FollowUps)))
		}
	}
	if len(problems) > 0 {
		return "No draft: follow-up " + strings.Join(problems, "; ") + ".", nil
	}
	return fmt.Sprintf("The pilot writes a draft of a follow-up milestone from follow-ups %s; it shows in the Retrospective tab.", strings.Join(names, ", ")),
		&aiv1.FollowUpDraftRequest{Numbers: nums, By: by}
}
