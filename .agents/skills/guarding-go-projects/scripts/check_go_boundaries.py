#!/usr/bin/env python3
"""Check coarse Go package boundary rules for modular Go projects."""

from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path


FORBIDDEN_PACKAGE_NAMES = {"common", "utils", "shared", "models", "types", "helpers"}
SIDECAR_COMMANDS = {"mcp-lsp", "mcp-orch"}

FORBIDDEN_EXTERNAL_BY_LAYER = {
    "domain": {
        "database/sql": "domain must not import database APIs",
        "net/http": "domain must not import transport APIs",
        "log": "domain must not import logging APIs",
        "log/slog": "domain must not import logging APIs",
        "os": "domain must not import OS APIs",
        "github.com/gin-gonic/gin": "domain must not import HTTP frameworks",
        "github.com/labstack/echo": "domain must not import HTTP frameworks",
        "gorm.io/": "domain must not import ORM packages",
        "github.com/jmoiron/sqlx": "domain must not import SQL helper packages",
        "github.com/redis/": "domain must not import cache clients",
        "github.com/go-redis/": "domain must not import cache clients",
        "go.uber.org/zap": "domain must not import logging implementations",
        "github.com/spf13/viper": "domain must not import configuration packages",
    },
    "app": {
        "github.com/gin-gonic/gin": "app must not import HTTP frameworks",
        "github.com/labstack/echo": "app must not import HTTP frameworks",
        "gorm.io/": "app must not import ORM packages",
        "github.com/jmoiron/sqlx": "app must not import SQL helper packages",
        "github.com/redis/": "app must not import cache clients",
        "github.com/go-redis/": "app must not import cache clients",
        "github.com/spf13/viper": "app must not import configuration packages",
    },
    "port": {
        "github.com/gin-gonic/gin": "app/port must not expose HTTP frameworks",
        "github.com/labstack/echo": "app/port must not expose HTTP frameworks",
        "gorm.io/": "app/port must not expose ORM packages",
        "github.com/jmoiron/sqlx": "app/port must not expose SQL helper packages",
        "github.com/redis/": "app/port must not expose cache clients",
        "github.com/go-redis/": "app/port must not expose cache clients",
    },
}


