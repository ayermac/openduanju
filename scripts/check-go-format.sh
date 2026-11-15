#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
python3 - "$root/server" <<'PY'
import subprocess
import sys
from pathlib import Path
files = [str(p) for p in Path(sys.argv[1]).rglob('*.go')]
result = subprocess.run(['gofmt', '-l', *files], capture_output=True, text=True, check=True)
if result.stdout.strip():
    raise SystemExit('Go files need gofmt:\n' + result.stdout)
print('Go formatting: PASS')
PY
