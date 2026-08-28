import importlib.util
import json
import pathlib
import sys
import tempfile
import unittest

from scripts.grader_feedback import parse_test_output
from scripts.cross_file_lsp_receipts import (
    ISOLATION_SCHEMA,
    SCHEMA as RECEIPT_SCHEMA,
    canonical_isolation_attestation_payload,
    digest_json,
    digest_text,
)
from scripts.cross_file_signed_receipts import (
    Ed25519Signer,
    Ed25519Verifier,
    attach_signature,
)


SCRIPT_PATH = pathlib.Path(__file__).with_name("cross_file_closed_loop_guard.py")
SIGNER = Ed25519Signer.generate("guard-key-01")
VERIFIER = Ed25519Verifier(SIGNER.public_key)
PATCH_HASH = "a" * 64
GRADER_ARTIFACT = {"id": "grader-v1", "sha256": "b" * 64}


def server_receipts(
    task,
    arm,
    session,
    thread,
    files,
    *,
    diagnostics_files=None,
    generation="generation-1",
    xref_position=None,
):
    """Build a small server-owned structure/xref/diagnostics receipt stream."""

    files = tuple(files)
    structure_path = files[0]
    structure_position = f"{structure_path}:10:2"
    xref_position = structure_position if xref_position is None else xref_position
    diagnostics_files = tuple(files if diagnostics_files is None else diagnostics_files)
    diagnostics_filter = {
        "mode": "changed_lines",
        "changed_lines": {path: [10] for path in diagnostics_files},
    }
    entries = (
        (
            "structure",
            {"action": "document_symbol", "file_path": structure_path},
            {"locations": [structure_position]},
        ),
        (
            "xref",
            {"action": "references", "pos": xref_position},
            {"locations": []},
        ),
        (
            "diagnostics",
            {"file_paths": list(diagnostics_files), "diff_filter": diagnostics_filter},
            {"rows": [], "status": "clean"},
        ),
    )
    receipts = []
    for seq, (tool, arguments, result) in enumerate(entries, 1):
        receipts.append(
            {
                "schema": RECEIPT_SCHEMA,
                "source": "mcp-server",
                "task": task,
                "arm": arm,
                "session": session,
                "thread": thread,
                "seq": seq,
                "request_id": f"{task}-request-{seq}",
                "args_hash": digest_json(arguments),
                "result_hash": digest_json(result),
                "status": "success",
                "server_generation": generation,
                "patch_hash": PATCH_HASH,
                "tool": tool,
                "arguments": arguments,
                "result": result,
            }
        )
    return [
        attach_signature(item, signer=SIGNER, kind="mcp_server_receipt", issuer="mcp-server")
        for item in receipts
    ]


def isolation_evidence(task, arm, session, thread):
    """Build executor-bound No-LSP evidence for canonical guard tests."""

    raw_prompt = "Fix the requested behavior in the repository."
    ingress = []
    evidence = {
        "schema": ISOLATION_SCHEMA,
        "mode": "no_lsp",
        "task": task,
        "arm": arm,
        "session": session,
        "thread": thread,
        "issuer": "executor",
        "patch_hash": PATCH_HASH,
        "network": {
            "disabled": True,
            "status": "enforced",
            "policy_hash": digest_text("network-policy"),
        },
        "sidecar": {
            "disabled": True,
            "status": "enforced",
            "policy_hash": digest_text("sidecar-policy"),
        },
        "ingress": {"count": 0, "receipts": ingress, "log_hash": digest_json(ingress)},
        "raw_prompt_hash": digest_text(raw_prompt),
        "raw_prompt": raw_prompt,
    }
    evidence["attestation_hash"] = digest_json(
        canonical_isolation_attestation_payload(evidence)
    )
    return attach_signature(
        evidence,
        signer=SIGNER,
        kind="no_lsp_isolation",
        issuer="executor",
    )


