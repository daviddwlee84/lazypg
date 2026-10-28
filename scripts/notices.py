#!/usr/bin/env python3
"""Collect license/notice texts from modules used by supported binary targets."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys

OUTPUT = Path("THIRD_PARTY_NOTICES.txt")

def documents(root):
    return sorted(p for p in root.rglob("*") if p.is_file() and
                  p.name.upper().split(".")[0] in {"LICENSE", "NOTICE", "COPYING", "COPYRIGHT", "PATENTS"})

def render():
    modules = {}
    decoder = json.JSONDecoder()
    for system in ("darwin", "linux"):
        for arch in ("amd64", "arm64"):
            env = {**os.environ, "GOOS": system, "GOARCH": arch, "CGO_ENABLED": "0"}
            text = subprocess.check_output(["go", "list", "-deps", "-json", "./cmd/lazypg"], env=env, text=True)
            while text.strip():
                package, end = decoder.raw_decode(text.lstrip())
                text = text.lstrip()[end:]
                module = package.get("Module", {})
                if module and not module.get("Main"):
                    if module.get("Replace"):
                        raise ValueError("resolve replacement licensing explicitly")
                    modules[(module["Path"], module["Version"])] = Path(module["Dir"])
    sections = ["Third-party notices for lazypg\n\nProject code is MIT; dependencies retain the terms reproduced below.\nThis inventory includes notice files shipped with modules used by at least one\nsupported binary target; individual files can cover portions not used by lazypg.\nRegenerate with the release Go toolchain: python3 scripts/notices.py\n"]
    goroot = Path(subprocess.check_output(["go", "env", "GOROOT"], text=True).strip())
    standard = [goroot / "LICENSE"]
    if (goroot / "PATENTS").exists():
        standard.append(goroot / "PATENTS")
    standard += documents(goroot / "src/vendor")
    for label, root, files in [("Go standard library and vendored notices", goroot, standard), *[(path+" "+version, directory, documents(directory)) for (path, version), directory in sorted(modules.items())]]:
        if not files:
            raise ValueError("no license files found for " + label)
        for file in files:
            sections.append("\n" + "=" * 72 + "\n" + label + " — " + str(file.relative_to(root)) + "\n" + "=" * 72 + "\n\n" + file.read_text().rstrip() + "\n")
    return "".join(sections)

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    text = render()
    if args.check:
        if not OUTPUT.exists() or OUTPUT.read_text() != text:
            sys.exit("Third-party notices are stale; run python3 scripts/notices.py with the release Go toolchain")
        print("Third-party notices verified")
    else:
        OUTPUT.write_text(text)
        print("Generated", OUTPUT)

if __name__ == "__main__":
    main()
