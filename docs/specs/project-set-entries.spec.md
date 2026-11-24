# Project sets: per-project settings inside the set, no Kustomize patches

Status: R1–R5 implemented in this repository; the infra companions R6 and
R7 are still to come. This file was rebuilt, not copied: see the
[Steering log](#steering-log).

## Goal

1. One project of an `AgentProjectSet` gets its own settings in the set
   itself, as an entry in `spec.projects`, written as the difference to
   `template`. No explicit AgentProject and no Kustomize patch for it.
2. An explicit AgentProject moves into the set without being deleted: its
   tasks, plans and GitLab hook stay.
3. A set without `spec.projects` behaves as before, and its children are
   not rewritten.

## Background

The group-projects spec ([group-projects.spec.md](group-projects.spec.md))
gave sets one `template` for every child. A project that needed other
settings had to stay an explicit AgentProject (its Decisions 1–2), and
"per-project overrides inside a set" was a non-goal. Moving such a project
into a set meant deleting its AgentProject, and with it its tasks.

The infra repository (`jo.wiemers/infra/cluster`) works around this with
a defaults carrier, `replacements` and Kustomize patches on its explicit
AgentProjects. `infra-cluster` runs with `release.mode: Auto`.

## Decisions (locked)

1. **Entries in `spec.projects`.** Each entry has `path` (one project's
   full path below `gitlab.group`, no globs), an optional `name` (the
   child's name) and an optional `spec` (any `AgentProjectSpec` field
   except `gitlab`, stored schemaless like `template`). The list is a map
   keyed by `path`. This reverses group-projects Decisions 1–2 for named
   projects. Rejected: Kustomize patches on the children; explicit
   AgentProjects for every special project.
2. **RFC 7386 merge.** The child's spec is the template with the entry's
   `spec` applied as a JSON Merge Patch: objects merge, `null` deletes a
   key, lists and scalars replace. The merged result is decoded strictly
   and needs the webhook fields like the template. The template hash is
   computed over the final child spec, so a child without an entry keeps
   the hash it had before entries existed. Rejected: strategic merge patch
   (needs patch-strategy markers on 100+ fields); per-field override lists.
3. **Strict validation; a broken entry stops the set.** Path problems
   (empty, a glob, not below the group, a duplicate in any case) and name
   problems (not a DNS label, a duplicate) give `Ready=False`,
   `InvalidSpec`. Each entry's `spec` is first decoded alone and strictly,
   so an unknown field is refused even with a `null` value; that and a
   `gitlab` key give `InvalidTemplate`. A merged spec without webhooks gives
   `WebhookRequired`. The messages name the entry, `projects[i]`.
4. **Named projects are never dormant.** A named project gets a child
   with or without work; `dormantAfter` does not remove it. `include`,
   `exclude` and archiving still apply, with the reason
   `named in spec.projects, but …`. An active named project's reason is
   `named in spec.projects`.
5. **Adoption by annotation.** An AgentProject the set does not own, with
   the annotation `ai.wiemers.me/project-set: <set name>`, is adopted as the
   child of its entry when it is in the set's namespace, has no controller,
   is the only AgentProject on that path and has the entry's child name. The
   set becomes its controller, adds the label and writes the merged spec in
   one update; it never deletes or recreates the object. Otherwise the
   project stays `explicit` and the reason says what is missing. Unnamed
   projects stay `explicit` as before.
6. **Removing an entry.** The child stays and gets the template on the next
   poll; from then on the dormant rules apply.
7. **Names.** `name` applies when a child is created; an existing child
   keeps its name (a rename would delete its tasks), and the status says
   so. Named projects are planned first, so a named project wins a child
   name over an unnamed one; any other clash is `nameTaken`.

## Requirements

- **R1** `ProjectSetEntry` type and `spec.projects`, with validation and
  strict decode (Decisions 1, 3). Tests: validation table, strict decode
  with null and unknown fields, CRD schema.
- **R2** RFC 7386 merge of template and entry in `planChild`, the hash over
  the merged result (Decision 2). Tests: merge cases (objects, null, lists), hash
  stable for unnamed children.
- **R3** Named projects in discovery: never dormant, the include/exclude
  condition, the name check (Decisions 4, 7). Tests: plan tables.
- **R4** Adoption by the `ai.wiemers.me/project-set` annotation, and
  removal of an entry (Decisions 5, 6). Tests: adoption and each refusal,
  controller tests of the adoption update.
- **R5** Docs: the AgentProjectSet reference, the project-sets guide, the
  glossary, this file, and a steer in `group-projects.spec.md`.
- **R6** infra/cluster companion: the cutover in one MR — entries for the
  special projects, the annotations and the no-prune markers, the defaults
  carrier, `replacements` and patches gone.
- **R7** infra/cluster companion: cleanup of the adopted manifests.

## Constraints

- Under 300 production lines per MR; tests, generated files and docs do not
  count.
- A set without `spec.projects` behaves as before; its children's hashes
  do not change.
- No new dependencies.
- R6 lands only after a release with R1–R4 is running. R7 lands only after
  R6 is merged and the owner has confirmed the adoption.

## Steering log

- 2026-11-24: this file was rebuilt from the milestone's R1–R5 issues,
  their merge requests and the merged code, not copied. The original text
  was the description of the milestone "Project sets: per-project settings
  inside the set, no Kustomize patches", which this repository does not
  hold. The Decisions are written as R1–R4 implemented them.
- The first run of the spec planned only the placeholder #679 ("t", MR
  !702), which ended Skipped with nothing implemented. The milestone
  "Project sets: split the spec into per-requirement issues and guard the
  review transition" split it into R1–R7.
