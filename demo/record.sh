#!/usr/bin/env bash
set -euo pipefail

# Records the demo: for each tape, a fresh sandbox (demo/setup.sh <tape>),
# then VHS. Outputs land in demo/out/, with the fake GitHub's call log and
# supatree's logs beside each recording for when a tape goes wrong.
#
#   demo/record.sh              every tape
#   demo/record.sh pm-review    one
#
# Tapes record MP4. DEMO_GIF=1 also converts each to a GIF, which takes
# longer than the recording; CI does it only for main and releases.

REPO=$(cd "$(dirname "$0")/.." && pwd)
cd "$REPO"
mkdir -p demo/out
tapes=("$@")
if [ ${#tapes[@]} -eq 0 ]; then
    for t in demo/tapes/*.tape; do tapes+=("$(basename "$t" .tape)"); done
fi
for tape in "${tapes[@]}"; do
    echo "=== $tape"
    demo/setup.sh "$tape"
    # VHS keeps the real HOME, where its browser lives; the shell it records
    # enters the sandbox first, off camera.
    status=0
    vhs "demo/tapes/$tape.tape" || status=$?
    (
        eval "$(demo/setup.sh env)"
        zellij kill-all-sessions -y >/dev/null 2>&1 || true
        cp "$HOME/demo/gh/calls.log" "demo/out/$tape.gh-calls.log" 2>/dev/null || true
        cp "$HOME/demo/gh/unsupported.log" "demo/out/$tape.gh-unsupported.log" 2>/dev/null || true
        cp -r "$XDG_STATE_HOME/supatree/logs" "demo/out/$tape.logs" 2>/dev/null || true
    )
    [ "$status" -eq 0 ] || { echo "vhs failed on $tape ($status)" >&2; exit "$status"; }
    if [ -n "${DEMO_GIF:-}" ]; then
        ffmpeg -loglevel error -y -i "demo/out/$tape.mp4" \
            -vf "fps=12,scale=1280:-1:flags=lanczos,split[a][b];[a]palettegen=stats_mode=diff[p];[b][p]paletteuse=dither=bayer" \
            "demo/out/$tape.gif"
    fi
done
