#!/usr/bin/env bash
# Prints the next release version in the form YEAR.MONTH.N (e.g. 2026.10.3):
# N starts at 1 every month and counts the releases of that month, based on
# the existing git tags. Months have no leading zero, so the versions are
# valid SemVer (required for Helm charts): 2026.1.1, ..., 2026.12.4.
set -euo pipefail

prefix="$(date -u +%Y).$(date -u +%-m)"
last="$(git tag --list "${prefix}.*" | sed -n "s/^${prefix//./\\.}\.\([0-9][0-9]*\)$/\1/p" | sort -n | tail -n1)"
echo "${prefix}.$(( ${last:-0} + 1 ))"
