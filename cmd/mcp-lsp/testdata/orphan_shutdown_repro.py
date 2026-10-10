#!/usr/bin/env python3
"""Exercise parent death through real MCP pipes and the Unix supervisor entry."""

import argparse
import json
import os
from pathlib import Path
import select
import signal
import subprocess
import sys
import tempfile
import time


SUPERVISOR_MODE = "__super_dolphin_lsp_process_supervisor_v1"


def process_identity(pid):
    result = subprocess.run(
        ["ps", "-p", str(pid), "-o", "lstart=,ppid=,pgid=,stat=,comm="],
        text=True, capture_output=True, check=False,
    )
    if result.returncode == 1 and not result.stdout.strip():
        return None
    if result.returncode != 0:
        raise RuntimeError(f"ps failed: {result.stderr.strip()}")
    fields = result.stdout.strip().split(maxsplit=8)
    if len(fields) != 9:
        raise RuntimeError(f"invalid process identity: {result.stdout!r}")
    return {
        "start": " ".join(fields[:5]), "ppid": int(fields[5]),
        "pgid": int(fields[6]), "stat": fields[7], "command": fields[8],
    }


def same_process(pid, original):
    current = process_identity(pid)
    return current if current and current["start"] == original["start"] else None


def live_group_members(pgid):
    output = subprocess.run(
        ["ps", "-axo", "pid=,pgid=,stat="], check=True,
        text=True, capture_output=True,
    ).stdout
    members = []
    for line in output.splitlines():
        pid, group, state = line.split()
        if int(group) == pgid and not state.startswith("Z"):
            members.append(int(pid))
    return members


def publish_report(path, record):
    temporary = str(path) + ".tmp"
    Path(temporary).write_text(json.dumps(record))
    os.replace(temporary, path)


def host(args):
    if args.case.startswith("supervisor"):
        os.dup2(args.control_fd, 3, inheritable=True)
        command = [args.binary, SUPERVISOR_MODE, args.workspace, "/bin/sh",
                   "-c", "trap '' TERM; sleep 60 & wait"]
        child = subprocess.Popen(command, pass_fds=(3,), start_new_session=True)
    else:
        env = os.environ.copy()
        for name in ("GO_AGENT_CTL_RPC_ADDR", "GO_AGENT_PEER_MODE",
                     "SUPER_DOLPHIN_SIDECAR_OWNER_ID", "SUPER_DOLPHIN_SQLITE_PATH",
                     "SUPER_DOLPHIN_LSP_RECEIPT_PATH", "SUPER_DOLPHIN_LSP_RECEIPT_SIGNING_KEY",
                     "MCP_LSP_RECEIPT_LOG_PATH", "MCP_LSP_RECEIPT_SIGNING_KEY_PATH",
                     "MCP_LSP_RECEIPT_INCLUDE_CANONICAL_IO"):
            env.pop(name, None)
        env.update(
            GO_AGENT_LSP_ROOT=args.workspace,
            GO_AGENT_LSP_ROOTS=json.dumps([args.workspace]),
            PROJECT_ROOT=args.workspace,
            SUPER_DOLPHIN_RUNTIME_MODE="dev",
            SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR=args.root,
            SUPER_DOLPHIN_DEPENDENCY_PROFILE="production",
            SUPER_DOLPHIN_HOME=args.workspace,
            GOMAXPROCS="2",
        )
        if args.signed_receipts:
            env.update(
                MCP_LSP_RECEIPT_LOG_PATH=str(Path(args.workspace) / "receipts.jsonl"),
                MCP_LSP_RECEIPT_SIGNING_KEY_PATH=str(Path(args.workspace) / "receipt-key.pem"),
            )
        child = subprocess.Popen([args.binary], env=env, cwd=args.workspace)
    publish_report(args.report, {"pid": child.pid})
    while True:
        code = child.poll()
        if code is not None:
            publish_report(args.report, {"pid": child.pid, "exit_code": code})
            signal.pause()
        time.sleep(0.02)


def rpc(host_process, seq, method, params):
    request = dict(jsonrpc="2.0", id=seq, method=method, params=params)
    host_process.stdin.write((json.dumps(request) + "\n").encode())
    host_process.stdin.flush()
    if not select.select([host_process.stdout], [], [], 10)[0]:
        raise RuntimeError(f"MCP {method} timed out")
    response = json.loads(host_process.stdout.readline())
    if response.get("id") != seq or "error" in response:
        raise RuntimeError(f"MCP {method} failed: {response}")
    return response


