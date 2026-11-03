# Merge — desk-live-agent-chat

**Agent:** shell | **Tier:** fast | **MR:** !— | **Date:** 2026-11-03

## Preconditions

- [ ] Pipeline green
- [ ] Review summary clean or deferred
- [ ] Human authorized merge

## Commands

```bash
env -u GITLAB_TOKEN glab ci status
env -u GITLAB_TOKEN glab mr merge <iid> --when-pipeline-succeeds
```

## Result

- **Merge SHA:**
- **Branch removed:**