def run_go_list(root: Path) -> list[dict]:
    result = subprocess.run(
        ["go", "list", "-json", "./..."],
        cwd=root,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    if result.returncode != 0:
        raise RuntimeError(result.stderr.strip() or "go list failed")

    packages: list[dict] = []
    decoder = json.JSONDecoder()
    data = result.stdout.strip()
    while data:
        obj, idx = decoder.raw_decode(data)
        packages.append(obj)
        data = data[idx:].lstrip()
    return packages


def default_baseline_path() -> Path:
    return Path(__file__).resolve().parents[1] / "baselines" / f"{Path(__file__).stem}.txt"


def load_baseline() -> set[str]:
    raw = os.environ.get("GO_GUARD_BOUNDARY_BASELINE") or os.environ.get("GO_GUARD_BASELINE")
    path = Path(raw).resolve() if raw else default_baseline_path()
    if not path.exists():
        return set()
    entries: set[str] = set()
    for line in path.read_text(encoding="utf-8", errors="ignore").splitlines():
        item = line.strip()
        if not item or item.startswith("#"):
            continue
        if item.startswith("- "):
            item = item[2:].strip()
        entries.add(item)
    return entries


def apply_baseline(violations: list[str]) -> list[str]:
    baseline = load_baseline()
    if not baseline:
        return violations
    return [item for item in violations if item not in baseline]


def package_rel_dir(pkg: dict, root: Path) -> str:
    directory = Path(pkg["Dir"]).resolve()
    return directory.relative_to(root.resolve()).as_posix()


def rel_import(import_path: str, module_path: str) -> str | None:
    if import_path == module_path:
        return "."
    prefix = module_path + "/"
    if import_path.startswith(prefix):
        return import_path[len(prefix) :]
    return None


def layer(path: str) -> str:
    parts = path.split("/")
    if len(parts) >= 3 and parts[0] == "cmd" and parts[1] in SIDECAR_COMMANDS:
        return "sidecar"
    if len(parts) >= 2 and parts[0] == "internal":
        if parts[1] in {"bootstrap", "platform"}:
            return parts[1]
        if len(parts) < 3:
            return "other"
        if parts[2] == "domain":
            return "domain"
        if parts[2] == "app":
            if len(parts) >= 4 and parts[3] == "port":
                return "port"
            return "app"
        if parts[2] == "adapter":
            return "adapter"
    if parts and parts[0] == "cmd":
        return "cmd"
    return "other"


def context_name(path: str) -> str | None:
    parts = path.split("/")
    if len(parts) >= 2 and parts[0] == "internal" and parts[1] not in {"bootstrap", "platform"}:
        return parts[1]
    return None


def adapter_kind(path: str) -> str | None:
    parts = path.split("/")
    if len(parts) >= 4 and parts[0] == "internal" and parts[2] == "adapter":
        return parts[3]
    return None


def external_violation_reason(source: str, import_path: str) -> str | None:
    src_layer = layer(source)
    forbidden = FORBIDDEN_EXTERNAL_BY_LAYER.get(src_layer, {})
    for forbidden_import, reason in forbidden.items():
        if forbidden_import.endswith("/"):
            if import_path.startswith(forbidden_import):
                return reason
        elif import_path == forbidden_import or import_path.startswith(forbidden_import + "/"):
            return reason
    return None


def violation_reason(source: str, target: str) -> str | None:
    src_layer = layer(source)
    dst_layer = layer(target)
    src_ctx = context_name(source)
    dst_ctx = context_name(target)
    src_adapter = adapter_kind(source)
    dst_adapter = adapter_kind(target)

    if src_layer == "domain" and dst_layer != "other":
        return "domain must not import project internal packages"
    if src_layer == "app" and dst_layer in {"adapter", "bootstrap", "platform", "cmd"}:
        return "app must not import adapters, bootstrap, platform, or cmd"
    if src_layer == "port" and dst_layer not in {"domain", "other"}:
        return "app/port must only depend on domain or external packages"
    if src_layer == "adapter" and dst_layer in {"bootstrap", "cmd"}:
        return "adapter must not import bootstrap or cmd"
    if src_layer == "adapter" and dst_layer == "adapter" and src_adapter != dst_adapter:
        return "adapters must not call each other directly"
    if src_layer == "adapter" and src_adapter not in {None, "http", "grpc", "graphql", "event"} and dst_layer == "app":
        return f"{src_adapter} adapter must implement ports, not import app use cases"
    if src_layer == "platform" and dst_layer in {"domain", "app", "port", "adapter"}:
        return "platform must not import business packages"
    if src_layer == "cmd" and dst_layer not in {"bootstrap", "other", "platform", "sidecar"}:
        return "cmd should import bootstrap, not business packages directly"

    if src_ctx and dst_ctx and src_ctx != dst_ctx and dst_layer == "adapter":
        return "bounded contexts must not import each other's adapters"
    return None


def package_name_violations(pkg: dict, root: Path) -> list[str]:
    source = package_rel_dir(pkg, root)
    parts = source.split("/")
    if not parts or parts[0] not in {"internal", "pkg"}:
        return []

    names = {parts[-1], pkg.get("Name", "")}
    bad = sorted(name for name in names if name in FORBIDDEN_PACKAGE_NAMES)
    return [f"{source}: package name {name!r} is too broad; name the concrete responsibility" for name in bad]


def main(argv: list[str]) -> int:
    root = Path(argv[1] if len(argv) > 1 else ".").resolve()
    if not (root / "go.mod").exists():
        print(f"SKIP: no go.mod found under {root}")
        return 0

    try:
        packages = run_go_list(root)
    except RuntimeError as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 2

    module_path = packages[0].get("Module", {}).get("Path")
    if not module_path:
        print("ERROR: unable to detect module path from go list", file=sys.stderr)
        return 2

    violations: list[str] = []
    for pkg in packages:
        source = package_rel_dir(pkg, root)
        violations.extend(package_name_violations(pkg, root))
        for import_path in pkg.get("Imports", []):
            target = rel_import(import_path, module_path)
            reason = external_violation_reason(source, import_path) if target is None else violation_reason(source, target)
            if reason:
                imported = target if target is not None else import_path
                violations.append(f"{source} imports {imported}: {reason}")

    violations = apply_baseline(violations)

    if violations:
        print("Go architecture boundary violations:")
        for item in violations:
            print(f"- {item}")
        return 1

    print("Go architecture boundary check passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