def load_guard_module():
    spec = importlib.util.spec_from_file_location("cross_file_closed_loop_guard", SCRIPT_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load closed-loop guard")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class CrossFileClosedLoopGuardTest(unittest.TestCase):
    def setUp(self):
        self.g = load_guard_module()

    def diagnostics(self, *paths):
        return [
            {
                "type": "mcp_tool_call",
                "tool": "diagnostics",
                "arguments": {"file_paths": list(paths)},
            }
        ]

    def semantic_completion(self, *paths):
        if not paths:
            return []
        first = paths[0]
        return [
            {
                "type": "mcp_tool_call",
                "tool": "structure",
                "arguments": {"action": "document_symbol", "file_path": first},
            },
            {
                "type": "mcp_tool_call",
                "tool": "xref",
                "arguments": {"action": "references", "pos": f"{first}:1:1"},
            },
            *self.diagnostics(*paths),
        ]

    def request(self, round_number=1, *, patch_hash="h1", files=("src/a.py",), **kwargs):
        values = {
            "round_number": round_number,
            "arm_id": "arm-1",
            "session_id": "session-1",
            "thread_id": "thread-1",
            "patch_hash": patch_hash,
            "changed_files": files,
            "trajectory": self.semantic_completion(*files),
            "mode": "with_lsp",
            "action": "start" if round_number == 1 else "resume",
            "receipt_verifier": VERIFIER,
        }
        values.update(kwargs)
        return self.g.RoundInput(**values)

    def test_round_one_persists_identity_and_round_two_must_resume_it(self):
        with tempfile.TemporaryDirectory() as directory:
            state_path = pathlib.Path(directory) / "closed-loop.json"
            guard = self.g.ClosedLoopGuard.from_store(state_path)

            first = guard.evaluate_round(self.request())
            self.assertTrue(first.grader_allowed)
            self.assertEqual(guard.state.session_id, "session-1")
            self.assertEqual(guard.state.thread_id, "thread-1")

            persisted = json.loads(state_path.read_text(encoding="utf-8"))
            self.assertEqual(persisted["session_id"], "session-1")
            self.assertEqual(persisted["thread_id"], "thread-1")

            second = guard.evaluate_round(
                self.request(2, patch_hash="h2", files=("src/b.py",))
            )
            self.assertTrue(second.grader_allowed)

            with self.assertRaises(self.g.SessionIdentityError):
                guard.evaluate_round(
                    self.request(
                        3,
                        patch_hash="h3",
                        files=("src/c.py",),
                        session_id="new-session",
                    )
                )

    def test_round_two_start_action_and_arm_drift_fail_fast(self):
        guard = self.g.ClosedLoopGuard()
        guard.evaluate_round(self.request())

        with self.assertRaises(self.g.SessionIdentityError):
            guard.evaluate_round(self.request(2, patch_hash="h2", action="start"))

        with self.assertRaises(self.g.SessionIdentityError):
            guard.evaluate_round(
                self.request(2, patch_hash="h2", arm_id="other-arm")
            )

    def test_same_patch_is_no_progress_and_never_calls_grader(self):
        guard = self.g.ClosedLoopGuard()
        first = guard.evaluate_round(self.request())
        self.assertTrue(first.grader_allowed)
        rejected = guard.run_grader(first, lambda _: False)
        self.assertFalse(rejected.quality_accepted)

        second = guard.evaluate_round(self.request(2, patch_hash="h1"))
        self.assertEqual(second.decision, self.g.RoundDecision.NO_PROGRESS.value)
        self.assertFalse(second.grader_allowed)
        self.assertFalse(second.relocalization_required)
        calls = []
        with self.assertRaises(self.g.GraderBlocked):
            guard.run_grader(second, lambda _: calls.append("called"))
        self.assertEqual(calls, [])

        third = guard.evaluate_round(self.request(3, patch_hash="h1"))
        self.assertTrue(third.terminal)
        self.assertTrue(third.relocalization_required)
        self.assertEqual(guard.state.status, self.g.RunnerStatus.RELOCALIZE_REQUIRED.value)

    def test_no_production_file_change_is_no_progress(self):
        guard = self.g.ClosedLoopGuard()
        outcome = guard.evaluate_round(
            self.request(files=(), trajectory=(), patch_hash="h1")
        )
        self.assertEqual(outcome.decision, self.g.RoundDecision.NO_PROGRESS.value)
        self.assertFalse(outcome.grader_allowed)

    def test_with_lsp_missing_completion_evidence_blocks_grader(self):
        guard = self.g.ClosedLoopGuard()
        request = self.request(
            files=("src/a.py", "src/b.py"),
            trajectory=self.diagnostics("src/a.py"),
        )
        outcome = guard.evaluate_round(request)
        self.assertEqual(outcome.decision, self.g.RoundDecision.EVIDENCE_RECOVERY_REQUIRED.value)
        self.assertFalse(outcome.grader_allowed)
        self.assertEqual(outcome.contract_failures, ())
        self.assertEqual(guard.state.last_round, 0)
        self.assertEqual(guard.state.no_progress_streak, 0)
        self.assertEqual(
            outcome.lsp_self_check,
            {
                "changed_production_files": ["src/a.py", "src/b.py"],
                "structure_evidence": [],
                "xref_evidence": [],
                "diagnostics_coverage": ["src/a.py"],
                "exemptions": [],
            },
        )

        zero_call = self.g.ClosedLoopGuard().evaluate_round(
            self.request(
                files=("src/a.py", "src/b.py"),
                trajectory=(),
            )
        )
        self.assertFalse(zero_call.grader_allowed)
        self.assertEqual(
            zero_call.decision,
            self.g.RoundDecision.EVIDENCE_RECOVERY_REQUIRED.value,
        )
        self.assertEqual(zero_call.diagnostics_covered_files, ())
        self.assertEqual(zero_call.missing_files, ("src/a.py", "src/b.py"))
        self.assertEqual(
            zero_call.lsp_self_check,
            {
                "changed_production_files": ["src/a.py", "src/b.py"],
                "structure_evidence": [],
                "xref_evidence": [],
                "diagnostics_coverage": [],
                "exemptions": [],
            },
        )

    def test_absolute_diagnostics_paths_align_to_required_repo_paths(self):
        trajectory = self.semantic_completion("src/a.py", "src/b.py")
        trajectory[-1]["arguments"] = {
            "file_paths": ["/workspace/src/a.py", "/workspace/src/b.py"]
        }
        outcome = self.g.ClosedLoopGuard().evaluate_round(
            self.request(files=("src/a.py", "src/b.py"), trajectory=trajectory)
        )
        self.assertTrue(outcome.grader_allowed)
        self.assertEqual(outcome.diagnostics_covered_files, ("src/a.py", "src/b.py"))

    def test_canonical_receipts_are_authoritative_over_agent_trajectory(self):
        files = ("src/a.py", "src/b.py")
        receipts = server_receipts(
            "task-canonical", "arm-1", "session-1", "thread-1", files,
            diagnostics_files=(files[0],),
        )
        guard = self.g.ClosedLoopGuard()
        pending = guard.evaluate_round(
            self.request(
                files=files,
                patch_hash=PATCH_HASH,
                task_id="task-canonical",
                server_generation="generation-1",
                server_receipts=receipts,
                # This deliberately claims complete coverage; it is not an
                # authority once server receipts are present.
                trajectory=self.diagnostics(*files),
            )
        )
        self.assertFalse(pending.grader_allowed)
        self.assertEqual(pending.decision, self.g.RoundDecision.EVIDENCE_RECOVERY_REQUIRED.value)
        self.assertEqual(pending.diagnostics_covered_files, (files[0],))
        self.assertEqual(pending.missing_files, (files[1],))
        self.assertEqual(guard.state.pending_evidence_source, "server_receipts")
        # Agent trajectory claims remain non-authoritative when canonical
        # receipts are supplied, but they do not create a coverage gate.
        self.assertEqual(
            pending.lsp_self_check["diagnostics_coverage"],
            [files[0]],
        )

    def test_canonical_recovery_allows_signed_sidecar_generation_rotation(self):
        files = ("src/a.py", "src/b.py")
        task = "task-generation-rotation"
        guard = self.g.ClosedLoopGuard()
        pending = guard.evaluate_round(
            self.request(
                files=files,
                patch_hash=PATCH_HASH,
                task_id=task,
                server_generation="generation-1",
                server_receipts=server_receipts(
                    task,
                    "arm-1",
                    "session-1",
                    "thread-1",
                    files,
                    diagnostics_files=(files[0],),
                    generation="generation-1",
                ),
            )
        )
        self.assertEqual(
            pending.decision,
            self.g.RoundDecision.EVIDENCE_RECOVERY_REQUIRED.value,
        )
        recovered = guard.evaluate_round(
            self.request(
                files=files,
                patch_hash=PATCH_HASH,
                task_id=task,
                server_generation="generation-2",
                server_receipts=server_receipts(
                    task,
                    "arm-1",
                    "session-1",
                    "thread-1",
                    files,
                    generation="generation-2",
                ),
                action="evidence_recovery",
                evidence_attempt=1,
            )
        )
        self.assertTrue(recovered.grader_allowed)
        self.assertTrue(recovered.evidence_recovery_succeeded)

    def test_canonical_empty_recovery_accepts_executor_attested_projection(self):
        files = ("src/a.py", "src/b.py")
        task = "task-executor-projection"
        guard = self.g.ClosedLoopGuard()
        pending = guard.evaluate_round(
            self.request(
                files=files,
                patch_hash=PATCH_HASH,
                task_id=task,
                server_generation="generation-1",
                server_receipts=[],
                trajectory=(),
            )
        )
        self.assertEqual(
            pending.decision,
            self.g.RoundDecision.EVIDENCE_RECOVERY_REQUIRED.value,
        )
        recovered = guard.evaluate_round(
            self.request(
                files=files,
                patch_hash=PATCH_HASH,
                task_id=task,
                server_generation="generation-2",
                server_receipts=None,
                trajectory=(),
                mcp_trajectory=self.semantic_completion(*files),
                action="evidence_recovery",
                evidence_attempt=1,
            )
        )
        self.assertTrue(recovered.grader_allowed)
        self.assertTrue(recovered.evidence_recovery_succeeded)

    def test_canonical_with_lsp_empty_receipts_block_changed_production(self):
        guard = self.g.ClosedLoopGuard()
        outcome = guard.evaluate_round(
            self.request(
                files=("src/a.py",),
                trajectory=(),
                task_id="task-empty",
                server_receipts=[],
            )
        )
        self.assertFalse(outcome.grader_allowed)
        self.assertEqual(outcome.decision, self.g.RoundDecision.EVIDENCE_RECOVERY_REQUIRED.value)
        self.assertEqual(outcome.diagnostics_covered_files, ())
        self.assertEqual(outcome.missing_files, ("src/a.py",))
        self.assertEqual(outcome.contract_failures, ())

    def test_canonical_no_lsp_requires_executor_isolation(self):
        guard = self.g.ClosedLoopGuard()
        outcome = guard.evaluate_round(
            self.request(
                mode="no_lsp",
                patch_hash=PATCH_HASH,
                task_id="task-no-lsp",
                isolation_evidence=isolation_evidence(
                    "task-no-lsp", "arm-1", "session-1", "thread-1"
                ),
                trajectory=[{"source": "shell", "command": "rg x"}],
            )
        )
        self.assertTrue(outcome.grader_allowed)
        self.assertEqual(outcome.diagnostics_covered_files, ())

    def test_current_contract_failure_fixtures_do_not_authorize_grader(self):
        """Lock the four observed v074 With-LSP failure shapes."""

        fixtures = (
            # astropy-13398 had a xref position that was not returned by the
            # preceding structure result.
            {
                "task": "astropy-13398",
                "files": ("astropy/coordinates/baseframe.py", "astropy/coordinates/itrs.py"),
                "kind": "position",
            },
            # The other three completed without a canonical diagnostics
            # server receipt.  Coverage is now an audit-only observation.
            {"task": "pylint-4551", "files": ("pylint/checkers/base.py", "pylint/utils.py"), "kind": "missing"},
            {"task": "sympy-13091", "files": ("sympy/core/expr.py", "sympy/core/numbers.py"), "kind": "missing"},
            {"task": "django-11400", "files": ("django/db/models/query.py", "django/db/models/fields.py"), "kind": "missing"},
        )
        for fixture in fixtures:
            with self.subTest(task=fixture["task"]):
                files = fixture["files"]
                if fixture["kind"] == "position":
                    receipts = server_receipts(
                        fixture["task"], "arm-1", "session-1", "thread-1", files,
                        xref_position=f"{files[0]}:10:5",
                    )
                    outcome = self.g.ClosedLoopGuard().evaluate_round(
                        self.request(
                            files=files,
                            patch_hash=PATCH_HASH,
                            task_id=fixture["task"],
                            server_generation="generation-1",
                            server_receipts=receipts,
                            trajectory=self.diagnostics(*files),
                        )
                    )
                    self.assertEqual(
                        outcome.decision,
                        self.g.RoundDecision.CONTRACT_FAILURE.value,
                    )
                    self.assertTrue(outcome.terminal)
                    self.assertFalse(outcome.grader_allowed)
                    self.assertIn("canonical MCP receipt chain", outcome.contract_failures[0])
                    continue

                guard = self.g.ClosedLoopGuard()
                pending = guard.evaluate_round(
                    self.request(
                        files=files,
                        patch_hash=PATCH_HASH,
                        task_id=fixture["task"],
                        server_generation="generation-1",
                        server_receipts=server_receipts(
                            fixture["task"], "arm-1", "session-1", "thread-1", files,
                            diagnostics_files=(files[0],),
                        ),
                        trajectory=self.diagnostics(*files),
                    )
                )
                self.assertFalse(pending.grader_allowed)
                self.assertEqual(
                    pending.decision,
                    self.g.RoundDecision.EVIDENCE_RECOVERY_REQUIRED.value,
                )
                self.assertEqual(
                    pending.missing_files,
                    (files[1],),
                )
                self.assertFalse(pending.terminal)
                self.assertEqual(pending.contract_failures, ())

    def test_transport_failure_is_infrastructure_and_can_retry_same_recovery(self):
        files = ("src/a.py", "src/b.py")
        task = "task-transport"
        guard = self.g.ClosedLoopGuard()
        pending = guard.evaluate_round(
            self.request(
                files=files,
                patch_hash=PATCH_HASH,
                task_id=task,
                server_generation="generation-1",
                server_receipts=[],
                trajectory=(),
                transport_failed=True,
            )
        )
        self.assertEqual(
            pending.decision,
            self.g.RoundDecision.INFRASTRUCTURE_FAILURE.value,
        )
        self.assertFalse(pending.grader_allowed)
        self.assertEqual(pending.failure_kind, self.g.FailureKind.INFRASTRUCTURE.value)
        self.assertFalse(pending.terminal)
        self.assertEqual(guard.state.quality_rounds_used, 0)
        transport = guard.evaluate_round(
            self.request(
                files=files,
                patch_hash=PATCH_HASH,
                task_id=task,
                server_generation="generation-1",
                server_receipts=server_receipts(
                    task, "arm-1", "session-1", "thread-1", files,
                ),
                trajectory=(),
                action="infra_recovery",
            )
        )
        self.assertTrue(transport.grader_allowed)
        self.assertEqual(transport.decision, self.g.RoundDecision.INFRASTRUCTURE_FAILURE.value)
        self.assertEqual(guard.state.status, self.g.RunnerStatus.ACTIVE.value)
        self.assertEqual(guard.state.quality_rounds_used, 0)

    def test_recovery_new_file_scope_restarts_quality_round_and_generation_is_bound(self):
        files = ("src/a.py", "src/b.py")
        task = "task-scope"
        guard = self.g.ClosedLoopGuard()
        guard.evaluate_round(
            self.request(
                files=files,
                patch_hash=PATCH_HASH,
                task_id=task,
                server_generation="generation-1",
                server_receipts=server_receipts(
                    task, "arm-1", "session-1", "thread-1", files,
                    diagnostics_files=(files[0],),
                ),
                trajectory=(),
            )
        )
        changed_scope = guard.evaluate_round(
            self.request(
                files=(files[0], "src/c.py"),
                patch_hash=PATCH_HASH,
                task_id=task,
                server_generation="generation-1",
                server_receipts=server_receipts(
                    task, "arm-1", "session-1", "thread-1", (files[0], "src/c.py"),
                    diagnostics_files=(files[0],),
                ),
                action="evidence_recovery",
                evidence_attempt=1,
            )
        )
        self.assertEqual(changed_scope.decision, self.g.RoundDecision.CONTRACT_FAILURE.value)
        self.assertFalse(changed_scope.terminal)
        self.assertIsNone(guard.state.pending_evidence_round)
        self.assertFalse(changed_scope.grader_allowed)


    def test_no_lsp_must_have_zero_mcp_events(self):
        guard = self.g.ClosedLoopGuard()
        outcome = guard.evaluate_round(
            self.request(mode="no_lsp", trajectory=[{"source": "mcp", "tool": "grep"}])
        )
        self.assertEqual(outcome.decision, self.g.RoundDecision.CONTRACT_FAILURE.value)
        self.assertIn("zero MCP", outcome.contract_failures[0])

        clean = self.g.ClosedLoopGuard().evaluate_round(
            self.request(mode="no_lsp", trajectory=[{"source": "shell", "command": "rg x"}])
        )
        self.assertTrue(clean.grader_allowed)

    def test_with_lsp_unknown_mcp_tool_remains_contract_failure(self):
        outcome = self.g.ClosedLoopGuard().evaluate_round(
            self.request(
                trajectory=[
                    {
                        "source": "mcp-server",
                        "tool": "unknown-tool",
                    }
                ]
            )
        )
        self.assertEqual(outcome.decision, self.g.RoundDecision.CONTRACT_FAILURE.value)
        self.assertFalse(outcome.grader_allowed)
        self.assertIn("unsupported MCP tool", outcome.contract_failures[0])

    def test_real_codex_item_completed_envelope_is_guarded(self):
        event = {
            "type": "item.completed",
            "item": {
                "type": "mcp_tool_call",
                "tool": "diagnostics",
                "arguments": {"file_path": "src/a.py"},
                "status": "completed",
            },
        }
        covered = self.g.ClosedLoopGuard().evaluate_round(
            self.request(
                trajectory=[
                    {
                        "type": "item.completed",
                        "item": {
                            "type": "mcp_tool_call",
                            "tool": "structure",
                            "arguments": {"file_path": "src/a.py"},
                            "status": "completed",
                        },
                    },
                    {
                        "type": "item.completed",
                        "item": {
                            "type": "mcp_tool_call",
                            "tool": "xref",
                            "arguments": {"file_path": "src/a.py"},
                            "status": "completed",
                        },
                    },
                    event,
                ]
            )
        )
        self.assertTrue(covered.grader_allowed)

        contaminated = self.g.ClosedLoopGuard().evaluate_round(
            self.request(mode="no_lsp", trajectory=[event])
        )
        self.assertEqual(
            contaminated.decision,
            self.g.RoundDecision.CONTRACT_FAILURE.value,
        )
        self.assertIn("zero MCP", contaminated.contract_failures[0])

    def test_generated_and_unsupported_files_need_current_reasoned_exemptions(self):
        generated = {"path": "gen/schema.json", "generated": True}
        registry = [{"path": "gen/schema.json", "kind": "generated", "reason": "producer output"}]
        guard = self.g.ClosedLoopGuard()
        outcome = guard.evaluate_round(
            self.request(
                files=(generated,),
                trajectory=(),
                exemptions=registry,
            )
        )
        self.assertTrue(outcome.grader_allowed)

        missing = self.g.ClosedLoopGuard().evaluate_round(
            self.request(files=({"path": "sql/query.sql", "kind": "unsupported_language"},), trajectory=())
        )
        self.assertEqual(missing.decision, self.g.RoundDecision.CONTRACT_FAILURE.value)
        self.assertIn("missing diagnostics exemption", missing.contract_failures[0])

        with self.assertRaises(self.g.ContractFailure):
            self.g.validate_exemption_registry(
                [generated],
                [{"path": "gen/schema.json", "kind": "generated", "reason": "   "}],
                round_number=1,
            )

        with self.assertRaisesRegex(self.g.ContractFailure, "stale"):
            self.g.validate_exemption_registry(
                ["src/a.py"],
                [{"path": "gen/old.json", "kind": "generated", "reason": "old"}],
                round_number=1,
            )

    def test_failure_channels_are_separate(self):
        valid = [
            self.g.FailureRecord(
                kind=self.g.FailureKind.INFRASTRUCTURE,
                message="MCP transport unavailable",
                source_agent_id="infra-agent",
            ),
            self.g.FailureRecord(
                kind=self.g.FailureKind.MODEL_QUALITY,
                message="grader failed assertion",
                source_agent_id="quality-agent",
                feedback_target_agent_id="code-agent",
            ),
        ]
        self.assertEqual(
            len(self.g.validate_failure_separation(valid, code_agent_id="code-agent")),
            2,
        )

        with self.assertRaisesRegex(self.g.ContractFailure, "cannot target"):
            self.g.validate_failure_separation(
                [
                    self.g.FailureRecord(
                        kind="infrastructure",
                        message="transport",
                        source_agent_id="infra-agent",
                        feedback_target_agent_id="code-agent",
                    )
                ],
                code_agent_id="code-agent",
            )

        with self.assertRaisesRegex(self.g.ContractFailure, "mixes"):
            self.g.validate_failure_separation(
                [
                    self.g.FailureRecord("infrastructure", "transport", "same-agent"),
                    self.g.FailureRecord("model_quality", "assertion", "same-agent"),
                ],
                code_agent_id="code-agent",
            )

    def test_success_stops_immediately_and_never_accepts_later_round(self):
        guard = self.g.ClosedLoopGuard()
        first = guard.evaluate_round(self.request())
        result = guard.run_grader(first, lambda _: True)
        self.assertTrue(result.quality_accepted)
        self.assertEqual(guard.state.status, self.g.RunnerStatus.SUCCEEDED.value)
        with self.assertRaises(self.g.RoundSequenceError):
            guard.evaluate_round(self.request(2, patch_hash="h2", files=("src/b.py",)))

    def test_third_round_is_last_and_failed_quality_is_not_accepted(self):
        guard = self.g.ClosedLoopGuard()
        for round_number in range(1, 4):
            outcome = guard.evaluate_round(
                self.request(
                    round_number,
                    patch_hash=f"h{round_number}",
                    files=(f"src/{round_number}.py",),
                )
            )
            self.assertTrue(outcome.grader_allowed)
            result = guard.run_grader(outcome, lambda _: False)
            self.assertFalse(result.quality_accepted)
        self.assertEqual(guard.state.status, self.g.RunnerStatus.MAX_ROUNDS_REACHED.value)
        with self.assertRaises(self.g.RoundSequenceError):
            guard.evaluate_round(
                self.request(4, patch_hash="h4", files=("src/4.py",))
            )

    def test_grader_exception_is_infrastructure_not_model_quality_feedback(self):
        guard = self.g.ClosedLoopGuard()
        outcome = guard.evaluate_round(self.request())

        result = guard.run_grader(outcome, lambda _: (_ for _ in ()).throw(RuntimeError("transport")))
        self.assertEqual(result.failure_kind, self.g.FailureKind.INFRASTRUCTURE.value)
        self.assertFalse(result.quality_accepted)
        self.assertEqual(guard.state.failures[-1].kind, self.g.FailureKind.INFRASTRUCTURE.value)
        self.assertIsNone(guard.state.failures[-1].feedback_target_agent_id)

    def test_structured_grader_feedback_uses_guard_failure_channels(self):
        unavailable_guard = self.g.ClosedLoopGuard()
        unavailable_round = unavailable_guard.evaluate_round(self.request())
        unavailable = parse_test_output(
            "Found 1 existing instance images. Will reuse them.\nRunning 1 instances..."
        )
        unavailable_result = unavailable_guard.run_grader(
            unavailable_round,
            lambda _: unavailable.as_runner_result(),
        )
        self.assertEqual(
            unavailable_result.failure_kind,
            self.g.FailureKind.INFRASTRUCTURE.value,
        )
        self.assertEqual(
            unavailable_guard.state.status,
            self.g.RunnerStatus.INFRASTRUCTURE_FAILURE.value,
        )

        quality_guard = self.g.ClosedLoopGuard()
        quality_round = quality_guard.evaluate_round(self.request())
        quality = parse_test_output(
            "FAILED tests/test_x.py::test_x - AssertionError: expected true\n"
            "E   AssertionError: expected true\n"
        )
        quality_result = quality_guard.run_grader(
            quality_round,
            lambda _: quality.as_runner_result(),
        )
        self.assertEqual(
            quality_result.failure_kind,
            self.g.FailureKind.MODEL_QUALITY.value,
        )
        self.assertEqual(quality_guard.state.status, self.g.RunnerStatus.ACTIVE.value)

    def test_state_round_trip_keeps_audit_history(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "state.json"
            guard = self.g.ClosedLoopGuard.from_store(path)
            guard.evaluate_round(self.request())
            restored = self.g.ClosedLoopGuard.from_store(path)
            self.assertEqual(restored.state.last_round, 1)
            self.assertEqual(restored.state.rounds[0].changed_production_files, ("src/a.py",))
            self.assertEqual(restored.state.rounds[0].decision, self.g.RoundDecision.GRADER_ALLOWED.value)

    def test_lsp_audit_round_trip_and_real_item_completed_envelopes(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "state.json"
            guard = self.g.ClosedLoopGuard.from_store(path)
            completed = guard.evaluate_round(
                self.request(
                    files=("src/a.py", "src/b.py"),
                    trajectory=self.semantic_completion("src/a.py", "src/b.py"),
                )
            )
            self.assertTrue(completed.grader_allowed)
            self.assertEqual(completed.evidence_attempt, None)
            restored = self.g.ClosedLoopGuard.from_store(path)
            self.assertEqual(restored.state.last_round, 1)
            self.assertIsNone(restored.state.pending_evidence_round)
            self.assertEqual(
                restored.state.rounds[0].lsp_self_check["diagnostics_coverage"],
                ["src/a.py", "src/b.py"],
            )
            recovery_trajectory = [
                {
                    "type": "item.completed",
                    "item": {
                        "type": "mcp_tool_call",
                        "tool": "structure",
                        "arguments": {"file_path": "src/b.py"},
                        "status": "completed",
                    },
                },
                {
                    "type": "item.completed",
                    "item": {
                        "type": "mcp_tool_call",
                        "tool": "xref",
                        "arguments": {"file_path": "src/b.py"},
                        "status": "completed",
                    },
                },
                {
                    "type": "item.completed",
                    "item": {
                        "type": "mcp_tool_call",
                        "tool": "diagnostics",
                        "arguments": {"file_paths": ["src/a.py", "src/b.py"]},
                        "status": "completed",
                    },
                },
            ]
            recovered = self.g.ClosedLoopGuard().evaluate_round(
                self.request(
                    files=("src/a.py", "src/b.py"),
                    trajectory=recovery_trajectory,
                )
            )
            self.assertTrue(recovered.grader_allowed)
            self.assertEqual(
                recovered.lsp_self_check["diagnostics_coverage"],
                ["src/a.py", "src/b.py"],
            )

    def test_recovery_trajectory_is_lsp_only(self):
        guard = self.g.ClosedLoopGuard()
        guard.evaluate_round(
            self.request(files=("src/a.py",), trajectory=())
        )
        blocked = guard.evaluate_round(
            self.request(
                files=("src/a.py",),
                trajectory=[{"source": "mcp", "tool": "grep", "arguments": {"path": "src/a.py"}}],
                action="evidence_recovery",
                evidence_attempt=1,
            )
        )
        self.assertEqual(blocked.decision, self.g.RoundDecision.CONTRACT_FAILURE.value)
        self.assertTrue(blocked.terminal)
        self.assertEqual(guard.state.status, self.g.RunnerStatus.CONTRACT_FAILURE.value)
        with self.assertRaises(self.g.GraderBlocked):
            guard.run_grader(blocked, lambda _: True)

    def test_recovery_second_missing_diagnostics_is_terminal_contract_failure(self):
        guard = self.g.ClosedLoopGuard()
        first = guard.evaluate_round(
            self.request(files=("src/a.py", "src/b.py"), trajectory=())
        )
        self.assertEqual(first.decision, self.g.RoundDecision.EVIDENCE_RECOVERY_REQUIRED.value)
        self.assertFalse(first.grader_allowed)
        failed = guard.evaluate_round(
            self.request(
                files=("src/a.py", "src/b.py"),
                trajectory=self.diagnostics("src/a.py"),
                action="evidence_recovery",
                evidence_attempt=1,
            )
        )
        self.assertEqual(failed.decision, self.g.RoundDecision.CONTRACT_FAILURE.value)
        self.assertTrue(failed.terminal)
        self.assertEqual(len(guard.state.rounds), 0)
        self.assertEqual(len(guard.state.evidence_attempts), 0)

    def test_recovery_identity_drift_is_persisted_as_contract_failure(self):
        guard = self.g.ClosedLoopGuard()
        guard.evaluate_round(self.request(files=("src/a.py",), trajectory=()))
        with self.assertRaises(self.g.SessionIdentityError):
            guard.evaluate_round(
                self.request(
                    round_number=1,
                    files=("src/a.py",),
                    trajectory=self.diagnostics("src/a.py"),
                    action="evidence_recovery",
                    evidence_attempt=1,
                    thread_id="drifted-thread",
                )
            )
        self.assertEqual(guard.state.status, self.g.RunnerStatus.CONTRACT_FAILURE.value)
        self.assertEqual(len(guard.state.rounds), 0)

    def test_patch_change_abandons_recovery_for_a_new_quality_round(self):
        guard = self.g.ClosedLoopGuard()
        guard.evaluate_round(
            self.request(files=("src/a.py",), patch_hash="h1", trajectory=())
        )
        changed = guard.evaluate_round(
            self.request(
                files=("src/a.py",),
                patch_hash="h2",
                trajectory=self.diagnostics("src/a.py"),
                action="evidence_recovery",
                evidence_attempt=1,
            )
        )
        self.assertEqual(changed.decision, self.g.RoundDecision.CONTRACT_FAILURE.value)
        self.assertFalse(changed.terminal)
        self.assertEqual(guard.state.status, self.g.RunnerStatus.ACTIVE.value)

    def test_same_hash_recovery_does_not_count_attempt_as_no_progress(self):
        guard = self.g.ClosedLoopGuard()
        first = guard.evaluate_round(self.request(patch_hash="h1"))
        guard.run_grader(first, lambda _: False)
        pending = guard.evaluate_round(
            self.request(
                round_number=2,
                patch_hash="h1",
                trajectory=self.semantic_completion("src/a.py"),
                files=("src/a.py",),
            )
        )
        self.assertEqual(pending.decision, self.g.RoundDecision.NO_PROGRESS.value)
        self.assertEqual(pending.no_progress_streak, 1)
        self.assertFalse(pending.grader_allowed)

    def test_infrastructure_grader_retry_does_not_consume_quality_round(self):
        guard = self.g.ClosedLoopGuard()
        first = guard.evaluate_round(self.request())
        unavailable = guard.run_grader(
            first,
            lambda _: (_ for _ in ()).throw(RuntimeError("grader transport")),
        )
        self.assertEqual(unavailable.failure_kind, self.g.FailureKind.INFRASTRUCTURE.value)
        self.assertEqual(guard.state.quality_rounds_used, 0)
        self.assertIsNone(guard.state.failures[-1].feedback_target_agent_id)
        retry = guard.evaluate_round(
            self.request(
                round_number=1,
                action="infra_recovery",
                trajectory=self.diagnostics("src/a.py"),
            )
        )
        self.assertTrue(retry.grader_allowed)
        accepted = guard.run_grader(retry, lambda _: True)
        self.assertTrue(accepted.quality_accepted)
        self.assertEqual(guard.state.quality_rounds_used, 1)
        self.assertEqual(guard.state.status, self.g.RunnerStatus.SUCCEEDED.value)

    def test_old_state_schema_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "old-state.json"
            path.write_text(json.dumps({"schema_version": 1}), encoding="utf-8")
            with self.assertRaises(self.g.StateDecodeError):
                self.g.ClosedLoopGuard.from_store(path)


if __name__ == "__main__":
    unittest.main()
