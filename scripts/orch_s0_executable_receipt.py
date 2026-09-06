#!/usr/bin/env python3
"""Validate a non-authorizing Gate S0 executable binding receipt.

This is deliberately a *receipt* for a planned/observed check, not a grant.
It binds an acceptance token to the source line and the dependency set in the
S0 inventory/DAG, while retaining only digests for the runner command.  The
validator rejects any attempt to turn the record into runtime authorization.
"""
from __future__ import annotations

import hashlib
import json
import argparse
import sys
from pathlib import Path
from typing import Any

try:
    from scripts.orch_acceptance_inventory import validate_inventory
    from scripts.orch_upgrade_gate import validate_inputs
except ModuleNotFoundError:
    from orch_acceptance_inventory import validate_inventory
    from orch_upgrade_gate import validate_inputs


class ReceiptError(ValueError):
    """Malformed, stale, or accidentally authorizing receipt."""


def _object(value: Any, expected: set[str], label: str) -> dict[str, Any]:
    if not isinstance(value, dict):
        raise ReceiptError(f"{label} must be an object")
    actual = set(value)
    if actual != expected:
        raise ReceiptError(f"{label} fields drift: missing={sorted(expected-actual)}, unknown={sorted(actual-expected)}")
    return value


def _text(value: Any, label: str) -> str:
    if not isinstance(value, str) or not value.strip() or "\x00" in value:
        raise ReceiptError(f"{label} must be non-empty text")
    return value.strip()


def _digest(value: Any, label: str) -> str:
    value = _text(value, label)
    if len(value) != 64 or any(char not in "0123456789abcdef" for char in value):
        raise ReceiptError(f"{label} must be lowercase SHA-256 hex")
    if set(value) == {"0"}:
        raise ReceiptError(f"{label} cannot be an all-zero placeholder")
    return value


def _inside(root: Path, relative: str, label: str) -> Path:
    relative = _text(relative, label)
    candidate = Path(relative)
    if candidate.is_absolute() or ".." in candidate.parts:
        raise ReceiptError(f"{label} must be workspace-relative")
    try:
        resolved = (root / candidate).resolve(strict=True)
        resolved.relative_to(root)
    except (OSError, RuntimeError, ValueError) as exc:
        raise ReceiptError(f"{label} is missing or escapes workspace: {relative}") from exc
    if not resolved.is_file():
        raise ReceiptError(f"{label} is not a regular file: {relative}")
    return resolved


def _strict_json(payload: str | bytes) -> Any:
    class DuplicateKey(Exception):
        pass

    def pairs(items: list[tuple[str, Any]]) -> dict[str, Any]:
        result: dict[str, Any] = {}
        for key, value in items:
            if key in result:
                raise DuplicateKey(key)
            result[key] = value
        return result

    try:
        return json.loads(payload, object_pairs_hook=pairs,
                          parse_constant=lambda name: (_ for _ in ()).throw(ValueError(name)))
    except DuplicateKey as exc:
        raise ReceiptError(f"receipt contains duplicate JSON key {exc.args[0]!r}") from exc
    except (ValueError, RecursionError) as exc:
        raise ReceiptError(f"receipt is invalid strict JSON: {exc}") from exc


