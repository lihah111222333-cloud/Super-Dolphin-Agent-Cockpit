import copy
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest

from scripts.cross_file_evidence_adapter_test import (
    CrossFileEvidenceAdapterTest as _AdapterFixtureBuilder,
)
from scripts.cross_file_identity_envelope import build_identity_envelope, sha256_text


setattr(_AdapterFixtureBuilder, "__test__", False)


ROOT = pathlib.Path(__file__).resolve().parent.parent
LAUNCHER = ROOT / "scripts" / "cross_file_canonical_launcher.py"
FIXTURE_NAME = "three_questions_adapter_input.json"


def _replace_strings(value, replacements):
    if isinstance(value, str):
        return replacements.get(value, value)
    if isinstance(value, dict):
        return {key: _replace_strings(child, replacements) for key, child in value.items()}
    if isinstance(value, list):
        return [_replace_strings(child, replacements) for child in value]
    return value


def _rebind_question(base, question_id, ordinal, builder):
    question = copy.deepcopy(base)
    old_task = question["question_id"]
    replacements = {old_task: question_id}
    for mode, arm in question["arms"].items():
        old_envelope = arm["identity_envelope"]
        old_bindings = old_envelope["bindings"]
        old_session = old_bindings["session"]["id"]
        old_thread = old_bindings["thread"]["id"]
        old_agent = old_bindings["agent"]["id"]
        old_patch = old_bindings["patch"]["id"]
        old_arm = arm["arm_id"]
        new_bindings = copy.deepcopy(old_bindings)
        new_bindings["task"]["id"] = question_id
        new_bindings["task"]["sha256"] = sha256_text(f"task-{question_id}")
        new_bindings["session"]["id"] = f"session-{ordinal}-{mode}"
        new_bindings["session"]["sha256"] = sha256_text(f"session-{ordinal}-{mode}")
        new_bindings["thread"]["id"] = f"thread-{ordinal}-{mode}"
        new_bindings["thread"]["sha256"] = sha256_text(f"thread-{ordinal}-{mode}")
        new_bindings["agent"]["id"] = f"q{ordinal}-{mode}-agent"
        new_bindings["agent"]["sha256"] = sha256_text(f"agent-{ordinal}-{mode}")
        new_bindings["patch"]["id"] = sha256_text(f"patch-{ordinal}").removeprefix("sha256:")
        new_bindings["patch"]["sha256"] = sha256_text(f"patch-binding-{ordinal}")
        new_envelope = build_identity_envelope(
            new_bindings,
            receipt_signing_key=old_envelope.get("receipt_signing_key"),
        )
        replacements.update({
            old_session: new_bindings["session"]["id"],
            old_thread: new_bindings["thread"]["id"],
            old_agent: new_bindings["agent"]["id"],
            old_patch: new_bindings["patch"]["id"],
            old_arm: f"q{ordinal}-{mode}",
            old_envelope["binding_sha256"]: new_envelope["binding_sha256"],
        })
        arm["identity_envelope"] = new_envelope
        arm["identity_envelopes"] = {
            name: copy.deepcopy(new_envelope)
            for name in ("runner_events", "arm_evidence", "grader_feedback", "usage")
        }
        arm["arm_id"] = f"q{ordinal}-{mode}"
        arm["code_agent_id"] = new_bindings["agent"]["id"]
        arm["server_generation"] = "generation-01"
        arm.update(_replace_strings(arm, replacements))
        arm["identity_envelope"] = new_envelope
        arm["identity_envelopes"] = {
            name: copy.deepcopy(new_envelope)
            for name in ("runner_events", "arm_evidence", "grader_feedback", "usage")
        }
        arm["arm_evidence"] = builder._canonical_arm_evidence(
            new_envelope,
            mode,
            arm["arm_id"],
        )
        identity = builder._audit_identity(new_envelope, arm["arm_id"], mode)
        arm["grader_feedback"] = builder._feedback(identity)
        arm["runner_events"] = {
            "schema_version": builder.adapter.RUNNER_EVENT_SCHEMA_VERSION,
            "events": builder._runner_events(identity, new_bindings["agent"]["id"]),
        }
        for source_name in ("official_report", "official_test_output"):
            arm[source_name] = builder._official_source(
                identity,
                "super-dolphin/grader-feedback/v1",
            )
    question["question_id"] = question_id
    question["discovery_shape"] = f"cross_file_{ordinal}"
    return question


