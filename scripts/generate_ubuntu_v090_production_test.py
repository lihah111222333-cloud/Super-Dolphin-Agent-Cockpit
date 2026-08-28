import base64
import hashlib
import pathlib
import tempfile
import unittest
import subprocess
import sys

from scripts.cross_file_identity_envelope import canonical_json, sha256_text
from scripts.cross_file_signed_receipts import Ed25519Signer
from scripts.generate_ubuntu_v090_production import generate_bundle


def _key():
    signer = Ed25519Signer.generate("fixture")
    return signer, signer.public_key.to_dict()


def _manifest():
    return {
        "schema": "episode-manifest/v1",
        "questions": [
            {
                "question_id": f"repo__project-{index:02d}",
                "repo": "repo/project",
                "issue": {"title": f"Issue {index}", "body": f"Real issue body {index}"},
                "episode": {
                    "base_commit": (f"{index + 1:040x}"),
                    "base_tree": (f"{index + 101:040x}"),
                    "repo_digest": f"repo/project@sha256:{index % 10}{index % 10}{index % 10}{index % 10}" + "0" * 60,
                },
            }
            for index in range(20)
        ],
    }


def _canonical_prompt(path):
    payload = (
        "Produce one complete candidate production patch and validate it with public tests.\n\n"
        "## Native baseline\n\n"
        "- Read and search with native tools before editing.\n"
        "- Keep each arm's task input identical.\n\n"
        "## Conditional With-LSP addendum\n\n"
        "Inject this section only when all three semantic tools are exposed.\n"
    ).encode("utf-8")
    path.write_bytes(payload)
    return hashlib.sha256(payload).hexdigest()


