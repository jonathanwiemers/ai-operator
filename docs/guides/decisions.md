# Read and revisit decisions on the Desk

Use this page to find what the agents decided on a milestone, see why, and
change a [decision](../concepts/glossary.md#decision) you want differently.
You do it all in the [Desk](../concepts/glossary.md#desk); GitLab stays the
place where the decisions are written.

## Example

The refund issue #42 of the milestone *Payments* is merged. You want to
know which currency the agent picked for refunds:

1. Open the project in the Desk, open its *Decisions* tab and pick *Payments* in the *Milestone* select.
2. Type `currency` in the search box. One card is left:

   > **How refunds are booked** `D42-3` *Implementation*
   >
   > In which currency is a refund booked?
   >
   > - The settlement currency — *Chosen*
   > - The order currency
   >
   > **Why:** the provider settles in EUR, so the books stay in one currency.
   > Run `shop-42#3`, 2 days ago · *on GitLab*

3. You want the order currency. The card says: "Opens a revisit prompt.
   Your choice creates a follow-up issue in this milestone, queued after
   the planned issues. D42-3 is marked superseded when the follow-up posts
   its new decision."
4. Click *Revisit*, then pick *Switch to The order currency* in the prompt
   pinned in the milestone's *Cockpit*.

The operator creates the follow-up #57. Once its agent posts D57-1, the card
of D42-3 is muted, reads *Superseded by D57-1* and shows the steps in
between.

## Where decisions show

- **A project's *Decisions* tab** shows the decisions of all its milestones,
  newest milestone first, from the [planner](../concepts/glossary.md#planner)
  and from the implementers, open issues included. Each issue's header names its
  milestone. Pick one in the *Milestone* select to narrow the list to it; *All
  milestones* shows them all again. A done milestone's decisions stay as long
  as its record is kept. `?tab=decisions` opens it.
- **A milestone's *Decisions* tab** shows the same cards for that milestone.
  `?tab=decisions` opens it.

Old `?panel=decisions` links open the *Decisions* tab, and the address
changes to match. A link that ends in `#D42-3` scrolls to that card.

The cards are grouped by issue, in the order of the plan. The issue's title
opens its issue page.

## Search and filter

The search box finds a decision by its ID (`D42-3`), its title, or a word
of its question, choice, why or alternatives. Case does not matter.

The chips narrow the list:

- *All*: every decision.
- *Active*: those that still hold.
- *Superseded*: those another decision replaced.

The search, the chip and the project's milestone are part of the address,
so a reload or a link you send keeps them. One list shows at most 200
decisions; search to find older ones.

## The card

Each decision has one card:

- the title, the ID and a chip: *Plan* for the planner's, *Implementation*
  for an implementer's;
- the question;
- the options: the chosen one first, marked *Chosen*, then the
  alternatives the agent rejected;
- why it was chosen, the run that decided it and when;
- *on GitLab*: the comment the decision came from.

A replaced decision is muted and reads *Superseded by D57-1*; the ID opens
that card. The text is the agent's text, shown as markdown without HTML.
On a phone the card stacks, so nothing scrolls sideways.

A link that ends in `#D42-3` opens the project tab at that card and highlights it
for a moment; on a milestone page it shows that line in the panel.

## The chain of a revisit

A decision someone asked to revisit shows its chain below the card, one
step per request:

1. Who asked and what for, with the comment or the follow-up issue (`#57`)
   and its state. A request from the Desk names the owner "via the Desk".
2. The decision that replaced it, once one did (`D57-1`).

If someone revisits D57-1 too, a second step follows with the decision that
replaced D57-1, and so on.

## What Revisit does

Before you revisit, the card says in one line what will happen. The Desk
picks one of four ways for each decision:

| The decision | What the card offers | Its line |
|--------------|----------------------|----------|
| of a closed issue, on a milestone with a pilot | *Revisit* | "Opens a revisit prompt. Your choice creates a follow-up issue in this milestone, queued after the planned issues. …" On a milestone without autopilot: the follow-up starts right away. |
| of an open issue its agent works on | a text box and *Comment* | "Comments on #42. Its next run changes the code and posts a decision that supersedes D42-3." |
| anything else | the command to copy and *Comment on GitLab* | "Comment this command on #42 or the tracking issue." |
| already replaced, or a follow-up is open for it | nothing | "Already replaced by D57-1." or "#57 already revisits D42-3; comment there." |

- *Revisit* opens the revisit prompt, pinned in the milestone's *Cockpit*:
  *Keep …*, one *Switch to …* per alternative, and a text box. See
  [Revisit a decision](steer-from-the-desk.md#revisit-a-decision).
- *Comment* posts `/agent revisit D42-3 <your text>` on the issue, in your
  GitLab name. The issue's next run changes the code and records the
  decision that replaces it.
- The copied command works as a comment on the issue or on the milestone's
  [tracking issue](../concepts/glossary.md#tracking-issue). See
  [Decisions and Revisit](iterate-and-finish.md#decisions-and-revisit).

Only the milestone's [owners](owners-and-pings.md) get the actions. Others
see the line without them.

## Links to a decision

- A decision ID on the *Issues* tab, and in the decision's event in its
  issue's thread in the *Cockpit*, opens the milestone's *Decisions* tab at
  its card.
- *Revisit* works in the Cockpit too: the decision's event in the issue's
  thread has the same button and options as the card.
- The tracking issue's *Decisions* list links to the milestone's
  *Decisions* tab.
- Each card keeps *on GitLab* for the comment itself.

## When the tab is empty

The tab reads the Desk's history database. Without it:

- the tab says "Decisions need the history database";
- decision IDs in the queue link to the comment on GitLab instead.

With the database, the operator copies the decisions of every planned
milestone into it, also of milestones without autopilot. It reads each
issue again once it changed, so a new decision shows up a poll after its
comment. The first time, it reads every issue of the milestone once. The
decisions are kept as long as the milestone's record. "No decisions
recorded yet." means the agents have posted none so far.

## Next

- [Steer from the Desk](steer-from-the-desk.md): prompts, the queue and
  more you do in the Desk.
- [Iterate and finish](iterate-and-finish.md): how decisions are recorded
  and revisited in GitLab.
- [The Desk](desk.md): every tab of a milestone and a project.
