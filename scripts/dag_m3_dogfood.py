#!/usr/bin/env python3
from __future__ import annotations

import argparse
import datetime as dt
import ipaddress
import json
import os
import pathlib
import re
import select
import signal
import shlex
import subprocess
import sys
import time
import urllib.parse
import urllib.request


REQUIRED_AGENT_KEYS = (
    "source_monitor",
    "data_inspector",
    "pr_summarizer",
    "note_organizer",
    "todo_prioritizer",
    "morning_briefer",
    "email_drafter",
    "weekly_reviewer",
    "health_reporter",
    "docs-writer",
)

CHECKLIST = (
    "Prompt templates enabled: all current dogfood agent_keys visible via prompt_list.",
    "DAG shape: 10 agent nodes, prompt_template-first, no command_card/hybrid/webhook/shell expansion.",
    "Inputs: downstream nodes use inputs.from_nodes for real upstream content and sharedfile for large output.",
    "Large output positive path: pr_summarizer asks for >4KB and writes sharedfile with to_node_result=false.",
    "Large output negative path: optional one-node probe asks for >4KB with to_node_result=true and should fail validation.",
    "Dispatch: script manually dispatches ready unassigned nodes via task_dispatch_node as they become ready.",
    "Metrics: /metrics contains dispatch_failed_total and retry_count_per_node; strict sample mode requires --retry-metrics-dag-key from a dispatcher retry probe.",
    "Rerun: use a fresh --dag-key or the default timestamped dag key; default shared_prefix follows dag_key; use a new idempotency key for each manual retry.",
)


def required_metric_names():
    return ("dispatch_failed_total", "retry_count_per_node")


def sample_backed_metric_fallbacks():
    return {"retry_count_per_node": "retry_count_per_node_overflow_total"}


def default_shared_prefix(dag_key):
    slug = re.sub(r"[^A-Za-z0-9._-]+", "-", (dag_key or "").strip()).strip(".-_")
    if not slug:
        slug = "m3-dogfood"
    return "reports/%s" % slug


def build_main_ops(assignee, shared_prefix, provider="codex", model="gpt-5", cwd=""):
    del assignee  # assigned_to is set later through task_dispatch_node.
    nodes = [
        ("source_monitor", "Source monitor", "source_monitor", [], ["source.md"], True),
        ("data_inspector", "Data inspector", "data_inspector", ["source_monitor"], [], True),
        ("pr_summarizer", "PR summarizer", "pr_summarizer", ["source_monitor", "data_inspector"], [], False),
        ("note_organizer", "Note organizer", "note_organizer", ["pr_summarizer", "data_inspector"], ["pr_summarizer.md"], True),
        ("todo_prioritizer", "Todo prioritizer", "todo_prioritizer", ["note_organizer"], [], True),
        ("morning_briefer", "Morning briefer", "morning_briefer", ["note_organizer", "todo_prioritizer"], [], True),
        ("email_drafter", "Email drafter", "email_drafter", ["morning_briefer"], [], True),
        ("weekly_reviewer", "Weekly reviewer", "weekly_reviewer", ["pr_summarizer", "email_drafter"], ["pr_summarizer.md"], True),
        ("health_reporter", "Health reporter", "health_reporter", ["source_monitor", "weekly_reviewer"], [], True),
        ("delivery_writer", "Delivery writer", "docs-writer", ["weekly_reviewer", "health_reporter"], ["pr_summarizer.md"], True),
    ]
    add_ops = []
    for node_key, title, agent_key, deps, shared_inputs, to_node_result in nodes:
        add_ops.append({
            "op": "add_node",
            "node": {
                "node_key": node_key,
                "title": title,
                "node_type": "agent",
                "depends_on": deps,
                "config": _agent_config(
                    agent_key=agent_key,
                    provider=provider,
                    model=model,
                    cwd=cwd,
                    deps=deps,
                    shared_inputs=[_join_shared(shared_prefix, p) for p in shared_inputs],
                    shared_output=_join_shared(shared_prefix, "%s.md" % node_key) if node_key == "pr_summarizer" else "",
                    to_node_result=to_node_result,
                ),
            },
        })
    return add_ops