def _canonical(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"))


def validate_receipt(root: str | Path, receipt: dict[str, Any] | str | bytes) -> dict[str, Any]:
    """Validate a receipt against the current planning inventory and DAG.

    Validation has no side effects and never returns an authorization result.
    ``observed`` means only that an external runner reported an observation;
    it is not a GREEN attestation or release decision.
    """
    root_path = Path(root)
    if not root_path.is_absolute() or not root_path.is_dir():
        raise ReceiptError(f"root must be an existing absolute directory: {root}")
    root_path = root_path.resolve()
    if isinstance(receipt, (str, bytes)):
        receipt = _strict_json(receipt)
    top = _object(receipt, {"schema_version", "authority", "release_decision", "binding", "execution", "authorization"}, "receipt")
    if type(top["schema_version"]) is not int or top["schema_version"] != 1:
        raise ReceiptError("receipt.schema_version must be 1")
    if top["authority"] != "planning_only" or top["release_decision"] != "NO-GO":
        raise ReceiptError("receipt must remain planning_only with release_decision=NO-GO")

    inventory = validate_inventory(root_path)
    graph = validate_inputs(root_path)["graph"]
    acceptance = {item["token"]: item for item in inventory["acceptance"]}
    binding = _object(top["binding"], {"token", "spec_id", "depends_on", "dependency_digest", "source", "command_id"}, "receipt.binding")
    token = _text(binding["token"], "receipt.binding.token")
    if token not in acceptance:
        raise ReceiptError(f"receipt.binding.token is not in acceptance inventory: {token}")
    spec_id = _text(binding["spec_id"], "receipt.binding.spec_id")
    spec_nodes = {node["id"]: node for node in graph["nodes"] if node["kind"] == "spec"}
    if spec_id not in spec_nodes:
        raise ReceiptError(f"receipt.binding.spec_id is not a spec node: {spec_id}")
    expected_ref = acceptance[token]["evidence"][0]
    if acceptance[token]["spec_id"] != spec_id or acceptance[token]["command_id"] != binding["command_id"]:
        raise ReceiptError("receipt.binding spec/command does not match acceptance inventory")
    source = _object(binding["source"], {"path", "symbol", "line", "sha256"}, "receipt.binding.source")
    source_path = _inside(root_path, source["path"], "receipt.binding.source.path")
    expected_path = (root_path / expected_ref["path"]).resolve(strict=True)
    if source_path != expected_path or source["symbol"] != expected_ref["symbol"] or source["line"] != expected_ref["line"]:
        raise ReceiptError("receipt.binding.source must match acceptance inventory exactly")
    if hashlib.sha256(source_path.read_bytes()).hexdigest() != _digest(source["sha256"], "receipt.binding.source.sha256"):
        raise ReceiptError("receipt.binding.source.sha256 does not match current source")
    depends_on = binding["depends_on"]
    if not isinstance(depends_on, list) or any(not isinstance(item, str) for item in depends_on):
        raise ReceiptError("receipt.binding.depends_on must be a string array")
    if len(depends_on) != len(set(depends_on)):
        raise ReceiptError("receipt.binding.depends_on must be unique")
    incoming = sorted({edge["from"] for edge in graph["edges"] if edge["to"] == spec_id and edge["kind"] != "provider_enable"})
    if sorted(depends_on) != incoming:
        raise ReceiptError("receipt.binding.depends_on does not match the machine DAG")
    expected_digest = hashlib.sha256(_canonical(incoming).encode("utf-8")).hexdigest()
    if _digest(binding["dependency_digest"], "receipt.binding.dependency_digest") != expected_digest:
        raise ReceiptError("receipt.binding.dependency_digest does not match the machine DAG")

    execution = _object(top["execution"], {"status", "runner_path", "argv", "command_id", "expected_exit", "expected_status", "expected_output", "command_digest", "runner_digest"}, "receipt.execution")
    if execution["status"] not in {"planned", "observed", "unverified"}:
        raise ReceiptError("receipt.execution.status must remain planned, observed, or unverified")
    runner_path = _inside(root_path, execution["runner_path"], "receipt.execution.runner_path")
    protected = {
        (root_path / "docs/work/plans/orch-subagent-upgrade/acceptance-inventory.json").resolve(),
        (root_path / "scripts/orch_acceptance_inventory.py").resolve(),
        (root_path / "scripts/orch_upgrade_gate.py").resolve(),
    }
    if runner_path in protected:
        raise ReceiptError("receipt.execution.runner_path cannot self-reference planning validators or inputs")
    _digest(execution["command_digest"], "receipt.execution.command_digest")
    command_id = _text(execution["command_id"], "receipt.execution.command_id")
    if binding["command_id"] != command_id:
        raise ReceiptError("receipt.binding.command_id must match execution.command_id")
    argv = execution["argv"]
    if not isinstance(argv, list) or not argv or any(not isinstance(arg, str) or not arg.strip() for arg in argv):
        raise ReceiptError("receipt.execution.argv must be a non-empty text array")
    if any("<" in arg or ">" in arg for arg in argv):
        raise ReceiptError("receipt.execution.argv contains placeholder")
    if type(execution["expected_exit"]) is not int or execution["expected_exit"] < 0 or execution["expected_exit"] > 255:
        raise ReceiptError("receipt.execution.expected_exit must be 0..255")
    for field in ("expected_status", "expected_output"):
        _text(execution[field], f"receipt.execution.{field}")
    runner_digest = _digest(execution["runner_digest"], "receipt.execution.runner_digest")
    if hashlib.sha256(runner_path.read_bytes()).hexdigest() != runner_digest:
        raise ReceiptError("receipt.execution.runner_digest does not match runner")
    index = validate_inputs(root_path).get("spec_execution_index")
    command = next((cmd for item in (index or {}).get("specs", []) if item.get("spec_id") == spec_id for cmd in item.get("commands", []) if cmd.get("id") == command_id), None)
    if command is None:
        raise ReceiptError("receipt.execution.command_id is not declared by spec execution index")
    if token not in command.get("acceptance_tokens", []):
        raise ReceiptError("receipt token is not declared by execution command")
    if command.get("runner") != execution["runner_path"] or command.get("argv") != argv:
        raise ReceiptError("receipt.execution command runner/argv does not match spec execution index")
    if command.get("expected_exit") != execution["expected_exit"] or command.get("expected_status") != execution["expected_status"] or command.get("expected_output") != execution["expected_output"]:
        raise ReceiptError("receipt execution expectations do not match spec execution index")
    authorization = _object(top["authorization"], {"granted", "runtime_effect", "capability_lease"}, "receipt.authorization")
    if authorization["granted"] is not False or authorization["runtime_effect"] != "none" or authorization["capability_lease"] is not None:
        raise ReceiptError("receipt authorization must be permanently disabled")
    return top


def validate_aggregate(root: str | Path, aggregate: dict[str, Any] | str | bytes) -> dict[str, Any]:
    if isinstance(aggregate, (str, bytes)):
        aggregate = _strict_json(aggregate)
    expected_fields = {"schema_version", "authority", "release_decision", "receipts"}
    if isinstance(aggregate, dict):
        optional = {"acceptance_tokens", "blocker_ids"}.intersection(aggregate)
        expected_fields |= optional
    top = _object(aggregate, expected_fields, "aggregate")
    if top["schema_version"] != 1 or top["authority"] != "planning_only" or top["release_decision"] != "NO-GO":
        raise ReceiptError("aggregate must remain planning_only with release_decision=NO-GO")
    receipts = top["receipts"]
    if not isinstance(receipts, list) or not receipts:
        raise ReceiptError("aggregate.receipts must be non-empty")
    checked = [validate_receipt(root, item) for item in receipts]
    tokens = {item["binding"]["token"] for item in checked}
    blockers = {bid for item in validate_inventory(root)["blockers"] for bid in [item["id"]]}
    expected_tokens = set(validate_inventory(root)["acceptance"][i]["token"] for i in range(51))
    if tokens != expected_tokens:
        raise ReceiptError(f"aggregate must cover all 51 acceptance tokens, missing={sorted(expected_tokens - tokens)}")
    expected_blockers = {f"B{i:02d}" for i in range(1, 19)}
    if "acceptance_tokens" in top and (not isinstance(top["acceptance_tokens"], list) or set(top["acceptance_tokens"]) != expected_tokens):
        raise ReceiptError("aggregate.acceptance_tokens coverage drift")
    if "blocker_ids" in top and (not isinstance(top["blocker_ids"], list) or set(top["blocker_ids"]) != expected_blockers):
        raise ReceiptError("aggregate.blocker_ids must cover B01-B18")
    if "blocker_ids" not in top:
        raise ReceiptError("aggregate must declare blocker_ids coverage for B01-B18")
    return top


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", required=True)
    parser.add_argument("--receipt")
    parser.add_argument("--aggregate")
    parser.add_argument("--aggregate-only", action="store_true")
    args = parser.parse_args(argv)
    try:
        root_path = Path(args.root).resolve()
        if args.aggregate_only and args.receipt:
            raise ReceiptError("--aggregate-only cannot be combined with --receipt")
        if not args.aggregate and not args.receipt:
            raise ReceiptError("--receipt or --aggregate is required")
        if args.receipt:
            receipt_arg = Path(args.receipt)
            if receipt_arg.is_absolute():
                receipt_arg = receipt_arg.resolve().relative_to(root_path)
            receipt_path = _inside(root_path, receipt_arg.as_posix(), "receipt")
            payload = _strict_json(receipt_path.read_bytes())
            validate_receipt(args.root, payload)
        if args.aggregate:
            aggregate_arg = Path(args.aggregate)
            if aggregate_arg.is_absolute():
                aggregate_arg = aggregate_arg.resolve().relative_to(root_path)
            aggregate_path = _inside(root_path, aggregate_arg.as_posix(), "aggregate")
            validate_aggregate(args.root, _strict_json(aggregate_path.read_bytes()))
    except (ReceiptError, OSError, ValueError) as exc:
        print(f"orch_s0_executable_receipt: {exc}", file=sys.stderr)
        return 1
    print("OK planning-only NO-GO executable receipt")
    return 0


__all__ = ["ReceiptError", "validate_receipt", "validate_aggregate"]


if __name__ == "__main__":
    raise SystemExit(main())
