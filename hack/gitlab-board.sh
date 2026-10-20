#!/usr/bin/env bash
# Creates the issue board "Agents" with one list per ai-operator workflow label,
# in a GitLab group or project. Existing boards and lists are never removed or
# renamed, so running it again changes nothing.
#   GITLAB_URL (default https://gitlab.com), GITLAB_TOKEN (your own token, api
#   scope, Maintainer on the target) and exactly one of GITLAB_GROUP (my-group)
#   or GITLAB_PROJECT (my-group/project)
# The labels must exist: hack/sandbox/gitlab-setup.sh (GITLAB_GROUP for group labels).
set -euo pipefail
: "${GITLAB_TOKEN:?set GITLAB_TOKEN (your own token with the api scope and Maintainer on the group or project)}"
GITLAB_URL="${GITLAB_URL:-https://gitlab.com}"
if [[ -n "${GITLAB_GROUP:-}" && -z "${GITLAB_PROJECT:-}" ]]; then
  base="/groups/$(printf '%s' "$GITLAB_GROUP" | sed 's#/#%2F#g')"
  target="group ${GITLAB_GROUP}"
  setup="GITLAB_GROUP=${GITLAB_GROUP}"
elif [[ -n "${GITLAB_PROJECT:-}" && -z "${GITLAB_GROUP:-}" ]]; then
  base="/projects/$(printf '%s' "$GITLAB_PROJECT" | sed 's#/#%2F#g')"
  target="project ${GITLAB_PROJECT}"
  setup="GITLAB_PROJECT=${GITLAB_PROJECT}"
else
  echo "set exactly one of GITLAB_GROUP (e.g. my-group) or GITLAB_PROJECT (e.g. my-group/shop)" >&2
  exit 1
fi
command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

board_name="Agents"
labels=("agent::draft" "agent::ready" "agent::working" "agent::needs-input" "agent::review" "agent::done")

# request METHOD PATH [curl args...] sets $code and $body.
request() {
  local method="$1" path="$2" out
  shift 2
  out="$(curl -sS -X "$method" -H "PRIVATE-TOKEN: ${GITLAB_TOKEN}" -w '\n%{http_code}' "$@" \
    "${GITLAB_URL}/api/v4${base}${path}")"
  code="${out##*$'\n'}"
  body="${out%$'\n'*}"
}

get() {
  request GET "$1"
  if [[ "$code" != 200 ]]; then
    echo "failed   GET ${base}$1 (HTTP ${code}): ${body}" >&2
    exit 1
  fi
}

# Look up all labels first, so a missing one stops the script before it
# changes anything. Project lookups include the labels of the parent groups.
label_ids=()
missing=()
for name in "${labels[@]}"; do
  get "/labels?per_page=100&search=$(jq -rn --arg s "$name" '$s|@uri')"
  id="$(jq -r --arg n "$name" 'map(select(.name == $n))[0].id // empty' <<<"$body")"
  if [[ -z "$id" ]]; then
    missing+=("$name")
  fi
  label_ids+=("${id:-0}")
done
if (( ${#missing[@]} > 0 )); then
  echo "missing labels in ${target}: ${missing[*]}" >&2
  echo "create them first: ${setup} GITLAB_TOKEN=... hack/sandbox/gitlab-setup.sh" >&2
  exit 1
fi

changed=0
get "/boards?per_page=100"
boards="$body"
board="$(jq -c --arg n "$board_name" 'map(select(.name == $n))[0] // empty' <<<"$boards")"
if [[ -n "$board" ]]; then
  echo "exists   board ${board_name}"
else
  request POST "/boards" --data-urlencode "name=${board_name}"
  if [[ "$code" == 201 ]]; then
    board="$body"
    changed=1
    echo "created  board ${board_name}"
  elif [[ "$(jq length <<<"$boards")" == 1 ]]; then
    # GitLab Free allows one board: add the lists to that one.
    board="$(jq -c '.[0]' <<<"$boards")"
    echo "using    board $(jq -r .name <<<"$board") (creating ${board_name} failed with HTTP ${code}; GitLab Free allows one board)"
  else
    echo "failed   board ${board_name} (HTTP ${code}): ${body}" >&2
    echo "if ${target} has no board yet, open its issue board page once and run again" >&2
    exit 1
  fi
fi
board_id="$(jq -r .id <<<"$board")"
used="$(jq -r .name <<<"$board")"

get "/boards/${board_id}/lists"
lists="$body"
for i in "${!labels[@]}"; do
  name="${labels[$i]}" id="${label_ids[$i]}"
  if jq -e --arg n "$name" --argjson id "$id" 'any(.[]; .label.id == $id or .label.name == $n)' <<<"$lists" >/dev/null; then
    echo "exists   list ${name}"
    continue
  fi
  request POST "/boards/${board_id}/lists" --data-urlencode "label_id=${id}"
  case "$code" in
    201) echo "created  list ${name}"; changed=1 ;;
    *) echo "failed   list ${name} (HTTP ${code}): ${body}" >&2; exit 1 ;;
  esac
done

if (( changed )); then
  echo "done: board ${used} in ${target} has all workflow lists"
else
  echo "nothing to change: board ${used} in ${target} already has all workflow lists"
fi