def build_negative_ops(assignee, shared_prefix, provider="codex", model="gpt-5", cwd=""):
    del assignee  # assigned_to is set later through task_dispatch_node.
    node_key = "long_node_result_rejected"
    return [
        {
            "op": "add_node",
            "node": {
                "node_key": node_key,
                "title": "Long node.result rejection probe",
                "node_type": "agent",
                "config": _agent_config(
                    agent_key="pr_summarizer",
                    provider=provider,
                    model=model,
                    cwd=cwd,
                    deps=[],
                    shared_inputs=[_join_shared(shared_prefix, "source.md")],
                    shared_output="",
                    to_node_result=True,
                    force_long=True,
                ),
            },
        },
    ]


def _agent_config(agent_key, provider, model, cwd, deps, shared_inputs, shared_output, to_node_result, force_long=False):
    outputs = {"to_node_result": to_node_result}
    if shared_output:
        outputs["to_sharedfile"] = {"path": shared_output, "lock_mode": "exclusive"}
    first_turn = _first_turn(agent_key, deps, shared_output, force_long)
    inputs = {
        "from_nodes": deps,
        "from_sharedfiles": shared_inputs,
    }
    return {
        "exec": {"provider": provider, "model": model, "agent_key": agent_key, "cwd": cwd, "language": "zh"},
        "inputs": inputs,
        "outputs": outputs,
        "first_turn": first_turn,
    }


def _first_turn(agent_key, deps, shared_output, force_long):
    base = (
        "M3 DAG dogfood node. Use the injected upstream inputs, cite the source node keys, "
        "and return concrete markdown output. Do not call external webhooks or shell commands."
    )
    if agent_key == "pr_summarizer" or force_long:
        target = shared_output or "node.result"
        return (
            "%s Produce a structured review brief longer than >4KB, minimum 6000 characters, "
            "with sections: context, findings, risks, action items, and verification notes. "
            "Write meaningful content for %s, not filler. Upstream deps: %s."
        ) % (base, target, ", ".join(deps) or "none")
    return "%s Keep the result under 2KB. Upstream deps: %s." % (base, ", ".join(deps) or "none")


def _join_shared(prefix, name):
    return "%s/%s" % (prefix.rstrip("/"), name)


class MCPError(RuntimeError):
    pass


def _opener_for_url(url):
    if _is_loopback_url(url):
        return urllib.request.build_opener(urllib.request.ProxyHandler({}))
    return urllib.request.build_opener()


def _is_loopback_url(url):
    host = urllib.parse.urlparse(url).hostname
    if not host:
        return False
    if host.lower() == "localhost":
        return True
    try:
        return ipaddress.ip_address(host).is_loopback
    except ValueError:
        return False


