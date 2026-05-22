#!/usr/bin/env python3
import argparse
import hashlib
import json
import pathlib
import secrets
import shutil
import socket
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request


RECALL_BODY_MARKER = "PROMPT_INTENT_RECALL_BODY_MARKER"
EXTERNAL_RECALL_BODY_MARKER = "EXTERNAL_PROVIDER_RECALL_BODY_MARKER"
MIN_SCHEMA_VERSION = 101


class E2EError(RuntimeError):
    pass


class JSONRPCClient:
    def __init__(self, addr, timeout):
        self.addr = addr
        self.timeout = timeout
        self.next_id = 1

    def request(self, method, params=None):
        request_id = self.next_id
        payload = {
            "jsonrpc": "2.0",
            "id": request_id,
            "method": method,
            "params": params or {},
        }
        self.next_id += 1
        try:
            with socket.create_connection(_split_host_port(self.addr), timeout=self.timeout) as conn:
                conn.settimeout(self.timeout)
                line = json.dumps(payload, separators=(",", ":")) + "\n"
                conn.sendall(line.encode("utf-8"))
                raw = _read_response_for_id(conn, request_id)
        except socket.timeout as exc:
            raise E2EError(
                "desktop RPC endpoint %s timed out waiting for response id %s" % (self.addr, request_id)
            ) from exc
        except OSError as exc:
            raise E2EError("desktop RPC endpoint %s is unreachable: %s" % (self.addr, exc)) from exc
        return _decode_rpc_response(raw)


def call_mcp_tool(mcp_url, name, arguments, cwd, timeout):
    assert_prompt_recall_arguments(arguments)
    params = {
        "name": name,
        "arguments": arguments,
        "_agentId": "prompt-intent-e2e",
        "_threadId": "prompt-intent-e2e-thread",
        "_callId": "prompt-intent-e2e-call",
        "_cwd": cwd,
        "_workspaceRoots": [cwd],
    }
    payload = {
        "jsonrpc": "2.0",
        "id": 1,
        "method": "tools/call",
        "params": params,
    }
    body = json.dumps(payload, separators=(",", ":")).encode("utf-8")
    req = urllib.request.Request(
        _normalize_mcp_url(mcp_url),
        data=body,
        headers={"content-type": "application/json"},
        method="POST",
    )
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        with opener.open(req, timeout=timeout) as resp:
            raw = resp.read().decode("utf-8")
    except (OSError, urllib.error.URLError) as exc:
        raise E2EError("MCP endpoint %s is unreachable: %s" % (mcp_url, exc)) from exc
    return _decode_rpc_response(raw)


def assert_prompt_recall_arguments(arguments):
    raw = json.dumps(arguments, sort_keys=True, separators=(",", ":"))
    forbidden = ("cwd", "project", "scope")
    for key in forbidden:
        if key in arguments or ('"%s"' % key) in raw:
            raise E2EError("prompt_recall arguments must not contain %s: %s" % (key, raw))


