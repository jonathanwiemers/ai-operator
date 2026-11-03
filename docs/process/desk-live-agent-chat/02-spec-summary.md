# Spec summary — desk-live-agent-chat

**Date:** 2026-11-03 | Full spec: `docs/specs/desk-live-agent-chat.spec.md`

## Plan

- Desk chat goes straight to the agent's live CLI session (claude stream-json input, cursor-agent ACP) behind a `Session` interface.
- Chat stored in Postgres (`chat_messages`); operator hands messages to tasks like pilot messages; runner serializes chat turns and runs on one session.
- GitLab keeps the code record; GitLab comments as input become optional per project (`workflow.inputs`).

## Key decisions (confirmed)

1. Claude: native stream-json input, not a community ACP adapter.
2. Postgres is the record for Desk chat.
3. UI gets a narrow chat writer login (insert user rows only).
4. Access rules (`chat` grant) decide who may chat.
5. Implementer runs move onto the session too.
- Vendor SDKs and A2A: not chosen, revisit triggers in the spec.

## Risks

- Long-lived CLI processes; less documented stream-json input and young ACP cancel; subscription burn; record split; UI DB write path; authority without GitLab membership.

## PR/MR plan

Ten slices, see the spec's Delivery and `00-index.md`.

## Approval

- [x] Human approved (date: 2026-11-03, "Accept, open MR")