class GenerateUbuntuV090ProductionTest(unittest.TestCase):
    def test_cli_exposes_explicit_remote_run_inputs(self):
        source = pathlib.Path(__file__).with_name("generate_ubuntu_v090_production.py")
        completed = subprocess.run(
            [sys.executable, str(source), "--help"],
            text=True,
            capture_output=True,
            check=False,
        )
        self.assertEqual(completed.returncode, 0)
        for option in ("--manifest", "--repo-digests", "--mcp-private-key-path", "--executor-private-key-path", "--grader-private-key-path", "--output"):
            self.assertIn(option, completed.stdout)
        self.assertNotIn("private_bytes", source.read_text(encoding="utf-8"))

    def test_generates_twenty_configs_forty_arms_and_real_identity_inputs(self):
        mcp_signer, mcp_public = _key()
        executor_signer, executor_public = _key()
        grader_signer, grader_public = _key()
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            mcp_key = root / "mcp.key"
            executor_key = root / "executor.key"
            grader_key = root / "grader.key"
            for path, signer in ((mcp_key, mcp_signer), (executor_key, executor_signer), (grader_key, grader_signer)):
                path.write_bytes(base64.b64encode(signer._private_key.private_bytes_raw()))  # noqa: SLF001
            manifest = _manifest()
            images = {item["question_id"]: item["episode"]["repo_digest"] for item in manifest["questions"]}
            canonical_prompt_path = root / "shared-developer-instructions.md"
            canonical_prompt_digest = _canonical_prompt(canonical_prompt_path)
            bundle = generate_bundle(
                manifest,
                images,
                prompt_sha256=canonical_prompt_digest,
                mcp_public_key=mcp_public,
                executor_public_key=executor_public,
                grader_public_key=grader_public,
                mcp_private_key_path=str(mcp_key),
                executor_private_key_path=str(executor_key),
                grader_private_key_path=str(grader_key),
                mcp_binary="/opt/mcp-lsp",
                codex_binary="/opt/codex",
                codex_auth_path="/root/.codex/auth.json",
                grader_binary="/opt/swebench",
                grader_dataset_path="/data/verified.json",
                workspace=str(root / "workspace"),
                artifact_dir=str(root / "artifacts"),
                canonical_prompt_path=str(canonical_prompt_path),
                dataset_id="dataset-v090",
                toolchain_id="toolchain-v090",
                model_id="model-v090",
                pricing_id="pricing-v090",
                scoring_id="scoring-v090",
            )
        self.assertEqual(len(bundle["question_configs"]), 20)
        self.assertEqual(bundle["plan"]["max_concurrency"], 40)
        self.assertEqual(sum(len(q["arms"]) for q in bundle["plan"]["questions"]), 40)
        first = bundle["question_configs"]["repo__project-00"]
        identity = first["identity_bindings"]
        self.assertEqual(identity["base"]["commit_sha"], "0" * 39 + "1")
        self.assertEqual(
            first["grader_command"][0:4],
            ["/opt/swebench", "-m", "swebench.harness.run_evaluation", "--dataset_name"],
        )
        self.assertIn("{instance_ids}", first["grader_command"])
        self.assertIn("{predictions_path}", first["grader_command"])
        self.assertNotIn("run", first["grader_command"])
        self.assertEqual(
            first["environment"]["SUPER_DOLPHIN_GRADER_RECEIPT_SIGNING_KEY_PATH"],
            str(grader_key),
        )
        self.assertNotIn(base64.b64encode(executor_signer._private_key.private_bytes_raw()).decode(), repr(bundle))  # noqa: SLF001
        self.assertNotIn(base64.b64encode(grader_signer._private_key.private_bytes_raw()).decode(), repr(bundle))  # noqa: SLF001

        metadata = bundle["prompt_contract"]
        self.assertEqual(metadata["schema"], "super-dolphin/cross-file-prompt-contract/v1")
        self.assertEqual(metadata["allowed_arm_differences"], ["system.with_lsp_addendum"])
        self.assertEqual(len(metadata["tasks"]), 20)
        configs = bundle["question_configs"]
        common_hashes = {
            config["prompt_contract"]["common_developer_instructions_sha256"]
            for config in configs.values()
        }
        addendum_hashes = {
            config["prompt_contract"]["with_lsp_addendum_sha256"]
            for config in configs.values()
        }
        canonical_hashes = {
            config["prompt_contract"]["canonical_prompt_sha256"]
            for config in configs.values()
        }
        self.assertEqual(len(common_hashes), 1)
        self.assertEqual(len(addendum_hashes), 1)
        self.assertEqual(len(canonical_hashes), 1)
        self.assertEqual(
            identity["prompt"]["sha256"],
            sha256_text(canonical_json({
                "task_prompt_sha256": "sha256:" + configs["repo__project-00"]["prompt_contract"]["task_prompt_sha256"],
                "common_developer_instructions_sha256": "sha256:" + next(iter(common_hashes)),
                "with_lsp_addendum_sha256": "sha256:" + next(iter(addendum_hashes)),
                "canonical_prompt_sha256": "sha256:" + next(iter(canonical_hashes)),
            })),
        )
        for question_id, config in configs.items():
            contract = config["prompt_contract"]
            self.assertEqual(contract["task_prompt"], config["prompt_template"])
            self.assertEqual(
                contract["task_prompt_sha256"],
                hashlib.sha256(config["prompt_template"].encode("utf-8")).hexdigest(),
            )
            self.assertEqual(metadata["tasks"][question_id], {
                "task_prompt": contract["task_prompt"],
                "task_prompt_sha256": contract["task_prompt_sha256"],
                "allowed_arm_differences": ["system.with_lsp_addendum"],
            })

    def test_rejects_missing_issue_and_wrong_question_count(self):
        _, mcp = _key(); _, executor = _key(); _, grader = _key()
        manifest = _manifest(); manifest["questions"] = manifest["questions"][:19]
        with tempfile.TemporaryDirectory() as directory:
            paths = [str(pathlib.Path(directory) / name) for name in ("mcp", "executor", "grader")]
            with self.assertRaises(Exception):
                generate_bundle(
                    manifest, {}, prompt_sha256="a" * 64, mcp_public_key=mcp, executor_public_key=executor, grader_public_key=grader,
                    mcp_private_key_path=paths[0], executor_private_key_path=paths[1], grader_private_key_path=paths[2],
                    mcp_binary="/mcp", codex_binary="/codex", codex_auth_path="/root/.codex/auth.json", grader_binary="/grader", grader_dataset_path="/data/verified.json", workspace="/workspace", artifact_dir="/artifacts", canonical_prompt_path="/prompt", dataset_id="d", toolchain_id="t", model_id="m", pricing_id="p", scoring_id="s",
                )

    def test_rejects_canonical_prompt_hash_drift_before_bundle_creation(self):
        _, mcp = _key(); _, executor = _key(); _, grader = _key()
        manifest = _manifest()
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            canonical = root / "shared-developer-instructions.md"
            digest = _canonical_prompt(canonical)
            canonical.write_bytes(canonical.read_bytes() + b"drift\n")
            with self.assertRaisesRegex(Exception, "prompt contract rejected"):
                generate_bundle(
                    manifest,
                    {item["question_id"]: item["episode"]["repo_digest"] for item in manifest["questions"]},
                    prompt_sha256=digest,
                    mcp_public_key=mcp,
                    executor_public_key=executor,
                    grader_public_key=grader,
                    mcp_private_key_path=str(root / "mcp"),
                    executor_private_key_path=str(root / "executor"),
                    grader_private_key_path=str(root / "grader"),
                    mcp_binary="/mcp",
                    codex_binary="/codex",
                    codex_auth_path="/root/.codex/auth.json",
                    grader_binary="/grader",
                    grader_dataset_path="/data/verified.json",
                    workspace="/workspace",
                    artifact_dir="/artifacts",
                    canonical_prompt_path=str(canonical),
                    dataset_id="d",
                    toolchain_id="t",
                    model_id="m",
                    pricing_id="p",
                    scoring_id="s",
                )


if __name__ == "__main__":
    unittest.main()
