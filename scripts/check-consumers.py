#!/usr/bin/env python3
"""Test the extraction through NTCharts aliases without editing sibling repos.

Requires sibling checkouts of ntcharts, ntcharts3d, ntqrcode and gloss.
Copies ntcanvas when present to preserve the current local canvas replacement. Copies
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
        env = os.environ | {
            "GOWORK": "off",
            "CGO_ENABLED": "0",
        }
        env.setdefault("GOCACHE", str(base / "go-cache"))
        for name in ("ntcharts", "ntcharts3d", "ntqrcode", "gloss"):
            copy_checkout(args.projects / name, base / name)
        for optional in ("ntcanvas", "nt3d", "ntcharts-svg", "ntcharts-pdf"):
            if (args.projects / optional).is_dir():
                copy_checkout(args.projects / optional, base / optional)
        picture = base / "ntcharts" / "picture"
        for path in picture.glob("*.go"):
            path.unlink()
        shutil.copy2(ROOT / "testdata/compat/picture.go", picture / "picture.go")
        legacy_calls = (
            "KittySupported(",
            "KittyEnvSignalled(",
            "QueryKittySupport(",
            "KittyUnavailable(",
            "RequestCellSize(",
            "IsPictureMsg(",
            "ForceKittyCapability(",
            "SetTmuxPassthrough(",
            "DefaultTerminalSession(",
            "NewModel(",
            "NewModelWithConfig(",
        )
        for go_file in base.rglob("*.go"):
            if go_file == picture / "picture.go":
                continue
            text = go_file.read_text()
            if '\tpicture "nimbleterminal.dev/ntpicture"\n' not in text:
                continue
            if not any(f"picture.{call}" in text for call in legacy_calls):
                continue
            text = text.replace(
                '\tpicture "nimbleterminal.dev/ntpicture"\n',
                '\tpicture "nimbleterminal.dev/ntpicture"\n\t"nimbleterminal.dev/ntpicture/legacy"\n',
            )
            for call in legacy_calls:
                text = text.replace(f"picture.{call}", f"legacy.{call}")
            go_file.write_text(text)
        modules = [base / name for name in ("ntcharts", "ntcharts3d", "ntqrcode", "gloss")]
        modules.append(picture / "chartpicture")
        for module in modules:
            # Preserve published requirements: nested consumer tests deliberately
            # ignore dependency replacements and resolve those versions remotely.
            edits = [
                "go", "mod", "edit",
                f"-replace=nimbleterminal.dev/ntpicture={ROOT}",
                f"-replace=github.com/NimbleMarkets/ntcharts/v2={base / 'ntcharts'}",
            ]
            if (base / "nt3d").is_dir():
                edits.append(f"-replace=nimbleterminal.dev/nt3d={base / 'nt3d'}")
            run(edits, module, env)
        checks = [
            (base / "ntcharts", ["./picture/heatpicture"]),
            (picture / "chartpicture", ["./..."]),
            (base / "ntqrcode", ["./qrcode"]),
            (base / "ntcharts3d", ["./..."]),
            (base / "gloss", ["./internal/app"]),
        ]
        # Older Gloss checkouts still have the standalone animation package.
        if (base / "gloss" / "internal" / "animate").is_dir():
            checks.append((base / "gloss", ["./internal/animate"]))
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
