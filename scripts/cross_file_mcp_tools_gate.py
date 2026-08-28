#!/usr/bin/env python3
"""Fail-fast MCP tools/list gate before a With-LSP Codex process starts."""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import select
import subprocess
import sys
import time
from typing import Any


class MCPToolsGateError(RuntimeError):
    """The sidecar did not expose the exact benchmark tool contract."""


def _request(process: subprocess.Popen[bytes], request_id: int, method: str, params: dict[str, Any], timeout: float) -> dict[str, Any]:
    if process.stdin is None or process.stdout is None:
        raise MCPToolsGateError("MCP process pipes are unavailable")
    process.stdin.write(json.dumps(
        {"jsonrpc": "2.0", "id": request_id, "method": method, "params": params},
        separators=(",", ":"),
    ).encode("utf-8") + b"\n")
    process.stdin.flush()
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        ready, _, _ = select.select((process.stdout,), (), (), max(0.0, deadline - time.monotonic()))
        if not ready:
            break
        line = process.stdout.readline()
        if not line:
            raise MCPToolsGateError(f"MCP server exited before {method} response")
        try:
            response = json.loads(line)
        except json.JSONDecodeError as exc:
            raise MCPToolsGateError(f"MCP {method} returned invalid JSON") from exc
        if response.get("id") != request_id:
            continue
        if response.get("error") is not None:
            raise MCPToolsGateError(f"MCP {method} returned error: {response['error']}")
        result = response.get("result")
        if not isinstance(result, dict):
            raise MCPToolsGateError(f"MCP {method} result is not an object")
        return result
    raise MCPToolsGateError(f"MCP {method} timed out after {timeout:g}s")


def verify_tools(server: str, cwd: pathlib.Path, stderr_path: pathlib.Path, timeout: float) -> dict[str, Any]:
    """Start the real sidecar and require exactly the three benchmark tools."""

    stderr_path.parent.mkdir(parents=True, exist_ok=True)
    with stderr_path.open("wb") as stderr_file:
        process = subprocess.Popen(
            (server,), cwd=cwd, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
            stderr=stderr_file, env=os.environ.copy(),
        )
        try:
            initialized = _request(process, 1, "initialize", {
                "protocolVersion": "2024-11-05", "capabilities": {},
                "clientInfo": {"name": "cross-file-mcp-gate", "version": "1"},
            }, timeout)
            if process.stdin is None:
                raise MCPToolsGateError("MCP stdin is unavailable")
            process.stdin.write(b'{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}\n')
            process.stdin.flush()
            listed = _request(process, 2, "tools/list", {}, timeout)
            tools = listed.get("tools")
            if not isinstance(tools, list):
                raise MCPToolsGateError("MCP tools/list tools is not an array")
            tool_names: list[str] = []
            for tool in tools:
                if isinstance(tool, dict):
                    name = tool.get("name")
                    if isinstance(name, str):
                        tool_names.append(name)
            names = tuple(sorted(tool_names))
            required = ("diagnostics", "structure", "xref")
            if names != required:
                raise MCPToolsGateError(f"MCP tools/list mismatch: expected {required}, got {names}")
            return {
                "schema": "super-dolphin/cross-file-mcp-tools-gate/v1",
                "protocol_version": initialized.get("protocolVersion"),
                "tools": list(names), "status": "passed",
            }
        finally:
            process.terminate()
            try:
                process.wait(timeout=3)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=3)
            if process.stdin is not None:
                process.stdin.close()
            if process.stdout is not None:
                process.stdout.close()


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--server", required=True)
    parser.add_argument("--cwd", required=True)
    parser.add_argument("--evidence", required=True)
    parser.add_argument("--stderr", required=True)
    parser.add_argument("--timeout", type=float, default=15.0)
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args(argv)
    command = list(args.command)
    if command and command[0] == "--":
        command.pop(0)
    if not command:
        raise MCPToolsGateError("Codex command is required after --")
    evidence_path = pathlib.Path(args.evidence)
    evidence = verify_tools(args.server, pathlib.Path(args.cwd), pathlib.Path(args.stderr), args.timeout)
    evidence_path.parent.mkdir(parents=True, exist_ok=True)
    temporary = evidence_path.with_suffix(evidence_path.suffix + ".tmp")
    temporary.write_text(json.dumps(evidence, sort_keys=True) + "\n", encoding="utf-8")
    os.replace(temporary, evidence_path)
    os.execvp(command[0], command)
    return 127


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:  # noqa: BLE001 - CLI boundary must fail closed.
        print(f"MCP tools gate failed: {type(exc).__name__}: {exc}", file=sys.stderr)
        raise SystemExit(78)
