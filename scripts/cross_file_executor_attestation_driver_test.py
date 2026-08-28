import base64
import copy
import dataclasses
import hashlib
import json
import pathlib
import unittest

from scripts.cross_file_go_receipts import SCHEMA as GO_RECEIPT_SCHEMA, _go_json
from scripts.cross_file_ubuntu_execution_driver import (
    UbuntuExecutionDriver,
    UbuntuExecutionDriverConfig,
    UbuntuExecutionDriverProtocolError,
)
from scripts.cross_file_benchmark_runner import _driver_artifact
from scripts.cross_file_ubuntu_execution_driver_test import (
    CrossFileUbuntuExecutionDriverTest as _FixtureBase,
    FakeSubprocess as _FakeSubprocess,
)
from scripts.cross_file_evidence_adapter_test import SIGNER


class AttestationFake(_FakeSubprocess):
    def run(self, argv, *, cwd, env):
        result = super().run(argv, cwd=cwd, env=env)
        # A real Codex process does not author the executor manifest.  Remove
        # the fake's optional file to prove the driver reconstructs it.
        pathlib.Path(env["SUPER_DOLPHIN_RESULT_PATH"]).unlink(missing_ok=True)
        if env["SUPER_DOLPHIN_MODE"] == "no_lsp" and env["SUPER_DOLPHIN_PHASE"] in {
            "agent",
            "evidence_recovery",
        }:
            return dataclasses.replace(
                result, stderr=b'{"type":"sidecar.completed"}\n'
            )
        return result

    def _write_agent(self, env, cwd):
        super()._write_agent(env, cwd)
        public = base64.b64decode(SIGNER.public_key.public_key)
        unsigned = {
            "schema": GO_RECEIPT_SCHEMA,
            "sequence": 1,
            "payload": {
                "receipt_id": "receipt-1",
                "tool": "diagnostics",
                "status": "success",
                "arguments": {"file_path": "src/shared.py"},
                "result": {"rows": []},
            },
            "signer_public_key_sha256": "sha256:" + hashlib.sha256(public).hexdigest(),
        }
        material = _go_json(unsigned)
        record = {
            **unsigned,
            "event_digest": "sha256:" + hashlib.sha256(material).hexdigest(),
            "signature": base64.b64encode(SIGNER.sign(material)).decode(),
        }
        if env["SUPER_DOLPHIN_MODE"] == "with_lsp":
            pathlib.Path(env["SUPER_DOLPHIN_RECEIPT_LOG_PATH"]).write_text(
                json.dumps(record, separators=(",", ":")) + "\n", encoding="utf-8"
            )
        pathlib.Path(env["SUPER_DOLPHIN_EVIDENCE_PATH"]).write_text(
            "agent-owned evidence must not be read", encoding="utf-8"
        )


class FailedAttestationFake(AttestationFake):
    def run(self, argv, *, cwd, env):
        result = super().run(argv, cwd=cwd, env=env)
        if env["SUPER_DOLPHIN_MODE"] == "no_lsp" and env["SUPER_DOLPHIN_PHASE"] == "agent":
            return dataclasses.replace(
                result,
                stdout=(
                    b'{"type":"thread.started","session_id":"session-no_lsp",'
                    b'"thread_id":"thread-no_lsp","code_agent_id":"no_lsp-agent"}\n'
                    b'{"type":"turn.failed","usage":{"input_tokens":10,"cached_input_tokens":0,'
                    b'"output_tokens":1,"reasoning_tokens":0}}\n'
                ),
                stderr=b'{"type":"sidecar.completed"}\n',
                returncode=17,
            )
        return result