- R1 renamed the status type `ProjectSetEntry` to `ProjectSetProjectStatus`,
  so the new spec type could take the name `ProjectSetEntry`.
- R1 (`48cc2ce9`): `ProjectSetEntry`, `spec.projects`, validation and
  strict decode.
- R2 (`01717e8d`): the merge in `api/v1alpha1/projectset_merge.go` and
  `planChild`.
- R3 (`9a12c7f4`): named projects in `planProjectSet`.
- R4 (`0630ab6b`): adoption and entry removal.
- R5: the docs, and a steering-log line in
  [group-projects.spec.md](group-projects.spec.md#steering-log).
- Defaults, as the code has them; the R1–R4 issue decisions on GitLab were
  not at hand when this file was rebuilt, so check them there:
  - `name` empty: the slug of the path, as for any child.
  - `spec` empty: the template as it is; the entry only names the project.
  - An entry for a project not found in the group is `dormant` with the
    reason `named in spec.projects, but not found in the group`.
  - The annotated AgentProject is preferred when several watch the path,
    and any second watcher refuses the adoption.
  - The reason `adopted from AgentProject …` shows in the poll that adopts;
    after it, `named in spec.projects`.
  - The adoption annotation stays on the adopted object; the set does not
    remove it.
  - A failed update by conflict is left to the next poll.
- R6 (#766): not applied yet. The issue carries no companion label
  `agent-repo::jo.wiemers/infra/cluster`, so its merge request holds only
  this line; the infra MR is still to do, by hand or by a follow-up issue
  with that label. Precondition, unchecked: the checkout had no tags, so
  no release containing R4 (`0630ab6b`) could be named. First run
  `git tag --contains 0630ab6b` and check that infra's operator image and
  chart are at least that release; do not bump them in the cutover MR. The
  change, in one infra MR, in
  `clusters/netcup01/applications/ai-operator/projects/` or wherever the
  manifests now are:
  - in the set `jo-wiemers`, `spec.projects`: one entry per project that
    has a patch today, `path` its full path, `name` its AgentProject's
    name, `spec` exactly what its patch adds to the defaults carrier
    (`null` for a key the patch removes), among them `jo.wiemers/bloop`
    (`bloop`) and `jo.wiemers/ai-operator` (`ai-operator`);
  - an entry `{path: jo.wiemers/infra/cluster, name: infra-cluster,
    spec: {release: {mode: Off}}}`; if `infra-cluster` is an explicit
    AgentProject, also set `release.mode: Off` in its manifest and give it
    the two annotations below;
  - on the explicit AgentProjects `bloop` and `ai-operator`, the
    annotations `ai.wiemers.me/project-set: jo-wiemers` and
    `kustomize.toolkit.fluxcd.io/prune: disabled` (Flux), their manifests
    kept, their spec equal to the template merged with their entry, so Flux
    and the set write the same values;
  - the defaults carrier, the `replacements` and the patches stay; R7
    removes them with the adopted manifests. This deviates from R6 above,
    which named them gone: removing them before the no-prune marker is live
    risks a prune, and deleting an AgentProject deletes its tasks.

  Checks for the owner after rollout: `kubectl -n agents get aps
  jo-wiemers -o yaml` lists `bloop` and `ai-operator` under
  `status.projects` with `state: active`, their own names as `child` and
  the reason `adopted from AgentProject agents/<name>` in the adopting
  poll (`named in spec.projects` after it); their `ownerReferences` name
  the set as controller; and `kubectl -n agents get agentproject
  infra-cluster -o jsonpath='{.spec.release.mode}'` prints `Off`.