def run_case(args):
    supervisor_mode = args.case.startswith("supervisor")
    stderr_pressure = args.case in ("stderr-backpressure", "supervisor-stderr")
    with tempfile.TemporaryDirectory(prefix="mcp-lsp-orphan-") as directory:
        workspace = Path(directory) / "workspace"
        workspace.mkdir(mode=0o700)
        if args.signed_receipts:
            subprocess.run(
                ["openssl", "genpkey", "-algorithm", "ED25519", "-out", str(workspace / "receipt-key.pem")],
                check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE,
            )
            (workspace / "receipt-key.pem").chmod(0o600)
        report = Path(directory) / "child.json"
        control_read, control_write = os.pipe()
        pressure_read, pressure_write = os.pipe()
        child_pid = None
        original = None
        with tempfile.TemporaryFile() as stderr:
            command = [sys.executable, str(Path(__file__).resolve()), "--host",
                       "--binary", args.binary, "--root", args.root,
                       "--case", args.case, "--workspace", str(workspace),
                       "--report", str(report), "--control-fd", str(control_read)]
            if args.signed_receipts:
                command.append("--signed-receipts")
            parent = subprocess.Popen(
                command, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                stderr=pressure_write if stderr_pressure else stderr, pass_fds=(control_read,),
            )
            try:
                deadline = time.monotonic() + 10
                while not report.exists():
                    if parent.poll() is not None or time.monotonic() >= deadline:
                        raise RuntimeError("fixture host did not publish child PID")
                    time.sleep(0.02)
                child_pid = json.loads(report.read_text())["pid"]
                original = process_identity(child_pid)
                if not original or original["ppid"] != parent.pid:
                    raise RuntimeError("fixture did not bind its direct parent")
                if supervisor_mode:
                    if original["pgid"] != child_pid:
                        raise RuntimeError("supervisor fixture is not a group leader")
                    # Allow the real child and its descendant to start before killing the host.
                    time.sleep(0.5)
                else:
                    rpc(parent, 1, "initialize", {"protocolVersion": "2024-11-05"})
                    rpc(parent, 2, "tools/list", {})
                    if args.case == "healthy":
                        time.sleep(1.5)
                        current = same_process(child_pid, original)
                        if not current or current["ppid"] != parent.pid:
                            raise RuntimeError("healthy parent lost its active MCP child")
                        rpc(parent, 3, "ping", {})
                        parent.stdin.close()
                        deadline = time.monotonic() + 5
                        while time.monotonic() < deadline:
                            status = json.loads(report.read_text())
                            if "exit_code" in status:
                                if status["exit_code"] != 0:
                                    raise RuntimeError(f"healthy MCP EOF exit failed: {status}")
                                print(json.dumps({"case": "healthy", "passed": True,
                                                  "parent_alive": parent.poll() is None,
                                                  "eof_exit_code": 0}), flush=True)
                                return 0
                            time.sleep(0.05)
                        raise RuntimeError("healthy MCP child did not exit after EOF")
                    if args.case == "backpressure":
                        # Retain the output reader without consuming responses, filling the pipe.
                        for seq in range(3, 67):
                            request = dict(jsonrpc="2.0", id=seq, method="tools/list", params={})
                            parent.stdin.write((json.dumps(request) + "\n").encode())
                        parent.stdin.flush()
                        time.sleep(0.5)
                if stderr_pressure:
                    os.set_blocking(pressure_write, False)
                    filled = 0
                    try:
                        while True:
                            filled += os.write(pressure_write, b"x" * 4096)
                    except BlockingIOError:
                        pass
                    finally:
                        os.set_blocking(pressure_write, True)
                    if filled == 0:
                        raise RuntimeError("stderr pressure fixture could not fill the pipe")
                parent.kill()
                parent.wait(timeout=5)
                started = time.monotonic()
                observed_orphan = None
                while time.monotonic() - started < args.timeout:
                    current = same_process(child_pid, original)
                    root_gone = current is None or current["stat"].startswith("Z")
                    group_gone = not supervisor_mode or not live_group_members(child_pid)
                    if root_gone and group_gone:
                        result = {
                            "case": args.case, "binary": args.binary, "passed": True,
                            "elapsed_seconds": round(time.monotonic() - started, 3),
                            "orphan": observed_orphan, "cwd_exists": workspace.is_dir(),
                            "stdio_peers_retained": True, "control_writer_retained": True,
                            "stderr_pipe_full": stderr_pressure,
                        }
                        print(json.dumps(result, ensure_ascii=False), flush=True)
                        return 0
                    if current and current["ppid"] == 1:
                        observed_orphan = current
                    time.sleep(0.05)
                stderr.seek(0)
                log_tail = stderr.read().decode(errors="replace")[-5000:]
                print(json.dumps({
                    "case": args.case, "binary": args.binary, "passed": False,
                    "elapsed_seconds": args.timeout, "orphan": observed_orphan,
                    "cwd_exists": workspace.is_dir(), "stdio_peers_retained": True,
                    "control_writer_retained": True, "log_tail": log_tail,
                    "stderr_pipe_full": stderr_pressure,
                }, ensure_ascii=False), flush=True)
                return 1
            except Exception:
                stderr.seek(0)
                print(stderr.read().decode(errors="replace")[-8000:], file=sys.stderr)
                raise
            finally:
                if parent.poll() is None:
                    parent.kill()
                parent.wait(timeout=5)
                if child_pid is not None and original and same_process(child_pid, original):
                    if supervisor_mode:
                        os.killpg(child_pid, signal.SIGKILL)
                    else:
                        os.kill(child_pid, signal.SIGKILL)
                parent.stdin.close()
                parent.stdout.close()
                os.close(control_read)
                os.close(control_write)
                os.close(pressure_read)
                os.close(pressure_write)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", required=True)
    parser.add_argument("--root", required=True)
    parser.add_argument("--case", choices=("healthy", "idle", "backpressure", "supervisor", "stderr-backpressure", "supervisor-stderr"), required=True)
    parser.add_argument("--timeout", type=float, default=40)
    parser.add_argument("--signed-receipts", action="store_true")
    parser.add_argument("--host", action="store_true")
    parser.add_argument("--workspace")
    parser.add_argument("--report")
    parser.add_argument("--control-fd", type=int)
    args = parser.parse_args()
    args.binary = str(Path(args.binary).resolve(strict=True))
    args.root = str(Path(args.root).resolve(strict=True))
    if args.host:
        host(args)
        return 0
    return run_case(args)


if __name__ == "__main__":
    sys.exit(main())
