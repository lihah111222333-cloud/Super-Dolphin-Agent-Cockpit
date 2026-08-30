import copy
import base64
from cryptography.hazmat.primitives import serialization
import pathlib
import shutil
import subprocess
import sys
import unittest

from scripts.cross_file_ubuntu_execution_driver import UbuntuExecutionDriverConfig, SubprocessResult
from scripts.cross_file_ubuntu_execution_driver_test import (
    SIGNER,
    CrossFileUbuntuExecutionDriverTest,
)
from scripts.cross_file_signed_receipts import Ed25519Signer
from scripts.cross_file_ubuntu_local_provider import (
    IMAGE_MANIFEST_SCHEMA,
    LocalDockerImageInspector,
    UbuntuLocalImageProvider,
    UbuntuLocalProviderError,
    parse_question_image_manifest,
)
from scripts.generate_cross_file_question_drivers import generate_question_driver_configs


class InspectSubprocess:
    def __init__(self, *, fail_image=None, mismatch=False):
        self.calls = []
        self.fail_image = fail_image
        self.mismatch = mismatch

    def run(self, argv, *, cwd, env):
        self.calls.append((tuple(argv), cwd, dict(env)))
        if len(argv) > 1 and argv[1] == "cp":
            destination = pathlib.Path(argv[-1])
            shutil.copytree(cwd, destination, dirs_exist_ok=True)
            return SubprocessResult(tuple(argv), 0, b"", b"")
        if self.fail_image and argv[-1] == self.fail_image:
            return SubprocessResult(tuple(argv), 1, b"", b"missing")
        import json

        repo_digest = "ubuntu@sha256:" + "f" * 64 if self.mismatch else argv[-1]
        return SubprocessResult(
            tuple(argv),
            0,
            json.dumps([repo_digest]).encode("utf-8"),
            b"",
        )


def _manifest():
    return {
        "schema": IMAGE_MANIFEST_SCHEMA,
        "questions": {
            f"question-{index:02d}": "ubuntu@sha256:" + str(index % 10) * 64
            for index in range(20)
        },
    }