def run(args):
    fixture_path = pathlib.Path(args.dream_fixture)
    if not fixture_path.is_file():
        raise E2EError("--dream-fixture is not readable: %s" % fixture_path)

    db_before = verify_database(args.database_url)
    rpc = JSONRPCClient(args.rpc_addr, args.timeout)
    caveats = []
    if args.real_dream:
        caveats.append("real-dream mode: fixture provider health assertion skipped")
    else:
        health = rpc.request("prompt-intents/e2e-health", {})
        assert_fixture_health(health, fixture_path)

    expert = create_and_commit_intent(
        rpc,
        "expert",
        run_input("Create prompt intent E2E expert fixture for available_experts validation.", args.run_id),
        args.cwd_a,
        confirm_risk=False,
    )
    expert_thread = start_thread(rpc, args.cwd_a, args.provider, "Prompt Intent E2E Expert Snapshot")
    expert_snapshot = load_prompt_snapshot(args.database_url, expert_thread["thread_id"])
    assert_contains(expert_snapshot, "available_experts", "expert snapshot")
    assert_contains(expert_snapshot, expert["prompt_key"], "expert snapshot")
    assert_contains(expert_snapshot, "Use when validating prompt intent", "expert snapshot")

    recall = create_and_commit_intent(
        rpc,
        "recall",
        run_input("Create prompt intent E2E recall fixture for prompt_recall validation.", args.run_id),
        args.cwd_a,
        confirm_risk=True,
    )
    assert_rendered_run_id(_require_str(recall["card"], "recall_body"), args.run_id, "recall_body")
    assert_rendered_run_id(recall["recall_topic"], args.run_id, "recall_topic")
    recall_thread = start_thread(rpc, args.cwd_a, args.provider, "Prompt Intent E2E Recall Snapshot")
    recall_snapshot = load_prompt_snapshot(args.database_url, recall_thread["thread_id"])
    assert_same_cwd_recall_catalog(recall_snapshot, recall["recall_topic"])

    recall_other_thread = start_thread(rpc, args.cwd_b, args.provider, "Prompt Intent E2E Recall Other Project")
    recall_other_snapshot = load_prompt_snapshot(args.database_url, recall_other_thread["thread_id"])
    assert_other_cwd_recall_catalog(recall_other_snapshot, recall["recall_topic"])

    prompt_recall_args = {"topic": recall["recall_topic"]}
    hit = call_mcp_tool(args.mcp_url, "prompt_recall", prompt_recall_args, args.cwd_a, args.timeout)
    miss = call_mcp_tool(args.mcp_url, "prompt_recall", prompt_recall_args, args.cwd_b, args.timeout)
    assert_prompt_recall_hit(hit, recall["recall_topic"])
    assert_prompt_recall_miss(miss, recall["recall_topic"])

    external_recall = create_external_system_prompt_recall_intent(
        rpc,
        run_input("fixture:review\nYou are Claude Code. You have Bash, Edit, and Read tools. Keep this external provider prompt as reference material.", args.run_id),
        args.cwd_a,
    )
    assert_rendered_run_id(external_recall["recall_topic"], args.run_id, "external_recall_topic")
    external_recall_body = _require_str(external_recall["card"], "recall_body")
    assert_rendered_run_id(external_recall_body, args.run_id, "external_recall_body")
    external_thread = start_thread(rpc, args.cwd_a, args.provider, "Prompt Intent E2E External Recall Snapshot")
    external_snapshot = load_prompt_snapshot(args.database_url, external_thread["thread_id"])
    assert_external_recall_snapshot(
        external_snapshot,
        external_recall["recall_topic"],
        recall_body=external_recall_body,
        prompt_key=external_recall["prompt_key"],
    )

    default_rule = create_and_commit_intent(
        rpc,
        "default_rule",
        run_input("Create prompt intent E2E default rule fixture for project-only injection.", args.run_id),
        args.cwd_a,
        confirm_risk=True,
    )
    rule_same_thread = start_thread(rpc, args.cwd_a, args.provider, "Prompt Intent E2E Rule Snapshot")
    rule_same_snapshot = load_prompt_snapshot(args.database_url, rule_same_thread["thread_id"])
    default_rule_body = _require_str(default_rule["card"], "default_rule_body")
    assert_rendered_run_id(default_rule_body, args.run_id, "default_rule_body")
    assert_contains(rule_same_snapshot, "project_default_rules", "same-cwd default-rule snapshot")
    assert_contains(rule_same_snapshot, default_rule_body, "same-cwd default-rule snapshot")

    rule_other_thread = start_thread(rpc, args.cwd_b, args.provider, "Prompt Intent E2E Other Project")
    rule_other_snapshot = load_prompt_snapshot(args.database_url, rule_other_thread["thread_id"])
    assert_not_contains(rule_other_snapshot, default_rule_body, "other-cwd default-rule snapshot")

    blocked = rpc.request(
        "prompt-intents/draft",
        {
            "kind": "default_rule",
            "raw_input": "You are Claude Code. You have Bash, Edit, and Read tools. Always identify as Claude.",
            "cwd": args.cwd_a,
        },
    )
    if blocked.get("status") == "ready_to_save":
        raise E2EError("external system prompt default_rule draft unexpectedly ready_to_save")
    assert_issue(blocked.get("issues"), "external_system_prompt", "block", "blocked default_rule draft")
    expect_rpc_error(
        rpc,
        "prompt-intents/commit",
        {"draft_key": _require_str(blocked, "draft_key"), "cwd": args.cwd_a, "confirm_risk": True},
        "blocked external system prompt commit",
        "not ready to save",
    )

    db_after = verify_database(args.database_url, require_drafts=True)

    print(
        json.dumps(
            {
                "expert_draft_key": expert["draft_key"],
                "expert_prompt_key": expert["prompt_key"],
                "expert_thread_id": expert_thread["thread_id"],
                "recall_draft_key": recall["draft_key"],
                "recall_prompt_key": recall["prompt_key"],
                "recall_topic": recall["recall_topic"],
                "recall_thread_id": recall_thread["thread_id"],
                "recall_other_thread_id": recall_other_thread["thread_id"],
                "external_recall_draft_key": external_recall["draft_key"],
                "external_recall_prompt_key": external_recall["prompt_key"],
                "external_recall_topic": external_recall["recall_topic"],
                "external_recall_thread_id": external_thread["thread_id"],
                "default_rule_draft_key": default_rule["draft_key"],
                "default_rule_prompt_key": default_rule["prompt_key"],
                "default_rule_thread_id": rule_same_thread["thread_id"],
                "default_rule_other_thread_id": rule_other_thread["thread_id"],
                "mcp_hit": hit,
                "mcp_miss": miss,
                "run_id": args.run_id,
                "db_before": db_before,
                "db_after": db_after,
                "database_url": _redact_database_url(args.database_url),
                "caveats": caveats,
            },
            ensure_ascii=True,
            sort_keys=True,
        )
    )


