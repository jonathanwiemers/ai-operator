# Deploy — desk-live-agent-chat

**Agent:** shell | **Tier:** fast | **Date:** 2026-11-03

## Cluster repo

- **Repo:** jo.wiemers/infra/cluster
- **MR:** !—
- **Workstream folder:** `docs/process/desk-live-agent-chat/` (in cluster repo if cross-repo)

## Image bump

| Workload | Old tag | New tag | Path |
|----------|---------|---------|------|
| | | | |

## Reconcile

```bash
flux reconcile kustomization cluster-apps -n flux-system --with-source
flux reconcile helmrelease <app> -n <namespace>
kubectl -n <namespace> rollout status deploy/<app>
```

## Verified

- [ ] Pods running new image
- [ ] Smoke check URL:
