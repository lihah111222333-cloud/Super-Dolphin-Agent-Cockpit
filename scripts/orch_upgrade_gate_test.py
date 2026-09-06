from __future__ import annotations

import copy
import json
import re
import subprocess
import sys
import tempfile
import unittest
from dataclasses import MISSING, fields, field as dataclass_field, make_dataclass
from pathlib import Path
from typing import Any, get_type_hints
from unittest import mock

import scripts.orch_upgrade_gate as gate


GateError = gate.GateError
ROOT_DIR = Path(__file__).resolve().parents[1]
SCRIPT = ROOT_DIR / "scripts" / "orch_upgrade_gate.py"

SOURCE = "docs/superpowers/specs/test-source.md"
README = "docs/work/plans/orch-subagent-upgrade/README.md"
SPEC_DIR = "docs/work/proposals/orch-subagent-s0"
GRAPH_RELATIVE = gate.GRAPH_RELATIVE
REGISTRY_RELATIVE = gate.REGISTRY_RELATIVE
OUTPUT_RELATIVE = gate.OUTPUT_RELATIVE
MATRIX_RELATIVE = gate.MATRIX_RELATIVE
MAX_FILE_BYTES = gate.MAX_FILE_BYTES

SPEC_FILES = (
    ("TRUST", "agent-output-trust-and-prompt-boundary-spec"),
    ("ID", "agent-identity-and-event-store-spec"),
    ("CONTENT", "content-lifecycle-and-crypto-erasure-spec"),
    ("RESOURCE", "durable-resource-admission-and-budget-spec"),
    ("OUT", "durable-native-command-outbox-spec"),
    ("PROBE", "provider-conformance-probe-spec"),
    ("CTL", "mcp-control-channel-and-native-agent-rpc-spec"),
    ("BRIDGE", "provider-cli-native-bridge-profiles-spec"),
    ("TOOLS", "versioned-tool-surface-rollout-spec"),
    ("ROOM", "agent-room-authz-and-flow-control-spec"),
    ("UI", "agent-ui-projection-spec"),
    ("A2A", "a2a-gateway-spec"),
    ("VERIFY", "machine-verification-and-release-evidence-spec"),
    ("ROLLOUT", "cross-process-rollout-and-rollback-spec"),
)
GATES = (
    "VERIFY_S",
    "ROLLOUT_S",
    "DOMAIN_DONE",
    "VERIFY_DONE",
    "PRECANARY_DONE",
    "CANARY_5_LEASE",
    "CANARY_5_ATTEST",
    "CANARY_25_LEASE",
    "CANARY_25_ATTEST",
    "CANARY_100_LEASE",
    "CANARY_100_ATTEST",
    "FINAL_DECISION",
    "FINAL_COMMIT",
    "ENABLE",
)
BLOCKERS = tuple(f"B{i:02d}" for i in range(1, 19))
CT_CAPABILITIES = tuple(sorted(gate.CT_CAPABILITIES))


def _edge(
    source: str,
    target: str,
    kind: str = "requires",
    providers: list[str] | None = None,
) -> dict[str, Any]:
    return {"from": source, "to": target, "kind": kind, "providers": providers or []}


def _fixture_edges() -> list[dict[str, Any]]:
    return [
        _edge("TRUST", "ID"),
        _edge("ID", "CONTENT"),
        _edge("ID", "RESOURCE"),
        _edge("ID", "OUT"),
        _edge("CONTENT", "OUT"),
        _edge("RESOURCE", "OUT"),
        _edge("CTL", "BRIDGE"),
        _edge("PROBE", "BRIDGE"),
        _edge("ID", "BRIDGE"),
        _edge("CONTENT", "BRIDGE"),
        _edge("RESOURCE", "BRIDGE"),
        _edge("OUT", "BRIDGE"),
        _edge("TRUST", "BRIDGE"),
        _edge("ID", "ROOM"),
        _edge("CONTENT", "ROOM"),
        _edge("RESOURCE", "ROOM"),
        _edge("OUT", "ROOM"),
        _edge("TRUST", "ROOM"),
        _edge("CTL", "TOOLS"),
        _edge("PROBE", "TOOLS"),
        _edge("ID", "TOOLS"),
        _edge("RESOURCE", "TOOLS"),
        _edge("OUT", "TOOLS"),
        _edge("TRUST", "TOOLS"),
        _edge("ROOM", "TOOLS"),
        _edge("BRIDGE", "TOOLS", "provider_enable", list(gate.PROVIDERS)),
        _edge("ID", "UI"),
        _edge("CONTENT", "UI"),
        _edge("RESOURCE", "UI"),
        _edge("OUT", "UI"),
        _edge("ROOM", "UI"),
        _edge("TOOLS", "UI"),
        _edge("ID", "A2A"),
        _edge("CONTENT", "A2A"),
        _edge("RESOURCE", "A2A"),
        _edge("OUT", "A2A"),
        _edge("TRUST", "A2A"),
        _edge("ROOM", "A2A"),
        _edge("VERIFY", "VERIFY_S"),
        _edge("ROLLOUT", "ROLLOUT_S"),
        _edge("VERIFY_S", "TRUST", "root_requirement"),
        _edge("VERIFY_S", "PROBE", "root_requirement"),
        _edge("VERIFY_S", "CTL", "root_requirement"),
        _edge("ROLLOUT_S", "TRUST", "root_compatibility"),
        _edge("ROLLOUT_S", "PROBE", "root_compatibility"),
        _edge("ROLLOUT_S", "CTL", "root_compatibility"),
        _edge("BRIDGE", "DOMAIN_DONE"),
        _edge("UI", "DOMAIN_DONE"),
        _edge("A2A", "DOMAIN_DONE"),
        _edge("DOMAIN_DONE", "VERIFY_DONE"),
        _edge("DOMAIN_DONE", "PRECANARY_DONE"),
        _edge("VERIFY", "VERIFY_DONE"),
        _edge("ROLLOUT", "PRECANARY_DONE"),
        _edge("VERIFY_DONE", "CANARY_5_LEASE"),
        _edge("PRECANARY_DONE", "CANARY_5_LEASE"),
        _edge("CANARY_5_LEASE", "CANARY_5_ATTEST"),
        _edge("CANARY_5_ATTEST", "CANARY_25_LEASE"),
        _edge("CANARY_25_LEASE", "CANARY_25_ATTEST"),
        _edge("CANARY_25_ATTEST", "CANARY_100_LEASE"),
        _edge("CANARY_100_LEASE", "CANARY_100_ATTEST"),
        _edge("CANARY_100_ATTEST", "FINAL_DECISION"),
        _edge("FINAL_DECISION", "FINAL_COMMIT"),
        _edge("FINAL_COMMIT", "ENABLE"),
    ]


def _node_ids() -> list[str]:
    return [identifier for identifier, _ in SPEC_FILES] + list(GATES)


def _mermaid(edges: list[dict[str, Any]]) -> str:
    labels = {identifier: identifier.lower() for identifier in _node_ids()}
    lines = ["```mermaid", "flowchart LR"]
    for edge in edges:
        source, target, kind = edge["from"], edge["to"], edge["kind"]
        arrow = "-->"
        if kind == "root_requirement":
            arrow = '-. "root requirement contract" .->'
        elif kind == "root_compatibility":
            arrow = '-. "root compatibility contract" .->'
        elif kind == "provider_enable":
            arrow = '-. "per-provider enable join gate" .->'
        lines.append(f'    {source}["{labels[source]}"] {arrow} {target}["{labels[target]}"]')
    return "\n".join(lines + ["```"])


