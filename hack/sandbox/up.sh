#!/usr/bin/env bash
# Builds the images, starts a kind cluster and installs the operator and the
# sandbox AgentProject. Run from the repository root:
#
#   export GITLAB_PROJECT=my-group/sandbox-todo GITLAB_TOKEN=glpat-...
#   export CLAUDE_CODE_OAUTH_TOKEN=...      # or ANTHROPIC_API_KEY=...
#   export CURSOR_API_KEY=...               # optional: fast profile + reviews on cursor auto
#   hack/sandbox/up.sh
set -euo pipefail
: "${GITLAB_PROJECT:?set GITLAB_PROJECT}"
: "${GITLAB_TOKEN:?set GITLAB_TOKEN}"
if [[ -z "${CLAUDE_CODE_OAUTH_TOKEN:-}" && -z "${ANTHROPIC_API_KEY:-}" ]]; then
  echo "set CLAUDE_CODE_OAUTH_TOKEN (claude setup-token) or ANTHROPIC_API_KEY" >&2
  exit 1
fi
export GITLAB_URL="${GITLAB_URL:-https://gitlab.com}"
CLUSTER="${CLUSTER:-ai-operator}"
TAG="${TAG:-sandbox}"

if [[ -n "${CURSOR_API_KEY:-}" ]]; then
  export FAST_ENGINE=cursor FAST_MODEL=auto
else
  export FAST_ENGINE=claude FAST_MODEL=claude-sonnet-5-5
fi

render_project() {
  sed -e "s|\${GITLAB_URL}|${GITLAB_URL}|g" \
      -e "s|\${GITLAB_PROJECT}|${GITLAB_PROJECT}|g" \
      -e "s|\${FAST_ENGINE}|${FAST_ENGINE}|g" \
      -e "s|\${FAST_MODEL}|${FAST_MODEL}|g" \
      hack/sandbox/agentproject.yaml
}

echo "==> building images"
docker build -t "ai-operator:${TAG}" .
docker build -f Dockerfile.agent -t "ai-operator-agent:${TAG}" \
  --build-arg INSTALL_CURSOR="$([[ $FAST_ENGINE == cursor ]] && echo true || echo false)" .

echo "==> cluster"
kind get clusters | grep -qx "$CLUSTER" || kind create cluster --name "$CLUSTER"
kubectl config use-context "kind-${CLUSTER}"
kind load docker-image --name "$CLUSTER" "ai-operator:${TAG}" "ai-operator-agent:${TAG}"

echo "==> operator"
helm upgrade --install ai-operator charts/ai-operator \
  --namespace ai-operator-system --create-namespace \
  --set image.repository=ai-operator --set "image.tag=${TAG}" \
  --set agent.image.repository=ai-operator-agent --set "agent.image.tag=${TAG}" \
  --set 'agent.namespaces={agents}' --set agent.createNamespaces=true \
  --set logLevel=debug --wait

echo "==> secrets"
kubectl -n agents create secret generic gitlab --from-literal=token="$GITLAB_TOKEN" \
  --dry-run=client -o yaml | kubectl apply -f -
creds=()
for key in CLAUDE_CODE_OAUTH_TOKEN ANTHROPIC_API_KEY CURSOR_API_KEY; do
  [[ -n "${!key:-}" ]] && creds+=(--from-literal="${key}=${!key}")
done
kubectl -n agents create secret generic agent-credentials "${creds[@]}" \
  --dry-run=client -o yaml | kubectl apply -f -

echo "==> AgentProject"
render_project | kubectl apply -f -

echo
echo "Done. Watch with:"
echo "  kubectl -n agents get agentprojects,agenttasks,pods -w"
echo "  kubectl -n ai-operator-system logs -f deploy/ai-operator"
