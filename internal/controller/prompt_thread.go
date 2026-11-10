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
// options, and it has an issue or merge request for its comment.
func letterAnswers(spec aiv1.AgentProjectSpec, holder *aiv1.AgentTask, p aiv1.Prompt, n promptNote) bool {
	return spec.Workflow.Takes(aiv1.InputGitLab) && holder.Spec.Type == aiv1.TaskTypeSpec && len(p.Options) > 0 && n.iid != 0
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
// issue iid, or merge request iid if mr, newer than since; tracking is the
// milestone's tracking issue. The threads are read only when there is such
// a reply and a prompt of st was posted there. Only a thread whose root is
// the bot's comment of a prompt of st on this issue or merge request
// counts: review threads, diff notes and comments outside a thread are
// never answered. A refusal, also of a reply by someone who is no owner,
// is replied in the thread; an answer is shown by the mirror.
func (r *AgentProjectReconciler) promptThreadAnswers(ctx context.Context, gl gitlab.API, project *aiv1.AgentProject, spec aiv1.AgentProjectSpec,
	st, coord *aiv1.AgentTask, iid int64, mr bool, tracking, since int64, notes []gitlab.Note) error {
	if st == nil || coord == nil || !spec.Workflow.Takes(aiv1.InputGitLab) {
		return nil
	}
	bot := project.Status.BotUserID
	letters := letterReplies(notes, since, bot)
	// here reports whether p takes letters and its recorded comment is note
	// root of this issue or merge request (root 0: any note of it).
	here := func(p aiv1.Prompt, root int64) bool {
		n := promptNoteOf(gl, spec, st, p, tracking)
		return p.PostedNoteID != 0 && (root == 0 || p.PostedNoteID == root) && n.iid == iid && n.mr == mr && letterAnswers(spec, st, p, n)
	}
	if len(letters) == 0 || !slices.ContainsFunc(st.Status.Prompts, func(p aiv1.Prompt) bool { return here(p, 0) }) {
		return nil
	}
	at := promptNote{gl: gl, project: spec.GitLab.Project, iid: iid, mr: mr}
	threads, err := at.discussions(ctx)
	if err != nil {
		ref := fmt.Sprintf("#%d", iid)
		if mr {
			ref = fmt.Sprintf("!%d", iid)
		}
		return fmt.Errorf("listing the threads of %s: %w", ref, err)
	}
	var current map[int64]bool // the owners of now, for prompts without pinned ones
	for _, d := range threads {
		if len(d.Notes) < 2 || d.Notes[0].Author.ID != bot || d.Notes[0].System {
			continue
		}
		m := marker.Prompt.FindStringSubmatch(d.Notes[0].Body)
		if m == nil {
			continue
		}
		// Not a prompt of a task before st, nor one that left its status,
		// nor one whose comment is elsewhere.
		i := slices.IndexFunc(st.Status.Prompts, func(p aiv1.Prompt) bool {
			return p.ID == m[1] && strconv.FormatInt(postedUnix(p), 10) == m[2] && here(p, d.Notes[0].ID)
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
				if reply, answered, err = r.answerPrompt(ctx, project, spec, st, coord, n.Author.Username, 0, cmd, "gitlab"); err != nil {
					return err
				}
				if answered {
					continue
				}
			}
			if _, err := at.replyThread(ctx, d.ID, "🧭 "+reply); err != nil {
				log.FromContext(ctx).Info("could not answer a reply to a prompt", "note", n.ID, "error", err.Error())
			}
		}
	}
	return nil
}