def _fixture_cross_task_requirements() -> list[dict[str, Any]]:
    requirements = []
    for index, capability in enumerate(CT_CAPABILITIES, 1):
        surface = "room" if capability == "room_no_escalation" else "unified_v2"
        owners = ["TRUST", "ID"]
        depends_on = ["TRUST", "ID"]
        title = f"cross-task {capability}"
        requirement_id = f"CT_{index:02d}"
        if capability == "message_reply_association":
            owners = ["ID", "CONTENT", "OUT", "TOOLS", "UI"]
            depends_on = ["ID", "CONTENT", "OUT", "TOOLS", "UI"]
            title = "Message/reply association integrity"
            requirement_id = "CT_MESSAGE_REPLY_ASSOCIATION"
        requirements.append(
            {
                "id": requirement_id,
                "capability": capability,
                "title": title,
                "canonical_surface": surface,
                "owner_spec_ids": owners,
                "depends_on": depends_on,
                "ancestor_gates": ["VERIFY_S", "ROLLOUT_S", "DOMAIN_DONE", "VERIFY_DONE", "PRECANARY_DONE"],
                "red_evidence_refs": [SOURCE],
                "green_evidence_refs": [SOURCE],
                "guard_evidence_refs": [SOURCE],
                "status": "pending" if index % 2 else "needs_revalidation",
            }
        )
    return requirements


def _replace_metadata_line(text: str, prefix: str, replacement: str) -> str:
    lines = text.splitlines(keepends=True)
    for index, line in enumerate(lines):
        if line.startswith(prefix):
            ending = "\n" if line.endswith("\n") else ""
            lines[index] = replacement + ending
            return "".join(lines)
    raise AssertionError(f"fixture does not contain metadata line {prefix!r}")


def _projection_sections(rendered: str) -> tuple[dict[str, Any], dict[str, Any]]:
    graph_marker = "### Graph JSON\n\n```json\n"
    registry_marker = "\n```\n\n### Registry JSON\n\n```json\n"
    end_marker = "\n```\n\n> Repeated notice"
    graph_start = rendered.index(graph_marker) + len(graph_marker)
    graph_end = rendered.index(registry_marker, graph_start)
    registry_start = graph_end + len(registry_marker)
    registry_end = rendered.index(end_marker, registry_start)
    return (
        json.loads(rendered[graph_start:graph_end]),
        json.loads(rendered[registry_start:registry_end]),
    )


class PlanningFixture(unittest.TestCase):
    """A temporary workspace that exercises the real source parser.

    Every successful validation parses §2.2, §19 Gate S0, and the Mermaid
    proposal from this temporary source document; the JSON is not treated as
    a precomputed substitute for source parsing.
    """

    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.edges = _fixture_edges()
        self.nodes: list[dict[str, Any]] = []
        incoming: dict[str, list[str]] = {identifier: [] for identifier in _node_ids()}
        for edge in self.edges:
            if edge["kind"] != "provider_enable":
                incoming[edge["to"]].append(edge["from"])

        for identifier, basename in SPEC_FILES:
            self.nodes.append(
                {
                    "id": identifier,
                    "title": f"title-{identifier}",
                    "kind": "spec",
                    "document": f"{SPEC_DIR}/{basename}.md",
                    "status": "draft",
                }
            )
        for identifier in GATES:
            self.nodes.append(
                {
                    "id": identifier,
                    "title": f"gate-{identifier}",
                    "kind": "gate",
                    "document": README,
                    "status": "pending",
                }
            )

        source_path = self.root / SOURCE
        source_path.parent.mkdir(parents=True, exist_ok=True)
        backlog = "\n".join(
            f"{index}. `{basename}`" for index, (_, basename) in enumerate(SPEC_FILES, 1)
        )
        blocker_table = "\n".join(
            f"| {identifier} | P1 | fixture | source-{identifier} | closure-{identifier} |"
            for identifier in BLOCKERS
        )
        source_path.write_text(
            "### 2.2 Current blockers\n\n"
            "| Blocker ID | Priority | Scope / dimensions | 可达风险与证据 | Gate S0 最小修复与锁定测试 |\n"
            "| --- | --- | --- | --- | --- |\n"
            + blocker_table
            + "\n\n## 19. Delivery\n\n### Gate S0\n"
            + backlog
            + "\n\n"
            + _mermaid(self.edges)
            + "\n",
            encoding="utf-8",
        )

        self.spec_documents: dict[str, str] = {}
        for node in self.nodes:
            if node["kind"] != "spec":
                continue
            dependencies = ", ".join(sorted(incoming[node["id"]])) or "none"
            document = (
                f"Spec-ID: {node['id']}\n"
                f"Status: Draft\n"
                f"Depends-On: {dependencies}\n"
                "S0-Owner: fixture owner\n"
                "S0-Scope: fixture planning boundary\n"
                f"S0-Dependencies: {dependencies}\n"
                "S0-Inputs: fixture request and source fence\n"
                "S0-Outputs: fixture receipt and projection\n"
                "S0-Failure: fixture reject and unknown outcome\n"
                "S0-Recovery: fixture replay and reconcile\n"
                "S0-Field-Chain: fixture producer -> owner -> consumer\n"
                "S0-Named-Acceptance: TestFixtureAcceptance\n"
            )
            if node["id"] == "TOOLS":
                document += "Provider-Join: BRIDGE\n"
            document += f"\n# {node['title']}\n\nAcceptance: TestFixtureAcceptance\n"
            path = self.root / node["document"]
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(document, encoding="utf-8")
            self.spec_documents[node["id"]] = document

        readme = self.root / README
        readme.parent.mkdir(parents=True, exist_ok=True)
        gate_lines = []
        for gate_id in GATES:
            dependencies = ", ".join(sorted(incoming[gate_id])) or "none"
            gate_lines.append(f"Gate-Dependencies: {gate_id} <- {dependencies}")
        readme.write_text("# fixture\n\n" + "\n\n".join(gate_lines) + "\n", encoding="utf-8")

        self.graph = {
            "schema_version": 1,
            "authority": "planning_only",
            "source_spec": SOURCE,
            "nodes": self.nodes,
            "edges": self.edges,
            "cross_task_requirements": _fixture_cross_task_requirements(),
        }
        matrix_sections = [
            "# Cross-task requirements acceptance matrix\n\n",
            "## Requirement records\n\n",
        ]
        for requirement in self.graph["cross_task_requirements"]:
            matrix_sections.extend(
                [
                    f"### {requirement['id']} — {requirement['title']}\n\n",
                    f"- **Capability / canonical surface:** `{requirement['capability']}` / `{requirement['canonical_surface']}`\n",
                    f"- **Owners:** " + ", ".join(f"`{value}`" for value in requirement["owner_spec_ids"]) + "\n",
                    f"- **Dependencies:** " + ", ".join(f"`{value}`" for value in requirement["depends_on"]) + "\n",
                    f"- **Ancestor gates:** " + ", ".join(f"`{value}`" for value in requirement["ancestor_gates"]) + "\n",
                    "- **Required fields:** fixture fields\n",
                    f"- **RED fixtures:** `RED-FIXTURE-{requirement['id']}`\n",
                    f"- **GREEN fixtures:** `GREEN-FIXTURE-{requirement['id']}`\n",
                    f"- **Guard/evidence refs:** `{SOURCE}`\n",
                    f"- **Current status / decision:** `{requirement['status']}`; `NO-GO`\n\n",
                ]
            )
        self.matrix_text = "".join(matrix_sections)
        self.write_raw(MATRIX_RELATIVE, self.matrix_text)
        self.registry = {
            "schema_version": 1,
            "authority": "planning_only",
            "source_spec": SOURCE,
            "baseline_commit": "a" * 40,
            "blockers": [
                {
                    "id": identifier,
                    "title": f"blocker-{identifier}",
                    "priority": "P1",
                    "scope": "fixture",
                    "source_requirement": f"source-{identifier}",
                    "closure_requirement": f"closure-{identifier}",
                    "owner_spec_ids": ["TRUST"],
                    "status": "needs_revalidation",
                    "evidence_refs": [SOURCE],
                    "red_evidence_refs": [SOURCE],
                    "green_evidence_refs": [SOURCE],
                    "guard_evidence_refs": [SOURCE],
                }
                for identifier in BLOCKERS
            ],
        }
        self.write_json(GRAPH_RELATIVE, self.graph)
        self.write_json(REGISTRY_RELATIVE, self.registry)

        self.source_text = source_path.read_text(encoding="utf-8")
        self.readme_text = readme.read_text(encoding="utf-8")
        self.addCleanup(self.temp.cleanup)

    def write_json(self, relative: str, value: Any) -> None:
        path = self.root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    def write_raw(self, relative: str, text: str | bytes) -> None:
        path = self.root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        if isinstance(text, bytes):
            path.write_bytes(text)
        else:
            path.write_text(text, encoding="utf-8")

    def mutate_graph(self, transform) -> None:
        value = copy.deepcopy(self.graph)
        transform(value)
        self.write_json(GRAPH_RELATIVE, value)

    def mutate_registry(self, transform) -> None:
        value = copy.deepcopy(self.registry)
        transform(value)
        self.write_json(REGISTRY_RELATIVE, value)

    def restore_baseline(self) -> None:
        self.write_json(GRAPH_RELATIVE, self.graph)
        self.write_json(REGISTRY_RELATIVE, self.registry)
        self.write_raw(SOURCE, self.source_text)
        self.write_raw(README, self.readme_text)
        self.write_raw(MATRIX_RELATIVE, self.matrix_text)
        for node in self.nodes:
            if node["kind"] == "spec":
                self.write_raw(node["document"], self.spec_documents[node["id"]])

    def document_path(self, node_id: str) -> Path:
        node = next(node for node in self.nodes if node["id"] == node_id)
        return self.root / node["document"]


