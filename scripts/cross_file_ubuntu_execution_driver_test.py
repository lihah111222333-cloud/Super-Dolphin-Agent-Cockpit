import copy
import dataclasses
import hashlib
import json
import os
import pathlib
import subprocess
import tempfile
import unittest
from unittest import mock

from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

from scripts import cross_file_benchmark_runner as benchmark_runner
from scripts import cross_file_ubuntu_execution_driver as driver_module
from scripts.cross_file_benchmark_runner import AgentContext, GraderContext
from scripts.cross_file_closed_loop_guard import RoundInput
from scripts.cross_file_evidence_adapter_test import (
    GRADER_ARTIFACT,
    SIGNER,
    CrossFileEvidenceAdapterTest as EvidenceFixtureBuilder,
)
from scripts.cross_file_identity_envelope import build_identity_envelope, sha256_bytes, sha256_text
from scripts.cross_file_grader_bridge import build_signed_feedback


class FakeSubprocess:
    """Container/grader seam that writes only artifacts declared by the driver."""

    def __init__(self, owner, *, fail_agent=False, fail_grader=False, stable_patch=False):
        self.owner = owner
        self.fail_agent = fail_agent
        self.fail_grader = fail_grader
        self.stable_patch = stable_patch
        self.calls = []
        self.agent_count = 0

    def run(self, argv, *, cwd, env):
        self.calls.append({"argv": tuple(argv), "cwd": cwd, "env": dict(env)})
        phase = env["SUPER_DOLPHIN_PHASE"]
        if phase in {"agent", "evidence_recovery"}:
            self.agent_count += 1
            self._write_agent(env, cwd)
            return driver_module.SubprocessResult(
                argv=tuple(argv),
                returncode=17 if self.fail_agent else 0,
                stdout=(
                    json.dumps({
                        "type": "thread.started",
                        "session_id": self._session(env),
                        "thread_id": self._thread(env),
                        "code_agent_id": self._agent(env),
                    })
                    .encode()
            + b"\n"
            + b"\n".join(
                json.dumps(event).encode()
                for event in self._trajectory(env["SUPER_DOLPHIN_MODE"])
            )
            + (b"\n" if self._trajectory(env["SUPER_DOLPHIN_MODE"]) else b"")
            + json.dumps({
                        "type": "turn.completed",
                        "usage": {
                            "input_tokens": 10,
                            "cached_input_tokens": 0,
                            "output_tokens": 5,
                            "reasoning_tokens": 1,
                        },
                    }).encode()
                    + b"\n"
                ),
                stderr=b"agent-stderr",
            )
        self._write_grader(env)
        return driver_module.SubprocessResult(
            argv=tuple(argv),
            returncode=23 if self.fail_grader else 0,
            stdout=b"grader-stdout",
            stderr=b"grader-stderr",
        )

    @staticmethod
    def _session(env):
        return f"session-{env['SUPER_DOLPHIN_MODE']}"

    @staticmethod
    def _thread(env):
        return f"thread-{env['SUPER_DOLPHIN_MODE']}"

    @staticmethod
    def _agent(env):
        return f"{env['SUPER_DOLPHIN_MODE']}-agent"

    @staticmethod
    def _trajectory(mode):
        if mode == "no_lsp":
            return []
        path = "src/shared.py"
        return [
            {
                "type": "mcp_tool_call",
                "tool": "structure",
                "arguments": {"file_path": path},
                "result": {"locations": [f"{path}:10:2"]},
            },
            {
                "type": "mcp_tool_call",
                "tool": "xref",
                "arguments": {"pos": f"{path}:10:2"},
                "result": {"locations": []},
            },
            {
                "type": "mcp_tool_call",
                "tool": "diagnostics",
                "arguments": {
                    "file_paths": [path],
                    "diff_filter": {
                        "mode": "changed_lines",
                        "changed_lines": {path: [10]},
                    },
                },
                "result": {"rows": [], "status": "clean"},
            },
        ]

    def _git_facts(self, cwd):
        diff = subprocess.run(
            ["git", "-C", str(cwd), "diff", "--binary", "HEAD", "--"],
            capture_output=True,
            check=True,
        ).stdout
        index_path = pathlib.Path(tempfile.mkstemp(prefix="fake-index-")[1])
        index_path.unlink(missing_ok=True)
        try:
            index_env = {"GIT_INDEX_FILE": str(index_path)}
            subprocess.run(
                ["git", "-C", str(cwd), "read-tree", "HEAD"],
                capture_output=True,
                check=True,
                env={**dict(os.environ), **index_env},
            )
            subprocess.run(
                ["git", "-C", str(cwd), "add", "-A", "--"],
                capture_output=True,
                check=True,
                env={**dict(os.environ), **index_env},
            )
            tree = subprocess.run(
                ["git", "-C", str(cwd), "write-tree"],
                capture_output=True,
                check=True,
                text=True,
                env={**dict(os.environ), **index_env},
            ).stdout.strip()
        finally:
            index_path.unlink(missing_ok=True)
        names = subprocess.run(
            ["git", "-C", str(cwd), "diff", "--name-only", "HEAD", "--"],
            capture_output=True,
            check=True,
            text=True,
        ).stdout.splitlines()
        # The fake edits a tracked file, so the patch material is sufficient.
        return sha256_bytes(diff).removeprefix("sha256:"), tree, tuple(sorted(names))

    def _dynamic_envelope(self, env, patch_hash, tree_sha):
        template = self.owner.builder.fixture()["arms"][env["SUPER_DOLPHIN_MODE"]][
            "identity_envelope"
        ]
        bindings = copy.deepcopy(template["bindings"])
        bindings["task"] = {
            "id": env["SUPER_DOLPHIN_QUESTION_ID"],
            "sha256": sha256_text(env["SUPER_DOLPHIN_QUESTION_ID"]),
        }
        bindings["session"] = {"id": self._session(env), "sha256": sha256_text(self._session(env))}
        bindings["thread"] = {"id": self._thread(env), "sha256": sha256_text(self._thread(env))}
        bindings["agent"] = {"id": self._agent(env), "sha256": sha256_text(self._agent(env))}
        bindings["patch"] = {"id": patch_hash, "sha256": sha256_text(patch_hash)}
        bindings["final_tree"] = {"tree_sha": tree_sha, "sha256": sha256_text(tree_sha)}
        return build_identity_envelope(
            bindings,
            receipt_signing_key=SIGNER.public_key.to_dict(),
        )

    def _write_agent(self, env, cwd):
        path = pathlib.Path(cwd) / "src" / "shared.py"
        content = "candidate\n" if self.stable_patch else (
            f"{env['SUPER_DOLPHIN_MODE']}:{env['SUPER_DOLPHIN_ROUND']}:{self.agent_count}\n"
        )
        path.write_text(content, encoding="utf-8")
        patch_hash, tree_sha, changed = self._git_facts(cwd)
        envelope = self._dynamic_envelope(env, patch_hash, tree_sha)
        mode = env["SUPER_DOLPHIN_MODE"]
        arm_id = env["SUPER_DOLPHIN_ARM_ID"]
        evidence = self.owner.builder._canonical_arm_evidence(envelope, mode, arm_id)
        pathlib.Path(env["SUPER_DOLPHIN_EVIDENCE_PATH"]).write_text(
            json.dumps(evidence), encoding="utf-8"
        )
        pathlib.Path(env["SUPER_DOLPHIN_USAGE_PATH"]).write_text(
            json.dumps({"input_tokens": 10, "cached_input_tokens": 0, "output_tokens": 5}),
            encoding="utf-8",
        )
        pathlib.Path(env["SUPER_DOLPHIN_SERVER_GENERATION_PATH"]).write_text(
            "generation-01", encoding="utf-8"
        )
        request = {
            "patch_hash": patch_hash,
            "changed_files": list(changed),
            "trajectory": self._trajectory(mode),
            "mode": mode,
        }
        manifest = {
            "request": request,
            "session_id": self._session(env),
            "thread_id": self._thread(env),
            "code_agent_id": self._agent(env),
            "patch_hash": patch_hash,
            "tree_sha": tree_sha,
        }
        pathlib.Path(env["SUPER_DOLPHIN_RESULT_PATH"]).write_text(
            json.dumps(manifest), encoding="utf-8"
        )

    def _write_grader(self, env):
        request = json.loads(pathlib.Path(env["SUPER_DOLPHIN_REQUEST_PATH"]).read_text())
        identity = {
            "question_id": env["SUPER_DOLPHIN_QUESTION_ID"],
            "arm_id": env["SUPER_DOLPHIN_ARM_ID"],
            "session_id": request["session_id"],
            "thread_id": request["thread_id"],
            "patch_hash": request["patch_hash"],
        }
        source = self.owner.builder._official_source(
            identity,
            "super-dolphin/grader-feedback/v1",
        )
        _, tree_sha, _ = self._git_facts(pathlib.Path(env["SUPER_DOLPHIN_WORKSPACE"]))
        feedback = build_signed_feedback(
            task=identity["question_id"],
            arm=identity["arm_id"],
            session=identity["session_id"],
            thread=identity["thread_id"],
            patch_hash=identity["patch_hash"],
            final_tree_hash=tree_sha,
            request_id=(
                f"grader-{identity['question_id']}-{identity['arm_id']}-"
                f"{env['SUPER_DOLPHIN_ROUND']}-"
                f"{env.get('SUPER_DOLPHIN_ATTEMPT', '1')}"
            ),
            seq=1,
            signer=SIGNER,
            report=source,
            test_output=source,
            stdout=b"grader-stdout",
            stderr=b"grader-stderr",
        )
        pathlib.Path(env["SUPER_DOLPHIN_FEEDBACK_PATH"]).write_text(
            json.dumps(feedback), encoding="utf-8"
        )
        pathlib.Path(env["SUPER_DOLPHIN_REPORT_PATH"]).write_text(source, encoding="utf-8")
        pathlib.Path(env["SUPER_DOLPHIN_TEST_OUTPUT_PATH"]).write_text(source, encoding="utf-8")


