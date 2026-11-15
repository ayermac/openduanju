#!/usr/bin/env python3
"""Validate tracked entry points and local handoff documents when available."""
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
LOCAL_DOCS = ROOT / ".doc"
has_local_docs = LOCAL_DOCS.is_dir()
REQUIRED = ["AGENTS.md", "README.md", ".doc/README.md", ".doc/product/requirements.md", ".doc/architecture/overview.md", ".doc/development/roadmap.md", ".doc/development/progress.md", ".doc/testing/strategy.md", ".doc/operations/runbook.md", ".doc/changes/CHANGELOG.md", "server/AGENTS.md", "admin/AGENTS.md", "web/AGENTS.md"]
errors = []
for name in REQUIRED:
    if name.startswith(".doc/") and not has_local_docs:
        continue
    if not (ROOT / name).is_file():
        errors.append("Missing required document: " + name)
files = ([ROOT / "README.md", ROOT / "AGENTS.md", ROOT / "CONTRIBUTING.md", ROOT / "CHANGELOG.md"]
         + list(ROOT.glob("*/README.md"))
         + list((ROOT / "docs").rglob("*.md"))
         + list((ROOT / ".doc").rglob("*.md")))
for path in files:
    if not path.is_file():
        continue  # Missing required files have already been reported above.
    content = path.read_text()
    if sum(line.startswith("```") for line in content.splitlines()) % 2:
        errors.append(str(path.relative_to(ROOT)) + ": unbalanced code fences")
    for match in re.finditer(r"\]\(([^)]+)\)", content):
        target = match.group(1).split("#", 1)[0]
        if not target or "://" in target or target.startswith("mailto:"):
            continue
        if not has_local_docs and (path.parent / target).resolve().is_relative_to(LOCAL_DOCS):
            continue
        if not (path.parent / target).exists():
            errors.append(str(path.relative_to(ROOT)) + ": broken link " + target)
if errors:
    raise SystemExit("\n".join(errors))
if has_local_docs:
    print("Documentation links and required handoff documents: PASS")
else:
    print("Tracked documentation entry points: PASS; local .doc unavailable, local handoff documents NOT CHECKED")
