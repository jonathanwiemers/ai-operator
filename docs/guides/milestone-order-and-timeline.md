# Order milestones and read the timeline

This page shows you how to put a project's
[autopilot](../concepts/glossary.md#autopilot) milestones in the order they
run, and how to read the timeline of which issue ran when. You do both in
the [Desk](../concepts/glossary.md#desk). The same order can be set from
GitLab with `/agent move`.

## The queue

A project page's *Milestones* tab starts with the queue. It lists the project's
open autopilot milestones in their [run order](../concepts/glossary.md#run-order),
first at the top:

```
⠿  %5 Checkout       Running              3/7   $4.10   …  Pause
⠿  %6 Coupons        Holding for %5       0/4   $0.00   …  Pause
⠿  %7 Search         Waiting to plan      0/0   $0.00   …  Pause
```

Each row shows the milestone's state, its issues done and its spend. The
states are *Running*, *Holding for %N* (planned, waits for the first),
*Waiting to plan*, *Paused* and *Budget*.

Only the first milestone starts new issues. When it is done, it leaves the
queue and the next one goes on. A new autopilot milestone joins at the
bottom.

## Move a milestone

You need the `chat` grant, and your Desk user name on the milestone's
`/agent owner-subjects` line (see [Owners and pings](owners-and-pings.md)).
Without them the rows have no handle and no menu. The operator refuses a
move by anyone else.

**Drag it.** Grab a row by its handle (⠿) and drop it at its new place.

**Use the row menu.** The *…* button of a row (named *Move %N* for screen
readers) opens a menu:

- *Move to top*;
- *Move up*;
- *Move down*.

The menu works with the keyboard and on a phone. *Move to top* is what
*Run next* used to be.

After a drop or a pick, the row shows its new place with *Moving…*. It
stays there once the queue reports the new order, usually on the next
poll. If the message cannot be sent or the operator refuses the move, the
row goes back and the reason shows below the queue.

The milestone's [tracking issue](../concepts/glossary.md#tracking-issue)
gets a note, for example `⏫ Moved before %6 (by alice via the Desk).`

### Above the running milestone

When your move puts a milestone above the running one, the Desk asks first:

> **Move above the running milestone?**
> Move %7 above %5? %5's running issues finish, then %7 runs. %5 starts no
> new issues until %7 is done.

*Move* sends it. *Cancel* or Escape sends nothing and the row goes back.

Then %5 shows *Holding for %7*. A running issue is never stopped: %5's
active issues finish, it starts no new ones, and %7 plans. A paused
milestone stays paused at the top until you resume it.

### From GitLab

Comment on the milestone's tracking issue:

```
/agent move top
/agent move bottom
/agent move before %6
/agent move after %6
```

`/agent start` is another way to write `move top`. A GitLab command applies
without asking, also above the running milestone. The words and refusals
are in [Commands](../reference/commands.md#on-the-tracking-issue).

## The issue timeline

A milestone's *Timeline* tab (after *Issues*) shows one row per issue, grouped by [wave](../concepts/glossary.md#wave),
on a time axis:

```
W1  #11 Add parser    ███▓▓▒▒▒▒//◆
    #12 Add lexer      ██▓▒▒▒//◆
W2  #13 Wire it up              ██▓▒▒░░░░███▓▒◆      │ now
```

Each bar is the issue's life, from its first run to its merge, its skip or
now. Every part of it has one of five kinds:

- **running**: an agent run worked on it, the implementer or a fix run;
- **review**: the reviewers read its merge request;
- **CI**: a pipeline of its merge request ran, also one started by a
  rebase without a new push;
- **waiting**: everything else, for example the wait for its merge turn, a
  usage limit or a full queue;
- **held**: it was [held](../concepts/glossary.md#hold).

A **diamond** marks the merge (green) or the skip (grey).

Each kind has its own fill and a thin stripe in its own colour along the
bottom of the bar. You tell the kinds apart by the stripe, in light and in
dark mode. Waiting also has a diagonal hatch, so it reads as part of the
bar and not as a gap. No part of a bar is blank.

Where two kinds meet, one wins: review, then running, then CI, then held,
then waiting. The reviewers work inside a run, so their time shows as
review. A pipeline that runs while the issue is held shows as CI.

An issue still open ends at the line that marks now. Issues that ran side
by side sit side by side. The key under the timeline names the five kinds,
*Merged* and *Skipped*.

Each bar is a link to the issue's page. When you hover it, or a screen
reader reaches it, you get the split, for example "#42 Add parser, running
15 min, review 5 min, CI 15 min, waiting 10 min, merged 14:05". A kind with
no time is left out. An open issue ends with what it does now: "running
now", "in review since 14:05", "in CI since 14:05", "waiting since 14:05"
or "held since 14:05". On a phone the issue names stay on the left and the
time axis scrolls sideways.

On the *Issues* tab, the switch under the table is *Lanes* or *Board*. *Lanes*, the default, is the queue by lane: *Running*, *Landing*, *Ready to
land*, *Waiting*, *Held* and *Done*. The tab is in the address (`?tab=timeline`), so you can send a link to it. An old `?view=timeline` link opens the tab.

The timeline needs the research database. Without it there is no *Timeline* tab
and the *Issues* tab has no Timeline view. Holds are drawn from the operator version that
records them; older issues show no held segments.

A review shows once its reviewer is done; while the reviewers still work,
that time shows as running. Issues from before the release that added
review and CI time still get their review segments. Their CI time runs
from their push to their CI result, and a pipeline started by a rebase
shows as waiting there.

## The Overview timeline

The *Overview* has a *Timeline* tab (`/?tab=timeline`). It shows one
row per project you may see:

```
6 h | 24 h | 7 d
■ %5 Checkout  ■ %9 Docs  ▁ Running  ▁ Review  ▁ CI  ▁ Waiting  ▁ Held  ◆ Merged  ◆ Skipped

shop   #340 ███▒▒◆   #341 ██▒▒▒▒▒◆
            #342  ████▒▒▒◆
docs                #12 ██▒◆                      │ now
```

- Each bar is one issue, labelled `#iid` and filled with its milestone's
  colour. The key lists the milestones in view first.
- The stripe along a bar's bottom shows the five kinds, in the same
  colours as on the *Issues* tab. Waiting has its hatch here too. The key
  names the kinds after the milestones.
- When bars of one project overlap, the row grows another line, so no bar
  hides another.
- Pick the window: *6 h*, *24 h* or *7 d*. It is part of the address
  (`?window=6h`, `24h` or `7d`).

A bar links to its issue, like on the *Issues* tab. When more issues ran
than the timeline can show, a note says so. Without the research database
the section shows an info alert instead.

## Next

- [Autopilot](autopilot.md#several-milestones-at-once): how milestones of
  one project take turns.
- [Commands](../reference/commands.md#on-the-tracking-issue): `/agent move`
  and the other tracking-issue commands.
- [Desk API](../reference/desk-api.md#timeline): the timeline's data for
  scripts.
- [The Desk](desk.md): every view of the Desk.
