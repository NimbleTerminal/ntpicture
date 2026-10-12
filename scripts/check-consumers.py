#!/usr/bin/env python3
"""Test the extraction through NTCharts aliases without editing sibling repos.

Requires sibling checkouts of ntcharts, ntcharts3d, ntqrcode and gloss. Copies
tracked/unignored files into a temporary directory; all module edits stay there.
"""
import argparse
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def run(args, cwd, env):
    print(f"[{cwd.name}] {' '.join(map(str, args))}", flush=True)
    subprocess.run(args, cwd=cwd, env=env, check=True)


def copy_checkout(source, dest):
    paths = subprocess.check_output(
        ["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"],
        cwd=source,
    ).decode().split("\0")
    for name in paths:
        if not name:
            continue
        src = source / name
        if src.is_file():
            target = dest / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(src, target)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--projects", type=Path, default=ROOT.parent)
    parser.add_argument("--restricted", action="store_true",
                        help="only compile HTTP tests that require local listening sockets")
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="ntpicture-consumers-") as temporary:
        base = Path(temporary)
        env = os.environ | {"GOWORK": "off", "CGO_ENABLED": "0"}
        env.setdefault("GOCACHE", str(base / "go-cache"))
        for name in ("ntcharts", "ntcharts3d", "ntqrcode", "gloss"):
            copy_checkout(args.projects / name, base / name)
        picture = base / "ntcharts" / "picture"
        for path in picture.glob("*.go"):
            path.unlink()
        shutil.copy2(ROOT / "testdata/compat/picture.go", picture / "picture.go")
        modules = [base / name for name in ("ntcharts", "ntcharts3d", "ntqrcode", "gloss")]
        modules.append(picture / "chartpicture")
        for module in modules:
            run(["go", "mod", "edit", "-require=nimbleterminal.dev/ntpicture@v0.0.0",
                 f"-replace=nimbleterminal.dev/ntpicture={ROOT}",
                 f"-replace=github.com/NimbleMarkets/ntcharts/v2={base / 'ntcharts'}"], module, env)
        checks = [
            (base / "ntcharts", ["./picture/heatpicture"]),
            (picture / "chartpicture", ["./..."]),
            (base / "ntqrcode", ["./qrcode"]),
            (base / "ntcharts3d", ["./..."]),
            (base / "gloss", ["./internal/animate", "./internal/app"]),
        ]
        checks.append((base / "ntcharts", ["./picture/pictureurl"]))
        failures = []
        for directory, packages in checks:
            flags = ["-run=^$"] if args.restricted and packages == ["./picture/pictureurl"] else []
            try:
                run(["go", "test", "-mod=mod", *flags, *packages], directory, env)
            except subprocess.CalledProcessError:
                failures.append(str(directory.relative_to(base)))
        if failures:
            raise SystemExit("Consumer checks failed: " + ", ".join(failures))


if __name__ == "__main__":
    main()
