#!/usr/bin/env bash
# Creates the ai-operator labels in a GitLab project.
#   GITLAB_URL (default https://gitlab.com), GITLAB_PROJECT (group/project), GITLAB_TOKEN
set -euo pipefail
: "${GITLAB_PROJECT:?set GITLAB_PROJECT, e.g. my-group/sandbox-todo}"
: "${GITLAB_TOKEN:?set GITLAB_TOKEN (project access token, Developer, api + write_repository)}"
GITLAB_URL="${GITLAB_URL:-https://gitlab.com}"
project="$(printf '%s' "$GITLAB_PROJECT" | sed 's#/#%2F#g')"

create() {
  local name="$1" color="$2" description="$3"
  local code
  code="$(curl -sS -o /dev/null -w '%{http_code}' -X POST \
    -H "PRIVATE-TOKEN: ${GITLAB_TOKEN}" \
    --data-urlencode "name=${name}" --data-urlencode "color=${color}" \
    --data-urlencode "description=${description}" \
    "${GITLAB_URL}/api/v4/projects/${project}/labels")"
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
create "agent-profile::fast"   "#6699cc" "Implemented by the fast profile"
create "agent-profile::strong" "#5843ad" "Implemented by the strong profile"
create "ai-generated"        "#cccccc" "Created by the spec agent"
