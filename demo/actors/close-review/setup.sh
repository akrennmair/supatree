#!/usr/bin/env bash
set -euo pipefail
# The scene starts after pm-review: the review tree exists, its reviewer has
# posted both reviews, and the authors have merged.
eval "$(demo/setup.sh env)"
supatree review acme/api#17 acme/web#42 --name review-rate-limit \
    --intent "Rate limiting (api#17) with its checkout errors (web#42)" >/dev/null
root="$HOME/supatree/trees/review-rate-limit"
# The reviewer's own script, headless: the same reviews pm-review posts.
(cd "$root" && SUPATREE=1 SUPATREE_NAME=review-rate-limit SUPATREE_ROOT="$root" \
    SUPATREE_AGENT=main SUPATREE_MODE=reviewing SUPATREE_MEMBERS=api,docs,web \
    timeout 60 claude </dev/null >/dev/null 2>&1) || true
supatree watch --once >/dev/null 2>&1 || true
gh demo merge acme/api 17
gh demo merge acme/web 42
supatree watch --once >/dev/null 2>&1 || true