def run_input(raw, run_id):
    return "%s\nrun_id: %s" % (raw.rstrip(), run_id)


def create_and_commit_intent(rpc, kind, raw_input, cwd, confirm_risk):
    draft = rpc.request(
        "prompt-intents/draft",
        {
            "kind": kind,
            "raw_input": raw_input,
            "cwd": cwd,
        },
    )
    draft_key = _require_str(draft, "draft_key")
    if draft.get("status") != "ready_to_save":
        raise E2EError("%s draft is not ready_to_save: %r" % (kind, draft))
    card = draft.get("card") or {}
    commit = rpc.request(
        "prompt-intents/commit",
        {
            "draft_key": draft_key,
            "cwd": cwd,
            "confirm_risk": bool(confirm_risk),
        },
    )
    prompt_key = _require_str(commit, "prompt_key")
    result = {
        "draft_key": draft_key,
        "prompt_key": prompt_key,
        "card": card,
    }
    if kind == "recall":
        result["recall_topic"] = _require_str(card, "recall_topic")
    return result


def create_external_system_prompt_recall_intent(rpc, raw_input, cwd):
    draft = rpc.request(
        "prompt-intents/draft",
        {
            "kind": "recall",
            "raw_input": raw_input,
            "cwd": cwd,
        },
    )
    draft_key = _require_str(draft, "draft_key")
    if draft.get("status") != "ready_to_save":
        raise E2EError("external system prompt recall draft is not ready_to_save: %r" % draft)
    assert_issue(draft.get("issues"), "external_system_prompt_source", "review", "external system prompt recall draft")
    expect_rpc_error(
        rpc,
        "prompt-intents/commit",
        {"draft_key": draft_key, "cwd": cwd, "confirm_risk": False},
        "external system prompt recall commit without confirmation",
        "requires risk confirmation",
    )
    commit = rpc.request(
        "prompt-intents/commit",
        {"draft_key": draft_key, "cwd": cwd, "confirm_risk": True},
    )
    card = draft.get("card") or {}
    return {
        "draft_key": draft_key,
        "prompt_key": _require_str(commit, "prompt_key"),
        "recall_topic": _require_str(card, "recall_topic"),
        "card": card,
    }


