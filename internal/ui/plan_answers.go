package ui

import (
	"fmt"
	"net/http"
	"unicode/utf8"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

// Limits of the decision answers posted with a plan chat message.
const (
	maxPlanAnswers = 30
	maxAnswerOther = 2000
	// maxAnswerBytes is room in the request for one answer: its other
	// text in UTF-8 and the JSON around it.
	maxAnswerBytes = 4*maxAnswerOther + 256
)

// errDecisionsChanged answers a post against versions the plan no longer
// has: the wizard reloads the decisions.
const errDecisionsChanged = "the decisions changed"

// checkPlanAnswers checks the answers of a plan chat message of kind kind
// against the plan's current decisions. It returns 0 when they are fine, 400
// for a malformed answer and 409 when an answered ID is gone or its version
// moved, with the message to answer.
func checkPlanAnswers(pl *aiv1.AgentPlan, kind string, answers []store.DecisionAnswer) (int, string) {
	if len(answers) == 0 {
		return 0, ""
	}
	if kind != "message" {
		return http.StatusBadRequest, "decisions are answered with a message"
	}
	seen := make(map[string]bool, len(answers))
	for i, a := range answers {
		set := 0
		for _, on := range []bool{a.Keep, a.Option != nil, a.Other != ""} {
			if on {
				set++
			}
		}
		switch {
		case set != 1:
			return http.StatusBadRequest, fmt.Sprintf("decision answer %d must set exactly one of keep, option and other", i+1)
		case utf8.RuneCountInString(a.Other) > maxAnswerOther:
			return http.StatusBadRequest, fmt.Sprintf("decision answer %d is longer than %d characters", i+1, maxAnswerOther)
		case seen[a.ID]:
			return http.StatusBadRequest, fmt.Sprintf("decision answer %d answers a decision twice", i+1)
		}
		seen[a.ID] = true
	}
	current := make(map[string]*aiv1.PlanDecision, len(pl.Status.Decisions))
	for i := range pl.Status.Decisions {
		current[pl.Status.Decisions[i].ID] = &pl.Status.Decisions[i]
	}
	for _, a := range answers {
		if d := current[a.ID]; d == nil || d.Version != a.Version {
			return http.StatusConflict, errDecisionsChanged
		}
	}
	for i, a := range answers {
		if o := a.Option; o != nil && (*o < 0 || int(*o) >= len(current[a.ID].Options)) {
			return http.StatusBadRequest, fmt.Sprintf("decision answer %d picks an option the decision does not have", i+1)
		}
	}
	return 0, ""
}
