#!/usr/bin/env bash
# Internal media smoke check: real ffmpeg processing plus real browser playback.
#
# Scope: generated, non-source media only. It proves the local normalize and
# preview path; it does NOT prove SDK resolution or COS delivery, which need
# authorized external inputs and are reported separately.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/openduanju-media-smoke.XXXXXX")"
trap 'rm -rf "$work"' EXIT

for tool in ffmpeg ffprobe node go python3; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "media-smoke: missing $tool" >&2
    exit 2
  fi
done

echo "media-smoke: workdir $work"
ffmpeg -nostdin -hide_banner -loglevel error -y \
  -f lavfi -i "testsrc=size=720x1280:rate=24" \
  -f lavfi -i "sine=frequency=440:sample_rate=44100" \
  -t 3 -c:v libx264 -preset ultrafast -pix_fmt yuv420p -c:a aac -movflags +faststart \
  "$work/compatible.mp4"
ffmpeg -nostdin -hide_banner -loglevel error -y \
  -f lavfi -i "testsrc=size=320x240:rate=15" \
  -f lavfi -i "sine=frequency=440:sample_rate=44100" \
  -t 2 -c:v mpeg4 -c:a pcm_s16le \
  "$work/incompatible.avi"

run_cli() {
  local input="$1" out="$2"
  ( cd "$root/server" && \
    KRATOS_MEDIA_TEMP_DIR="$work/tmp" \
    go run ./cmd/openduanju-media -conf ./configs -input "$input" -process-only -keep "$work/preview" \
    >"$out" 2>"$out.log" )
}

echo "media-smoke: remux path"
run_cli "$work/compatible.mp4" "$work/remux.json"
python3 - "$work/remux.json" <<'PY'
import json, sys
data = json.load(open(sys.argv[1]))
assert data["ok"], data
stages = [s["stage"] for s in data["stages"]]
assert stages == ["acquire", "validate", "process"], stages
assert data["processed_path"].endswith(".mp4"), data
print("media-smoke: remux ok stages=", ",".join(stages))
PY

echo "media-smoke: browser playback"
node "$root/scripts/media-playback-check.mjs" --file "$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["processed_path"])' "$work/remux.json")" \
  | tee "$work/playback.json"
python3 - "$work/playback.json" <<'PY'
import json, sys
data = json.load(open(sys.argv[1]))
assert data["ok"] and not data["failures"], data
assert data["videoWidth"] > 0 and data["videoHeight"] > 0, data
print(f"media-smoke: browser ok {data['videoWidth']}x{data['videoHeight']} played={data['playedTo']} seek={data['afterSeek']}")
PY

echo "media-smoke: transcode path"
run_cli "$work/incompatible.avi" "$work/transcode.json"
python3 - "$work/transcode.json" <<'PY'
import json, sys
data = json.load(open(sys.argv[1]))
assert data["ok"], data
process = [s for s in data["stages"] if s["stage"] == "process"]
assert process and "transcode" in process[0]["detail"], data
print("media-smoke: transcode ok")
PY

echo "media-smoke: PASS (internal path only; SDK and COS not exercised)"
