#!/usr/bin/env python3
"""Emit a non-authorizing planning fixture result for one named token."""
from __future__ import annotations
import argparse, json, sys
from pathlib import Path


class DuplicateKey(ValueError):
    pass


def strict_json(path: Path) -> dict:
    def pairs(items):
        result = {}
        for key, value in items:
            if key in result:
                raise DuplicateKey(key)
            result[key] = value
        return result
    return json.loads(path.read_text(encoding="utf-8"), object_pairs_hook=pairs)

def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--token")
    group.add_argument("--blocker")
    args = parser.parse_args(argv)
    key, value = ("token", args.token) if args.token else ("blocker", args.blocker)
    if not value or any(ch in value for ch in "<>\x00"):
        print("fixture selector must be concrete", file=sys.stderr)
        return 2
    root = Path.cwd()
    try:
        inventory = strict_json(root / "docs/work/plans/orch-subagent-upgrade/acceptance-inventory.json")
        index = strict_json(root / "docs/work/plans/orch-subagent-upgrade/spec-execution-index.json")
        registry = strict_json(root / "docs/work/plans/orch-subagent-upgrade/current-blocker-registry.json")
    except (OSError, ValueError, DuplicateKey) as exc:
        print(f"planning inputs unavailable: {exc}", file=sys.stderr)
        return 2
    binding = {}
    if key == "token":
        matches = [item for item in inventory.get("acceptance", []) if item.get("token") == value]
        if len(matches) != 1:
            print("unknown or duplicate token", file=sys.stderr)
            return 2
        item = matches[0]
        commands = [
            command
            for spec in index.get("specs", [])
            if spec.get("spec_id") == item.get("spec_id")
            for command in spec.get("commands", [])
            if command.get("id") == item.get("command_id")
        ]
        if len(commands) != 1 or value not in commands[0].get("acceptance_tokens", []) or value not in commands[0].get("argv", []):
            print("token command binding is missing or inconsistent", file=sys.stderr)
            return 2
        binding = {"spec_id": matches[0]["spec_id"], "command_id": matches[0]["command_id"]}
    else:
        if sum(item.get("id") == value for item in registry.get("blockers", [])) != 1:
            print("unknown or duplicate blocker", file=sys.stderr)
            return 2
        slots = registry.get("evidence_slots", {})
        if not isinstance(slots, dict) or value not in slots:
            print("blocker evidence slot is missing", file=sys.stderr)
            return 2
        binding = {"blocker_id": value}
    print(json.dumps({"authority": "planning_only", "release_decision": "NO-GO", "status": "unverified", "fixture": "planned", key: value, **binding}, sort_keys=True))
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