class ExecutorAttestationDriverTest(unittest.TestCase):
    _fixture_cls = _FixtureBase

    def setUp(self):
        self.fixture = self._fixture_cls()
        self.fixture.setUp()
        self.temp_key = pathlib.Path(self.fixture.root) / "executor.seed"
        self.temp_key.write_bytes(b"e" * 32)

    def tearDown(self):
        self.fixture.tearDown()

    def test_driver_generates_attestation_without_reading_agent_arm_evidence(self):
        config_data = copy.deepcopy(self.fixture.config_data)
        artifact_dir = pathlib.Path(config_data["artifact_dir"])
        config_data.update(
            {
                "receipt_log_path_template": "receipt-{question_id}-{mode}-{round}-{nonce}.jsonl",
                "attestation_path_template": "attestation-{question_id}-{mode}-{round}-{nonce}.json",
                "prompt_policy_path_template": "policy-{question_id}-{mode}-{round}-{nonce}.json",
                "executor_attestation_signing_key_path": str(self.temp_key),
                "executor_attestation_key_id": "executor-key",
                "attestation_replay_store_path": str(artifact_dir / "replay.json"),
            }
        )
        config = UbuntuExecutionDriverConfig.from_dict(config_data)
        fake = AttestationFake(self.fixture)
        driver = UbuntuExecutionDriver(config, subprocess_adapter=fake)
        context = self.fixture._context(mode="with_lsp")
        context = dataclasses.replace(
            context,
            runner_snapshot={**context.runner_snapshot, "run_id": "run-1"},
        )
        result = driver.run_agent(context)
        self.assertNotIn("arm_evidence", result)
        self.assertNotIn("server_receipts", result["request"])
        self.assertEqual(
            [event["tool"] for event in result["request"]["mcp_trajectory"]],
            ["diagnostics"],
        )
        attestation = result["executor_attestation"]
        self.assertEqual(attestation["run_id"], "run-1")
        self.assertEqual(attestation["attempt"], 1)
        self.assertEqual(attestation["signature"]["issuer"], "executor")
        attestation_path = pathlib.Path(
            next(call["env"]["SUPER_DOLPHIN_ATTESTATION_PATH"] for call in fake.calls)
        )
        self.assertTrue(attestation_path.is_file())
        self.assertTrue((artifact_dir / "replay.json").parent.is_dir())
        with self.assertRaises(UbuntuExecutionDriverProtocolError):
            driver.consume_executor_attestation(attestation)
        artifact, _, identity = _driver_artifact(
            result,
            question_id=context.question_id,
            mode="with_lsp",
            arm_id="with-lsp",
            run_id="run-1",
        )
        self.assertIn("executor_attestation", artifact)
        self.assertNotIn("arm_evidence", artifact)
        self.assertEqual(identity["mode"], "with_lsp")
        # The fake writes an invalid self-authored evidence file, but the
        # executor-owned path still succeeds because that file is not read.
        pathlib.Path(config_data["artifact_dir"]).joinpath(
            "evidence-django__django-16560-with_lsp-1-agent.json"
        )

    def test_partial_attestation_configuration_fails_closed(self):
        config_data = copy.deepcopy(self.fixture.config_data)
        config_data["receipt_log_path_template"] = "receipt.jsonl"
        with self.assertRaisesRegex(
            Exception, "executor attestation config is incomplete"
        ):
            UbuntuExecutionDriverConfig.from_dict(config_data)

    def test_driver_generates_no_lsp_zero_ingress_from_stdout_and_sidecar(self):
        config_data = copy.deepcopy(self.fixture.config_data)
        artifact_dir = pathlib.Path(config_data["artifact_dir"])
        config_data.update(
            {
                "receipt_log_path_template": "receipt-{question_id}-{mode}-{round}-{nonce}.jsonl",
                "attestation_path_template": "attestation-{question_id}-{mode}-{round}-{nonce}.json",
                "prompt_policy_path_template": "policy-{question_id}-{mode}-{round}-{nonce}.json",
                "executor_attestation_signing_key_path": str(self.temp_key),
                "executor_attestation_key_id": "executor-key",
                "attestation_replay_store_path": str(artifact_dir / "replay.json"),
            }
        )
        config = UbuntuExecutionDriverConfig.from_dict(config_data)
        driver = UbuntuExecutionDriver(
            config, subprocess_adapter=AttestationFake(self.fixture)
        )
        context = self.fixture._context(mode="no_lsp")
        context = dataclasses.replace(
            context,
            runner_snapshot={**context.runner_snapshot, "run_id": "run-no-lsp"},
        )
        result = driver.run_agent(context)
        attestation = result["executor_attestation"]
        self.assertEqual(attestation["mode"], "no_lsp")
        self.assertEqual(attestation["receipt_count"], 0)
        self.assertEqual(attestation["isolation_policy"]["ingress_count"], 0)
        self.assertTrue(attestation["isolation_policy"]["model_egress"]["allowed"])
        self.assertIsNone(attestation["isolation_policy"]["model_egress"]["proxy"])
        self.assertTrue(attestation["isolation_policy"]["mcp_sidecar"]["disabled"])

    def test_failed_turn_keeps_dynamic_identity_and_generated_artifacts(self):
        config_data = copy.deepcopy(self.fixture.config_data)
        artifact_dir = pathlib.Path(config_data["artifact_dir"])
        config_data.update(
            {
                "receipt_log_path_template": "receipt-{question_id}-{mode}-{round}-{nonce}.jsonl",
                "attestation_path_template": "attestation-{question_id}-{mode}-{round}-{nonce}.json",
                "prompt_policy_path_template": "policy-{question_id}-{mode}-{round}-{nonce}.json",
                "executor_attestation_signing_key_path": str(self.temp_key),
                "executor_attestation_key_id": "executor-key",
                "attestation_replay_store_path": str(artifact_dir / "replay.json"),
            }
        )
        config = UbuntuExecutionDriverConfig.from_dict(config_data)
        driver = UbuntuExecutionDriver(
            config, subprocess_adapter=FailedAttestationFake(self.fixture)
        )
        context = self.fixture._context(mode="no_lsp")
        context = dataclasses.replace(
            context,
            runner_snapshot={**context.runner_snapshot, "run_id": "run-failed"},
        )
        result = driver.run_agent(context)
        self.assertIn("transport_error", result)
        self.assertEqual(
            result["identity_envelope"]["bindings"]["session"]["id"],
            "session-no_lsp",
        )
        self.assertEqual(result["executor_attestation"]["receipt_count"], 0)


del _FixtureBase


if __name__ == "__main__":
    unittest.main()
