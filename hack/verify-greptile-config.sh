#!/bin/bash
# Copyright 2026 NVIDIA CORPORATION
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Validate the .greptile/ review configuration.
#
# The failure this exists to catch: a rule `scope` glob, or a files.json path,
# that stops matching anything after a package move or a rename. Greptile fails
# open on both, so the rule simply stops applying and no error is reported
# anywhere. Without this check that drift is invisible until someone notices a
# class of review comment has quietly disappeared.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

python3 - "${REPO_ROOT}" <<'PY'
import json
import re
import subprocess
import sys
from pathlib import Path

root = Path(sys.argv[1])
cfg_dir = root / ".greptile"
failures = []
checks = 0


def check(ok, label, detail=""):
    global checks
    checks += 1
    if ok:
        print(f"  ok    {label}")
    else:
        print(f"  FAIL  {label}{(': ' + detail) if detail else ''}")
        failures.append(label)


def glob_to_re(pat):
    """Translate a glob to a regex with correct ** and / semantics."""
    out, i = [], 0
    while i < len(pat):
        if pat.startswith("**/", i):
            out.append("(?:.*/)?")
            i += 3
        elif pat.startswith("**", i):
            out.append(".*")
            i += 2
        elif pat[i] == "*":
            out.append("[^/]*")
            i += 1
        elif pat[i] == "?":
            out.append("[^/]")
            i += 1
        else:
            out.append(re.escape(pat[i]))
            i += 1
    return re.compile("^" + "".join(out) + "$")


tracked = subprocess.run(
    ["git", "ls-files"], cwd=root, capture_output=True, text=True, check=True
).stdout.splitlines()
print(f"tracked files: {len(tracked)}\n")

print("JSON parses:")
docs = {}
for name in ("config.json", "files.json"):
    path = cfg_dir / name
    try:
        docs[name] = json.loads(path.read_text())
        check(True, f".greptile/{name} is valid JSON")
    except Exception as exc:
        check(False, f".greptile/{name} is valid JSON", str(exc))

if len(docs) != 2:
    print("\ncannot continue without both JSON documents")
    sys.exit(1)

cfg = docs["config.json"]
rules = cfg.get("rules", [])

print("\nrule shape:")
check(bool(rules), "config.json declares at least one rule")
ids = [r.get("id") for r in rules]
check(all(ids), "every rule carries an id", f"ids={ids}")
check(len(ids) == len(set(ids)), "rule ids are unique", f"ids={ids}")
for r in rules:
    rid = r.get("id", "<no id>")
    check(bool(r.get("rule", "").strip()), f"rule {rid} has non-empty text")
    check(
        r.get("severity") in ("low", "medium", "high"),
        f"rule {rid} severity is low|medium|high",
        repr(r.get("severity")),
    )

print("\nrule scope globs match tracked files:")
for r in rules:
    rid = r.get("id", "<no id>")
    for pat in r.get("scope", []):
        rx = glob_to_re(pat)
        n = sum(1 for f in tracked if rx.match(f))
        check(n > 0, f"rule {rid} scope {pat!r} matches {n} file(s)", "matches nothing")

print("\nfiles.json context paths exist:")
files_doc = docs["files.json"]
check(isinstance(files_doc, dict) and "files" in files_doc,
      "files.json is an object with a 'files' key")
for entry in files_doc.get("files", []):
    p = entry.get("path", "")
    check((root / p).is_file(), f"context file {p!r} exists")
    for pat in entry.get("scope", []):
        rx = glob_to_re(pat)
        n = sum(1 for f in tracked if rx.match(f))
        check(n > 0, f"context {p!r} scope {pat!r} matches {n} file(s)", "matches nothing")

print("\nignorePatterns refer to real paths:")
raw = cfg.get("ignorePatterns", "")
check(isinstance(raw, str), "ignorePatterns is a string in .gitignore syntax",
      f"got {type(raw).__name__}")
for line in [l.strip() for l in str(raw).splitlines() if l.strip()]:
    if line.startswith("**"):
        rx = glob_to_re(line)
        n = sum(1 for f in tracked if rx.match(f))
        ok, detail = n > 0, "matches no tracked file"
    else:
        target = root / line.rstrip("/")
        n = 1 if target.exists() else 0
        ok, detail = target.exists(), "path does not exist"
    check(ok, f"ignore {line!r} resolves ({n})", detail)

print("\ntone guard on the config text itself:")
banned = {"—": "em dash"}
for name in ("config.json", "files.json", "rules.md"):
    text = (cfg_dir / name).read_text()
    hits = [lbl for ch, lbl in banned.items() if ch in text]
    emoji = [c for c in text if 0x1F300 <= ord(c) <= 0x1FAFF or 0x2600 <= ord(c) <= 0x27BF]
    check(not hits and not emoji, f"{name} carries no em dash or emoji",
          f"{hits} {emoji}")

print(f"\n{checks - len(failures)}/{checks} checks passed")
if failures:
    print(f"FAILED: {len(failures)}")
    sys.exit(1)
print("OK")
PY
