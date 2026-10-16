#!/usr/bin/env bash
# Deletes the sandbox kind cluster (GitLab state is left alone).
set -euo pipefail
kind delete cluster --name "${CLUSTER:-ai-operator}"
