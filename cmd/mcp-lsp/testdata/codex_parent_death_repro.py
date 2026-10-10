#!/usr/bin/env python3
"""Kill an isolated real Codex app-server and verify its direct LSP child exits."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import select
import signal
import subprocess
import tempfile
import time

from orphan_shutdown_repro import process_identity, same_process


def toml_value(value):
    if isinstance(value, dict):
        return "{" + ",".join(json.dumps(k) + "=" + toml_value(v)
                              for k, v in value.items()) + "}"
    if isinstance(value, list):
        return "[" + ",".join(toml_value(v) for v in value) + "]"
    return json.dumps(value)


def request(process, seq, method, params):
    message = {"id": seq, "method": method, "params": params}
    process.stdin.write((json.dumps(message) + "\n").encode())
    process.stdin.flush()
    deadline = time.monotonic() + 20
    while time.monotonic() < deadline:
        if not select.select([process.stdout], [], [], 0.5)[0]:
            continue
        line = process.stdout.readline()
        if not line:
            raise RuntimeError("Codex stdout closed before the response")
        response = json.loads(line)
        if response.get("id") != seq:
            continue
        if "error" in response:
            raise RuntimeError(f"Codex {method} failed: {response['error']}")
        return response["result"]
    raise RuntimeError(f"Codex {method} timed out")


def owned_children(parent_pid):
    output = subprocess.run(
        ["ps", "-axo", "pid=,ppid=,pgid=,stat=,command="], check=True,
        text=True, capture_output=True,
    ).stdout
    rows = [line.split(maxsplit=4) for line in output.splitlines()]
    found = {parent_pid}
    records = {}
    while True:
        added = False
        for row in rows:
            if len(row) != 5 or int(row[0]) in found or int(row[1]) not in found:
                continue
            pid = int(row[0])
            found.add(pid)
            identity = process_identity(pid)
            if identity:
                records[pid] = {"identity": identity, "command": row[4],
                                "direct": int(row[1]) == parent_pid}
            added = True
        if not added:
            return records


def cleanup_owned(records, parent_pid):
    # Only groups anchored by a recorded, still matching fixture process may be killed.
    groups = {parent_pid}
    groups.update(pid for pid, record in records.items()
                  if record["identity"]["pgid"] == pid)
    killed = set()
    for pid, record in records.items():
        current = same_process(pid, record["identity"])
        if not current or current["stat"].startswith("Z"):
            continue
        group = current["pgid"]
        if group != record["identity"]["pgid"] or group not in groups:
            raise RuntimeError(f"fixture process group identity changed for PID {pid}")
        if group not in killed:
            try:
                os.killpg(group, signal.SIGKILL)
            except ProcessLookupError:
                # The recorded group exited between the identity probe and signal.
                continue
            killed.add(group)


def run(args):
    with tempfile.TemporaryDirectory(prefix="mcp-lsp-codex-kill-") as directory:
        workspace = Path(directory)
        key = workspace / "receipt.pem"
        if args.signed_receipts:
            subprocess.run(["openssl", "genpkey", "-algorithm", "ED25519", "-out", str(key)],
                           check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
            key.chmod(0o600)
        env = {
            "GO_AGENT_LSP_ROOT": directory,
            "GO_AGENT_LSP_ROOTS": json.dumps([directory]),
            "PROJECT_ROOT": directory,
            "SUPER_DOLPHIN_RUNTIME_MODE": "dev",
            "SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR": args.root,
            "SUPER_DOLPHIN_DEPENDENCY_PROFILE": "production",
            "SUPER_DOLPHIN_HOME": directory,
            "MCP_LSP_RECEIPT_LOG_PATH": "",
            "MCP_LSP_RECEIPT_SIGNING_KEY_PATH": "",
            "MCP_LSP_RECEIPT_INCLUDE_CANONICAL_IO": "",
            "GOMAXPROCS": "2",
        }
        if args.signed_receipts:
            env.update({"MCP_LSP_RECEIPT_LOG_PATH": str(workspace / "receipts.jsonl"),
                        "MCP_LSP_RECEIPT_SIGNING_KEY_PATH": str(key)})
        config = {"lsp_orphan_fixture": {"command": args.binary, "env": env,
                                         "startup_timeout_sec": 15}}
        records = {}
        with tempfile.TemporaryFile() as stderr:
            parent = subprocess.Popen(
                [args.codex, "-c", "mcp_servers=" + toml_value(config), "-c", "plugins={}",
                 "app-server", "--listen", "stdio://"], cwd=directory,
                stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=stderr,
                start_new_session=True, bufsize=0,
            )
            try:
                request(parent, 1, "initialize", {
                    "clientInfo": {"name": "lsp_orphan_fixture", "version": "1"},
                    "capabilities": {"experimentalApi": True},
                })
                parent.stdin.write(b'{"method":"initialized"}\n')
                parent.stdin.flush()
                thread = request(parent, 2, "thread/start", {
                    "cwd": directory, "ephemeral": True,
                    "approvalPolicy": "never", "sandbox": "read-only",
                })
                status = request(parent, 3, "mcpServerStatus/list", {
                    "threadId": thread["thread"]["id"],
                    "serverName": "lsp_orphan_fixture", "limit": 100,
                })
                servers = [item for item in status["data"]
                           if item["name"] == "lsp_orphan_fixture"]
                if len(servers) != 1 or not servers[0]["tools"]:
                    raise RuntimeError("Codex did not initialize the configured real LSP")
                time.sleep(0.5)
                records = owned_children(parent.pid)
                matches = [(pid, record) for pid, record in records.items()
                           if record["direct"] and record["command"] == args.binary]
                if len(matches) != 1:
                    raise RuntimeError("expected exactly one active direct LSP child")
                child_pid, record = matches[0]
                if record["identity"]["ppid"] != parent.pid:
                    raise RuntimeError("LSP is not a direct child of the test Codex")
                parent.kill()
                parent.wait(timeout=5)
                started = time.monotonic()
                orphan_seen = False
                while time.monotonic() - started < args.timeout:
                    current = same_process(child_pid, record["identity"])
                    if not current or current["stat"].startswith("Z"):
                        result = {
                            "case": "real-codex-sigkill", "passed": True,
                            "codex_binary": args.codex, "codex_pid": parent.pid,
                            "codex_exit_code": parent.returncode,
                            "lsp_binary": args.binary, "lsp_pid": child_pid,
                            "lsp_ppid_before": record["identity"]["ppid"],
                            "lsp_sha256": hashlib.sha256(Path(args.binary).read_bytes()).hexdigest(),
                            "elapsed_seconds": round(time.monotonic() - started, 3),
                            "orphan_observed": orphan_seen, "stdio_peers_retained": False,
                            "cwd_exists": workspace.is_dir(), "ephemeral": True,
                            "signed_receipts": args.signed_receipts,
                        }
                        print(json.dumps(result), flush=True)
                        return 0
                    orphan_seen |= current["ppid"] == 1
                    time.sleep(0.05)
                raise RuntimeError(f"LSP survived Codex SIGKILL for {args.timeout} seconds")
            finally:
                if parent.poll() is None:
                    records.update(owned_children(parent.pid))
                    parent.kill()
                parent.wait(timeout=5)
                cleanup_owned(records, parent.pid)
                parent.stdin.close()
                parent.stdout.close()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--codex", required=True)
    parser.add_argument("--binary", required=True)
    parser.add_argument("--root", required=True)
    parser.add_argument("--timeout", type=float, default=40)
    parser.add_argument("--signed-receipts", action="store_true")
    args = parser.parse_args()
    for name in ("codex", "binary", "root"):
        setattr(args, name, str(Path(getattr(args, name)).resolve(strict=True)))
    return run(args)


if __name__ == "__main__":
    raise SystemExit(main())
