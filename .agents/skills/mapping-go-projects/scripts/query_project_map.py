#!/usr/bin/env python3
"""Query the local Go project map."""

from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path
from typing import Any


def load_map(root: Path, map_path: str | None) -> dict[str, Any]:
    path = Path(map_path) if map_path else root / ".project-map" / "project-map.json"
    if not path.is_absolute():
        path = root / path
    if not path.exists():
        raise SystemExit(
            f"project map not found: {path}\n"
            "Run `make project-map` from the repository root first."
        )
    with path.open("r", encoding="utf-8") as fh:
        return json.load(fh)


def is_stale(root: Path, map_file: Path) -> bool:
    if not map_file.exists():
        return True
    map_mtime = map_file.stat().st_mtime
    ignored_dirs = {
        ".git",
        ".agents",
        ".github",
        ".githooks",
        ".project-map",
        "vendor",
        "node_modules",
        "dist",
        "build",
        "tmp",
        "coverage",
        "testdata",
    }
    for current, dirs, files in os.walk(root):
        dirs[:] = [item for item in dirs if item not in ignored_dirs and not item.startswith(".")]
        for name in files:
            if name.endswith(".go") and (Path(current) / name).stat().st_mtime > map_mtime:
                return True
    return False


def match_symbol(symbol: dict[str, Any], query: str, kinds: set[str], exact: bool) -> bool:
    if kinds and symbol.get("kind") not in kinds:
        return False
    fields = [
        symbol.get("name", ""),
        symbol.get("qualified_name", ""),
        symbol.get("package", ""),
        symbol.get("import_path", ""),
        symbol.get("file", ""),
        symbol.get("signature", ""),
        symbol.get("doc", ""),
    ]
    if exact:
        return query in {symbol.get("name", ""), symbol.get("qualified_name", "")}
    needle = query.lower()
    return any(needle in str(field).lower() for field in fields)


def match_package(package: dict[str, Any], query: str, exact: bool) -> bool:
    fields = [
        package.get("name", ""),
        package.get("import_path", ""),
        package.get("dir", ""),
        package.get("doc", ""),
    ]
    if exact:
        return query in {package.get("name", ""), package.get("import_path", ""), package.get("dir", "")}
    needle = query.lower()
    return any(needle in str(field).lower() for field in fields)


def main() -> int:
    parser = argparse.ArgumentParser(description="Query .project-map/project-map.json")
    parser.add_argument("query", help="symbol, package, file, or import search term")
    parser.add_argument("--root", default=".", help="repository root")
    parser.add_argument("--map", dest="map_path", default=None, help="project map JSON path")
    parser.add_argument("--kind", action="append", default=[], help="symbol kind filter: func, method, struct, interface, type, const, var")
    parser.add_argument("--exact", action="store_true", help="match exact symbol/package names")
    parser.add_argument("--json", action="store_true", help="emit JSON results")
    parser.add_argument("--limit", type=int, default=50, help="maximum result rows")
    parser.add_argument("--no-stale-warning", action="store_true", help="suppress stale map warning")
    args = parser.parse_args()

    root = Path(args.root).resolve()
    map_file = Path(args.map_path) if args.map_path else root / ".project-map" / "project-map.json"
    if not map_file.is_absolute():
        map_file = root / map_file

    data = load_map(root, args.map_path)
    if not args.no_stale_warning and is_stale(root, map_file):
        print("WARN: project map may be stale; run `make project-map`.", file=sys.stderr)

    kinds = {kind.lower() for kind in args.kind}
    symbol_hits = [
        symbol
        for symbol in data.get("symbols", [])
        if match_symbol(symbol, args.query, kinds, args.exact)
    ]
    package_hits = [
        package
        for package in data.get("packages", [])
        if not kinds and match_package(package, args.query, args.exact)
    ]

    result = {
        "query": args.query,
        "packages": package_hits[: args.limit],
        "symbols": symbol_hits[: args.limit],
        "truncated": len(package_hits) + len(symbol_hits) > args.limit,
    }
    if args.json:
        print(json.dumps(result, ensure_ascii=False, indent=2))
        return 0

    if package_hits:
        print("PACKAGES")
        for package in package_hits[: args.limit]:
            print(
                f"{package.get('import_path')}\t{package.get('dir')}\t"
                f"files={len(package.get('files', []))}\t{package.get('doc', '')}"
            )
    if symbol_hits:
        print("SYMBOLS")
        for symbol in symbol_hits[: args.limit]:
            location = f"{symbol.get('file')}:{symbol.get('line')}"
            receiver = symbol.get("receiver")
            receiver_text = f" receiver={receiver}" if receiver else ""
            print(
                f"{symbol.get('kind')}\t{symbol.get('qualified_name')}\t{location}\t"
                f"{symbol.get('signature', '')}{receiver_text}"
            )
    if not package_hits and not symbol_hits:
        print(f"No project-map matches for {args.query!r}.")
        return 1
    if result["truncated"]:
        print(f"WARN: results truncated to {args.limit}; use --limit to expand.", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
