#!/usr/bin/env python3
"""Enforce the initial FinalWeave Go dependency boundaries."""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path


MODULE = "github.com/wowtrust/final-weave"
ROOT = Path(__file__).resolve().parents[1]
PUBLIC_ROOT = MODULE + "/pkg"
INTERNAL_ROOT = MODULE + "/internal"
TESTKIT_ROOT = PUBLIC_ROOT + "/testkit"


def in_package_tree(package: str, root: str) -> bool:
    return package == root or package.startswith(root + "/")


def standard_packages() -> set[str]:
    result = subprocess.run(
        ["go", "list", "std"],
        check=True,
        capture_output=True,
        cwd=ROOT,
        text=True,
    )
    return set(result.stdout.splitlines())


def go_packages() -> list[tuple[str, list[str]]]:
    template = "{{.ImportPath}}\t{{join .Imports \" \"}}"
    result = subprocess.run(
        ["go", "list", "-mod=readonly", "-f", template, "./..."],
        check=True,
        capture_output=True,
        cwd=ROOT,
        text=True,
    )
    packages: list[tuple[str, list[str]]] = []
    for line in result.stdout.splitlines():
        path, imports = (line.split("\t", 1) + [""])[:2]
        packages.append((path, imports.split()))
    return packages


def main() -> int:
    errors: list[str] = []
    standard = standard_packages()

    for package, imports in go_packages():
        module_imports = [item for item in imports if in_package_tree(item, MODULE)]
        non_standard_imports = [item for item in imports if item not in standard]

        if in_package_tree(package, PUBLIC_ROOT):
            forbidden = [item for item in module_imports if in_package_tree(item, INTERNAL_ROOT)]
            for item in forbidden:
                errors.append(f"{package} must not import internal package {item}")

        if package == MODULE + "/pkg/types" and non_standard_imports:
            errors.append(
                f"{package} must remain a standard-library-only dependency leaf; "
                f"found {', '.join(non_standard_imports)}"
            )

        if package == MODULE + "/internal/buildinfo" and non_standard_imports:
            errors.append(
                f"{package} must use only the standard library; "
                f"found {', '.join(non_standard_imports)}"
            )

        if not in_package_tree(package, TESTKIT_ROOT):
            for item in imports:
                if in_package_tree(item, TESTKIT_ROOT):
                    errors.append(f"{package} must not depend on testkit package {item}")

    if errors:
        print("Go architecture check failed:", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
        return 1

    print("Go architecture check passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