def three_question_fixture():
    builder = _AdapterFixtureBuilder()
    builder.setUp()
    base = builder.fixture()
    questions = [
        {
            "question_id": f"django__django-1656{ordinal}",
            "discovery_shape": f"cross_file_{ordinal}",
            "arms": _rebind_question(base, f"django__django-1656{ordinal}", ordinal, builder)["arms"],
        }
        for ordinal in range(1, 4)
    ]
    return {
        "schema": base["schema"],
        "run_id": "canonical-launcher-three-question",
        "pricing_manifest": base["pricing_manifest"],
        "questions": questions,
    }


class CanonicalLauncherTests(unittest.TestCase):
    def _run(self, payload, *, output=None):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            input_path = root / FIXTURE_NAME
            input_path.write_text(json.dumps(payload), encoding="utf-8")
            command = [sys.executable, str(LAUNCHER), "--input", str(input_path)]
            if output is not None:
                command.extend(["--output", str(output)])
            return subprocess.run(command, cwd=ROOT, text=True, capture_output=True)

    def test_three_question_fixture_has_one_canonical_json_output(self):
        completed = self._run(three_question_fixture())
        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertEqual(completed.stderr, "")
        report = json.loads(completed.stdout)
        self.assertEqual(report["schema"], "super-dolphin/cross-file-ab-audit/v1")
        self.assertEqual(len(report["questions"]), 3)
        self.assertEqual(report["integrity"]["policy_hash"], report["policy_hash"])
        self.assertEqual(report["statistics"]["eligible"], False)

    def test_adapter_tamper_fails_nonzero_without_canonical_stdout(self):
        payload = three_question_fixture()
        payload["questions"][1]["arms"]["with_lsp"]["pricing"]["manifest_hash"] = "drifted"
        completed = self._run(payload)
        self.assertNotEqual(completed.returncode, 0)
        self.assertEqual(completed.stdout, "")
        self.assertIn("canonical evidence audit failed", completed.stderr)

    def test_output_file_contains_only_canonical_json(self):
        payload = three_question_fixture()
        with tempfile.TemporaryDirectory() as directory:
            output = pathlib.Path(directory) / "canonical.json"
            completed = self._run(payload, output=output)
            self.assertEqual(completed.returncode, 0, completed.stderr)
            self.assertEqual(completed.stdout, "")
            report = json.loads(output.read_text(encoding="utf-8"))
            self.assertEqual(len(report["questions"]), 3)

    def test_execute_rejects_non_twenty_question_plan_before_provider_start(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            input_path = root / "plan.json"
            input_path.write_text(
                json.dumps({"questions": [{"question_id": "only-one"}], "max_concurrency": 40}),
                encoding="utf-8",
            )
            completed = subprocess.run(
                [
                    sys.executable,
                    str(LAUNCHER),
                    "--input",
                    str(input_path),
                    "--mode",
                    "execute",
                    "--state-dir",
                    str(root / "state"),
                    "--driver-config",
                    str(root / "driver.json"),
                    "--image-manifest",
                    str(root / "images.json"),
                ],
                cwd=ROOT,
                text=True,
                capture_output=True,
            )
        self.assertEqual(completed.returncode, 2)
        self.assertEqual(completed.stdout, "")
        self.assertIn("exactly 20 questions", completed.stderr)

    def test_production_call_is_rg_discoverable_and_adapter_only(self):
        source = LAUNCHER.read_text(encoding="utf-8")
        self.assertIn("cross_file_evidence_adapter", source)
        self.assertIn("audit_evidence_set", source)
        self.assertIn("cross_file_benchmark_runner", source)
        self.assertIn("load_local_image_provider", source)
        self.assertIn("max_concurrency=40", source)
        self.assertIn("exactly 8 grader slots", source)
        self.assertIn("execute_plan", source)
        self.assertNotIn("audit_module.audit", source)
        self.assertNotIn("ClosedLoopGuard", source)
        self.assertNotIn("resume --last", source)
        self.assertNotIn('"resolved_ids"', source)


if __name__ == "__main__":
    unittest.main()
