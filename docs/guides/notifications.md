# Notifications and the Desk on your phone

This page shows how to get a browser notification when something in the
[Desk](../concepts/glossary.md#desk) needs you, how to pick what notifies,
and how to install the Desk as an app on your computer or phone. The
categories, their defaults and which browsers show what are in the
[Notifications reference](../reference/notifications.md).

## Turn notifications on

1. Open the account menu (your name at the bottom of the rail; on a phone,
   tap *Projects* in the bottom bar first) and choose **Notifications**.
   You can also click *Notify me* in the Inbox header. Both open the same
   dialog.
2. Turn on *Notifications in this browser*. Your browser asks for
   permission; allow it.
3. Turn the categories you want on or off, such as *Problems*, *Questions*
   or *Releases*. Each switch has a one-line description.
4. Close the dialog. The Inbox button now reads *Notifications on*.

From now on, each new item of a category you picked raises one
notification. Two stuck issues of one milestone raise two notifications, one
per issue. An item notifies once. If it leaves the
[Inbox](desk.md#inbox) and comes back later, it notifies again. Items that
were already there when you opened the tab or turned notifications on don't
notify.

Your choices are kept in this browser only. A phone and a desktop each keep
their own, so you can pick fewer categories on the phone.

### Milestone and release notifications

*Milestone started*, *Milestone done* and *Releases* come from the history
[history events](../reference/status-and-history.md#history-events) the
operator records in the research database. Without that database, the dialog shows these three switches
disabled, with the line "Needs the history database".

- *Milestone started* comes when the [planner](../concepts/glossary.md#planner)
  has split a milestone into issues.
- *Milestone done* comes when the milestone's
  [autopilot](../concepts/glossary.md#autopilot) is done.
- *Releases* comes when a release is published, or when it fails.

Only events after you opened the tab notify, never older ones.

### When a plan agent answers you

You send a message in a [plan](write-a-milestone.md#draft-it-in-the-desk)
and close the Desk. When the agent has answered, you get **Plan ready ·
<plan title>** with the start of its answer. If the turn did not finish,
the text says why, for example "The turn did not finish: usage limit until
…".

1. Open the account menu and choose **Notifications**.
2. Check that *Plans ready* is on. Turn it off to keep plans quiet on this
   device.

The plan also shows as *Plan ready* in your Inbox until you send your next
message, create all its milestones, or delete it. Only you get it, as the
sender of the message the agent answered. Other owners of the project don't.

## When the browser blocks them

If you once refused the permission, the dialog shows *Notifications in this
browser* disabled, with "Blocked in this browser's settings". Allow
notifications for the Desk's address in your browser's site settings, then
open the dialog again and turn them on.

A browser that has no notifications at all shows "Not supported in this
browser".

## What a click opens

A click on a notification opens the item's place in the Desk, the same
place as its Inbox row:

- a problem, pause, question or proposal opens its card in the milestone's
  [Cockpit](../concepts/glossary.md#cockpit);
- a task opens the task page;
- a ready plan opens the plan;
- a milestone or release notification opens the milestone.

If a Desk window is open, the click brings that window forward and opens
the item there. It does not open a second window. Only without an open Desk
window does the browser open a new one.

## With the Desk closed

With a [push subscription](../concepts/glossary.md#push-subscription), your
device gets notifications while no Desk tab is open: on an iPhone, on
Android with Chrome closed, and on a computer. Push needs a database and the
Desk's external URL. If your Desk has none, the dialog says why, and you get
in-tab notifications only. The
[reference](../reference/notifications.md#push) lists the reasons.

### On an iPhone or iPad

1. Use iOS 16.4 or later. Open the Desk in Safari, tap **Share**, then **Add
   to Home Screen**, and tap *Add*.
2. Open the Desk from its Home Screen icon, not from Safari. In Safari the
   dialog shows "Add the Desk to your Home Screen to get notifications"
   instead of the switch.
3. Open the account menu, choose **Notifications** and turn on
   *Notifications on this device*. Allow the permission.

### On Android and on a computer

Open the account menu, choose **Notifications** and turn on
*Notifications on this device*. Allow the permission. You need no install;
you may [install the Desk](#install-the-desk) if you like.

### Pick categories per device

The same switches pick what this device gets. A phone and a computer each
keep their own, so you can pick fewer categories on the phone. A change
applies at once.

### Turn them off

Turn off *Notifications on this device*. The device stops getting pushes.
**Sign out** on a device does the same for that device. A device you don't
open for 30 days stops getting pushes too; open the Desk to keep it.

### One notification, not two

On a device with push on, an open Desk tab shows no notifications of its
own: the push shows each one once. A device without push keeps the in-tab
notifications.

As in the Inbox, you get pushes only for projects you can view. A tap opens
the same place as for an in-tab notification.

## Install the Desk

You can install the Desk as an app. It then has its own icon and opens in
its own window, without browser bars.

**On a computer, in Chrome:** open the Desk, then choose **Install app** in
the address bar, or in the browser menu.

**On Android, in Chrome:** open the Desk, tap the menu (⋮) and choose
**Install app** or *Add to home screen* → *Install*. The Desk icon appears
on your home screen. Turn on notifications in the installed app as above.
They show as system notifications, and a tap opens the item.

**On an iPhone or iPad (iOS 16.4 or later), in Safari:** open the Desk, tap
**Share**, then **Add to Home Screen**, and tap *Add*. The Desk icon opens
the Desk full screen, without Safari's bars. Turn on notifications from that
icon, as [above](#on-an-iphone-or-ipad).

## Next

- [Notifications reference](../reference/notifications.md): the categories,
  their defaults, what is behind each, and which browsers show what.
- [The Desk](desk.md): the Inbox and the account menu.
- [Steer from the Desk](steer-from-the-desk.md#on-a-phone): the Desk on a
  phone.
- [When autopilot pauses](pauses.md): what to do when a problem notifies.
- [Write a milestone](write-a-milestone.md#draft-it-in-the-desk): plan a
  milestone with an agent in the Desk.
