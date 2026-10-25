#!/usr/bin/env bash
# Creates the ai-operator labels in a GitLab project, or with GITLAB_GROUP as
# group labels shared by every project in the group (needed for group boards,
# see hack/gitlab-board.sh).
#   GITLAB_URL (default https://gitlab.com), GITLAB_PROJECT (group/project), GITLAB_TOKEN
#   GITLAB_GROUP (group path) instead of GITLAB_PROJECT; the token needs Maintainer on the group
#   GITLAB_PACE: seconds between the per-project calls of a group (default 1)
set -euo pipefail
if [[ -n "${GITLAB_GROUP:-}" ]]; then
  if [[ -n "${GITLAB_PROJECT:-}" ]]; then
    echo "set either GITLAB_GROUP or GITLAB_PROJECT, not both" >&2
    exit 1
  fi
  : "${GITLAB_TOKEN:?set GITLAB_TOKEN (your own token, api scope, Maintainer on the group)}"
  target="groups/$(printf '%s' "$GITLAB_GROUP" | sed 's#/#%2F#g')"
else
  : "${GITLAB_PROJECT:?set GITLAB_PROJECT, e.g. my-group/sandbox-todo (or GITLAB_GROUP for group labels)}"
  : "${GITLAB_TOKEN:?set GITLAB_TOKEN (project access token, Developer, api + write_repository)}"
  target="projects/$(printf '%s' "$GITLAB_PROJECT" | sed 's#/#%2F#g')"
fi
GITLAB_URL="${GITLAB_URL:-https://gitlab.com}"

# Group labels next to project labels of the same name would split the issues
# between two labels: the operator and project boards keep using the project
# label, a group board shows only the group label. Stop before creating any.
# GitLab.com lists the group's labels too despite include_ancestor_groups=false,
# so only labels with is_project_label count.
if [[ -n "${GITLAB_GROUP:-}" ]]; then
  command -v jq >/dev/null || { echo "jq is required for GITLAB_GROUP" >&2; exit 1; }
  api() { curl -fsS -H "PRIVATE-TOKEN: ${GITLAB_TOKEN}" "${GITLAB_URL}/api/v4/$1"; }
  page=1 dupes=""
  while :; do
    projects="$(api "${target}/projects?include_subgroups=true&simple=true&per_page=100&page=${page}")"
    while IFS=$'\t' read -r pid path; do
      [[ -z "$pid" ]] && continue
      all='[]' lpage=1
      while :; do
        labels="$(api "projects/${pid}/labels?include_ancestor_groups=false&per_page=100&page=${lpage}")"
        all="$(jq -c --argjson all "$all" '$all + .' <<<"$labels")"
        (( $(jq length <<<"$labels") < 100 )) && break
        lpage=$((lpage + 1))
      done
      names="$(jq -r '[.[] | select(.is_project_label) | .name | select(startswith("agent::") or startswith("agent-blocked::") or startswith("agent-profile::") or . == "ai-generated" or . == "ci::main-red" or . == "ci::flaky")] | join(", ")' <<<"$all")"
      [[ -n "$names" ]] && dupes+="  ${path}: ${names}"$'\n'
      sleep "${GITLAB_PACE:-1}" # one call per project: stay under GitLab.com's per-user rate limit
    done < <(jq -r '.[] | [.id, .path_with_namespace] | @tsv' <<<"$projects")
    (( $(jq length <<<"$projects") < 100 )) && break
    page=$((page + 1))
  done
  if [[ -n "$dupes" ]]; then
    echo "these projects already have ai-operator labels as project labels:" >&2
    printf '%s' "$dupes" >&2
    echo "promote them to group labels instead (project > Manage > Labels > Promote; it merges" >&2
    echo "same-named labels of all projects in the group), then run this again for the rest" >&2
    exit 1
  fi
fi

create() {
  local name="$1" color="$2" description="$3"
  local code
  code="$(curl -sS -o /dev/null -w '%{http_code}' -X POST \
    -H "PRIVATE-TOKEN: ${GITLAB_TOKEN}" \
    --data-urlencode "name=${name}" --data-urlencode "color=${color}" \
    --data-urlencode "description=${description}" \
    "${GITLAB_URL}/api/v4/${target}/labels")"
  case "$code" in
    201) echo "created  ${name}" ;;
    409) echo "exists   ${name}" ;;
    *) echo "failed   ${name} (HTTP ${code})" >&2; return 1 ;;
  esac
}

create "agent::draft"        "#9e9e9e" "Planned by the spec agent, waiting for a human"
create "agent::ready"        "#1f75cb" "Hand the issue to the implementing agent"
create "agent::working"      "#e9be74" "An agent run is requested or running"
create "agent::needs-input"  "#dc143c" "The agent has questions or the run failed"
create "agent::review"       "#8fbc8f" "Draft MR ready for review"
create "agent::done"         "#428bca" "Finished; pod and workspace are removed"
create "agent::blocked"      "#6c757d" "Waits for something outside the issue; nothing to answer"
for reason in infra main-red quota dependency; do
  create "agent-blocked::$reason" "#adb5bd" "Why the issue is blocked"
done
create "agent-profile::fast"   "#6699cc" "Implemented by the fast profile"
create "agent-profile::strong" "#5843ad" "Implemented by the strong profile"
create "ai-generated"        "#cccccc" "Created by the spec agent"
create "ci::main-red"        "#c92a2a" "The default branch is red"
create "ci::flaky"           "#f08c00" "A flaky CI job"
