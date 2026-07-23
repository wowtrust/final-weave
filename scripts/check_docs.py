#!/usr/bin/env python3
"""Validate repository Markdown entry points and local file links."""

from __future__ import annotations

import re
import sys
from html import unescape
from pathlib import Path
from urllib.parse import unquote, urlsplit


ROOT = Path(__file__).resolve().parents[1]
REQUIRED_FILES = (
    "README.md",
    "CONTRIBUTING.md",
    "SECURITY.md",
    "CODE_OF_CONDUCT.md",
    "LICENSE",
    "doc/README.md",
    "doc/01-system-architecture.md",
    "doc/02-requirements-and-invariants.md",
    "doc/protocol/README.md",
    "doc/engineering/README.md",
    "doc/tutorial/README.md",
    "doc/decisions/README.md",
)

INLINE_LINK = re.compile(r"!?\[[^\]]*\]\(([^)]+)\)")
REFERENCE_LINK = re.compile(r"^\s*\[[^\]]+\]:\s*(\S+)")
INLINE_CODE = re.compile(r"`[^`]*`")
HEADING = re.compile(r"^\s{0,3}#{1,6}\s+(.+?)\s*$")
TRAILING_HEADING_MARKS = re.compile(r"\s+#+\s*$")
MARKDOWN_LINK_TEXT = re.compile(r"!?\[([^\]]*)\]\([^)]+\)")
HTML_TAG = re.compile(r"<[^>]+>")
HTML_ANCHOR = re.compile(r"<a\s+(?:[^>]*?\s)?(?:id|name)=[\"']([^\"']+)[\"']", re.I)


def markdown_files() -> list[Path]:
    return sorted(
        path
        for path in ROOT.rglob("*.md")
        if ".git" not in path.parts
    )


def without_fenced_code(path: Path) -> list[tuple[int, str]]:
    output: list[tuple[int, str]] = []
    fence: str | None = None

    for number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        stripped = line.lstrip()
        marker = None
        if stripped.startswith("```"):
            marker = "```"
        elif stripped.startswith("~~~"):
            marker = "~~~"

        if marker is not None:
            if fence is None:
                fence = marker
            elif fence == marker:
                fence = None
            continue

        if fence is None:
            output.append((number, INLINE_CODE.sub("", line)))

    return output


def destination(raw: str) -> str:
    value = raw.strip()
    if value.startswith("<") and ">" in value:
        return value[1 : value.index(">")]
    return value.split(maxsplit=1)[0]


def github_slug(text: str) -> str:
    """Approximate GitHub's heading slugger for repository Markdown."""

    value = TRAILING_HEADING_MARKS.sub("", text.strip())
    value = MARKDOWN_LINK_TEXT.sub(lambda match: match.group(1), value)
    value = re.sub(r"`+([^`]+?)`+", r"\1", value)
    value = unescape(HTML_TAG.sub("", value)).lower()
    value = re.sub(r"[^\w\-\s]", "", value, flags=re.UNICODE)
    return re.sub(r"\s", "-", value)


def anchors(path: Path) -> set[str]:
    result: set[str] = set()
    seen: dict[str, int] = {}
    fence: str | None = None

    for line in path.read_text(encoding="utf-8").splitlines():
        stripped = line.lstrip()
        marker = None
        if stripped.startswith("```"):
            marker = "```"
        elif stripped.startswith("~~~"):
            marker = "~~~"

        if marker is not None:
            if fence is None:
                fence = marker
            elif fence == marker:
                fence = None
            continue
        if fence is not None:
            continue

        for explicit in HTML_ANCHOR.findall(line):
            result.add(unquote(explicit))

        heading = HEADING.match(line)
        if not heading:
            continue
        base = github_slug(heading.group(1))
        if not base:
            continue
        duplicate = seen.get(base, 0)
        seen[base] = duplicate + 1
        result.add(base if duplicate == 0 else f"{base}-{duplicate}")

    return result


def has_exact_case(relative: Path) -> bool:
    current = ROOT
    for part in relative.parts:
        try:
            names = {entry.name for entry in current.iterdir()}
        except OSError:
            return False
        if part not in names:
            return False
        current = current / part
    return True


def validate_local_link(
    source: Path,
    line: int,
    raw: str,
    anchor_index: dict[Path, set[str]],
) -> str | None:
    target = destination(raw)
    if not target or target.startswith("//"):
        return None

    parsed = urlsplit(target)
    if parsed.scheme or parsed.netloc:
        return None

    decoded = unquote(parsed.path)
    if not decoded and parsed.fragment:
        candidate = source.resolve()
    elif not decoded:
        return None
    else:
        if decoded.startswith("/"):
            return f"{source.relative_to(ROOT)}:{line}: absolute local path is not portable: {target}"
        candidate = (source.parent / decoded).resolve()
    try:
        relative = candidate.relative_to(ROOT)
    except ValueError:
        return f"{source.relative_to(ROOT)}:{line}: link escapes repository: {target}"

    if not candidate.exists():
        return f"{source.relative_to(ROOT)}:{line}: missing local target: {target}"
    if not has_exact_case(relative):
        return f"{source.relative_to(ROOT)}:{line}: target case does not match repository: {target}"
    if parsed.fragment and candidate.suffix.lower() == ".md":
        fragment = unquote(parsed.fragment)
        if fragment not in anchor_index.get(candidate, set()):
            return f"{source.relative_to(ROOT)}:{line}: missing Markdown anchor: {target}"
    return None


def main() -> int:
    errors: list[str] = []

    for required in REQUIRED_FILES:
        path = ROOT / required
        if not path.is_file() or path.stat().st_size == 0:
            errors.append(f"missing or empty required file: {required}")

    docs = sorted((ROOT / "doc").rglob("*.md"))
    if len(docs) < 34:
        errors.append(f"expected at least 34 Markdown documents under doc/, found {len(docs)}")

    checked_links = 0
    files = markdown_files()
    anchor_index = {path.resolve(): anchors(path) for path in files}
    for path in files:
        for number, line in without_fenced_code(path):
            targets = [match.group(1) for match in INLINE_LINK.finditer(line)]
            reference = REFERENCE_LINK.match(line)
            if reference:
                targets.append(reference.group(1))

            for raw in targets:
                checked_links += 1
                error = validate_local_link(path, number, raw, anchor_index)
                if error:
                    errors.append(error)

    if errors:
        print("Documentation integrity check failed:", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
        return 1

    print(
        f"Documentation integrity passed: {len(files)} Markdown files, "
        f"{len(docs)} files under doc/, {checked_links} links checked."
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
