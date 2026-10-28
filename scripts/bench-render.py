#!/usr/bin/env python3
"""Capture repeatable Go benchmark samples and narrowly scoped provenance."""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess
import sys

ROOT = Path(__file__).resolve().parent.parent


def capture(args):
    return subprocess.check_output(args, cwd=ROOT, text=True).strip()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", required=True, type=Path)
    parser.add_argument("--count", type=int, default=5)
    parser.add_argument("--benchtime", default="200ms")
    parser.add_argument("--filter", default="Benchmark(Presentation|PlaybackCache)$")
    args = parser.parse_args()
    if args.count < 1:
        parser.error("--count must be positive")
    args.out.mkdir(parents=True, exist_ok=False)
    filenames = capture(["git", "ls-files", "--cached", "--others", "--exclude-standard"]).splitlines()
    hashes = {}
    for name in filenames:
        path = ROOT / name
        if path.is_file() and (path.suffix in (".go", ".mod", ".sum") or name == "Taskfile.yml"):
            hashes[name] = hashlib.sha256(path.read_bytes()).hexdigest()
    command = ["task", "bench-render", "--", "-count=" + str(args.count),
               "-benchtime=" + args.benchtime, "-bench=" + args.filter]
    env = dict(os.environ, GOWORK="off")
    metadata = {
        "schema": 1,
        "started_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "revision": capture(["git", "rev-parse", "HEAD"]),
        "working_tree": capture(["git", "status", "--short"]),
        "platform": platform.platform(),
        "machine": platform.machine(),
        "go": capture(["go", "version"]),
        "command": command,
        "measurement": "synchronous command execution to discard sink; not terminal display FPS",
        "source_sha256": hashes,
    }
    with (args.out / "bench.txt").open("w") as stdout, (args.out / "stderr.txt").open("w") as stderr:
        result = subprocess.run(command, cwd=ROOT, env=env, stdout=stdout, stderr=stderr)
    metadata["exit_code"] = result.returncode
    (args.out / "metadata.json").write_text(json.dumps(metadata, indent=2) + "\n")
    print(args.out / "bench.txt")
    return result.returncode


if __name__ == "__main__":
    sys.exit(main())
