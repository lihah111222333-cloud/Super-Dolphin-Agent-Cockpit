#!/usr/bin/env python3
"""
Evaluate tool-routing quality on a fixed 100-case LSP-19 benchmark.

Supports:
- single-pass routing
- two-pass routing (stage1 recall + stage2 rerank)
- alias normalization and trigger policies

Examples:
  # baseline
  python3 scripts/lsp_router_eval.py \
    --models qwen2.5:7b qwen2.5:14b \
    --out-dir test-results/router-eval/2026-02-23-lsp19-100-baseline

  # two-pass: 7b -> 14b rerank
  python3 scripts/lsp_router_eval.py \
    --models qwen2.5:7b \
    --rerank-model qwen2.5:14b \
    --rerank-mode parse_or_ambiguous \
    --out-dir test-results/router-eval/2026-02-23-lsp19-100-two-pass
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import pathlib
import re
import ssl
import statistics
import time
import urllib.error
import urllib.request
from typing import Dict, List, Optional, Tuple


LSP_TOOLS: List[str] = [
    "lsp_open_file",
    "lsp_document_symbol",
    "lsp_hover",
    "lsp_diagnostics",
    "lsp_definition",
    "lsp_references",
    "lsp_rename",
    "lsp_completion",
    "lsp_did_change",
    "lsp_workspace_symbol",
    "lsp_implementation",
    "lsp_type_definition",
    "lsp_call_hierarchy",
    "lsp_type_hierarchy",
    "lsp_code_action",
    "lsp_signature_help",
    "lsp_format",
    "lsp_semantic_tokens",
    "lsp_folding_range",
]

LSP_TOOL_SET = set(LSP_TOOLS)

TOOL_ALIASES: Dict[str, str] = {
    "lsp_typeDefinition": "lsp_type_definition",
    "lspDefinition": "lsp_definition",
    "lsp_documentSymbol": "lsp_document_symbol",
    "lsp_workspaceSymbol": "lsp_workspace_symbol",
    "lsp_callHierarchy": "lsp_call_hierarchy",
    "lsp_typeHierarchy": "lsp_type_hierarchy",
    "lsp_codeAction": "lsp_code_action",
    "lsp_signatureHelp": "lsp_signature_help",
    "lsp_semanticTokens": "lsp_semantic_tokens",
    "lsp_foldingRange": "lsp_folding_range",
}

TOOL_ALIASES_LOWER: Dict[str, str] = {
    k.lower(): v for k, v in TOOL_ALIASES.items()
}

AMBIGUOUS_TOOLS = {
    "lsp_implementation",
    "lsp_type_definition",
    "lsp_type_hierarchy",
    "lsp_definition",
    "lsp_workspace_symbol",
    "lsp_code_action",
}

QUERY_HINT_RULES: List[Tuple[re.Pattern[str], str]] = [
    (re.compile(r"(实现|implementation|implement|trait|接口实现)", re.I), "lsp_implementation"),
    (re.compile(r"(类型定义|type definition|底层类型|类型声明)", re.I), "lsp_type_definition"),
    (re.compile(r"(继承|type hierarchy|supertypes|subtypes|父类|子类)", re.I), "lsp_type_hierarchy"),
    (re.compile(r"(调用层级|call hierarchy|incoming|outgoing|调用关系)", re.I), "lsp_call_hierarchy"),
    (re.compile(r"(quick fix|code action|重构建议|修复动作)", re.I), "lsp_code_action"),
    (re.compile(r"(workspace|全工程|跨文件.*symbol|workspace symbol)", re.I), "lsp_workspace_symbol"),
    (re.compile(r"(跳转定义|go to definition|定义位置)", re.I), "lsp_definition"),
    (re.compile(r"(引用|references|调用点)", re.I), "lsp_references"),
    (re.compile(r"(签名|signature|参数提示)", re.I), "lsp_signature_help"),
    (re.compile(r"(格式化|format)", re.I), "lsp_format"),
    (re.compile(r"(语义.*token|semantic token)", re.I), "lsp_semantic_tokens"),
    (re.compile(r"(折叠|folding)", re.I), "lsp_folding_range"),
]


TOOL_QUERIES: Dict[str, List[str]] = {
    "lsp_open_file": [
        "请先打开 internal/apiserver/methods.go 文件，我要开始分析。",
        "先把 internal/lsp/manager.go 打开到 LSP 上下文里。",
        "分析前先 open 这个文件：cmd/agent-terminal/main.go。",
        "把 pkg/logger/logger.go 先加载到编辑器上下文。",
        "先打开 internal/config/config.go，后面再看代码。",
    ],
    "lsp_document_symbol": [
        "列出 internal/lsp/manager.go 的函数和类型大纲。",
        "给我这个文件的 symbol 列表（函数/方法/结构体）。",
        "查看 cmd/agent-terminal/main.go 的文档符号树。",
        "我想看 internal/apiserver/server.go 的 outline。",
        "请返回这个 Go 文件的顶层符号清单。",
    ],
    "lsp_hover": [
        "在 internal/lsp/manager.go 第120行第15列做 hover 看类型说明。",
        "帮我查看变量 ctx 的 hover 文档。",
        "鼠标悬停位置 line 45 col 18，取出注释与签名。",
        "看一下函数 NewServer 在该位置的 hover 信息。",
        "需要目标符号的类型文档，请走 hover。",
    ],
    "lsp_diagnostics": [
        "检查 internal/lsp/manager.go 当前诊断错误和警告。",
        "读取这个文件的 diagnostics。",
        "看下有没有编译错误、lint 警告。",
        "请返回最新诊断列表。",
        "我需要当前文件问题清单（error/warning）。",
    ],
    "lsp_definition": [
        "跳到 symbol ParseConfig 的定义位置。",
        "找到这个调用点对应函数定义。",
        "定位变量 server 的定义。",
        "根据 line/column 跳转定义。",
        "请执行 go to definition。",
    ],
    "lsp_references": [
        "查找函数 registerMethods 的所有引用。",
        "列出符号 Config 在项目内被使用的位置。",
        "找出变量 logger 的 references。",
        "我要看该方法的全部调用点。",
        "执行 find references。",
    ],
    "lsp_rename": [
        "把变量 oldName 安全重命名为 newName。",
        "跨文件把函数 Foo 重命名成 FooV2。",
        "执行符号 rename，更新所有引用。",
        "我要批量重命名类型 UserDTO 为 UserPayload。",
        "对光标处标识符做安全重命名。",
    ],
    "lsp_completion": [
        "在 internal/lsp/client.go 第88行第12列请求补全建议。",
        "给我当前位置的代码补全候选。",
        "触发 completion 看有哪些方法可用。",
        "我在调用链后面输入点号，请返回补全列表。",
        "请求自动补全条目。",
    ],
    "lsp_did_change": [
        "我刚改了 internal/lsp/manager.go，请通知 LSP 文件已变更。",
        "把新的文件内容同步到 language server。",
        "执行 didChange 更新文档版本。",
        "编辑后请发送文本变更事件。",
        "我要刷新 LSP 内存文档内容。",
    ],
    "lsp_workspace_symbol": [
        "全工程搜索符号名 Router。",
        "在 workspace 范围查找包含 Bootstrap 的符号。",
        "跨文件按关键字检索 symbol。",
        "列出项目里名字像 Config 的符号。",
        "做一次 workspace symbol 查询。",
    ],
    "lsp_implementation": [
        "查找接口 DataProvider 的实现。",
        "定位该抽象方法有哪些实现类。",
        "我要看这个接口函数的 implementation。",
        "找出 trait Handler 的具体实现。",
        "执行 go to implementation。",
    ],
    "lsp_type_definition": [
        "跳到变量 req 的类型定义。",
        "查看这个字段类型在哪里定义。",
        "定位当前符号的 type definition。",
        "这个对象的底层类型声明在哪。",
        "请跳转到类型定义位置。",
    ],
    "lsp_call_hierarchy": [
        "查看函数 HandleTurn 的调用层级（incoming/outgoing）。",
        "我要看谁调用了这个方法，以及它调用了谁。",
        "获取该符号 call hierarchy。",
        "分析当前函数的调用关系树。",
        "请求调用层级数据。",
    ],
    "lsp_type_hierarchy": [
        "查看类型 BaseHandler 的子类型层级。",
        "给我该类型的 supertypes 和 subtypes。",
        "查询 type hierarchy。",
        "我要看这个类的继承关系。",
        "分析当前类型上下层结构。",
    ],
    "lsp_code_action": [
        "在第 60 行附近获取可用 code actions。",
        "这个错误位置有什么 quick fix？",
        "请求 textDocument/codeAction 列表。",
        "给我当前范围的重构建议。",
        "查看该诊断点可执行的修复动作。",
    ],
    "lsp_signature_help": [
        "函数调用处请求 signature help。",
        "我输入到参数列表里了，显示函数签名。",
        "查看当前位置的参数提示。",
        "触发 signatureHelp。",
        "需要当前调用的入参说明。",
    ],
    "lsp_format": [
        "格式化整个 internal/lsp/client.go 文档。",
        "对当前文件执行 document formatting。",
        "把这份 Go 文件按 LSP 规则格式化。",
        "我要统一缩进和空格，请 format。",
        "执行 lsp_format。",
    ],
    "lsp_semantic_tokens": [
        "获取该文件的 semantic tokens。",
        "请求语义高亮 token 数据。",
        "返回 textDocument/semanticTokens/full。",
        "我要语义标记结果用于着色。",
        "执行 semantic tokens 查询。",
    ],
    "lsp_folding_range": [
        "获取该文件可折叠区域。",
        "请求 folding ranges。",
        "返回代码折叠区间列表。",
        "我想知道哪些块可以折叠。",
        "执行 folding range。",
    ],
}

EXTRA_CASES: List[Tuple[str, str]] = [
    ("lsp_workspace_symbol", "在项目范围搜索 ServerConfig 符号。"),
    ("lsp_definition", "把 line 120 col 8 的标识符跳到定义。"),
    ("lsp_implementation", "这个接口方法实现在哪些 struct 里？"),
    ("lsp_type_definition", "变量 err 的真实类型定义位置在哪？"),
    ("lsp_code_action", "诊断报错处给出 quick fix 动作。"),
]


def build_cases() -> List[dict]:
    cases: List[dict] = []
    cid = 1
    for tool in LSP_TOOLS:
        prompts = TOOL_QUERIES.get(tool, [])
        if len(prompts) != 5:
            raise ValueError(f"{tool} prompts must be exactly 5, got {len(prompts)}")
        for prompt in prompts:
            cases.append(
                {
                    "id": cid,
                    "query": prompt,
                    "expected_primary": tool,
                    "expected_tools": [tool],
                }
            )
            cid += 1
    for tool, prompt in EXTRA_CASES:
        cases.append(
            {
                "id": cid,
                "query": prompt,
                "expected_primary": tool,
                "expected_tools": [tool],
            }
        )
        cid += 1
    if len(cases) != 100:
        raise ValueError(f"dataset size must be 100, got {len(cases)}")
    return cases


def load_cases_from_jsonl(path: pathlib.Path) -> List[dict]:
    rows: List[dict] = []
    with path.open("r", encoding="utf-8") as f:
        for line_no, line in enumerate(f, start=1):
            line = line.strip()
            if not line:
                continue
            try:
                row = json.loads(line)
            except json.JSONDecodeError as e:
                raise ValueError(f"invalid json at line {line_no}: {e}") from e
            if not isinstance(row, dict):
                raise ValueError(f"line {line_no}: row must be object")

            query = row.get("query")
            expected_primary = row.get("expected_primary")
            if not isinstance(query, str) or not query.strip():
                raise ValueError(f"line {line_no}: query missing")
            if not isinstance(expected_primary, str) or expected_primary not in LSP_TOOL_SET:
                raise ValueError(f"line {line_no}: invalid expected_primary")

            expected_tools = row.get("expected_tools")
            if not isinstance(expected_tools, list) or not expected_tools:
                expected_tools = [expected_primary]
            expected_tools = [
                t for t in expected_tools if isinstance(t, str) and t in LSP_TOOL_SET
            ]
            if not expected_tools:
                expected_tools = [expected_primary]

            rows.append(
                {
                    "id": row.get("id", len(rows) + 1),
                    "query": query.strip(),
                    "expected_primary": expected_primary,
                    "expected_tools": expected_tools,
                }
            )
    if not rows:
        raise ValueError(f"dataset file is empty: {path}")
    return rows


def normalize_tool_name(raw: str) -> str:
    t = (raw or "").strip()
    if not t:
        return ""
    if t in LSP_TOOL_SET:
        return t
    if t in TOOL_ALIASES:
        return TOOL_ALIASES[t]
    low = t.lower()
    if low in TOOL_ALIASES_LOWER:
        return TOOL_ALIASES_LOWER[low]
    t2 = t.replace("-", "_")
    if t2 in LSP_TOOL_SET:
        return t2
    return ""


def build_router_prompt(query: str) -> str:
    tool_lines = "\n".join(f"- {name}" for name in LSP_TOOLS)
    return (
        "你是一个工具路由器，只负责在候选工具中选出最合适的 1-3 个工具。\n"
        "必须严格遵守：\n"
        "1) 只能从候选工具中选择；\n"
        "2) 仅输出 JSON，不要任何解释；\n"
        "3) JSON 格式固定为 {\"tools\":[\"tool_name1\",\"tool_name2\"]}。\n"
        "候选工具如下：\n"
        f"{tool_lines}\n\n"
        f"用户请求：{query}\n"
    )


def build_rerank_prompt(query: str, candidates: List[str]) -> str:
    candidate_lines = "\n".join(f"- {name}" for name in candidates)
    return (
        "你是二次路由重排器。给定用户请求和候选工具，只能从候选工具中选 1 个最优工具。\n"
        "必须严格遵守：\n"
        "1) 只能选择候选工具之一；\n"
        "2) 仅输出 JSON；\n"
        "3) 格式固定为 {\"tool\":\"tool_name\"}。\n"
        "候选工具：\n"
        f"{candidate_lines}\n\n"
        f"用户请求：{query}\n"
    )


def _extract_text_from_chat_content(content: object) -> str:
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        out: List[str] = []
        for part in content:
            if isinstance(part, dict):
                text = part.get("text")
                if isinstance(text, str):
                    out.append(text)
        return "\n".join(out)
    return ""


def _extract_text_from_openai_payload(payload: dict) -> str:
    output_text = payload.get("output_text")
    if isinstance(output_text, str) and output_text:
        return output_text
    if isinstance(output_text, list):
        output_text_parts = [x for x in output_text if isinstance(x, str)]
        if output_text_parts:
            return "\n".join(output_text_parts)

    choices = payload.get("choices")
    if isinstance(choices, list) and choices:
        first = choices[0]
        if isinstance(first, dict):
            message = first.get("message", {})
            if isinstance(message, dict):
                return _extract_text_from_chat_content(message.get("content"))

    output = payload.get("output")
    if isinstance(output, list):
        out: List[str] = []
        for item in output:
            if not isinstance(item, dict):
                continue
            content = item.get("content")
            if not isinstance(content, list):
                continue
            for part in content:
                if not isinstance(part, dict):
                    continue
                text = part.get("text")
                if isinstance(text, str):
                    out.append(text)
        if out:
            return "\n".join(out)
    return ""


def call_model(
    provider: str,
    model: str,
    prompt: str,
    timeout_s: int,
    api_base: str,
    api_key: str,
    openai_endpoint: str,
    max_output_tokens: int,
    reasoning_effort: str,
    tls_insecure: bool,
    ca_bundle: str,
    user_agent: str,
) -> dict:
    if provider == "ollama":
        return call_ollama(model, prompt, timeout_s)
    if provider == "openai":
        return call_openai_compatible(
            model=model,
            prompt=prompt,
            timeout_s=timeout_s,
            api_base=api_base,
            api_key=api_key,
            endpoint=openai_endpoint,
            max_output_tokens=max_output_tokens,
            reasoning_effort=reasoning_effort,
            tls_insecure=tls_insecure,
            ca_bundle=ca_bundle,
            user_agent=user_agent,
        )
    raise ValueError(f"unsupported provider: {provider}")


def call_ollama(model: str, prompt: str, timeout_s: int) -> dict:
    payload = {
        "model": model,
        "prompt": prompt,
        "stream": False,
        "options": {
            "temperature": 0,
            "top_p": 1,
            "num_predict": 96,
        },
    }
    req = urllib.request.Request(
        "http://127.0.0.1:11434/api/generate",
        data=json.dumps(payload, ensure_ascii=False).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=timeout_s) as resp:
        return json.loads(resp.read().decode("utf-8"))


def call_openai_compatible(
    model: str,
    prompt: str,
    timeout_s: int,
    api_base: str,
    api_key: str,
    endpoint: str,
    max_output_tokens: int,
    reasoning_effort: str,
    tls_insecure: bool,
    ca_bundle: str,
    user_agent: str,
) -> dict:
    if not api_key:
        raise ValueError("missing_api_key")

    base = api_base.rstrip("/")
    if endpoint == "responses":
        url = f"{base}/responses"
        payload = {
            "model": model,
            "input": [
                {
                    "role": "user",
                    "content": [{"type": "input_text", "text": prompt}],
                }
            ],
            "temperature": 0,
            "max_output_tokens": max_output_tokens,
        }
        if reasoning_effort:
            payload["reasoning"] = {"effort": reasoning_effort}
    elif endpoint == "chat":
        url = f"{base}/chat/completions"
        payload = {
            "model": model,
            "messages": [{"role": "user", "content": prompt}],
            "temperature": 0,
            "max_tokens": max_output_tokens,
        }
    else:
        raise ValueError(f"unsupported_openai_endpoint:{endpoint}")

    req = urllib.request.Request(
        url,
        data=json.dumps(payload, ensure_ascii=False).encode("utf-8"),
        headers={
            "Content-Type": "application/json",
            "Authorization": f"Bearer {api_key}",
            "User-Agent": user_agent,
        },
        method="POST",
    )
    ctx = None
    if tls_insecure:
        ctx = ssl._create_unverified_context()
    elif ca_bundle:
        ctx = ssl.create_default_context(cafile=ca_bundle)

    with urllib.request.urlopen(req, timeout=timeout_s, context=ctx) as resp:
        raw = json.loads(resp.read().decode("utf-8"))
    return {"response": _extract_text_from_openai_payload(raw), "raw": raw}


def _dedupe_keep_order(items: List[str]) -> List[str]:
    seen = set()
    out: List[str] = []
    for item in items:
        if item in seen:
            continue
        seen.add(item)
        out.append(item)
    return out


def _extract_json_obj(text: str) -> Tuple[Optional[dict], str]:
    s = (text or "").strip()
    if not s:
        return None, "empty_response"

    fence_match = re.search(r"```(?:json)?\s*(\{.*?\})\s*```", s, flags=re.S)
    if fence_match:
        s = fence_match.group(1).strip()

    try:
        obj = json.loads(s)
        if isinstance(obj, dict):
            return obj, ""
    except json.JSONDecodeError:
        pass

    start = s.find("{")
    end = s.rfind("}")
    if start >= 0 and end > start:
        chunk = s[start : end + 1]
        try:
            obj = json.loads(chunk)
            if isinstance(obj, dict):
                return obj, ""
        except json.JSONDecodeError:
            return None, "invalid_json"
    return None, "invalid_json"


def parse_tools(text: str, max_tools: int = 3) -> Tuple[List[str], str]:
    obj, err = _extract_json_obj(text)
    if obj is None:
        return [], err

    tools_raw = obj.get("tools")
    if tools_raw is None and isinstance(obj.get("tool"), str):
        tools_raw = [obj.get("tool")]

    if not isinstance(tools_raw, list):
        return [], "tools_not_list"

    out: List[str] = []
    for item in tools_raw:
        if not isinstance(item, str):
            continue
        t = normalize_tool_name(item)
        if t:
            out.append(t)
    out = _dedupe_keep_order(out)
    out = out[:max_tools]
    if not out:
        return [], "no_valid_tools"
    return out, ""


def query_hints(query: str) -> List[str]:
    out: List[str] = []
    for pattern, tool in QUERY_HINT_RULES:
        if pattern.search(query):
            out.append(tool)
    return _dedupe_keep_order(out)


def should_trigger_second_pass(
    stage1_tools: List[str],
    parse_err: str,
    rerank_mode: str,
) -> bool:
    if rerank_mode == "off":
        return False
    if rerank_mode == "always":
        return True
    if rerank_mode == "parse_or_ambiguous":
        if parse_err:
            return True
        if not stage1_tools:
            return True
        if len(stage1_tools) > 1:
            return True
        return stage1_tools[0] in AMBIGUOUS_TOOLS
    if rerank_mode == "parse_or_ambiguous_strict":
        if parse_err:
            return True
        if not stage1_tools:
            return True
        return stage1_tools[0] in AMBIGUOUS_TOOLS
    raise ValueError(f"unsupported rerank_mode: {rerank_mode}")


def build_candidate_pool(query: str, stage1_tools: List[str], max_candidates: int) -> List[str]:
    pool: List[str] = []
    pool.extend(stage1_tools)
    pool.extend(query_hints(query))

    if not pool:
        pool.extend(["lsp_definition", "lsp_references", "lsp_workspace_symbol"])

    pool = _dedupe_keep_order([p for p in pool if p in LSP_TOOL_SET])
    if len(pool) < 2:
        pool.extend(["lsp_definition", "lsp_references"])
        pool = _dedupe_keep_order([p for p in pool if p in LSP_TOOL_SET])
    return pool[: max(2, max_candidates)]


def evaluate_model(
    model: str,
    cases: List[dict],
    timeout_s: int,
    provider: str,
    api_base: str,
    api_key: str,
    openai_endpoint: str,
    max_output_tokens: int,
    reasoning_effort: str,
    tls_insecure: bool,
    ca_bundle: str,
    user_agent: str,
    rerank_model: str,
    rerank_mode: str,
    max_candidates: int,
) -> dict:
    details: List[dict] = []
    started = time.time()
    stage2_count = 0

    for idx, case in enumerate(cases, start=1):
        prompt = build_router_prompt(case["query"])

        t1 = time.perf_counter()
        stage1_err = ""
        stage1_raw_text = ""
        stage1_raw_resp = {}
        try:
            stage1_raw_resp = call_model(
                provider=provider,
                model=model,
                prompt=prompt,
                timeout_s=timeout_s,
                api_base=api_base,
                api_key=api_key,
                openai_endpoint=openai_endpoint,
                max_output_tokens=max_output_tokens,
                reasoning_effort=reasoning_effort,
                tls_insecure=tls_insecure,
                ca_bundle=ca_bundle,
                user_agent=user_agent,
            )
            stage1_raw_text = str(stage1_raw_resp.get("response", ""))
        except urllib.error.HTTPError as e:
            try:
                body = e.read().decode("utf-8", errors="replace")[:240]
            except Exception:
                body = ""
            stage1_err = f"http_error:{e.code}:{e.reason}:{body}"
        except urllib.error.URLError as e:
            stage1_err = f"url_error:{e.reason}"
        except Exception as e:  # noqa: BLE001
            stage1_err = f"exception:{e}"
        stage1_latency = time.perf_counter() - t1

        stage1_tools: List[str] = []
        stage1_parse_err = ""
        if not stage1_err:
            stage1_tools, stage1_parse_err = parse_tools(stage1_raw_text, max_tools=3)

        final_tools = list(stage1_tools)
        stage2_triggered = False
        stage2_err = ""
        stage2_parse_err = ""
        stage2_raw_text = ""
        stage2_latency = 0.0
        stage2_tool = ""
        candidate_pool: List[str] = []

        if rerank_model and should_trigger_second_pass(stage1_tools, stage1_parse_err, rerank_mode):
            stage2_triggered = True
            stage2_count += 1
            candidate_pool = build_candidate_pool(case["query"], stage1_tools, max_candidates=max_candidates)
            rerank_prompt = build_rerank_prompt(case["query"], candidate_pool)
            t2 = time.perf_counter()
            try:
                stage2_resp = call_model(
                    provider=provider,
                    model=rerank_model,
                    prompt=rerank_prompt,
                    timeout_s=timeout_s,
                    api_base=api_base,
                    api_key=api_key,
                    openai_endpoint=openai_endpoint,
                    max_output_tokens=max_output_tokens,
                    reasoning_effort=reasoning_effort,
                    tls_insecure=tls_insecure,
                    ca_bundle=ca_bundle,
                    user_agent=user_agent,
                )
                stage2_raw_text = str(stage2_resp.get("response", ""))
            except urllib.error.HTTPError as e:
                try:
                    body = e.read().decode("utf-8", errors="replace")[:240]
                except Exception:
                    body = ""
                stage2_err = f"http_error:{e.code}:{e.reason}:{body}"
            except urllib.error.URLError as e:
                stage2_err = f"url_error:{e.reason}"
            except Exception as e:  # noqa: BLE001
                stage2_err = f"exception:{e}"
            stage2_latency = time.perf_counter() - t2

            if not stage2_err:
                stage2_tools, stage2_parse_err = parse_tools(stage2_raw_text, max_tools=1)
                if stage2_tools:
                    stage2_tool = stage2_tools[0]
                    final_tools = [stage2_tool] + [t for t in stage1_tools if t != stage2_tool]

        expected_primary = case["expected_primary"]
        expected_tools = case["expected_tools"]
        final_top1_ok = bool(final_tools) and final_tools[0] == expected_primary
        final_hit_primary_ok = expected_primary in final_tools
        final_exact_set_ok = set(final_tools) == set(expected_tools)

        stage1_top1_ok = bool(stage1_tools) and stage1_tools[0] == expected_primary
        stage1_hit_primary_ok = expected_primary in stage1_tools

        details.append(
            {
                "id": case["id"],
                "query": case["query"],
                "expected_primary": expected_primary,
                "expected_tools": expected_tools,
                "stage1_pred_tools": stage1_tools,
                "stage1_top1_ok": stage1_top1_ok,
                "stage1_hit_primary_ok": stage1_hit_primary_ok,
                "stage1_parse_error": stage1_parse_err,
                "stage1_call_error": stage1_err,
                "stage1_raw_response_text": stage1_raw_text,
                "stage2_triggered": stage2_triggered,
                "stage2_model": rerank_model if stage2_triggered else "",
                "stage2_candidate_pool": candidate_pool,
                "stage2_selected_tool": stage2_tool,
                "stage2_parse_error": stage2_parse_err,
                "stage2_call_error": stage2_err,
                "stage2_raw_response_text": stage2_raw_text,
                "final_pred_tools": final_tools,
                "top1_ok": final_top1_ok,
                "hit_primary_ok": final_hit_primary_ok,
                "exact_set_ok": final_exact_set_ok,
                "stage1_latency_s": round(stage1_latency, 4),
                "stage2_latency_s": round(stage2_latency, 4),
                "latency_s": round(stage1_latency + stage2_latency, 4),
            }
        )

        if idx % 10 == 0:
            print(f"[{model}] progress {idx}/{len(cases)}")

    latencies = [d["latency_s"] for d in details]
    total = len(details)

    stage1_top1 = sum(1 for d in details if d["stage1_top1_ok"])
    stage1_hit = sum(1 for d in details if d["stage1_hit_primary_ok"])

    final_top1 = sum(1 for d in details if d["top1_ok"])
    final_hit = sum(1 for d in details if d["hit_primary_ok"])
    exact_set = sum(1 for d in details if d["exact_set_ok"])

    parse_fail = sum(1 for d in details if d["final_pred_tools"] == [])
    call_fail = sum(1 for d in details if d["stage1_call_error"])

    per_tool = {}
    for tool in LSP_TOOLS:
        subset = [d for d in details if d["expected_primary"] == tool]
        if not subset:
            continue
        per_tool[tool] = {
            "n": len(subset),
            "top1_acc": round(sum(1 for d in subset if d["top1_ok"]) / len(subset), 4),
            "hit_primary_acc": round(
                sum(1 for d in subset if d["hit_primary_ok"]) / len(subset), 4
            ),
        }

    summary = {
        "model": model,
        "rerank_model": rerank_model,
        "rerank_mode": rerank_mode,
        "total_cases": total,
        "stage1_top1_accuracy": round(stage1_top1 / total, 4),
        "stage1_hit_primary_accuracy": round(stage1_hit / total, 4),
        "top1_accuracy": round(final_top1 / total, 4),
        "hit_primary_accuracy": round(final_hit / total, 4),
        "exact_set_accuracy": round(exact_set / total, 4),
        "parse_fail_rate": round(parse_fail / total, 4),
        "call_fail_rate": round(call_fail / total, 4),
        "stage2_trigger_rate": round(stage2_count / total, 4),
        "latency_avg_s": round(statistics.mean(latencies), 4) if latencies else 0.0,
        "latency_p50_s": round(statistics.median(latencies), 4) if latencies else 0.0,
        "latency_p95_s": round(statistics.quantiles(latencies, n=20)[18], 4)
        if len(latencies) >= 20
        else (round(max(latencies), 4) if latencies else 0.0),
        "elapsed_wall_s": round(time.time() - started, 3),
        "per_tool": per_tool,
    }
    return {"summary": summary, "details": details}


def write_jsonl(path: pathlib.Path, rows: List[dict]) -> None:
    with path.open("w", encoding="utf-8") as f:
        for row in rows:
            f.write(json.dumps(row, ensure_ascii=False) + "\n")


def result_key(model: str, rerank_model: str, rerank_mode: str) -> str:
    m = model.replace("/", "_").replace(":", "_")
    if not rerank_model or rerank_mode == "off":
        return m
    r = rerank_model.replace("/", "_").replace(":", "_")
    return f"{m}__rerank_{r}__{rerank_mode}"


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--models",
        nargs="+",
        default=["qwen2.5:7b", "qwen2.5:14b"],
        help="Stage1 model names",
    )
    parser.add_argument(
        "--provider",
        default="ollama",
        choices=["ollama", "openai"],
        help="LLM provider backend",
    )
    parser.add_argument(
        "--api-base",
        default="http://127.0.0.1:11434/api",
        help="API base URL. Ollama expects .../api, OpenAI-compatible expects .../v1",
    )
    parser.add_argument(
        "--api-key-env",
        default="OPENAI_API_KEY",
        help="Env var name for OpenAI-compatible API key",
    )
    parser.add_argument(
        "--openai-endpoint",
        default="responses",
        choices=["responses", "chat"],
        help="OpenAI-compatible endpoint style",
    )
    parser.add_argument(
        "--max-output-tokens",
        type=int,
        default=96,
        help="Max generation tokens for each request",
    )
    parser.add_argument(
        "--reasoning-effort",
        default="",
        choices=["", "low", "medium", "high"],
        help="OpenAI responses reasoning.effort",
    )
    parser.add_argument(
        "--insecure",
        action="store_true",
        help="Disable TLS certificate verification (testing only)",
    )
    parser.add_argument(
        "--ca-bundle",
        default="",
        help="Custom CA bundle path for TLS verification",
    )
    parser.add_argument(
        "--user-agent",
        default="curl/8.7.1",
        help="HTTP User-Agent for OpenAI-compatible requests",
    )
    parser.add_argument(
        "--out-dir",
        default="",
        help="Output directory. Default: test-results/router-eval/<timestamp>-lsp19-100",
    )
    parser.add_argument(
        "--timeout-s",
        type=int,
        default=60,
        help="Per request timeout in seconds",
    )
    parser.add_argument(
        "--limit",
        type=int,
        default=0,
        help="Only run first N cases for smoke test (0 means all)",
    )
    parser.add_argument(
        "--rerank-model",
        default="",
        help="Stage2 rerank model. Empty means single-pass only.",
    )
    parser.add_argument(
        "--rerank-mode",
        default="off",
        choices=["off", "parse_or_ambiguous", "parse_or_ambiguous_strict", "always"],
        help="Stage2 trigger policy",
    )
    parser.add_argument(
        "--max-candidates",
        type=int,
        default=5,
        help="Max candidates fed to stage2",
    )
    parser.add_argument(
        "--dataset-path",
        default="",
        help="Optional JSONL dataset path. If empty, use built-in 100-case dataset.",
    )
    args = parser.parse_args()
    api_key = os.getenv(args.api_key_env, "").strip()
    if args.provider == "openai" and not api_key:
        print(f"[error] provider=openai requires env var {args.api_key_env}")
        return 2

    ts = dt.datetime.now().strftime("%Y%m%d-%H%M%S")
    out_dir = pathlib.Path(args.out_dir) if args.out_dir else pathlib.Path(
        f"test-results/router-eval/{ts}-lsp19-100"
    )
    out_dir.mkdir(parents=True, exist_ok=True)

    if args.dataset_path:
        cases = load_cases_from_jsonl(pathlib.Path(args.dataset_path))
    else:
        cases = build_cases()
    if args.limit > 0:
        cases = cases[: args.limit]
    write_jsonl(out_dir / "dataset.jsonl", cases)

    model_summaries: Dict[str, dict] = {}
    aggregate: Dict[str, object] = {
        "generated_at": dt.datetime.now().isoformat(),
        "tools": LSP_TOOLS,
        "total_cases": len(cases),
        "provider": args.provider,
        "api_base": args.api_base,
        "openai_endpoint": args.openai_endpoint if args.provider == "openai" else "",
        "reasoning_effort": args.reasoning_effort if args.provider == "openai" else "",
        "tls_insecure": bool(args.insecure) if args.provider == "openai" else False,
        "ca_bundle": args.ca_bundle if args.provider == "openai" else "",
        "user_agent": args.user_agent if args.provider == "openai" else "",
        "rerank_model": args.rerank_model,
        "rerank_mode": args.rerank_mode,
        "models": model_summaries,
    }

    for model in args.models:
        print(f"[start] model={model} cases={len(cases)} rerank={args.rerank_model or 'off'} mode={args.rerank_mode}")
        result = evaluate_model(
            model=model,
            cases=cases,
            timeout_s=args.timeout_s,
            provider=args.provider,
            api_base=args.api_base,
            api_key=api_key,
            openai_endpoint=args.openai_endpoint,
            max_output_tokens=args.max_output_tokens,
            reasoning_effort=args.reasoning_effort,
            tls_insecure=bool(args.insecure),
            ca_bundle=args.ca_bundle,
            user_agent=args.user_agent,
            rerank_model=args.rerank_model,
            rerank_mode=args.rerank_mode,
            max_candidates=args.max_candidates,
        )
        key = result_key(model, args.rerank_model, args.rerank_mode)
        with (out_dir / f"{key}.summary.json").open("w", encoding="utf-8") as f:
            json.dump(result["summary"], f, ensure_ascii=False, indent=2)
        write_jsonl(out_dir / f"{key}.details.jsonl", result["details"])
        model_summaries[key] = result["summary"]
        print(
            f"[done] model={model} "
            f"top1={result['summary']['top1_accuracy']:.4f} "
            f"hit={result['summary']['hit_primary_accuracy']:.4f} "
            f"stage2={result['summary']['stage2_trigger_rate']:.2%} "
            f"p95={result['summary']['latency_p95_s']:.3f}s"
        )

    with (out_dir / "aggregate.json").open("w", encoding="utf-8") as f:
        json.dump(aggregate, f, ensure_ascii=False, indent=2)

    print(f"[output] {out_dir}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
