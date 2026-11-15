#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
before="$(mktemp)"
after="$(mktemp)"
trap 'rm -f "$before" "$after"' EXIT
snapshot() {
  python3 - "$root/server" <<'PYCODE'
import hashlib
import sys
from pathlib import Path
root = Path(sys.argv[1])
for p in sorted(root.rglob('*')):
    if p.is_file() and (p.name.endswith('.pb.go') or p.name == 'wire_gen.go' or '/internal/data/ent/' in str(p) or p.name in ('openapi.yaml','go.mod','go.sum')):
        if '/ent/schema/' not in str(p):
            print(str(p.relative_to(root)), hashlib.sha256(p.read_bytes()).hexdigest())
PYCODE
}
snapshot > "$before"
make -C "$root/server" all
snapshot > "$after"
diff -u "$before" "$after"