class HTTPMCPClient:
    def __init__(self, url, timeout):
        self.url = _normalize_mcp_url(url)
        self.timeout = timeout
        self.next_id = 1
        self.opener = _opener_for_url(self.url)
        self.tool_scope = {}

    def close(self):
        return None

    def request(self, method, params=None):
        payload = {"jsonrpc": "2.0", "id": self.next_id, "method": method, "params": params or {}}
        self.next_id += 1
        data = json.dumps(payload).encode("utf-8")
        req = urllib.request.Request(
            self.url,
            data=data,
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        with self.opener.open(req, timeout=self.timeout) as resp:
            return _decode_rpc_response(json.loads(resp.read().decode("utf-8")))


class StdioMCPClient:
    def __init__(self, cmd, cwd, env, timeout):
        self.cmd = cmd
        self.cwd = cwd
        self.env = env
        self.timeout = timeout
        self.next_id = 1
        self.proc = None
        self.stderr_handle = None
        self.tool_scope = {}
        self.stderr_path = pathlib.Path(os.getenv("TMPDIR", "/tmp")) / ("dag-m3-dogfood-mcp-orch-%d.log" % os.getpid())

    def start(self):
        self.stderr_handle = open(self.stderr_path, "a", encoding="utf-8")
        self.proc = subprocess.Popen(
            self.cmd,
            cwd=self.cwd,
            env=self.env,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=self.stderr_handle,
            text=True,
            bufsize=1,
            start_new_session=True,
        )

    def close(self):
        if self.proc is None:
            return
        if self.proc.poll() is None:
            _terminate_process_group(self.proc)
            try:
                self.proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                _kill_process_group(self.proc)
        if self.stderr_handle is not None:
            self.stderr_handle.close()
            self.stderr_handle = None

    def request(self, method, params=None):
        proc = self.proc
        if proc is None:
            self.start()
            proc = self.proc
        if proc is None:
            raise MCPError("mcp-orch process did not start")
        if proc.poll() is not None:
            err = _tail_file(self.stderr_path)
            raise MCPError("mcp-orch exited before request: %s" % err.strip())
        stdin = proc.stdin
        stdout = proc.stdout
        if stdin is None or stdout is None:
            raise MCPError("mcp-orch process pipes are unavailable")
        payload = {"jsonrpc": "2.0", "id": self.next_id, "method": method, "params": params or {}}
        self.next_id += 1
        stdin.write(json.dumps(payload) + "\n")
        stdin.flush()
        line = _readline_with_timeout(stdout, self.timeout)
        return _decode_rpc_response(json.loads(line))


def _readline_with_timeout(pipe, timeout):
    deadline = time.time() + timeout
    while time.time() < deadline:
        ready, _, _ = select.select([pipe], [], [], 0.1)
        if ready:
            line = pipe.readline()
            if line:
                return line
            break
        time.sleep(0.05)
    raise MCPError("timeout waiting for mcp-orch JSON-RPC response")


def _normalize_mcp_url(url: str) -> str:
    parsed = urllib.parse.urlparse(url)
    if parsed.path in ("", "/"):
        parsed = parsed._replace(path="/mcp")
    return urllib.parse.urlunparse(parsed)


def _terminate_process_group(proc):
    try:
        if os.name == "nt":
            proc.terminate()
            return
        kill_process_group = getattr(os, "killpg")
        kill_process_group(proc.pid, signal.SIGTERM)
    except ProcessLookupError:
        return


def _kill_process_group(proc):
    try:
        if os.name == "nt":
            proc.kill()
            return
        kill_process_group = getattr(os, "killpg")
        kill_signal = getattr(signal, "SIGKILL")
        kill_process_group(proc.pid, kill_signal)
    except ProcessLookupError:
        return


def _tail_file(path, limit=4000):
    try:
        data = pathlib.Path(path).read_text(encoding="utf-8", errors="replace")
    except OSError:
        return ""
    return data[-limit:]


def _decode_rpc_response(resp):
    if resp.get("error"):
        err = resp["error"]
        raise MCPError("%s: %s" % (err.get("code"), err.get("message")))
    return resp.get("result")


def call_tool(client, name, arguments):
    params = {"name": name, "arguments": arguments}
    params.update(getattr(client, "tool_scope", {}) or {})
    result = client.request("tools/call", params)
    if "structuredContent" in result:
        return unwrap_tool_result(name, result["structuredContent"])
    content = result.get("content") or []
    if content:
        return unwrap_tool_result(name, json.loads(content[0]["text"]))
    return None


def unwrap_tool_result(name, payload):
    if isinstance(payload, dict) and payload.get("success") is False:
        code = payload.get("code") or "tool_error"
        error = payload.get("error") or payload.get("message") or payload
        raise MCPError("%s failed (%s): %s" % (name, code, error))
    return payload


def validate_prompts(rows):
    rows = normalize_prompt_rows(rows)
    enabled = {row.get("agent_key") for row in rows if row.get("enabled")}
    missing = sorted(set(REQUIRED_AGENT_KEYS) - enabled)
    if missing:
        raise MCPError("missing enabled prompt_template agent_keys: %s" % ", ".join(missing))


def normalize_prompt_rows(payload):
    if isinstance(payload, dict):
        payload = payload.get("items")
    if not isinstance(payload, list):
        raise MCPError("prompt_list returned unexpected payload shape")
    if not all(isinstance(row, dict) for row in payload):
        raise MCPError("prompt_list returned non-object rows")
    return payload


def validate_tools(client):
    result = client.request("tools/list", {})
    names = {tool.get("name") for tool in result.get("tools", [])}
    required = {
        "prompt_list",
        "shared_file_list",
        "shared_file_write",
        "task_create_dag",
        "task_dag_apply_ops",
        "task_dispatch_node",
        "task_start_dag",
        "task_get_dag",
    }
    missing = sorted(required - names)
    if missing:
        raise MCPError("missing MCP tools: %s" % ", ".join(missing))


def create_payload(args, dag_key):
    return {
        "agent_id": args.creator,
        "dag_key": dag_key,
        "title": "M3 dogfood prompt-template-first DAG",
        "description": "Phase 4 M3 hard-threshold dogfood harness; no command_card path.",
        "schedule": {
            "trigger": "manual",
            "default_retry": 1,
            "default_timeout_sec": 900,
            "fail_fast": True,
            "max_concurrency": 3,
            "queue_policy": "fifo",
        },
        "nodes": [],
    }


def run(args):
    client = make_client(args)
    try:
        client.request("initialize", {"protocolVersion": "2024-11-05"})
        validate_tools(client)
        prompts = call_tool(client, "prompt_list", {"keyword": ""})
        validate_prompts(prompts)
        call_tool(client, "shared_file_list", {"prefix": args.shared_prefix, "limit": 5})
        call_tool(client, "shared_file_write", {
            "path": _join_shared(args.shared_prefix, "source.md"),
            "content": source_content(),
        })
        call_tool(client, "task_create_dag", create_payload(args, args.dag_key))
        ops = build_main_ops(args.assignee, args.shared_prefix, args.provider, args.model, args.cwd)
        call_tool(client, "task_dag_apply_ops", {"dag_key": args.dag_key, "base_version": 0, "ops": ops})
        started = call_tool(client, "task_start_dag", {
            "dag_key": args.dag_key,
            "trigger_source": "manual",
            "idempotency_key": "m3-main-%s" % int(time.time()),
        })
        print("started main run:", json.dumps(started, ensure_ascii=False))
        run_key = started_run_key(started)
        wait_for_dag(
            client,
            args.dag_key,
            run_key,
            args.timeout_sec,
            args.poll_sec,
            args.assignee,
            expect_failed=False,
            expected_node_keys=node_keys_from_ops(ops),
        )
        if not args.skip_negative:
            run_negative_probe(client, args)
        check_metrics(args)
    finally:
        client.close()


def run_negative_probe(client, args):
    neg_key = "%s-negative" % args.dag_key
    call_tool(client, "task_create_dag", create_payload(args, neg_key))
    ops = build_negative_ops(args.assignee, args.shared_prefix, args.provider, args.model, args.cwd)
    call_tool(client, "task_dag_apply_ops", {"dag_key": neg_key, "base_version": 0, "ops": ops})
    started = call_tool(client, "task_start_dag", {
        "dag_key": neg_key,
        "trigger_source": "manual",
        "idempotency_key": "m3-negative-%s" % int(time.time()),
    })
    print("started negative run:", json.dumps(started, ensure_ascii=False))
    run_key = started_run_key(started)
    wait_for_dag(
        client,
        neg_key,
        run_key,
        args.timeout_sec,
        args.poll_sec,
        args.assignee,
        expect_failed=True,
        expected_node_keys=node_keys_from_ops(ops),
    )


def node_keys_from_ops(ops):
    return [
        op.get("node", {}).get("node_key")
        for op in ops
        if op.get("op") == "add_node" and op.get("node", {}).get("node_key")
    ]


def started_run_key(started):
    if not isinstance(started, dict):
        raise MCPError("task_start_dag returned unexpected payload shape")
    for key in ("run_key", "RunKey", "runKey"):
        run_key = (started.get(key) or "").strip()
        if run_key:
            return run_key
    raise MCPError("task_start_dag response missing run_key")


def run_id_from_detail(detail):
    run = detail.get("run") if isinstance(detail, dict) else None
    if not isinstance(run, dict):
        raise MCPError("task_get_run returned unexpected payload shape")
    run_id = run.get("id") or run.get("ID")
    if not isinstance(run_id, int) or run_id <= 0:
        raise MCPError("task_get_run response missing run.id")
    return run_id


def wait_for_dag(client, dag_key, run_key, timeout_sec, poll_sec, assignee, expect_failed, expected_node_keys=None):
    deadline = time.time() + timeout_sec
    last_counts = {}
    while time.time() < deadline:
        detail = call_tool(client, "task_get_run", {"run_key": run_key})
        if not isinstance(detail, dict):
            raise MCPError("task_get_run returned unexpected payload shape")
        run_id = run_id_from_detail(detail)
        nodes = detail.get("nodes", [])
        dispatch_ready_nodes(client, dag_key, run_id, nodes, assignee)
        last_counts = count_statuses(nodes)
        print("poll %s/%s: %s" % (dag_key, run_key, last_counts))
        missing_nodes = missing_expected_node_keys(nodes, expected_node_keys)
        if nodes and all(n.get("status") in ("done", "failed") for n in nodes):
            if missing_nodes:
                raise MCPError("%s missing expected nodes before terminal success: %s" % (dag_key, ", ".join(missing_nodes)))
            failed = [n for n in nodes if n.get("status") == "failed"]
            if expect_failed and not failed:
                raise MCPError("%s finished without expected validation failure" % dag_key)
            if expect_failed:
                assert_size_cap_failure(failed)
            if not expect_failed and failed:
                raise MCPError("%s has failed nodes: %s" % (dag_key, [n.get("node_key") for n in failed]))
            return
        time.sleep(poll_sec)
    raise MCPError("timeout waiting for %s; last status counts=%s" % (dag_key, last_counts))


def missing_expected_node_keys(nodes, expected_node_keys):
    expected = sorted({key for key in (expected_node_keys or []) if key})
    if not expected:
        return []
    seen = {n.get("node_key") for n in nodes}
    return [key for key in expected if key not in seen]


def count_statuses(nodes):
    counts = {}
    for node in nodes:
        status = node.get("status", "")
        counts[status] = counts.get(status, 0) + 1
    return counts


def dispatch_ready_nodes(client, dag_key, run_id, nodes, assignee, verbose=True):
    dispatched = []
    for node in nodes:
        if node.get("status") != "ready":
            continue
        if (node.get("assigned_to") or "").strip():
            continue
        node_key = node.get("node_key")
        if not node_key:
            continue
        call_tool(client, "task_dispatch_node", {
            "dag_key": dag_key,
            "node_key": node_key,
            "run_id": run_id,
            "assigned_to": assignee,
        })
        dispatched.append(node_key)
    if dispatched and verbose:
        print("dispatched ready nodes:", ", ".join(dispatched))
    return dispatched


def assert_size_cap_failure(failed_nodes):
    target = next((n for n in failed_nodes if n.get("node_key") == "long_node_result_rejected"), None)
    if not target:
        raise MCPError("negative probe failed a different node: %s" % [n.get("node_key") for n in failed_nodes])
    reason = node_failure_reason(target)
    normalized = reason.lower()
    if not (
        "result exceeds" in normalized
        and "4kb" in normalized
        and ("size cap" in normalized or "adr-006" in normalized)
    ):
        raise MCPError("negative probe failure reason does not look like ADR-006 size-cap validation: %s" % reason)


def node_failure_reason(node):
    raw = node.get("result")
    if isinstance(raw, dict):
        return str(raw.get("reason", raw))
    if isinstance(raw, str):
        try:
            parsed = json.loads(raw)
        except json.JSONDecodeError:
            return raw
        if isinstance(parsed, dict):
            return str(parsed.get("reason", parsed))
        return str(parsed)
    return str(raw)


def check_metrics(args):
    if not args.metrics_url:
        msg = "metrics URL not set; pass --metrics-url or M3_DOGFOOD_METRICS_URL"
        if args.allow_missing_metrics:
            print("SKIP:", msg)
            return
        raise MCPError(msg)
    opener = _opener_for_url(args.metrics_url)
    with opener.open(args.metrics_url, timeout=10) as resp:
        body = resp.read().decode("utf-8", errors="replace")
    families = parse_prometheus_metrics(body)
    missing = missing_required_metric_names(families, args.require_metric_samples)
    if missing:
        raise MCPError("metrics endpoint missing: %s" % ", ".join(missing))
    if args.require_metric_samples:
        retry_metrics_dag_key = (getattr(args, "retry_metrics_dag_key", "") or "").strip()
        if not retry_metrics_dag_key:
            raise MCPError(
                "strict metrics require --retry-metrics-dag-key from a dispatcher retry probe, "
                "or pass --metrics-family-only for family presence only"
            )
        validate_metric_samples(families, retry_metrics_dag_key)
    summary = []
    for name in required_metric_names():
        if name in families:
            samples = families[name]["samples"]
            value = samples[0] if samples else "family-present"
        else:
            value = "%s-present" % sample_backed_metric_fallbacks()[name]
        summary.append("%s=%s" % (name, value))
    print("metrics ok:", "; ".join(summary))


def missing_required_metric_names(families, require_metric_samples):
    missing = []
    fallbacks = sample_backed_metric_fallbacks()
    for name in required_metric_names():
        if name in families:
            continue
        fallback = fallbacks.get(name)
        if fallback and fallback in families and not require_metric_samples:
            continue
        missing.append(name)
    return missing


def parse_prometheus_metrics(body):
    families = {}
    for line in body.splitlines():
        stripped = line.strip()
        if not stripped:
            continue
        if stripped.startswith("# HELP ") or stripped.startswith("# TYPE "):
            parts = stripped.split()
            if len(parts) >= 3:
                families.setdefault(parts[2], {"samples": []})
            continue
        name = stripped.split(None, 1)[0].split("{", 1)[0]
        families.setdefault(name, {"samples": []})["samples"].append(stripped)
    return families


def validate_metric_samples(families, retry_metrics_dag_key):
    dispatch = [parse_metric_sample(s) for s in families["dispatch_failed_total"]["samples"]]
    dispatch_values = [s["value"] for s in dispatch if s and s["value"] is not None]
    if not dispatch_values or max(dispatch_values) <= 0:
        raise MCPError("dispatch_failed_total has no positive sample after dogfood failure probe")

    retry = [parse_metric_sample(s) for s in families["retry_count_per_node"]["samples"]]
    retry = [s for s in retry if s and s["labels"].get("dag_key") == retry_metrics_dag_key]
    retry_values = [s["value"] for s in retry if s["value"] is not None]
    if not retry_values or max(retry_values) <= 0:
        raise MCPError(
            "retry_count_per_node has no positive sample for retry metrics dag_key %s; "
            "run a dispatcher retry probe or pass --metrics-family-only"
            % retry_metrics_dag_key
        )


def parse_metric_sample(line):
    match = re.match(r"^([A-Za-z_:][A-Za-z0-9_:]*)(?:\{([^}]*)\})?\s+([-+0-9.eE]+)\b", line)
    if not match:
        return None
    try:
        value = float(match.group(3))
    except ValueError:
        value = None
    return {"name": match.group(1), "labels": parse_metric_labels(match.group(2) or ""), "value": value}


def parse_metric_labels(raw):
    labels = {}
    if not raw:
        return labels
    for item in raw.split(","):
        key, sep, value = item.partition("=")
        if not sep:
            continue
        labels[key.strip()] = value.strip().strip('"')
    return labels


def make_client(args):
    if args.mcp_url:
        client = HTTPMCPClient(args.mcp_url, args.rpc_timeout_sec)
    else:
        env = os.environ.copy()
        env.setdefault("PROJECT_ROOT", str(repo_root()))
        client = StdioMCPClient(shlex.split(args.mcp_cmd), str(repo_root()), env, args.rpc_timeout_sec)
    client.tool_scope = tool_scope(args)
    return client


def tool_scope(args):
    cwd = pathlib.Path(args.cwd).resolve()
    return {
        "_agentId": args.creator,
        "_threadId": args.creator,
        "_callId": "dag-m3-dogfood",
        "_cwd": str(cwd),
        "_workspaceRoots": [str(cwd)],
    }


def default_mcp_cmd():
    candidate = repo_root() / "bin" / "mcp-orch"
    if candidate.exists():
        return str(candidate)
    return "go run ./cmd/mcp-orch"


def repo_root():
    return pathlib.Path(__file__).resolve().parents[1]


def source_content():
    return "\n".join([
        "# M3 Dogfood Source Pack",
        "",
        "- Goal: validate prompt-template-first DAG lifecycle.",
        "- Constraints: no command_card, no webhook/http/shell expansion.",
        "- Required checks: 10 nodes, downstream inputs.from_nodes, >4KB sharedfile output, metrics.",
        "- Scenario: synthesize source monitoring, data inspection, PR review, health reporting, and delivery writing outputs.",
    ])


def print_checklist():
    for i, item in enumerate(CHECKLIST, 1):
        print("%d. %s" % (i, item))


def print_dry_run(args):
    payload = {
        "create_dag": create_payload(args, args.dag_key),
        "apply_ops": {
            "dag_key": args.dag_key,
            "base_version": 0,
            "ops": build_main_ops(args.assignee, args.shared_prefix, args.provider, args.model, args.cwd),
        },
        "negative_apply_ops": {
            "dag_key": "%s-negative" % args.dag_key,
            "base_version": 0,
            "ops": build_negative_ops(args.assignee, args.shared_prefix, args.provider, args.model, args.cwd),
        },
        "metrics": required_metric_names(),
        "retry_metrics_dag_key": args.retry_metrics_dag_key,
        "checklist": CHECKLIST,
    }
    print(json.dumps(payload, indent=2, ensure_ascii=False))


def parse_args(argv):
    now = dt.datetime.now().strftime("%Y%m%d-%H%M%S")
    parser = argparse.ArgumentParser(description="Phase 4 / M3 DAG hard-threshold dogfood harness.")
    parser.add_argument("--mode", choices=("checklist", "dry-run", "run"), default="checklist")
    parser.add_argument("--dag-key", default=os.getenv("M3_DOGFOOD_DAG_KEY", "m3-dogfood-%s" % now))
    parser.add_argument("--creator", default=os.getenv("M3_DOGFOOD_AGENT_ID", "agent_m3_dogfood_runner"))
    parser.add_argument("--assignee", default=os.getenv("M3_DOGFOOD_ASSIGN_TO", os.getenv("M3_DOGFOOD_AGENT_ID", "agent_m3_dogfood_runner")))
    parser.add_argument("--shared-prefix", default=os.getenv("M3_DOGFOOD_SHARED_PREFIX", ""))
    parser.add_argument("--provider", default=os.getenv("M3_DOGFOOD_PROVIDER", "codex"))
    parser.add_argument("--model", default=os.getenv("M3_DOGFOOD_MODEL", "gpt-5"))
    parser.add_argument("--cwd", default=os.getenv("M3_DOGFOOD_CWD", str(repo_root())))
    parser.add_argument("--mcp-url", default=os.getenv("M3_DOGFOOD_MCP_URL", os.getenv("MCP_ORCH_HTTP_URL", "")))
    parser.add_argument("--mcp-cmd", default=os.getenv("MCP_ORCH_CMD", default_mcp_cmd()))
    parser.add_argument("--metrics-url", default=os.getenv("M3_DOGFOOD_METRICS_URL", ""))
    parser.add_argument("--retry-metrics-dag-key", default=os.getenv("M3_DOGFOOD_RETRY_METRICS_DAG_KEY", ""))
    parser.add_argument("--timeout-sec", type=int, default=int(os.getenv("M3_DOGFOOD_TIMEOUT_SEC", "900")))
    parser.add_argument("--poll-sec", type=int, default=int(os.getenv("M3_DOGFOOD_POLL_SEC", "10")))
    parser.add_argument("--rpc-timeout-sec", type=int, default=int(os.getenv("M3_DOGFOOD_RPC_TIMEOUT_SEC", "60")))
    parser.add_argument("--skip-negative", action="store_true")
    parser.add_argument("--allow-missing-metrics", action="store_true")
    parser.add_argument("--metrics-family-only", action="store_true")
    parser.add_argument("--require-metric-samples", action="store_true", default=None, help=argparse.SUPPRESS)
    args = parser.parse_args(argv)
    args.cwd = str(pathlib.Path(args.cwd).resolve())
    args.shared_prefix = (args.shared_prefix or "").strip()
    if not args.shared_prefix:
        args.shared_prefix = default_shared_prefix(args.dag_key)
    if args.require_metric_samples is None:
        args.require_metric_samples = not args.metrics_family_only
    return args


def main(argv):
    args = parse_args(argv)
    if args.mode == "checklist":
        print_checklist()
        return 0
    if args.mode == "dry-run":
        print_dry_run(args)
        return 0
    try:
        run(args)
        return 0
    except Exception as exc:
        print("FAIL: %s" % exc, file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