class RetrySubprocess(FakeSubprocess):
    """Emit one real-session transport failure before the successful turn."""

    def __init__(self, owner, *, dirty_without_session=False):
        super().__init__(owner)
        self.dirty_without_session = dirty_without_session

    def run(self, argv, *, cwd, env):
        if env["SUPER_DOLPHIN_PHASE"] in {"agent", "evidence_recovery"} and len(
            [call for call in self.calls if call["env"]["SUPER_DOLPHIN_PHASE"] in {"agent", "evidence_recovery"}]
        ) == 0:
            self.calls.append({"argv": tuple(argv), "cwd": cwd, "env": dict(env)})
            if self.dirty_without_session:
                pathlib.Path(cwd, "src", "partial.py").write_text("partial\n", encoding="utf-8")
                stdout = b""
            else:
                stdout = (
                    b'{"type":"thread.started","session_id":"session-no_lsp",'
                    b'"thread_id":"thread-no_lsp","code_agent_id":"no_lsp-agent"}\n'
                )
            return driver_module.SubprocessResult(
                argv=tuple(argv),
                returncode=1,
                stdout=stdout,
                stderr=b"429 Too Many Requests: stream disconnected",
            )
        return super().run(argv, cwd=cwd, env=env)


class CrossFileUbuntuExecutionDriverTest(unittest.TestCase):
    def test_production_change_classifier_excludes_tests_and_docs(self):
        self.assertTrue(driver_module._is_production_change("django/db/models/query.py"))
        for path in (
            "tests/queries/test_qs_combinators.py",
            "testing/test_conftest.py",
            "src/pkg/query_test.go",
            "doc/whats_new/v1.rst",
            "changelog/5819.bugfix.rst",
            "CHANGES",
            "ChangeLog",
            "CONTRIBUTORS.txt",
            "setup.cfg",
        ):
            self.assertFalse(driver_module._is_production_change(path), path)

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.temp.name)
        self.workspace = self.root / "repo"
        (self.workspace / "src").mkdir(parents=True)
        (self.workspace / "src" / "shared.py").write_text("base\n", encoding="utf-8")
        self._git(["init", "-q", str(self.workspace)])
        self._git(["-C", str(self.workspace), "config", "user.email", "driver@example.invalid"])
        self._git(["-C", str(self.workspace), "config", "user.name", "driver-test"])
        self._git(["-C", str(self.workspace), "add", "src/shared.py"])
        self._git(["-C", str(self.workspace), "commit", "-qm", "base"])
        self.builder = EvidenceFixtureBuilder()
        self.builder.setUp()
        self.canonical_prompt = self.root / "shared-developer-instructions.md"
        self.canonical_prompt.write_text(
            "## Native baseline\n\n"
            "Native baseline instructions.\n\n"
            "## Conditional With-LSP addendum\n"
            "Use structure only when definitions are unknown; run diagnostics after edits.\n",
            encoding="utf-8",
        )
        self.config_data = self._config_data()

    def tearDown(self):
        self.temp.cleanup()

    def _git(self, args):
        return subprocess.run(["git", *args], check=True, capture_output=True)

    def _config_data(self):
        base = self.builder.fixture()["arms"]["no_lsp"]["identity_envelope"]["bindings"]
        bindings = copy.deepcopy(base)
        head = subprocess.run(
            ["git", "-C", str(self.workspace), "rev-parse", "HEAD"],
            check=True,
            capture_output=True,
            text=True,
        ).stdout.strip()
        tree = subprocess.run(
            ["git", "-C", str(self.workspace), "rev-parse", "HEAD^{tree}"],
            check=True,
            capture_output=True,
            text=True,
        ).stdout.strip()
        bindings["base"] = {"commit_sha": head, "tree_sha": tree, "sha256": sha256_text("base")}
        return {
            "container_command": ["container", "exec", "{image}"],
            "container_image": "ubuntu@sha256:" + "1" * 64,
            "codex_exec_command": ["codex", "exec"],
            "codex_resume_command": ["codex", "exec", "resume"],
            "grader_command": ["swebench", "run"],
            "workspace": str(self.workspace),
            "artifact_dir": str(self.root / "artifacts"),
            "prompt_template": "fix {question_id} {mode} round {round}",
            "canonical_lsp_prompt_path": str(self.canonical_prompt),
            "canonical_lsp_prompt_sha256": hashlib.sha256(
                self.canonical_prompt.read_bytes()
            ).hexdigest(),
            "identity_bindings": bindings,
            "receipt_signing_key": SIGNER.public_key.to_dict(),
            "grader_receipt_signing_key": SIGNER.public_key.to_dict(),
            "agent_result_path_template": "agent-{question_id}-{mode}-{round}-{phase}.json",
            "agent_stdout_path_template": "agent-{question_id}-{mode}-{round}-{phase}.stdout",
            "agent_stderr_path_template": "agent-{question_id}-{mode}-{round}-{phase}.stderr",
            "session_state_path_template": "session-{question_id}-{mode}.json",
            "arm_evidence_path_template": "evidence-{question_id}-{mode}-{round}-{phase}.json",
            "usage_path_template": "usage-{question_id}-{mode}-{round}-{phase}.json",
            "server_generation_path_template": "generation-{question_id}-{mode}-{round}-{phase}.txt",
            "grader_request_path_template": "grader-request-{question_id}-{mode}-{round}-{attempt}.json",
            "grader_feedback_path_template": "grader-feedback-{question_id}-{mode}-{round}-{attempt}.json",
            "grader_report_path_template": "grader-report-{question_id}-{mode}-{round}-{attempt}.json",
            "grader_test_output_path_template": "grader-test-{question_id}-{mode}-{round}-{attempt}.txt",
            "grader_stdout_path_template": "grader-{question_id}-{mode}-{round}-{attempt}.stdout",
            "grader_stderr_path_template": "grader-{question_id}-{mode}-{round}-{attempt}.stderr",
            "grader_slots": 1,
            "environment": {},
        }

    def _driver(self, *, fail_agent=False, fail_grader=False, stable_patch=False):
        config = driver_module.UbuntuExecutionDriverConfig.from_dict(self.config_data)
        fake = FakeSubprocess(
            self,
            fail_agent=fail_agent,
            fail_grader=fail_grader,
            stable_patch=stable_patch,
        )
        return driver_module.UbuntuExecutionDriver(config, subprocess_adapter=fake), fake

    def _context(self, mode="no_lsp", round_number=1, identity=None):
        return AgentContext(
            question_id="django__django-16560",
            mode=mode,
            round_number=round_number,
            identity=identity or {"question_id": "django__django-16560", "arm_id": "no-lsp", "mode": mode},
            arm={"mode": mode, "arm_id": "no-lsp" if mode == "no_lsp" else "with-lsp"},
            runner_snapshot={"status": "new", "last_round": round_number - 1},
        )

    def test_config_is_explicit_and_fail_fast(self):
        for required in (
            "canonical_lsp_prompt_path",
            "canonical_lsp_prompt_sha256",
        ):
            missing_prompt_contract = dict(self.config_data)
            missing_prompt_contract.pop(required)
            with self.subTest(required=required), self.assertRaises(
                driver_module.UbuntuExecutionDriverConfigError
            ):
                driver_module.UbuntuExecutionDriverConfig.from_dict(
                    missing_prompt_contract
                )
        missing = dict(self.config_data)
        missing.pop("container_image")
        with self.assertRaises(driver_module.UbuntuExecutionDriverConfigError):
            driver_module.UbuntuExecutionDriverConfig.from_dict(missing)
        mutable = dict(self.config_data)
        mutable["container_image"] = "ubuntu:latest"
        with self.assertRaises(driver_module.UbuntuExecutionDriverConfigError):
            driver_module.UbuntuExecutionDriverConfig.from_dict(mutable)
        relative = dict(self.config_data)
        relative["workspace"] = "repo"
        with self.assertRaises(driver_module.UbuntuExecutionDriverConfigError):
            driver_module.UbuntuExecutionDriverConfig.from_dict(relative)
        unknown = dict(self.config_data)
        unknown["agent_timeout_seconds"] = 30
        with self.assertRaises(driver_module.UbuntuExecutionDriverConfigError):
            driver_module.UbuntuExecutionDriverConfig.from_dict(unknown)

        stale_prompt = dict(self.config_data)
        stale_prompt["canonical_lsp_prompt_sha256"] = "0" * 64
        with self.assertRaises(driver_module.UbuntuExecutionDriverConfigError):
            driver_module.UbuntuExecutionDriverConfig.from_dict(stale_prompt)

    def test_prompt_is_arm_specific_and_no_lsp_contamination_fails_closed(self):
        driver, fake = self._driver()
        driver.run_agent(self._context(mode="with_lsp"))
        self.assertEqual(fake.calls[-1]["argv"][-1], "fix django__django-16560 with_lsp round 1")
        system_path = pathlib.Path(fake.calls[-1]["env"]["SUPER_DOLPHIN_PROMPT_SYSTEM_PATH"])
        self.assertEqual(
            system_path.read_text(encoding="utf-8"),
            self.canonical_prompt.read_text(encoding="utf-8"),
        )
        self.workspace.joinpath("src", "shared.py").write_text("base\n", encoding="utf-8")
        no_driver, no_fake = self._driver()
        no_driver.run_agent(self._context(mode="no_lsp"))
        no_system_path = pathlib.Path(no_fake.calls[-1]["env"]["SUPER_DOLPHIN_PROMPT_SYSTEM_PATH"])
        canonical = self.canonical_prompt.read_text(encoding="utf-8")
        self.assertEqual(
            no_system_path.read_text(encoding="utf-8"),
            canonical[: canonical.index("## Conditional With-LSP addendum")],
        )

        contaminated = dict(self.config_data)
        contaminated["prompt_template"] = "fix with diagnostics"
        self.workspace.joinpath("src", "shared.py").write_text("base\n", encoding="utf-8")
        config = driver_module.UbuntuExecutionDriverConfig.from_dict(contaminated)
        bad = driver_module.UbuntuExecutionDriver(config, subprocess_adapter=FakeSubprocess(self))
        bad.run_agent(self._context(mode="no_lsp"))
        self.assertEqual(
            bad.subprocess.calls[-1]["argv"][-1],
            "fix with diagnostics",
        )

    def test_prompt_contract_is_strict_and_identity_covers_all_four_hashes(self):
        canonical = self.canonical_prompt.read_text(encoding="utf-8")
        marker = "## Conditional With-LSP addendum"
        marker_at = canonical.index(marker)
        contract = {
            "schema": "super-dolphin/cross-file-prompt-contract/v1",
            "task_prompt": self.config_data["prompt_template"],
            "task_prompt_sha256": hashlib.sha256(
                self.config_data["prompt_template"].encode()
            ).hexdigest(),
            "common_developer_instructions": canonical[:marker_at],
            "common_developer_instructions_sha256": hashlib.sha256(
                canonical[:marker_at].encode()
            ).hexdigest(),
            "with_lsp_addendum": canonical[marker_at:],
            "with_lsp_addendum_sha256": hashlib.sha256(
                canonical[marker_at:].encode()
            ).hexdigest(),
            "canonical_prompt_path": str(self.canonical_prompt),
            "canonical_prompt_sha256": self.config_data["canonical_lsp_prompt_sha256"],
            "allowed_arm_differences": ["system.with_lsp_addendum"],
        }
        explicit = dict(self.config_data)
        explicit["prompt_contract"] = contract
        prompt_binding = dict(explicit["identity_bindings"]["prompt"])
        prompt_binding["sha256"] = driver_module._prompt_binding_digest(contract)
        explicit["identity_bindings"] = {
            **explicit["identity_bindings"],
            "prompt": prompt_binding,
        }
        config = driver_module.UbuntuExecutionDriverConfig.from_dict(explicit)
        self.assertTrue(config.prompt_contract_explicit)
        self.assertEqual(
            config.prompt_contract["common_developer_instructions"], canonical[:marker_at]
        )
        self.assertEqual(
            config.prompt_contract["with_lsp_addendum"], canonical[marker_at:]
        )
        broken = dict(explicit)
        broken["prompt_contract"] = {**contract, "with_lsp_addendum": "drift"}
        with self.assertRaisesRegex(driver_module.UbuntuExecutionDriverConfigError, "addendum"):
            driver_module.UbuntuExecutionDriverConfig.from_dict(broken)

    def test_factory_creates_distinct_clean_worktrees_per_arm(self):
        config = driver_module.UbuntuExecutionDriverConfig.from_dict(self.config_data)
        factory = driver_module.UbuntuExecutionDriverFactory(config)
        no_driver = factory.for_arm("question", "no_lsp", {"arm_id": "no-lsp"})
        with_driver = factory.for_arm("question", "with_lsp", {"arm_id": "with-lsp"})
        self.assertNotEqual(no_driver._workspace, with_driver._workspace)
        self.assertEqual(no_driver._base_head, with_driver._base_head)
        self.assertIs(no_driver._grader_slots, with_driver._grader_slots)
        self.assertFalse(no_driver._git_output(("status", "--porcelain"), "status"))
        self.assertFalse(with_driver._git_output(("status", "--porcelain"), "status"))

    def test_exec_then_resume_uses_one_session_without_absolute_timeout(self):
        driver, fake = self._driver()
        first = driver.run_agent(self._context())
        first_identity = first["identity_envelope"]["bindings"]
        identity = {
            "question_id": first_identity["task"]["id"],
            "arm_id": "no-lsp",
            "mode": "no_lsp",
            "session_id": first_identity["session"]["id"],
            "thread_id": first_identity["thread"]["id"],
            "patch_hash": first_identity["patch"]["id"],
        }
        second = driver.run_agent(self._context(round_number=2, identity=identity))
        self.assertNotEqual(
            first["identity_envelope"]["bindings"]["patch"]["id"],
            second["identity_envelope"]["bindings"]["patch"]["id"],
        )
        self.assertIn("codex", fake.calls[0]["argv"])
        self.assertEqual(fake.calls[0]["argv"][3:5], ("codex", "exec"))
        self.assertIn("resume", fake.calls[1]["argv"])
        self.assertIn("session-no_lsp", fake.calls[1]["argv"])
        self.assertTrue(all("timeout" not in item for call in fake.calls for item in call["argv"]))

    def test_dynamic_signed_arm_evidence_usage_generation_and_tree_are_read(self):
        driver, fake = self._driver()
        result = driver.run_agent(self._context(mode="with_lsp"))
        patch_hash = result["identity_envelope"]["bindings"]["patch"]["id"]
        self.assertEqual(result["identity_envelope"]["bindings"]["final_tree"]["tree_sha"].__len__(), 40)
        self.assertEqual(result["server_generation"], "generation-01")
        self.assertEqual(result["usage"]["input_tokens"], 10)
        self.assertTrue(all(item["patch_hash"] == patch_hash for item in result["arm_evidence"]["receipts"]))
        self.assertEqual(result["identity_envelopes"]["arm_evidence"]["bindings"]["patch"]["id"], patch_hash)
        self.assertIn("signature", result["arm_evidence"]["receipts"][0])
        metrics = json.loads(
            pathlib.Path(fake.calls[-1]["env"]["SUPER_DOLPHIN_METRICS_PATH"]).read_text(
                encoding="utf-8"
            )
        )
        self.assertTrue(metrics["lsp_available"])
        self.assertTrue(metrics["adopted"])
        self.assertEqual(metrics["calls"], 3)
        self.assertEqual(metrics["tool"]["by_name"]["structure"], 1)
        self.assertEqual(metrics["tool"]["by_name"]["xref"], 1)
        self.assertEqual(metrics["tool"]["by_name"]["diagnostics"], 1)
        self.assertEqual(
            metrics["prompt"]["sections"]["system"]["sha256"],
            hashlib.sha256(self.canonical_prompt.read_bytes()).hexdigest(),
        )
        self.assertEqual(metrics["tokens"]["uncached_input_tokens"], 10)
        self.assertEqual(metrics["tokens"]["cached_input_tokens_90pct"], 0)

    def test_zero_call_receipt_log_is_explicit_canonical_empty_evidence(self):
        configured = dict(self.config_data)
        private_path = self.root / "executor-ed25519.pem"
        private_path.write_bytes(
            Ed25519PrivateKey.generate().private_bytes(
                encoding=serialization.Encoding.PEM,
                format=serialization.PrivateFormat.PKCS8,
                encryption_algorithm=serialization.NoEncryption(),
            )
        )
        configured["receipt_log_path_template"] = "receipt-{question_id}-{mode}-{round}.jsonl"
        configured["attestation_path_template"] = "attestation-{question_id}-{mode}-{round}.json"
        configured["prompt_policy_path_template"] = "policy-{question_id}-{mode}-{round}.json"
        configured["executor_attestation_signing_key_path"] = str(private_path)
        configured["executor_attestation_key_id"] = "test-executor"
        configured["attestation_replay_store_path"] = str(self.root / "attestation-replay")
        config = driver_module.UbuntuExecutionDriverConfig.from_dict(configured)
        fake = FakeSubprocess(self)
        driver = driver_module.UbuntuExecutionDriver(config, subprocess_adapter=fake)
        result = driver.run_agent(
            dataclasses.replace(self._context(mode="with_lsp"), run_id="test-run")
        )
        self.assertEqual(result["request"]["server_receipts"], [])
        self.assertEqual(result["request"]["task_id"], "django__django-16560")
        self.assertEqual(result["request"]["server_generation"], "generation-01")
        self.assertEqual(
            result["request"]["receipt_public_key"],
            configured["receipt_signing_key"],
        )

    def test_rate_limit_retry_resumes_the_real_session_from_partial_stdout(self):
        config = driver_module.UbuntuExecutionDriverConfig.from_dict(self.config_data)
        fake = RetrySubprocess(self)
        driver = driver_module.UbuntuExecutionDriver(config, subprocess_adapter=fake)
        with mock.patch.object(driver_module.time, "sleep") as sleep:
            result = driver.run_agent(self._context())
        self.assertNotIn("transport_error", result)
        self.assertEqual(len(fake.calls), 2)
        self.assertIn("resume", fake.calls[1]["argv"])
        self.assertIn("session-no_lsp", fake.calls[1]["argv"])
        self.assertNotIn("resume", fake.calls[0]["argv"])
        sleep.assert_called_once_with(10.0)

    def test_dirty_workspace_without_real_session_never_silently_starts_again(self):
        config = driver_module.UbuntuExecutionDriverConfig.from_dict(self.config_data)
        fake = RetrySubprocess(self, dirty_without_session=True)
        driver = driver_module.UbuntuExecutionDriver(config, subprocess_adapter=fake)
        with mock.patch.object(driver_module.time, "sleep"):
            with self.assertRaisesRegex(
                driver_module.UbuntuExecutionDriverProtocolError,
                "dirty workspace",
            ):
                driver.run_agent(self._context())
        self.assertEqual(len(fake.calls), 1)

    def test_rate_limit_retries_are_bounded(self):
        config = driver_module.UbuntuExecutionDriverConfig.from_dict(self.config_data)

        class AlwaysRateLimited:
            def __init__(self):
                self.calls = []

            def run(self, argv, *, cwd, env):
                self.calls.append(tuple(argv))
                return driver_module.SubprocessResult(
                    argv=tuple(argv),
                    returncode=1,
                    stdout=b"",
                    stderr=b"429 Too Many Requests",
                )

        fake = AlwaysRateLimited()
        driver = driver_module.UbuntuExecutionDriver(config, subprocess_adapter=fake)
        with mock.patch.object(driver_module.time, "sleep"):
            with self.assertRaises(driver_module.UbuntuExecutionDriverProtocolError):
                driver.run_agent(self._context())
        self.assertEqual(len(fake.calls), 3)

    def test_grader_saves_report_stdout_stderr_and_returns_structured_feedback(self):
        driver, fake = self._driver()
        agent = driver.run_agent(self._context())
        bindings = agent["identity_envelope"]["bindings"]
        request = RoundInput(
            round_number=1,
            arm_id="no-lsp",
            session_id=bindings["session"]["id"],
            thread_id=bindings["thread"]["id"],
            code_agent_id=bindings["agent"]["id"],
            patch_hash=bindings["patch"]["id"],
            changed_files=("src/shared.py",),
            trajectory=(),
            mode="no_lsp",
        )
        context = GraderContext(
            question_id="django__django-16560",
            mode="no_lsp",
            round_number=1,
            request=request,
            arm=agent,
            runner_snapshot={"infra_attempts": 0},
        )
        first_values = driver._grader_values(context, phase="grader", attempt=1)
        retry_values = driver._grader_values(context, phase="infra_retry", attempt=2)
        self.assertEqual(first_values["run_id"], first_values["request_id"])
        self.assertEqual(retry_values["run_id"], retry_values["request_id"])
        self.assertNotEqual(first_values["run_id"], retry_values["run_id"])
        result = driver.run_grader(context)
        self.assertIn("receipt", result["feedback"])
        self.assertIn(b"PASSED", result["official_report"])
        self.assertIn(b"grader-stdout", (pathlib.Path(self.config_data["artifact_dir"]) / "grader-django__django-16560-no_lsp-1-1.stdout").read_bytes())
        self.assertIn(b"grader-stderr", (pathlib.Path(self.config_data["artifact_dir"]) / "grader-django__django-16560-no_lsp-1-1.stderr").read_bytes())
        self.assertIn("official_stdout", result)
        self.assertIn("grader_receipt_digest", result)
        with self.assertRaises(driver_module.UbuntuExecutionDriverProtocolError):
            driver.run_grader(context)
        self.assertEqual(len(fake.calls), 2)

    def test_agent_and_grader_returncode_are_transport_classified(self):
        driver, _ = self._driver(fail_agent=True)
        result = driver.run_agent(self._context())
        self.assertIn("transport_error", result)
        driver, _ = self._driver(fail_grader=True)
        agent = driver.run_agent(self._context())
        bindings = agent["identity_envelope"]["bindings"]
        request = RoundInput(
            round_number=1,
            arm_id="no-lsp",
            session_id=bindings["session"]["id"],
            thread_id=bindings["thread"]["id"],
            code_agent_id=bindings["agent"]["id"],
            patch_hash=bindings["patch"]["id"],
            changed_files=("src/shared.py",),
            trajectory=(),
            mode="no_lsp",
        )
        result = driver.run_grader(
            GraderContext(
                question_id="django__django-16560",
                mode="no_lsp",
                round_number=1,
                request=request,
                arm=agent,
                runner_snapshot={"infra_attempts": 0},
            )
        )
        self.assertIn("transport_error", result)

    def test_signing_private_material_never_enters_command_environment_or_logs(self):
        driver, fake = self._driver()
        driver.run_agent(self._context())
        marker = "private-ed25519-marker"
        for call in fake.calls:
            self.assertNotIn(marker, " ".join(call["argv"]))
            self.assertNotIn(marker, "\n".join(call["env"].values()))
        for path in pathlib.Path(self.config_data["artifact_dir"]).glob("*"):
            if path.is_file():
                self.assertNotIn(marker.encode(), path.read_bytes())

    def test_fake_two_arm_execute_reaches_canonical_audit(self):
        base = self.builder.fixture()
        plan = {
            "schema": benchmark_runner.RUN_PLAN_SCHEMA,
            "run_id": "ubuntu-driver-e2e",
            "max_concurrency": 1,
            "pricing_manifest": base["pricing_manifest"],
            "questions": [{
                "question_id": base["question_id"],
                "discovery_shape": base["discovery_shape"],
                "arms": {
                    "no_lsp": {"mode": "no_lsp", "arm_id": "no-lsp"},
                    "with_lsp": {"mode": "with_lsp", "arm_id": "with-lsp"},
                },
            }],
        }
        config = driver_module.UbuntuExecutionDriverConfig.from_dict(self.config_data)
        fake = FakeSubprocess(self, stable_patch=True)
        driver = driver_module.UbuntuExecutionDriverFactory(
            config,
            subprocess_adapter=fake,
        )
        with tempfile.TemporaryDirectory() as state_dir:
            execution = benchmark_runner.execute_plan(plan, state_dir=state_dir, driver=driver)
        self.assertTrue(execution.scoreable, execution.error)
        self.assertIsNotNone(execution.audit_result)
        self.assertTrue(execution.audit_result.questions[0].passed)


if __name__ == "__main__":
    unittest.main()
