package controller

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/chatcmd"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/marker"
)

// While a project takes GitLab input, an owner answers a prompt by
// replying with an option's letter in the thread of its comment, through
// the same answerPrompt as the Desk. Free text stays in the Desk.

// promptLetter matches a reply that starts with an option's letter on its
// own: alone, before a line break, or before punctuation ("A", "b.",
// "C: cheaper"). "A lot" is discussion, not an answer.
var promptLetter = regexp.MustCompile(`^\s*([A-Ea-e])[ \t\r]*(?:$|\n|[.,:;!)—–-])`)

// letterAnswers reports whether p, with its comment at n, takes letter
// replies: the project takes GitLab input, p is a milestone's prompt with
// options, and its comment is on an issue (threads of merge requests are
// not read).
func letterAnswers(spec aiv1.AgentProjectSpec, holder *aiv1.AgentTask, p aiv1.Prompt, n promptNote) bool {
	return spec.Workflow.Takes(aiv1.InputGitLab) && holder.Spec.Type == aiv1.TaskTypeSpec && len(p.Options) > 0 && n.iid != 0 && !n.mr
}

// letterReplies are the option letters of the notes newer than since that
// are no system notes and not the bot's, by note ID.
func letterReplies(notes []gitlab.Note, since, bot int64) map[int64]string {
	out := map[int64]string{}
	for _, n := range notes {
		if m := promptLetter.FindStringSubmatch(n.Body); m != nil && n.ID > since && !n.System && n.Author.ID != bot {
			out[n.ID] = strings.ToUpper(m[1])
		}
	}
	return out
}

// promptThreadAnswers answers the prompts of the milestone's spec task st
// with the letters the owners replied in the threads of their comments on
// issue iid, newer than since; tracking is the milestone's tracking issue.
// The threads are read only when there is such a reply and a prompt of st
// was posted on iid. A refusal, also of a reply by someone who is no
// owner, is replied in the thread; an answer is shown by the mirror.
func (r *AgentProjectReconciler) promptThreadAnswers(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	st, coord *aiv1.AgentTask, iid, tracking, since int64, notes []gitlab.Note) error {
	if st == nil || coord == nil || !spec.Workflow.Takes(aiv1.InputGitLab) {
		return nil
	}
	bot := project.Status.BotUserID
	letters := letterReplies(notes, since, bot)
	if len(letters) == 0 || !slices.ContainsFunc(st.Status.Prompts, func(p aiv1.Prompt) bool {
		n := promptNoteOf(gl, spec, st, p, tracking)
		return p.PostedNoteID != 0 && n.iid == iid && letterAnswers(spec, st, p, n)
	}) {
		return nil
	}
	threads, err := gl.ListIssueDiscussions(ctx, spec.GitLab.Project, iid)
	if err != nil {
		return fmt.Errorf("listing the threads of #%d: %w", iid, err)
	}
	var current map[int64]bool // the owners of now, for prompts without pinned ones
	for _, d := range threads {
		if len(d.Notes) < 2 || d.Notes[0].Author.ID != bot {
			continue
		}
		m := marker.Prompt.FindStringSubmatch(d.Notes[0].Body)
		if m == nil {
			continue
		}
		// Not a prompt of a task before st, nor one that left its status.
		i := slices.IndexFunc(st.Status.Prompts, func(p aiv1.Prompt) bool {
			return p.ID == m[1] && strconv.FormatInt(postedUnix(p), 10) == m[2]
		})
		if i < 0 {
			continue
		}
		p := st.Status.Prompts[i]
		for _, n := range d.Notes[1:] {
			letter, ok := letters[n.ID]
			if !ok {
				continue
			}
			owner := slices.Contains(p.OwnerIDs, n.Author.ID)
			if len(p.OwnerIDs) == 0 {
				if current == nil {
					if current, err = ownerIDs(ctx, gl, st.Spec.Owners); err != nil {
						return err
					}
				}
				owner = current[n.Author.ID]
			}
			reply := fmt.Sprintf("Only the milestone's owners can answer %s; this reply changes nothing.", p.ID)
			if owner {
				cmd := chatcmd.Command{Cmd: "answer", Prompt: p.ID, Option: letter}
				var answered bool
				if reply, answered, err = r.answerPrompt(ctx, project, spec, st, coord, n.Author.Username, cmd, "gitlab"); err != nil {
					return err
				}
				if answered {
					continue
				}
			}
			if _, err := gl.ReplyIssueDiscussion(ctx, spec.GitLab.Project, iid, d.ID, "🧭 "+reply); err != nil {
				log.FromContext(ctx).Info("could not answer a reply to a prompt", "note", n.ID, "error", err.Error())
			}
		}
	}
	return nil
}