class OrchUpgradeGateTests(PlanningFixture):
    def test_success_and_render_projection_preserve_every_validated_field(self):
        validated = gate.validate_inputs(self.root)
        self.assertEqual(validated["graph"], self.graph)
        self.assertEqual(validated["registry"], self.registry)

        rendered = gate.render_dependency_map(validated)
        graph_projection, registry_projection = _projection_sections(rendered)

        expected_graph = copy.deepcopy(self.graph)
        expected_graph["nodes"] = sorted(expected_graph["nodes"], key=lambda item: item["id"])
        expected_graph["edges"] = sorted(
            expected_graph["edges"],
            key=lambda item: (item["from"], item["to"], item["kind"], item["providers"]),
        )
        expected_registry = copy.deepcopy(self.registry)
        expected_registry["blockers"] = sorted(
            expected_registry["blockers"], key=lambda item: item["id"]
        )
        self.assertEqual(graph_projection, expected_graph)
        self.assertEqual(registry_projection, expected_registry)
        self.assertEqual(rendered, gate.render_dependency_map(validated))
        self.assertIn("provider_enable:codex,claude,agy,grok", rendered)
        self.assertIn("closure-B18", rendered)
        self.assertIn("| RED evidence entries | 18 |", rendered)
        self.assertIn("| GREEN evidence entries | 18 |", rendered)
        self.assertIn("| Guard evidence entries | 18 |", rendered)
        self.assertIn("| Cross-task requirements | 8 |", rendered)
        self.assertIn("| Cross-task RED planned refs | 8 |", rendered)
        self.assertIn("| Cross-task GREEN planned refs | 8 |", rendered)
        self.assertIn("| Cross-task Guard planned refs | 8 |", rendered)
        self.assertIn("`legacy send_message` is explicitly not a canonical surface", rendered)
        self.assertIn("strict_discover_read_send_grant", rendered)
        self.assertIn("planned or observed workspace references only", rendered)
        self.assertIn("| RED entries |", rendered)
        self.assertIn("| GREEN entries |", rendered)
        self.assertIn("| Guard entries |", rendered)
        self.assertIn("document/evidence snapshot SHA-256", rendered)
        self.assertIn("not JCS", rendered)
        self.assertIn("non-authorizing", rendered)
        self.assertIn("historical source-spec snapshot", rendered)

    def test_message_reply_association_requirement_keeps_owned_planning_contract(self):
        requirement = next(
            item
            for item in self.graph["cross_task_requirements"]
            if item["capability"] == "message_reply_association"
        )
        self.assertEqual(requirement["id"], "CT_MESSAGE_REPLY_ASSOCIATION")
        self.assertEqual(requirement["canonical_surface"], "unified_v2")
        self.assertEqual(requirement["owner_spec_ids"], ["ID", "CONTENT", "OUT", "TOOLS", "UI"])
        self.assertEqual(requirement["depends_on"], ["ID", "CONTENT", "OUT", "TOOLS", "UI"])
        self.assertEqual(requirement["ancestor_gates"], ["VERIFY_S", "ROLLOUT_S", "DOMAIN_DONE", "VERIFY_DONE", "PRECANARY_DONE"])
        self.assertIn(requirement["status"], {"pending", "needs_revalidation"})
        for field_name in ("red_evidence_refs", "green_evidence_refs", "guard_evidence_refs"):
            self.assertTrue(requirement[field_name])
            self.assertEqual(len(requirement[field_name]), len(set(requirement[field_name])))

    def test_write_check_is_byte_and_mtime_read_only_and_drift_is_refused(self):
        gate.run(self.root, "write")
        output = self.root / OUTPUT_RELATIVE
        before_bytes = output.read_bytes()
        before_stat = output.stat()

        gate.run(self.root, "check")
        after_check = output.stat()
        self.assertEqual(output.read_bytes(), before_bytes)
        self.assertEqual(after_check.st_mtime_ns, before_stat.st_mtime_ns)
        self.assertEqual(after_check.st_size, before_stat.st_size)

        # A second write is deterministic, while check must not repair drift.
        gate.run(self.root, "write")
        self.assertEqual(output.read_bytes(), before_bytes)
        output.write_bytes(before_bytes + b"\ndrift\n")
        drift_bytes = output.read_bytes()
        drift_stat = output.stat()
        with self.assertRaisesRegex(GateError, "drift"):
            gate.run(self.root, "check")
        self.assertEqual(output.read_bytes(), drift_bytes)
        self.assertEqual(output.stat().st_mtime_ns, drift_stat.st_mtime_ns)

    def test_cli_modes_and_invalid_or_missing_outputs_fail_closed(self):
        command = [sys.executable, str(SCRIPT), "--root", str(self.root)]
        missing = subprocess.run(command + ["--check"], capture_output=True, text=True, check=False)
        self.assertNotEqual(missing.returncode, 0)
        self.assertIn("generated output", missing.stderr)
        self.assertEqual(
            subprocess.run(command, capture_output=True, text=True, check=False).returncode,
            2,
        )
        self.assertEqual(
            subprocess.run(
                command + ["--write", "--check"],
                capture_output=True,
                text=True,
                check=False,
            ).returncode,
            2,
        )
        with self.assertRaises(GateError):
            gate.run(self.root, "repair")

    def test_schema_dataclasses_are_dynamic_and_every_field_rejects_missing_and_null(self):
        # The object locations are deliberately derived from dataclass names;
        # field names themselves come only from dataclasses.fields().
        schema_writers = {
            "Graph": self.mutate_graph,
            "Node": self.mutate_graph,
            "Edge": self.mutate_graph,
            "CrossTaskRequirement": self.mutate_graph,
            "Registry": self.mutate_registry,
            "Blocker": self.mutate_registry,
        }
        for schema in gate.SCHEMA_TYPES:
            writer = schema_writers[schema.__name__]
            for schema_field in fields(schema):
                json_name = schema_field.metadata.get("json_name", schema_field.name)
                with self.subTest(schema=schema.__name__, field=schema_field.name, mutation="missing"):
                    self.restore_baseline()

                    def remove(value, schema_name=schema.__name__, field_name=json_name):
                        target = value
                        if schema_name == "Node":
                            target = value["nodes"][0]
                        elif schema_name == "Edge":
                            target = value["edges"][0]
                        elif schema_name == "CrossTaskRequirement":
                            target = value["cross_task_requirements"][0]
                        elif schema_name == "Blocker":
                            target = value["blockers"][0]
                        target.pop(field_name)

                    writer(remove)
                    with self.assertRaises(GateError):
                        gate.validate_inputs(self.root)

                with self.subTest(schema=schema.__name__, field=schema_field.name, mutation="null"):
                    self.restore_baseline()

                    def null(value, schema_name=schema.__name__, field_name=json_name):
                        target = value
                        if schema_name == "Node":
                            target = value["nodes"][0]
                        elif schema_name == "Edge":
                            target = value["edges"][0]
                        elif schema_name == "CrossTaskRequirement":
                            target = value["cross_task_requirements"][0]
                        elif schema_name == "Blocker":
                            target = value["blockers"][0]
                        target[field_name] = None

                    writer(null)
                    with self.assertRaises(GateError):
                        gate.validate_inputs(self.root)
        self.restore_baseline()

    def test_unknown_fields_are_rejected_at_root_and_nested_schema_levels(self):
        cases = [
            ("graph root", lambda value: value.update(unexpected=1), self.mutate_graph),
            ("node", lambda value: value["nodes"][0].update(unexpected=1), self.mutate_graph),
            ("edge", lambda value: value["edges"][0].update(unexpected=1), self.mutate_graph),
            (
                "cross-task requirement",
                lambda value: value["cross_task_requirements"][0].update(unexpected=1),
                self.mutate_graph,
            ),
            ("registry root", lambda value: value.update(unexpected=1), self.mutate_registry),
            ("blocker", lambda value: value["blockers"][0].update(unexpected=1), self.mutate_registry),
        ]
        for label, transform, writer in cases:
            with self.subTest(label=label):
                self.restore_baseline()
                writer(transform)
                with self.assertRaises(GateError):
                    gate.validate_inputs(self.root)

    def test_wrong_types_and_enum_values_are_rejected(self):
        cases = [
            ("graph schema_version", lambda value: value.update(schema_version="1"), self.mutate_graph),
            ("graph nodes", lambda value: value.update(nodes={}), self.mutate_graph),
            ("node id", lambda value: value["nodes"][0].update(id=7), self.mutate_graph),
            ("edge providers", lambda value: value["edges"][0].update(providers={}), self.mutate_graph),
            ("registry blockers", lambda value: value.update(blockers={}), self.mutate_registry),
            (
                "registry baseline_commit",
                lambda value: value.update(baseline_commit=7),
                self.mutate_registry,
            ),
            (
                "blocker evidence refs entry",
                lambda value: value["blockers"][0].update(evidence_refs=[7]),
                self.mutate_registry,
            ),
            *[
                (
                    f"blocker {field_name} entry",
                    lambda value, name=field_name: value["blockers"][0].update({name: [7]}),
                    self.mutate_registry,
                )
                for field_name in ("red_evidence_refs", "green_evidence_refs", "guard_evidence_refs")
            ],
            *[
                (
                    f"cross-task {field_name} entry",
                    lambda value, name=field_name: value["cross_task_requirements"][0].update({name: [7]}),
                    self.mutate_graph,
                )
                for field_name in (
                    "owner_spec_ids",
                    "depends_on",
                    "ancestor_gates",
                    "red_evidence_refs",
                    "green_evidence_refs",
                    "guard_evidence_refs",
                )
            ],
            ("node kind enum", lambda value: value["nodes"][0].update(kind="unknown"), self.mutate_graph),
            ("node status enum", lambda value: value["nodes"][0].update(status="closed"), self.mutate_graph),
            ("edge kind enum", lambda value: value["edges"][0].update(kind="unknown"), self.mutate_graph),
            (
                "blocker priority enum",
                lambda value: value["blockers"][0].update(priority="P9"),
                self.mutate_registry,
            ),
            (
                "blocker status enum",
                lambda value: value["blockers"][0].update(status="closed"),
                self.mutate_registry,
            ),
        ]
        for label, transform, writer in cases:
            with self.subTest(label=label):
                self.restore_baseline()
                writer(transform)
                with self.assertRaises(GateError):
                    gate.validate_inputs(self.root)

    def test_edge_from_alias_is_rejected_and_json_name_metadata_is_enforced(self):
        edge_field = next(item for item in fields(gate.Edge) if item.name == "from_")
        self.assertEqual(edge_field.metadata.get("json_name"), "from")

        def alias(value):
            edge = value["edges"][0]
            edge["from_"] = edge.pop("from")

        self.mutate_graph(alias)
        with self.assertRaises(GateError):
            gate.validate_inputs(self.root)

    def test_duplicate_json_keys_are_rejected_at_root_and_nested_levels(self):
        raw_graph = json.dumps(self.graph, ensure_ascii=False, indent=2)
        raw_registry = json.dumps(self.registry, ensure_ascii=False, indent=2)
        cases = [
            (GRAPH_RELATIVE, '{"schema_version":1,"schema_version":1}', "root graph"),
            (
                GRAPH_RELATIVE,
                raw_graph.replace('"id": "TRUST"', '"id": "TRUST", "id": "TRUST"', 1),
                "nested node",
            ),
            (
                GRAPH_RELATIVE,
                raw_graph.replace('"from": "TRUST"', '"from": "TRUST", "from": "TRUST"', 1),
                "nested edge",
            ),
            (REGISTRY_RELATIVE, '{"schema_version":1,"schema_version":1}', "root registry"),
            (
                REGISTRY_RELATIVE,
                raw_registry.replace('"id": "B01"', '"id": "B01", "id": "B01"', 1),
                "nested blocker",
            ),
        ]
        for relative, text, label in cases:
            with self.subTest(label=label):
                self.restore_baseline()
                self.write_raw(relative, text)
                with self.assertRaisesRegex(GateError, "duplicate JSON key"):
                    gate.validate_inputs(self.root)

    def test_source_is_authoritative_for_specs_blockers_and_non_isolated_edges(self):
        # Remove a real spec and all its incident edges.  This keeps the
        # remaining graph connected, so the failure must be source coverage,
        # not a false positive from the isolated-node check.
        def remove_spec(value):
            value["nodes"] = [node for node in value["nodes"] if node["id"] != "UI"]
            value["edges"] = [
                edge for edge in value["edges"] if edge["from"] != "UI" and edge["to"] != "UI"
            ]

        self.mutate_graph(remove_spec)
        with self.assertRaises(GateError):
            gate.validate_inputs(self.root)

        self.restore_baseline()
        self.mutate_registry(lambda value: value["blockers"].pop())
        with self.assertRaises(GateError):
            gate.validate_inputs(self.root)

        self.restore_baseline()
        # TRUST has several other incoming and outgoing edges; deleting this
        # one cannot make either endpoint isolated.
        self.mutate_graph(
            lambda value: value["edges"].remove(
                next(edge for edge in value["edges"] if edge["from"] == "TRUST" and edge["to"] == "TOOLS")
            )
        )
        with self.assertRaises(GateError):
            gate.validate_inputs(self.root)

    def test_cross_task_requirements_cover_capabilities_and_reject_legacy_or_unknown_bindings(self):
        self.mutate_graph(
            lambda value: value["cross_task_requirements"].__getitem__(0).update(
                canonical_surface="legacy send_message"
            )
        )
        with self.assertRaisesRegex(GateError, "canonical_surface"):
            gate.validate_inputs(self.root)

        self.restore_baseline()

    def test_cross_task_matrix_must_match_dag_fields_and_status(self):
        matrix = (self.root / MATRIX_RELATIVE).read_text(encoding="utf-8")
        first_id = self.graph["cross_task_requirements"][0]["id"]
        first_section = re.search(
            rf"^###\s+{re.escape(first_id)}\s+—.*?(?=^###\s+|\Z)",
            matrix,
            flags=re.MULTILINE | re.DOTALL,
        )
        self.assertIsNotNone(first_section)

        changed = matrix.replace(
            f"- **Current status / decision:** `{self.graph['cross_task_requirements'][0]['status']}`; `NO-GO`",
            "- **Current status / decision:** `pending`; `NO-GO`"
            if self.graph["cross_task_requirements"][0]["status"] != "pending"
            else "- **Current status / decision:** `needs_revalidation`; `NO-GO`",
            1,
        )
        self.write_raw(MATRIX_RELATIVE, changed)
        with self.assertRaisesRegex(GateError, "status does not match DAG"):
            gate.validate_inputs(self.root)

        self.restore_baseline()

    def test_cross_task_matrix_requires_planning_fields_fixtures_and_bound_evidence(self):
        first_id = self.graph["cross_task_requirements"][0]["id"]
        labels = (
            "Required fields",
            "RED fixtures",
            "GREEN fixtures",
            "Guard/evidence refs",
            "Current status / decision",
        )
        for label in labels:
            with self.subTest(label=label, mutation="missing"):
                self.restore_baseline()
                matrix = (self.root / MATRIX_RELATIVE).read_text(encoding="utf-8")
                changed = re.sub(rf"^- \*\*{re.escape(label)}:\*\*.*\n", "", matrix, count=1, flags=re.MULTILINE)
                self.write_raw(MATRIX_RELATIVE, changed)
                with self.assertRaisesRegex(GateError, rf"requires exactly one {re.escape(label)} line"):
                    gate.validate_inputs(self.root)

            with self.subTest(label=label, mutation="duplicate"):
                self.restore_baseline()
                matrix = (self.root / MATRIX_RELATIVE).read_text(encoding="utf-8")
                line = next(line for line in matrix.splitlines() if line.startswith(f"- **{label}:") )
                self.write_raw(MATRIX_RELATIVE, matrix.replace(line + "\n", line + "\n" + line + "\n", 1))
                with self.assertRaisesRegex(GateError, rf"requires exactly one {re.escape(label)} line"):
                    gate.validate_inputs(self.root)

        self.restore_baseline()
        matrix = (self.root / MATRIX_RELATIVE).read_text(encoding="utf-8")
        changed = re.sub(
            rf"^- \*\*RED fixtures:\*\*.*$",
            "- **RED fixtures:** no named fixture token",
            matrix,
            count=1,
            flags=re.MULTILINE,
        )
        self.write_raw(MATRIX_RELATIVE, changed)
        with self.assertRaisesRegex(GateError, "requires at least one named RED-"):
            gate.validate_inputs(self.root)

        self.restore_baseline()
        matrix = (self.root / MATRIX_RELATIVE).read_text(encoding="utf-8")
        first_red = re.search(r"`(RED-[A-Z0-9_-]+)`", matrix)
        self.assertIsNotNone(first_red)
        second_red = matrix.find("- **RED fixtures:**", matrix.find("- **RED fixtures:**") + 1)
        self.assertGreaterEqual(second_red, 0)
        changed = matrix[:second_red] + matrix[second_red:].replace(
            re.search(r"`(RED-[A-Z0-9_-]+)`", matrix[second_red:]).group(0),
            first_red.group(0),
            1,
        )
        self.write_raw(MATRIX_RELATIVE, changed)
        with self.assertRaisesRegex(GateError, "repeats named RED-/GREEN- fixture token"):
            gate.validate_inputs(self.root)

        self.restore_baseline()
        matrix = (self.root / MATRIX_RELATIVE).read_text(encoding="utf-8")
        changed = matrix.replace(
            f"- **Guard/evidence refs:** `{SOURCE}`",
            "- **Guard/evidence refs:** `missing-evidence.md`",
            1,
        )
        self.write_raw(MATRIX_RELATIVE, changed)
        with self.assertRaisesRegex(GateError, "Guard/evidence refs.*does not exist"):
            gate.validate_inputs(self.root)

        self.restore_baseline()
        matrix = (self.root / MATRIX_RELATIVE).read_text(encoding="utf-8")
        changed = matrix.replace(
            f"- **Guard/evidence refs:** `{SOURCE}`",
            f"- **Guard/evidence refs:** `{MATRIX_RELATIVE}`",
            1,
        )
        self.write_raw(MATRIX_RELATIVE, changed)
        with self.assertRaisesRegex(GateError, "cannot self-reference"):
            gate.validate_inputs(self.root)
        self.restore_baseline()
        changed = matrix.replace(
            "- **Owners:** `TRUST`, `ID`",
            "- **Owners:** `ID`",
            1,
        )
        self.write_raw(MATRIX_RELATIVE, changed)
        with self.assertRaisesRegex(GateError, "owner_spec_ids does not match DAG"):
            gate.validate_inputs(self.root)

        self.restore_baseline()
        changed = re.sub(
            rf"^###\s+{re.escape(first_id)}\s+—\s+.*$",
            f"### {first_id} — changed title",
            matrix,
            count=1,
            flags=re.MULTILINE,
        )
        self.write_raw(MATRIX_RELATIVE, changed)
        with self.assertRaisesRegex(GateError, "title does not match DAG"):
            gate.validate_inputs(self.root)
        self.restore_baseline()
        self.mutate_graph(
            lambda value: value["cross_task_requirements"].__getitem__(0).update(
                canonical_surface="room"
            )
        )
        with self.assertRaisesRegex(GateError, "must be unified_v2"):
            gate.validate_inputs(self.root)

        self.restore_baseline()
        self.mutate_graph(
            lambda value: value["cross_task_requirements"].__getitem__(0).update(
                depends_on=["NOT_A_NODE"]
            )
        )
        with self.assertRaisesRegex(GateError, "depends_on"):
            gate.validate_inputs(self.root)

        self.restore_baseline()
        self.mutate_graph(
            lambda value: value["cross_task_requirements"].__getitem__(0).update(
                ancestor_gates=["TRUST"]
            )
        )
        with self.assertRaisesRegex(GateError, "ancestor_gates"):
            gate.validate_inputs(self.root)

        self.restore_baseline()
        self.mutate_graph(
            lambda value: value["cross_task_requirements"].__getitem__(0).update(
                ancestor_gates=["VERIFY_S", "DOMAIN_DONE"]
            )
        )
        with self.assertRaisesRegex(GateError, "must include VERIFY_S"):
            gate.validate_inputs(self.root)

        self.restore_baseline()
        self.mutate_graph(lambda value: value["cross_task_requirements"].pop())
        with self.assertRaisesRegex(GateError, "cover each capability"):
            gate.validate_inputs(self.root)

        self.restore_baseline()
        self.mutate_graph(
            lambda value: value["cross_task_requirements"].__getitem__(1).update(
                capability=value["cross_task_requirements"][0]["capability"]
            )
        )
        with self.assertRaisesRegex(GateError, "capabilities must be unique"):
            gate.validate_inputs(self.root)
        self.restore_baseline()

    def test_source_sections_and_mermaid_are_actually_parsed(self):
        self.write_raw(SOURCE, self.source_text.replace("flowchart LR", "flowchart TD", 1))
        with self.assertRaises(GateError):
            gate.validate_inputs(self.root)

        self.restore_baseline()
        old_line = "1. `agent-output-trust-and-prompt-boundary-spec`"
        self.assertIn(old_line, self.source_text)
        self.write_raw(
            SOURCE,
            self.source_text.replace(
                old_line,
                "2. `agent-output-trust-and-prompt-boundary-spec`",
                1,
            ),
        )
        with self.assertRaises(GateError):
            gate.validate_inputs(self.root)

        self.restore_baseline()
        blocker_line = "| B18 | P1 | fixture | source-B18 | closure-B18 |"
        self.assertIn(blocker_line, self.source_text)
        self.write_raw(SOURCE, self.source_text.replace(blocker_line + "\n", "", 1))
        with self.assertRaises(GateError):
            gate.validate_inputs(self.root)

    def test_source_blocker_rows_are_ssot_for_registry_fields(self):
        original = self.source_text
        row = "| B01 | P1 | fixture | source-B01 | closure-B01 |"
        self.assertIn(row, original)
        for replacement in (
            "| B01 | P2 | fixture | source-B01 | closure-B01 |",
            "| B01 | P1 | changed-scope | source-B01 | closure-B01 |",
            "| B01 | P1 | fixture | changed-source | closure-B01 |",
            "| B01 | P1 | fixture | source-B01 | changed-closure |",
        ):
            with self.subTest(replacement=replacement):
                self.restore_baseline()
                self.write_raw(SOURCE, original.replace(row, replacement, 1))
                with self.assertRaisesRegex(GateError, "does not match source §2.2 fields"):
                    gate.validate_inputs(self.root)

    def test_source_blocker_table_requires_exact_five_cells_and_unique_ids(self):
        row = "| B01 | P1 | fixture | source-B01 | closure-B01 |"
        cases = (
            "| B01 | P1 | fixture | source-B01 |",
            "| B01 | P1 | fixture | source-B01 | closure-B01 | extra |",
            row + "\n" + row,
        )
        for mutated in cases:
            with self.subTest(mutated=mutated):
                self.restore_baseline()
                text = self.source_text.replace(row, mutated, 1)
                self.write_raw(SOURCE, text)
                with self.assertRaises(GateError):
                    gate.validate_inputs(self.root)

        header = "| Blocker ID | Priority | Scope / dimensions | 可达风险与证据 | Gate S0 最小修复与锁定测试 |"
        for mutated_header in (
            "| Blocker ID | Priority | Scope / dimensions | 可达风险与证据 |",
            header[:-2] + " extra |",
        ):
            with self.subTest(mutated_header=mutated_header):
                self.restore_baseline()
                self.write_raw(SOURCE, self.source_text.replace(header, mutated_header, 1))
                with self.assertRaises(GateError):
                    gate.validate_inputs(self.root)

    def test_graph_rejects_duplicate_nodes_edges_cycles_self_edges_and_unknown_refs(self):
        cases = [
            ("duplicate node", lambda value: value["nodes"].append(copy.deepcopy(value["nodes"][0]))),
            ("duplicate edge", lambda value: value["edges"].append(copy.deepcopy(value["edges"][0]))),
            ("self edge", lambda value: value["edges"].append(_edge("TRUST", "TRUST"))),
            ("unknown reference", lambda value: value["edges"].append(_edge("TRUST", "MISSING_NODE"))),
            ("cycle", lambda value: value["edges"].append(_edge("ID", "TRUST"))),
        ]
        for label, transform in cases:
            with self.subTest(label=label):
                self.restore_baseline()
                self.mutate_graph(transform)
                with self.assertRaises(GateError):
                    gate.validate_inputs(self.root)

    def test_root_edge_kind_and_provider_join_must_match_exactly(self):
        self.mutate_graph(
            lambda value: next(
                edge
                for edge in value["edges"]
                if edge["from"] == "VERIFY_S" and edge["to"] == "TRUST"
            ).update(kind="requires")
        )
        with self.assertRaises(GateError):
            gate.validate_inputs(self.root)

        self.restore_baseline()
        self.mutate_graph(
            lambda value: next(
                edge for edge in value["edges"] if edge["kind"] == "provider_enable"
            ).update(providers=["codex", "codex", "claude", "agy", "grok"])
        )
        with self.assertRaises(GateError):
            gate.validate_inputs(self.root)

        self.restore_baseline()
        tools_doc = self.document_path("TOOLS")
        original = tools_doc.read_text(encoding="utf-8")
        self.assertIn("Provider-Join: BRIDGE", original)
        tools_doc.write_text(original.replace("Provider-Join: BRIDGE\n", "", 1), encoding="utf-8")
        with self.assertRaises(GateError):
            gate.validate_inputs(self.root)

        self.restore_baseline()
        tools_doc = self.document_path("TOOLS")
        original = tools_doc.read_text(encoding="utf-8")
        tools_doc.write_text(
            original.replace("Provider-Join: BRIDGE", "Provider-Join: OTHER", 1),
            encoding="utf-8",
        )
        with self.assertRaises(GateError):
            gate.validate_inputs(self.root)

    def test_document_id_status_and_depends_on_must_match_graph(self):
        target = self.document_path("TRUST")
        original = target.read_text(encoding="utf-8")

        changed = _replace_metadata_line(original, "Spec-ID:", "Spec-ID: WRONG")
        self.assertNotEqual(changed, original)
        target.write_text(changed, encoding="utf-8")
        with self.assertRaises(GateError):
            gate.validate_inputs(self.root)

    def test_spec_completeness_requires_each_explicit_s0_field(self):
        fields_to_check = tuple(gate.S0_COMPLETENESS_FIELDS)
        for field_name in fields_to_check:
            with self.subTest(field=field_name, mutation="missing"):
                self.restore_baseline()
                target = self.document_path("TRUST")
                lines = [line for line in target.read_text(encoding="utf-8").splitlines(True)
                         if not line.startswith(f"S0-{field_name}:")]
                target.write_text("".join(lines), encoding="utf-8")
                with self.assertRaisesRegex(GateError, f"S0-{field_name}"):
                    gate.validate_inputs(self.root)

            with self.subTest(field=field_name, mutation="placeholder"):
                self.restore_baseline()
                target = self.document_path("TRUST")
                original = target.read_text(encoding="utf-8")
                if field_name != "Dependencies":
                    lines = [
                        (f"S0-{field_name}: TBD\n" if line.startswith(f"S0-{field_name}:") else line)
                        for line in original.splitlines(True)
                    ]
                    target.write_text("".join(lines), encoding="utf-8")
                    with self.assertRaisesRegex(GateError, f"S0-{field_name}"):
                        gate.validate_inputs(self.root)
        self.restore_baseline()

    def test_spec_completeness_dependencies_and_named_acceptance_are_checked(self):
        target = self.document_path("TRUST")
        original = target.read_text(encoding="utf-8")
        target.write_text(original.replace("S0-Dependencies: ROLLOUT_S, VERIFY_S", "S0-Dependencies: VERIFY_S", 1), encoding="utf-8")
        with self.assertRaisesRegex(GateError, "S0-Dependencies"):
            gate.validate_inputs(self.root)

        self.restore_baseline()
        target = self.document_path("TRUST")
        original = target.read_text(encoding="utf-8")
        target.write_text(re.sub(r"^S0-Named-Acceptance:.*$", "S0-Named-Acceptance: planned acceptance", original, count=1, flags=re.MULTILINE), encoding="utf-8")
        with self.assertRaisesRegex(GateError, "S0-Named-Acceptance"):
            gate.validate_inputs(self.root)
        self.restore_baseline()
        target = self.document_path("TRUST")
        original = target.read_text(encoding="utf-8")
        target.write_text(re.sub(r"^S0-Named-Acceptance:.*$", "S0-Named-Acceptance: OTHER-P01", original, count=1, flags=re.MULTILINE), encoding="utf-8")
        with self.assertRaisesRegex(GateError, "S0-Named-Acceptance"):
            gate.validate_inputs(self.root)
        self.restore_baseline()
        target = self.document_path("TRUST")
        original = target.read_text(encoding="utf-8")
        target.write_text(re.sub(r"^S0-Named-Acceptance:.*$", "S0-Named-Acceptance: TRUST-P99", original, count=1, flags=re.MULTILINE), encoding="utf-8")
        with self.assertRaisesRegex(GateError, "not traceable"):
            gate.validate_inputs(self.root)
        self.restore_baseline()

        target = self.document_path("TRUST")
        original = target.read_text(encoding="utf-8")
        candidate = re.sub(r"^S0-Named-Acceptance:.*$", "S0-Named-Acceptance: TRUST-P01", original, count=1, flags=re.MULTILINE)
        target.write_text(candidate + "\nAcceptance: TRUST-P01\n", encoding="utf-8")
        self.assertEqual(gate.validate_inputs(self.root)["graph"]["nodes"][0]["id"], "TRUST")
        self.restore_baseline()
        target = self.document_path("TRUST")
        original = target.read_text(encoding="utf-8")
        target.write_text(re.sub(r"^S0-Named-Acceptance:.*$", "S0-Named-Acceptance: TestFixtureAcceptance; TRUST-P01", original, count=1, flags=re.MULTILINE), encoding="utf-8")
        with self.assertRaisesRegex(GateError, "TRUST-P01.*not traceable"):
            gate.validate_inputs(self.root)
        self.restore_baseline()
        target = self.document_path("TRUST")
        original = target.read_text(encoding="utf-8")
        target.write_text(re.sub(r"^S0-Named-Acceptance:.*$", "S0-Named-Acceptance: TRUST-X01", original, count=1, flags=re.MULTILINE), encoding="utf-8")
        with self.assertRaisesRegex(GateError, "S0-Named-Acceptance"):
            gate.validate_inputs(self.root)
        self.restore_baseline()

    def test_spec_completeness_rejects_duplicate_field(self):
        target = self.document_path("TRUST")
        original = target.read_text(encoding="utf-8")
        target.write_text(original + "S0-Owner: duplicate owner\n", encoding="utf-8")
        with self.assertRaisesRegex(GateError, "S0-Owner"):
            gate.validate_inputs(self.root)
        self.restore_baseline()

        target = self.document_path("TRUST")
        original = target.read_text(encoding="utf-8")
        target.write_text(original + "S0-Unknown: future\n", encoding="utf-8")
        with self.assertRaisesRegex(GateError, "unknown S0-Unknown"):
            gate.validate_inputs(self.root)
        self.restore_baseline()

        target = self.document_path("TRUST")
        original = target.read_text(encoding="utf-8")
        target.write_text(original + "```text\nS0-Owner: fenced\n```\n", encoding="utf-8")
        with self.assertRaisesRegex(GateError, "inside a fenced block"):
            gate.validate_inputs(self.root)
        self.restore_baseline()

        target = self.document_path("TRUST")
        original = target.read_text(encoding="utf-8")
        owner_line = next(line for line in original.splitlines(True) if line.startswith("S0-Owner:"))
        moved = original.replace(owner_line, "", 1)
        moved = moved.replace("Depends-On: ROLLOUT_S, VERIFY_S\n", "Depends-On: ROLLOUT_S, VERIFY_S\n" + owner_line, 1)
        # Put the owner before Depends-On to prove ordering is enforced.
        moved = moved.replace(owner_line, "", 1)
        moved = owner_line + moved
        target.write_text(moved, encoding="utf-8")
        with self.assertRaisesRegex(GateError, "must appear after Depends-On"):
            gate.validate_inputs(self.root)
        self.restore_baseline()

        self.restore_baseline()
        target = self.document_path("TRUST")
        original = target.read_text(encoding="utf-8")
        changed = _replace_metadata_line(original, "Status:", "Status: Pending")
        self.assertNotEqual(changed, original)
        target.write_text(changed, encoding="utf-8")
        with self.assertRaises(GateError):
            gate.validate_inputs(self.root)

        self.restore_baseline()
        target = self.document_path("TRUST")
        original = target.read_text(encoding="utf-8")
        old_depends = next(line for line in original.splitlines() if line.startswith("Depends-On:"))
        self.assertEqual(old_depends, "Depends-On: ROLLOUT_S, VERIFY_S")
        changed = _replace_metadata_line(original, "Depends-On:", "Depends-On: VERIFY_S")
        self.assertNotEqual(changed, original)
        target.write_text(changed, encoding="utf-8")
        with self.assertRaises(GateError):
            gate.validate_inputs(self.root)

    def test_empty_owner_and_evidence_lists_fail_fast(self):
        self.mutate_registry(lambda value: value["blockers"][0].update(owner_spec_ids=[]))
        with self.assertRaises(GateError):
            gate.validate_inputs(self.root)

        self.restore_baseline()
        self.mutate_registry(lambda value: value["blockers"][0].update(evidence_refs=[]))
        with self.assertRaises(GateError):
            gate.validate_inputs(self.root)

        for field_name in ("red_evidence_refs", "green_evidence_refs", "guard_evidence_refs"):
            with self.subTest(field=field_name):
                self.restore_baseline()
                self.mutate_registry(lambda value, name=field_name: value["blockers"][0].update({name: []}))
                with self.assertRaises(GateError):
                    gate.validate_inputs(self.root)

    def test_evidence_reference_lists_are_unique_and_workspace_bound(self):
        for field_name in ("red_evidence_refs", "green_evidence_refs", "guard_evidence_refs"):
            with self.subTest(field=field_name, mutation="duplicate"):
                self.restore_baseline()
                self.mutate_registry(
                    lambda value, name=field_name: value["blockers"][0].update({name: [SOURCE, SOURCE]})
                )
                with self.assertRaisesRegex(GateError, "duplicate canonical evidence path"):
                    gate.validate_inputs(self.root)

            with self.subTest(field=field_name, mutation="canonical alias"):
                self.restore_baseline()
                alias = self.root / f"alias-source-{field_name}.md"
                alias.symlink_to(self.root / SOURCE)
                self.mutate_registry(
                    lambda value, name=field_name: value["blockers"][0].update(
                        {name: [SOURCE, alias.name]}
                    )
                )
                with self.assertRaisesRegex(GateError, "duplicate canonical evidence path"):
                    gate.validate_inputs(self.root)

            with self.subTest(field=field_name, mutation="escape"):
                self.restore_baseline()
                self.mutate_registry(
                    lambda value, name=field_name: value["blockers"][0].update(
                        {name: ["../outside-evidence.md"]}
                    )
                )
                with self.assertRaises(GateError):
                    gate.validate_inputs(self.root)

    def test_evidence_references_cannot_self_reference_registry_or_generated_map(self):
        self.mutate_registry(
            lambda value: value["blockers"][0].update(
                red_evidence_refs=[REGISTRY_RELATIVE]
            )
        )
        with self.assertRaisesRegex(GateError, "cannot self-reference"):
            gate.validate_inputs(self.root)

        self.restore_baseline()
        gate.run(self.root, "write")
        self.mutate_registry(
            lambda value: value["blockers"][0].update(
                green_evidence_refs=[OUTPUT_RELATIVE]
            )
        )
        with self.assertRaisesRegex(GateError, "cannot self-reference"):
            gate.validate_inputs(self.root)

    def test_document_snapshot_detects_content_drift_and_invalid_evidence_bytes(self):
        gate.run(self.root, "write")
        source_path = self.root / SOURCE
        source_path.write_text(self.source_text + "\nchanged\n", encoding="utf-8")
        with self.assertRaisesRegex(GateError, "generated output drift"):
            gate.run(self.root, "check")

        self.restore_baseline()
        oversized = self.root / "oversized-evidence.md"
        oversized.write_text("x" * (MAX_FILE_BYTES + 1), encoding="utf-8")
        self.mutate_registry(
            lambda value: value["blockers"][0].update(
                guard_evidence_refs=["oversized-evidence.md"]
            )
        )
        with self.assertRaisesRegex(GateError, "exceeds"):
            gate.validate_inputs(self.root)

        self.restore_baseline()
        invalid = self.root / "invalid-evidence.md"
        invalid.write_bytes(b"\xff\xfe")
        self.mutate_registry(
            lambda value: value["blockers"][0].update(
                guard_evidence_refs=["invalid-evidence.md"]
            )
        )
        with self.assertRaisesRegex(GateError, "not valid UTF-8"):
            gate.validate_inputs(self.root)

    def test_workspace_relative_paths_and_symlinks_cannot_escape_root(self):
        original_document = self.nodes[0]["document"]
        basename = Path(original_document).name
        outside_dir = self.root.parent / f"orch-upgrade-outside-{self.root.name}"
        outside_dir.mkdir()
        outside_document = outside_dir / basename
        outside_document.write_text("outside", encoding="utf-8")

        # Keep the valid basename so this test reaches _inside_file rather
        # than being rejected by the source-backlog basename check first.
        self.mutate_graph(
            lambda value: value["nodes"][0].update(
                document=f"../{outside_dir.name}/{basename}"
            )
        )
        with self.assertRaises(GateError):
            gate.validate_inputs(self.root)

        self.restore_baseline()
        target = self.document_path("TRUST")
        target.unlink()
        target.symlink_to(outside_document)
        with self.assertRaises(GateError):
            gate.validate_inputs(self.root)

        # Do not let baseline restoration follow the deliberate escape link.
        target.unlink()
        self.restore_baseline()
        source_basename = Path(SOURCE).name
        outside_evidence = outside_dir / source_basename
        outside_evidence.write_text("outside evidence", encoding="utf-8")
        self.mutate_registry(
            lambda value: value["blockers"][0].update(
                evidence_refs=[f"../{outside_dir.name}/{source_basename}"]
            )
        )
        with self.assertRaises(GateError):
            gate.validate_inputs(self.root)

        self.restore_baseline()
        gate.run(self.root, "write")
        output = self.root / OUTPUT_RELATIVE
        outside_output = outside_dir / output.name
        outside_output.write_text(output.read_text(encoding="utf-8"), encoding="utf-8")
        output.unlink()
        output.symlink_to(outside_output)
        with self.assertRaises(GateError):
            gate.run(self.root, "check")

    def test_all_read_inputs_and_generated_output_have_one_mib_limit(self):
        oversized = "x" * (MAX_FILE_BYTES + 1)
        cases = [
            (
                "graph JSON",
                lambda: self.mutate_graph(lambda value: value["nodes"][0].update(title=oversized)),
            ),
            (
                "registry JSON",
                lambda: self.mutate_registry(
                    lambda value: value["blockers"][0].update(title=oversized)
                ),
            ),
            ("source specification", lambda: self.write_raw(SOURCE, oversized)),
            ("spec document", lambda: self.write_raw(self.nodes[0]["document"], oversized)),
        ]
        for label, mutate in cases:
            with self.subTest(label=label):
                self.restore_baseline()
                mutate()
                with self.assertRaises(GateError):
                    gate.validate_inputs(self.root)

        self.restore_baseline()
        gate.run(self.root, "write")
        output = self.root / OUTPUT_RELATIVE
        original = output.read_bytes()
        output.write_bytes(original + b"x" * (MAX_FILE_BYTES + 1))
        with self.assertRaises(GateError):
            gate.run(self.root, "check")

    def test_future_dataclass_field_is_discovered_and_rendered_without_hardcoded_field_list(self):
        graph_type_hints = get_type_hints(gate.Graph)
        future_fields = []
        for schema_field in fields(gate.Graph):
            future_fields.append(
                (
                    schema_field.name,
                    graph_type_hints[schema_field.name],
                    dataclass_field(
                        default=MISSING,
                        metadata=dict(schema_field.metadata),
                    ),
                )
            )
        future_fields.append(("future_marker", str))
        FutureGraph = make_dataclass("FutureGraph", future_fields, frozen=True)

        graph_with_future = copy.deepcopy(self.graph)
        graph_with_future["future_marker"] = "future-schema-v2"
        self.write_json(GRAPH_RELATIVE, graph_with_future)

        # Graph is patched at the schema boundary, not by changing a parser
        # allow-list.  A dynamic parser must accept and carry this field into
        # the full validated projection.
        with mock.patch.object(gate, "Graph", FutureGraph):
            validated = gate.validate_inputs(self.root)
            self.assertEqual(validated["graph"]["future_marker"], "future-schema-v2")
            rendered = gate.render_dependency_map(validated)
            graph_projection, _ = _projection_sections(rendered)
            self.assertEqual(graph_projection["future_marker"], "future-schema-v2")


if __name__ == "__main__":
    unittest.main()