def assert_fixture_health(health, fixture_path):
    if health.get("provider") != "e2e-fixture":
        raise E2EError("expected e2e fixture provider, got %r" % health)
    expected = fixture_content_hash(fixture_path)
    actual = (health.get("fixture_path_hash") or "").strip()
    if actual != expected:
        raise E2EError("fixture hash mismatch: RPC returned %r, local fixture is %r" % (actual, expected))


def assert_rendered_run_id(value, run_id, label):
    value = (value or "").strip()
    if run_id not in value:
        raise E2EError("%s does not contain run id %r: %r" % (label, run_id, value))
    if "{{RUN_ID}}" in value:
        raise E2EError("%s still contains unrendered run id placeholder: %r" % (label, value))


def fixture_content_hash(fixture_path):
    data = pathlib.Path(fixture_path).read_bytes()
    return hashlib.sha256(data).hexdigest()[:16]


def start_thread(rpc, cwd, provider, name):
    result = rpc.request(
        "thread/start",
        {
            "provider": provider,
            "cwd": cwd,
            "name": name,
            "prompt": name,
        },
    )
    thread_id = _require_str(result, "thread_id")
    return {"thread_id": thread_id, "result": result}


def expect_rpc_error(rpc, method, params, label, want_message=""):
    try:
        rpc.request(method, params)
    except E2EError as exc:
        if want_message and want_message not in str(exc):
            raise E2EError("%s error = %s, want contains %r" % (label, exc, want_message)) from exc
        return
    raise E2EError("%s unexpectedly succeeded" % label)