class CrossFileUbuntuLocalProviderTest(unittest.TestCase):
    def setUp(self):
        self.fixture = CrossFileUbuntuExecutionDriverTest()
        self.fixture.setUp()
        self.addCleanup(self.fixture.tearDown)
        config_data = copy.deepcopy(self.fixture.config_data)
        config_data["grader_slots"] = 8
        key_path = self.fixture.root / "executor-attestation.key"
        executor_signer = Ed25519Signer.generate("executor-fixture")
        grader_signer = Ed25519Signer.generate("grader-fixture")
        key_path.write_bytes(base64.b64encode(
            executor_signer._private_key.private_bytes(  # noqa: SLF001 - test fixture only
                encoding=serialization.Encoding.Raw,
                format=serialization.PrivateFormat.Raw,
                encryption_algorithm=serialization.NoEncryption(),
            )
        ))
        mcp_key_path = self.fixture.root / "mcp-receipt.key"
        mcp_signer = SIGNER
        mcp_key_path.write_bytes(base64.b64encode(
            mcp_signer._private_key.private_bytes(  # noqa: SLF001 - test fixture only
                encoding=serialization.Encoding.Raw,
                format=serialization.PrivateFormat.Raw,
                encryption_algorithm=serialization.NoEncryption(),
            )
        ))
        auth_path = self.fixture.root / "auth.json"
        auth_path.write_text("{\"test\":true}\n", encoding="utf-8")
        (auth_path.parent / "models_cache.json").write_text(
            "{\"models\":[]}", encoding="utf-8"
        )
        codex_release = self.fixture.root / "codex-release"
        codex_binary = codex_release / "bin" / "codex"
        bwrap = codex_release / "codex-resources" / "bwrap"
        codex_binary.parent.mkdir(parents=True)
        bwrap.parent.mkdir(parents=True)
        codex_binary.write_text("#!/bin/sh\n", encoding="utf-8")
        bwrap.write_text("#!/bin/sh\n", encoding="utf-8")
        codex_binary.chmod(0o755)
        bwrap.chmod(0o755)
        config_data.update({
            "codex_exec_command": [str(codex_binary), "exec"],
            "codex_resume_command": [str(codex_binary), "exec", "resume"],
            "grader_command": [sys.executable, "-m", "fake_grader"],
            "receipt_log_path_template": "receipts-{question_id}-{mode}-{round}.jsonl",
            "attestation_path_template": "attestation-{question_id}-{mode}-{round}.json",
            "prompt_policy_path_template": "prompt-policy-{question_id}-{mode}-{round}.json",
            "executor_attestation_signing_key_path": str(key_path),
            "executor_attestation_key_id": "fixture-key",
            "attestation_replay_store_path": str(self.fixture.root / "replay"),
            "environment": {"SUPER_DOLPHIN_CODEX_AUTH_PATH": str(auth_path), "MCP_LSP_RECEIPT_SIGNING_KEY_PATH": str(mcp_key_path), "MCP_LSP_BINARY": sys.executable},
            "receipt_signing_key": mcp_signer.public_key.to_dict(),
            "grader_receipt_signing_key": grader_signer.public_key.to_dict(),
        })
        self.config_data = config_data
        self.config = UbuntuExecutionDriverConfig.from_dict(config_data)

    def test_manifest_accepts_dynamic_nonempty_immutable_question_images(self):
        parsed = parse_question_image_manifest(_manifest())
        self.assertEqual(len(parsed), 20)
        short = copy.deepcopy(_manifest())
        short["questions"].pop("question-19")
        self.assertEqual(len(parse_question_image_manifest(short)), 19)
        empty = copy.deepcopy(_manifest())
        empty["questions"] = {}
        with self.assertRaises(UbuntuLocalProviderError):
            parse_question_image_manifest(empty)
        mutable = copy.deepcopy(_manifest())
        mutable["questions"]["question-00"] = "ubuntu:latest"
        with self.assertRaises(UbuntuLocalProviderError):
            parse_question_image_manifest(mutable)

    def test_preflight_inspects_every_image_before_factory(self):
        fake = InspectSubprocess()
        provider = UbuntuLocalImageProvider(self.config, _manifest(), subprocess_adapter=fake)
        inspect_calls = [call for call in fake.calls if call[0][1:4] == ("image", "inspect", "--format")]
        self.assertEqual(len(inspect_calls), 20)
        self.assertTrue(all("RepoDigests" in call[0][4] for call in inspect_calls))
        self.assertEqual(len(provider.preflight.image_ids), 20)

    def test_preflight_fails_before_any_factory_can_be_used(self):
        manifest = _manifest()
        fake = InspectSubprocess(fail_image=manifest["questions"]["question-07"])
        with self.assertRaises(UbuntuLocalProviderError):
            UbuntuLocalImageProvider(self.config, manifest, subprocess_adapter=fake)
        self.assertEqual(len(fake.calls), 20)

    def test_repo_digest_mismatch_is_rejected(self):
        fake = InspectSubprocess(mismatch=True)
        manifest = _manifest()
        manifest["questions"]["question-00"] = "ubuntu@sha256:" + "a" * 64
        with self.assertRaisesRegex(UbuntuLocalProviderError, "RepoDigest mismatch"):
            UbuntuLocalImageProvider(self.config, manifest, subprocess_adapter=fake)

    def test_wrong_role_mcp_key_is_rejected_against_mcp_public_binding(self):
        config_data = copy.deepcopy(self.fixture.config_data)
        config_data["grader_slots"] = 8
        config_data["environment"] = {
            "MCP_LSP_RECEIPT_SIGNING_KEY_PATH": str(self.fixture.root / "executor-attestation.key")
        }
        config_data.update({
            "receipt_log_path_template": "receipts-{question_id}-{mode}-{round}.jsonl",
            "attestation_path_template": "attestation-{question_id}-{mode}-{round}.json",
            "prompt_policy_path_template": "prompt-policy-{question_id}-{mode}-{round}.json",
            "executor_attestation_signing_key_path": str(self.fixture.root / "executor-attestation.key"),
            "executor_attestation_key_id": "fixture-key",
            "attestation_replay_store_path": str(self.fixture.root / "replay"),
            "grader_receipt_signing_key": self.config.grader_receipt_signing_key,
        })
        with self.assertRaisesRegex(UbuntuLocalProviderError, "MCP receipt signing key"):
            UbuntuLocalImageProvider(config_data, _manifest(), subprocess_adapter=InspectSubprocess())

    def test_unknown_runtime_mode_is_rejected_before_driver_start(self):
        provider = UbuntuLocalImageProvider(self.config, _manifest(), subprocess_adapter=InspectSubprocess())
        with self.assertRaisesRegex(UbuntuLocalProviderError, "unsupported arm mode"):
            provider.for_arm("question-00", "unknown", {"arm_id": "bad"})

    def test_missing_auth_path_fails_fast(self):
        config_data = copy.deepcopy(self.config_data)
        config_data["environment"].pop("SUPER_DOLPHIN_CODEX_AUTH_PATH")
        with self.assertRaisesRegex(UbuntuLocalProviderError, "CODEX_AUTH_PATH"):
            UbuntuLocalImageProvider(config_data, _manifest(), subprocess_adapter=InspectSubprocess())

    def test_arm_codex_config_is_mode_specific_and_auth_is_private(self):
        provider = UbuntuLocalImageProvider(self.config, _manifest(), subprocess_adapter=InspectSubprocess())
        no_lsp = provider.for_arm("question-01", "no_lsp", {"arm_id": "no-lsp"})
        with_lsp = provider.for_arm("question-01", "with_lsp", {"arm_id": "with-lsp"})
        no_text = (no_lsp._artifact_root / "codex-home" / "config.toml").read_text()
        with_text = (with_lsp._artifact_root / "codex-home" / "config.toml").read_text()
        self.assertNotIn("mcp_servers", no_text)
        self.assertIn("[features]", no_text)
        self.assertIn("enable_mcp_apps = false", no_text)
        self.assertIn("[memories]", no_text)
        self.assertIn("use_memories = false", no_text)
        self.assertIn('[mcp_servers.mcp_lsp]', with_text)
        self.assertIn("enable_mcp_apps = false", with_text)
        self.assertIn("use_memories = false", with_text)
        self.assertIn('command = "/opt/benchmark/mcp/mcp-lsp"', with_text)
        self.assertIn(
            f'MCP_LSP_RECEIPT_LOG_PATH = "{with_lsp._artifact_root / "mcp-receipts.jsonl"}"',
            with_text,
        )
        auth = no_lsp._artifact_root / "codex-home" / "auth.json"
        self.assertEqual(auth.stat().st_mode & 0o777, 0o600)
        self.assertNotIn(auth.read_text(), no_text + with_text)

    def test_provider_builds_distinct_arm_docker_runs_with_shared_grader_slots(self):
        provider = UbuntuLocalImageProvider(self.config, _manifest(), subprocess_adapter=InspectSubprocess())
        no_lsp = provider.for_arm("question-00", "no_lsp", {"arm_id": "no-lsp"})
        with_lsp = provider.for_arm("question-00", "with_lsp", {"arm_id": "with-lsp"})
        self.assertNotEqual(no_lsp._workspace, with_lsp._workspace)
        self.assertIs(no_lsp._grader_slots, with_lsp._grader_slots)
        for arm in (no_lsp, with_lsp):
            command = arm.config.container_command
            self.assertEqual(command[0:2], ("docker", "run"))
            self.assertIn("--pull=never", command)
            self.assertIn("--init", command)
            self.assertNotIn("pull", command)
            self.assertEqual(
                arm.config.codex_exec_command[0],
                "/opt/benchmark/codex-release/codex",
            )
            self.assertEqual(arm.config.grader_command, self.config.grader_command)
            self.assertIn("--network=host", command)
            self.assertIn("HTTP_PROXY=http://127.0.0.1:2081", command)
            self.assertIn("HTTPS_PROXY=http://127.0.0.1:2081", command)
            self.assertIn("ALL_PROXY=http://127.0.0.1:2081", command)
            self.assertEqual(sum(item == "--mount" for item in command), 5 if arm is no_lsp else 7)
            self.assertNotIn("/opt/benchmark/grader/python", command)
            self.assertTrue(any(item.startswith("CODEX_HOME=") for item in command))
            self.assertTrue(any("codex-home" in item for item in command))
            self.assertTrue(any("dst=" + str(arm._workspace) in item for item in command))
            self.assertNotIn("executor-attestation", " ".join(command))
            self.assertNotIn("grader-ed25519.pem", " ".join(command))
        self.assertNotEqual(no_lsp.config.container_command, with_lsp.config.container_command)
        self.assertNotIn("MCP", " ".join(no_lsp.config.container_command))
        self.assertNotIn("RECEIPT", " ".join(no_lsp.config.container_command))
        self.assertIn("MCP_LSP_RECEIPT_LOG_PATH=", " ".join(with_lsp.config.container_command))
        self.assertIn("MCP_LSP_RECEIPT_SIGNING_KEY_PATH=", " ".join(with_lsp.config.container_command))
        self.assertIn("dst=/opt/benchmark/mcp/mcp-lsp,readonly", " ".join(with_lsp.config.container_command))
        self.assertTrue(
            any(
                "dst=/opt/benchmark/codex-release,readonly" in item
                for item in with_lsp.config.container_command
            )
        )
        self.assertNotIn("codex-home", " ".join(with_lsp.config.container_command).split("MCP_LSP_RECEIPT_SIGNING_KEY_PATH=", 1)[1])

    def test_generator_emits_loadable_twenty_question_map_and_rejects_pull(self):
        template = copy.deepcopy(self.config_data)
        template["grader_slots"] = 8
        generated = generate_question_driver_configs(template, _manifest())
        self.assertEqual(generated["schema"], "super-dolphin/cross-file-question-drivers/v1")
        self.assertEqual(len(generated["question_configs"]), 20)
        for question_id, config in generated["question_configs"].items():
            self.assertEqual(config["container_image"], _manifest()["questions"][question_id])
            self.assertIn("--pull=never", config["container_command"])
            self.assertNotIn("pull", config["container_command"])
            UbuntuExecutionDriverConfig.from_dict(config)
        with self.assertRaises(UbuntuLocalProviderError):
            generate_question_driver_configs(
                template,
                _manifest(),
                container_command=("docker", "pull", "{image}"),
            )


if __name__ == "__main__":
    unittest.main()
