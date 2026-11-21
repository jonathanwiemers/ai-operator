# Order milestones and read the timeline

This page shows you how to put a project's
[autopilot](../concepts/glossary.md#autopilot) milestones in the order they
run, and how to read the timeline of which issue ran when. You do both in
the [Desk](../concepts/glossary.md#desk). The same order can be set from
GitLab with `/agent move`.

## The queue

A project page's *Stream* tab opens with the queue. It lists the project's
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

A milestone's *Issues* tab has a switch: *Timeline* or *Lanes*. *Timeline*
shows one row per issue, grouped by [wave](../concepts/glossary.md#wave),
on a time axis:

```
W1  #11 Add parser    ████▒▒▒▒▒▒◆
    #12 Add lexer      ███▒▒▒▒◆
W2  #13 Wire it up              ██▒▒░░░░███▒◆      │ now
```

Each bar is the issue's life, from its first run:

- **running** (strong colour): an agent run worked on it;
- **waiting** (pale): it waited for CI, a review or a free slot;
- **held** (warning colour): it was [held](../concepts/glossary.md#hold);
- a **diamond** marks the merge (green) or the skip (grey).

An issue still open ends at the line that marks now. Issues that ran side
by side sit side by side. The legend under the timeline names the colours.

Each bar is a link to the issue's page. A screen reader reads it as, for
example, "#42 Add parser, running 12 min, merged 14:05". On a phone the
issue names stay on the left and the time axis scrolls sideways.

*Lanes* is the queue by lane, as before: *Running*, *Landing*, *Ready to
land*, *Waiting*, *Held* and *Done*. Your choice is part of the address
(`?view=timeline` or `?view=lanes`), so you can send a link to it.

The timeline needs the research database. Without it the tab shows
*Lanes* and no switch. Holds are drawn from the operator version that
records them; older issues show no held segments.

## The Overview timeline

The *Overview* has a *Timeline* section after *Working now*. It shows one
row per project you may see:

```
6 h | 24 h | 7 d          ■ %5 Checkout  ■ %9 Docs  ◆ Merged  ◆ Skipped

shop   #340 ███▒▒◆   #341 ██▒▒▒▒▒◆
            #342  ████▒▒▒◆
docs                #12 ██▒◆                      │ now
```

- Each bar is one issue, labelled `#iid` and coloured by its milestone.
  The legend lists the milestones in view.
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