def verify_database(database_url, require_drafts=False):
    max_version = int(run_psql_scalar(database_url, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations;"))
    if max_version < MIN_SCHEMA_VERSION:
        raise E2EError("schema version %d is below required %d" % (max_version, MIN_SCHEMA_VERSION))
    draft_table = run_psql_scalar(database_url, "SELECT COALESCE(to_regclass('public.prompt_intent_drafts')::text, '');")
    if draft_table != "prompt_intent_drafts":
        raise E2EError("prompt_intent_drafts table missing")
    old_index = run_psql_scalar(database_url, "SELECT COALESCE(to_regclass('public.idx_prompt_sections_recall_topic')::text, '');")
    if old_index != "":
        raise E2EError("old global recall topic index still exists: %s" % old_index)
    lookup_index = run_psql_scalar(database_url, "SELECT COALESCE(to_regclass('public.idx_prompt_sections_recall_topic_lookup')::text, '');")
    if lookup_index != "idx_prompt_sections_recall_topic_lookup":
        raise E2EError("scoped recall lookup index missing")
    draft_count = int(run_psql_scalar(database_url, "SELECT count(*) FROM prompt_intent_drafts;"))
    if require_drafts and draft_count < 1:
        raise E2EError("prompt_intent_drafts should contain at least one draft after E2E")
    return {"schema_version": max_version, "draft_count": draft_count}


def load_prompt_snapshot(database_url, thread_id):
    snapshot = run_psql_scalar(
        database_url,
        "SELECT COALESCE(prompt_snapshot::text, '') FROM agent_threads WHERE thread_id = :'thread_id' LIMIT 1;",
        {"thread_id": thread_id},
    )
    if not snapshot:
        raise E2EError("thread %s has empty prompt_snapshot" % thread_id)
    return snapshot


def run_psql_scalar(database_url, sql, variables=None):
    psql = shutil.which("psql")
    if not psql:
        raise E2EError("psql is required for prompt intent E2E DB verification")
    cmd = [psql, database_url, "-AtX", "-v", "ON_ERROR_STOP=1"]
    for key, value in sorted((variables or {}).items()):
        cmd.extend(["-v", "%s=%s" % (key, value)])
    proc = subprocess.run(cmd, input=sql, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
    if proc.returncode != 0:
        raise E2EError("psql query failed: %s" % (proc.stderr.strip() or proc.stdout.strip()))
    lines = [line for line in proc.stdout.splitlines() if line.strip()]
    if not lines:
        return ""
    if len(lines) > 1:
        raise E2EError("psql scalar query returned multiple rows: %r" % lines)
    return lines[0].strip()


def assert_prompt_recall_hit(result, topic):
    payload = _tool_structured_payload(result)
    if payload.get("success") is False:
        raise E2EError("same-cwd prompt_recall returned tool error: %r" % payload)
    if payload.get("topic") != topic:
        raise E2EError("same-cwd prompt_recall topic = %r, want %r" % (payload.get("topic"), topic))
    body = payload.get("body")
    if not isinstance(body, str) or RECALL_BODY_MARKER not in body:
        raise E2EError("same-cwd prompt_recall body missing marker: %r" % payload)
    if payload.get("error"):
        raise E2EError("same-cwd prompt_recall returned soft error: %r" % payload)


def assert_prompt_recall_miss(result, topic):
    payload = _tool_structured_payload(result)
    if payload.get("success") is False:
        raise E2EError("other-cwd prompt_recall returned tool error instead of soft miss: %r" % payload)
    if payload.get("topic") != topic:
        raise E2EError("other-cwd prompt_recall topic = %r, want %r" % (payload.get("topic"), topic))
    if payload.get("body") or payload.get("length"):
        raise E2EError("other-cwd prompt_recall unexpectedly returned body: %r" % payload)
    if payload.get("error") != "unknown topic":
        raise E2EError("other-cwd prompt_recall error = %r, want unknown topic: %r" % (payload.get("error"), payload))


def assert_same_cwd_recall_catalog(snapshot, topic):
    assert_contains(snapshot, "recall_catalog", "same-cwd recall snapshot")
    assert_contains(snapshot, topic, "same-cwd recall snapshot")
    assert_not_contains(snapshot, RECALL_BODY_MARKER, "same-cwd recall snapshot")


def assert_other_cwd_recall_catalog(snapshot, topic):
    assert_not_contains(snapshot, topic, "other-cwd recall snapshot")
    assert_not_contains(snapshot, RECALL_BODY_MARKER, "other-cwd recall snapshot")


def assert_external_recall_snapshot(snapshot, topic, recall_body="", prompt_key=""):
    assert_contains(snapshot, "recall_catalog", "external recall snapshot")
    assert_contains(snapshot, topic, "external recall snapshot")
    assert_not_contains(snapshot, "project_default_rules", "external recall snapshot")
    assert_not_contains(snapshot, EXTERNAL_RECALL_BODY_MARKER, "external recall snapshot")
    if prompt_key:
        assert_not_contains(snapshot, prompt_key, "external recall snapshot")
    if recall_body:
        assert_not_contains(snapshot, recall_body, "external recall snapshot")


def _tool_structured_payload(result):
    if not isinstance(result, dict):
        raise E2EError("tools/call result must be an object: %r" % result)
    structured = result.get("structuredContent")
    if isinstance(structured, dict):
        return structured
    content = result.get("content")
    if isinstance(content, list) and content:
        text = content[0].get("text") if isinstance(content[0], dict) else None
        if isinstance(text, str):
            try:
                parsed = json.loads(text)
            except json.JSONDecodeError as exc:
                raise E2EError("tools/call content text is not JSON: %s" % exc) from exc
            if isinstance(parsed, dict):
                return parsed
    raise E2EError("tools/call result missing structured payload: %r" % result)


def assert_contains(text, marker, label):
    if marker not in text:
        raise E2EError("%s missing marker %r" % (label, marker))


def assert_not_contains(text, marker, label):
    if marker in text:
        raise E2EError("%s unexpectedly contains marker %r" % (label, marker))


def assert_issue(issues, code, severity, label):
    if not isinstance(issues, list):
        raise E2EError("%s issues must be a list: %r" % (label, issues))
    for issue in issues:
        if not isinstance(issue, dict):
            continue
        if issue.get("code") == code and issue.get("severity") == severity:
            return
    raise E2EError("%s missing issue code=%s severity=%s: %r" % (label, code, severity, issues))


def _require_str(mapping, key):
    value = mapping.get(key) if isinstance(mapping, dict) else None
    if not isinstance(value, str) or not value.strip():
        raise E2EError("missing non-empty %s in %r" % (key, mapping))
    return value.strip()


def _split_host_port(addr):
    host, sep, port = addr.rpartition(":")
    if not sep or not host or not port:
        raise E2EError("invalid --rpc-addr %r, want host:port" % addr)
    return (host, int(port))


def _readline(conn, empty_message="RPC connection closed without a response"):
    chunks = []
    while True:
        part = conn.recv(1)
        if not part:
            break
        chunks.append(part)
        if part == b"\n":
            break
    if not chunks:
        raise E2EError(empty_message)
    return b"".join(chunks).decode("utf-8")


def _read_response_for_id(conn, request_id):
    while True:
        raw = _readline(conn, "RPC connection closed before response id %s" % request_id)
        try:
            resp = json.loads(raw)
        except json.JSONDecodeError as exc:
            raise E2EError("invalid JSON-RPC response %r: %s" % (raw, exc)) from exc
        if resp.get("id") != request_id:
            continue
        return raw


def _decode_rpc_response(raw):
    try:
        resp = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise E2EError("invalid JSON-RPC response %r: %s" % (raw, exc)) from exc
    if resp.get("error"):
        err = resp["error"]
        raise E2EError("JSON-RPC error %s: %s" % (err.get("code"), err.get("message")))
    if "result" not in resp:
        raise E2EError("JSON-RPC response missing result: %r" % resp)
    return resp["result"]


def _normalize_mcp_url(url):
    parsed = urllib.parse.urlparse(url)
    if parsed.path in ("", "/"):
        parsed = parsed._replace(path="/mcp")
    return urllib.parse.urlunparse(parsed)


def _redact_database_url(url):
    parsed = urllib.parse.urlparse(url)
    if parsed.password is None:
        return url
    netloc = parsed.netloc.replace(":" + parsed.password + "@", ":***@")
    return urllib.parse.urlunparse(parsed._replace(netloc=netloc))


def parse_args(argv):
    parser = argparse.ArgumentParser(description="Prompt intent E2E fixture driver")
    parser.add_argument("--rpc-addr", required=True)
    parser.add_argument("--mcp-url", required=True)
    parser.add_argument("--database-url", required=True)
    parser.add_argument("--dream-fixture", required=True)
    parser.add_argument("--cwd-a", required=True)
    parser.add_argument("--cwd-b", required=True)
    parser.add_argument("--provider", default="codex")
    parser.add_argument("--timeout", type=float, default=10)
    parser.add_argument("--run-id", default="")
    parser.add_argument("--real-dream", action="store_true")
    args = parser.parse_args(argv)
    for name, value in vars(args).items():
        if name in ("real_dream", "timeout", "run_id"):
            continue
        if not isinstance(value, str) or not value.strip():
            raise E2EError("--%s must be non-empty" % name.replace("_", "-"))
    if args.timeout <= 0:
        raise E2EError("--timeout must be positive")
    args.run_id = normalize_run_id(args.run_id or secrets.token_hex(4))
    return args


def normalize_run_id(raw):
    value = (raw or "").strip().lower()
    if not (4 <= len(value) <= 24):
        raise E2EError("--run-id must be 4-24 lowercase letters, digits, or dashes")
    if any(ch not in "abcdefghijklmnopqrstuvwxyz0123456789-" for ch in value):
        raise E2EError("--run-id must be 4-24 lowercase letters, digits, or dashes")
    return value


def main(argv):
    try:
        run(parse_args(argv))
    except E2EError as exc:
        print("prompt_intent_e2e: %s" % exc, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
