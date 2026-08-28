#!/usr/bin/env python3
"""Local Docker provider for the Cross-File Ubuntu benchmark.

This module is the provider-owned boundary between the benchmark driver and
Docker.  It validates every question image before creating a worktree, and it
builds an arm-local ``docker run`` command with ``--pull=never``.  The runner's
grader semaphore remains owned by :class:`UbuntuExecutionDriverFactory`; this
provider does not introduce another scheduling or receipt path.
"""

from __future__ import annotations

import dataclasses
import hashlib
import json
import os
import pathlib
import shutil
from collections.abc import Mapping, Sequence
from typing import Any, Protocol

try:
    from .cross_file_ubuntu_execution_driver import (
        LocalSubprocessAdapter,
        SubprocessAdapter,
        UbuntuExecutionDriverConfig,
        UbuntuExecutionDriverConfigError,
        QuestionMappedExecutionDriverFactory,
        _text,
        _IMAGE_DIGEST,
        _SAFE_ID,
    )
except ImportError:  # pragma: no cover - direct script invocation compatibility.
    from cross_file_ubuntu_execution_driver import (  # type: ignore[no-redef]
        LocalSubprocessAdapter,
        SubprocessAdapter,
        UbuntuExecutionDriverConfig,
        UbuntuExecutionDriverConfigError,
        QuestionMappedExecutionDriverFactory,
        _IMAGE_DIGEST,
        _SAFE_ID,
        _text,
    )
try:
    from .cross_file_signed_receipts import Ed25519Signer
except ImportError:  # pragma: no cover - direct script invocation compatibility.
    from cross_file_signed_receipts import Ed25519Signer  # type: ignore[no-redef]


IMAGE_MANIFEST_SCHEMA = "super-dolphin/cross-file-ubuntu-question-images/v1"
PROVIDER_SCHEMA = "super-dolphin/cross-file-ubuntu-local-provider/v1"
GO_RECEIPT_SCHEMA = "super-dolphin/mcp-server-receipt/v2"
GO_RECEIPT_SCHEMA_ENV = "SUPER_DOLPHIN_GO_RECEIPT_SCHEMA"
GO_RECEIPT_LOG_ENV = "SUPER_DOLPHIN_GO_RECEIPT_LOG_PATH"
MCP_RECEIPT_KEY_ENV = "MCP_LSP_RECEIPT_SIGNING_KEY_PATH"
# The historical representative set contains 20 questions.  Larger immutable
# validation image sets are supported; this value is only the production
# minimum used by the bundle/launcher entry points.
MIN_VALIDATION_QUESTION_COUNT = 20
# Backward-compatible export for callers that imported the old name.
EXPECTED_QUESTION_COUNT = MIN_VALIDATION_QUESTION_COUNT
REQUIRED_GRADER_SLOTS = 8
CODEX_AUTH_PATH_ENV = "SUPER_DOLPHIN_CODEX_AUTH_PATH"
CODEX_CONTAINER_PATH = "/opt/benchmark/codex-release/codex"
GRADER_CONTAINER_PATH = "/opt/benchmark/grader/python"
MCP_CONTAINER_PATH = "/opt/benchmark/mcp/mcp-lsp"
MCP_GATE_CONTAINER_PATH = "/opt/benchmark/gate/mcp-tools-gate.py"
PROXY_ENVIRONMENT = {
    "HTTP_PROXY": "http://127.0.0.1:2081",
    "HTTPS_PROXY": "http://127.0.0.1:2081",
    "ALL_PROXY": "http://127.0.0.1:2081",
}


class UbuntuLocalProviderError(UbuntuExecutionDriverConfigError):
    """A local image manifest or Docker preflight violates the provider contract."""


class DockerImageInspector(Protocol):
    """Small seam allowing image preflight tests without a Docker daemon."""

    def inspect(self, image: str) -> str:
        """Inspect one immutable image and return Docker's image ID."""
        raise NotImplementedError


@dataclasses.dataclass(frozen=True)
class DockerImagePreflight:
    """Result of the all-images-before-execution Docker preflight."""

    image_ids: Mapping[str, str]


class LocalDockerImageInspector:
    """Run only ``docker image inspect``; this class never pulls images."""

    def __init__(
        self,
        *,
        docker_command: Sequence[str] = ("docker",),
        cwd: pathlib.Path,
        subprocess_adapter: SubprocessAdapter | None = None,
        environment: Mapping[str, str] | None = None,
    ) -> None:
        if isinstance(docker_command, (str, bytes, bytearray)):
            raise UbuntuLocalProviderError("docker_command must be an argv array")
        command = tuple(_text(item, "docker_command item") for item in docker_command)
        if not command:
            raise UbuntuLocalProviderError("docker_command must be a non-empty argv array")
        if pathlib.Path(command[0]).name != "docker":
            raise UbuntuLocalProviderError("docker_command must invoke docker")
        self._docker_command = command
        self._cwd = cwd.resolve()
        if not self._cwd.is_dir():
            raise UbuntuLocalProviderError("Docker preflight cwd must be an existing directory")
        self._subprocess = subprocess_adapter or LocalSubprocessAdapter()
        self._environment = dict(os.environ)
        if environment:
            self._environment.update({
                _text(key, "Docker environment key"): _text(value, f"Docker environment[{key}]")
                for key, value in environment.items()
            })

    def inspect(self, image: str) -> str:
        """Inspect RepoDigests and require the requested digest to be present."""

        result = self._subprocess.run(
            (*self._docker_command, "image", "inspect", "--format", "{{json .RepoDigests}}", image),
            cwd=self._cwd,
            env=self._environment,
        )
        if result.returncode != 0:
            raise UbuntuLocalProviderError(
                f"Docker image preflight failed for {image}: exit {result.returncode}"
            )
        try:
            repo_digests = json.loads(result.stdout.decode("utf-8", errors="strict"))
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            raise UbuntuLocalProviderError(
                f"Docker image preflight returned invalid RepoDigests for {image}"
            ) from exc
        if not isinstance(repo_digests, list) or not all(isinstance(item, str) for item in repo_digests):
            raise UbuntuLocalProviderError(
                f"Docker image preflight returned invalid RepoDigests for {image}"
            )
        matches = image in repo_digests
        if image.startswith("sha256:"):
            matches = any(item.endswith("@" + image) for item in repo_digests)
        if not matches:
            raise UbuntuLocalProviderError(
                f"Docker image preflight RepoDigest mismatch for {image}"
            )
        return image


def _object(value: Any, label: str) -> Mapping[str, Any]:
    if not isinstance(value, Mapping):
        raise UbuntuLocalProviderError(f"{label} must be an object")
    return value


def _strict(value: Mapping[str, Any], allowed: set[str], label: str) -> None:
    unknown = sorted(str(key) for key in value if key not in allowed)
    if unknown:
        raise UbuntuLocalProviderError(f"{label} has unknown field(s): {', '.join(unknown)}")


def parse_question_image_manifest(value: Mapping[str, Any]) -> dict[str, str]:
    """Parse a strict validation image map with immutable references.

    The manifest is intentionally count-agnostic: the historical 20-question
    set and larger validation sets share the same immutable per-question
    mapping contract.
    """

    raw = _object(value, "question image manifest")
    _strict(raw, {"schema", "questions"}, "question image manifest")
    if raw.get("schema") != IMAGE_MANIFEST_SCHEMA:
        raise UbuntuLocalProviderError("unsupported question image manifest schema")
    questions = _object(raw.get("questions"), "question image manifest.questions")
    if not questions:
        raise UbuntuLocalProviderError("question image manifest must not be empty")
    result: dict[str, str] = {}
    for question_id, raw_image in questions.items():
        normalized_id = _text(question_id, "question image manifest question id")
        if _SAFE_ID.fullmatch(normalized_id) is None:
            raise UbuntuLocalProviderError(f"unsafe question id: {normalized_id}")
        if isinstance(raw_image, str):
            image = _text(raw_image, f"questions[{normalized_id}]")
        else:
            entry = _object(raw_image, f"questions[{normalized_id}]")
            _strict(entry, {"image"}, f"questions[{normalized_id}]")
            image = _text(entry.get("image"), f"questions[{normalized_id}].image")
        if _IMAGE_DIGEST.fullmatch(image) is None:
            raise UbuntuLocalProviderError(
                f"questions[{normalized_id}] image must be an immutable digest"
            )
        if normalized_id in result:
            raise UbuntuLocalProviderError(f"duplicate question id: {normalized_id}")
        result[normalized_id] = image
    return result


def load_question_image_manifest(path: str | pathlib.Path) -> dict[str, str]:
    """Read and validate a question image manifest from JSON."""

    candidate = pathlib.Path(path)
    try:
        value = json.loads(candidate.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise UbuntuLocalProviderError(f"read question image manifest {candidate}: {exc}") from exc
    return parse_question_image_manifest(_object(value, "question image manifest"))


def preflight_question_images(
    images: Mapping[str, str],
    *,
    inspector: DockerImageInspector,
) -> DockerImagePreflight:
    """Inspect every mapped image before any execution factory is materialized."""

    if not images:
        raise UbuntuLocalProviderError("image preflight requires at least one question")
    image_ids: dict[str, str] = {}
    first_error: UbuntuLocalProviderError | None = None
    for question_id, image in images.items():
        normalized_id = _text(question_id, "image preflight question id")
        normalized_image = _text(image, f"image preflight image[{normalized_id}]")
        if _IMAGE_DIGEST.fullmatch(normalized_image) is None:
            raise UbuntuLocalProviderError(f"image[{normalized_id}] is not immutable")
        try:
            image_ids[normalized_id] = _text(
                inspector.inspect(normalized_image), f"image id[{normalized_id}]"
            )
        except UbuntuLocalProviderError as exc:
            # Continue the preflight so every mapped image is checked before
            # one aggregate failure can reach factory construction.
            if first_error is None:
                first_error = exc
    if first_error is not None:
        raise first_error
    return DockerImagePreflight(image_ids=image_ids)


def _arm_token(question_id: str, mode: str, arm_id: str) -> str:
    digest = hashlib.sha256(f"{question_id}\0{mode}\0{arm_id}".encode("utf-8")).hexdigest()
    return digest[:24]


def _local_container_command(
    docker_command: Sequence[str],
    question_id: str,
    mode: str,
    arm_id: str,
    workspace: pathlib.Path,
    artifact_dir: pathlib.Path,
    codex_home: pathlib.Path,
    mcp_signing_key_path: pathlib.Path | None,
    codex_binary: pathlib.Path,
    grader_binary: pathlib.Path,
    mcp_binary: pathlib.Path,
    source_workspace: pathlib.Path,
    codex_release_dir: pathlib.Path,
    mcp_gate_script: pathlib.Path,
) -> tuple[str, ...]:
    """Build one arm's fully isolated Docker argv, including the no-pull fence."""

    if isinstance(docker_command, (str, bytes, bytearray)):
        raise UbuntuLocalProviderError("docker_command must be an argv array")
    if not docker_command or pathlib.Path(str(docker_command[0])).name != "docker":
        raise UbuntuLocalProviderError("docker_command must invoke docker")
    if mode not in {"no_lsp", "with_lsp"}:
        raise UbuntuLocalProviderError(f"unsupported arm mode: {mode!r}")
    token = _arm_token(question_id, mode, arm_id)
    name = f"sd-cross-file-{token}"
    mounts = (
        f"type=bind,src={workspace},dst={workspace}",
        f"type=bind,src={source_workspace},dst={source_workspace}",
        f"type=bind,src={artifact_dir},dst={artifact_dir}",
        f"type=bind,src={codex_home},dst={codex_home}",
        f"type=bind,src={codex_release_dir},dst=/opt/benchmark/codex-release,readonly",
    )
    if mode == "with_lsp":
        mounts += (
            f"type=bind,src={mcp_binary},dst={MCP_CONTAINER_PATH},readonly",
            f"type=bind,src={mcp_gate_script},dst={MCP_GATE_CONTAINER_PATH},readonly",
        )
    command = (
        *(str(item) for item in docker_command),
        "run",
        "--rm",
        "--init",
        "--pull=never",
        "--name",
        name,
        "--network=host",
        "--workdir",
        str(workspace),
        *(item for mount in mounts for item in ("--mount", mount)),
        "--env",
        f"CODEX_HOME={codex_home}",
        *(item for key, value in PROXY_ENVIRONMENT.items() for item in ("--env", f"{key}={value}")),
    )
    if mode == "with_lsp":
        lsp_roots = json.dumps([str(workspace)], separators=(",", ":"))
        if mcp_signing_key_path is None:
            raise UbuntuLocalProviderError(
                "with_lsp requires an explicit MCP receipt signing key path"
            )
        command += (
            "--mount",
            f"type=bind,src={mcp_signing_key_path},dst=/opt/benchmark/sidecar/mcp-server.key,readonly",
            "--env",
            f"{GO_RECEIPT_SCHEMA_ENV}={GO_RECEIPT_SCHEMA}",
            "--env",
            f"{GO_RECEIPT_LOG_ENV}={artifact_dir / 'mcp-receipts.jsonl'}",
            "--env",
            f"MCP_LSP_RECEIPT_LOG_PATH={artifact_dir / 'mcp-receipts.jsonl'}",
            "--env",
            "MCP_LSP_RECEIPT_INCLUDE_CANONICAL_IO=1",
            "--env",
            f"{MCP_RECEIPT_KEY_ENV}=/opt/benchmark/sidecar/mcp-server.key",
            "--env", "SUPER_DOLPHIN_RUNTIME_MODE=dev",
            "--env", f"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR={workspace}",
            "--env", "SUPER_DOLPHIN_DEPENDENCY_PROFILE=production",
            "--env", f"PROJECT_ROOT={workspace}",
            "--env", f"GO_AGENT_LSP_ROOT={workspace}",
            "--env", f"GO_AGENT_LSP_ROOTS={lsp_roots}",
        )
        return (
            *command,
            "{image}",
            "python3", MCP_GATE_CONTAINER_PATH,
            "--server", MCP_CONTAINER_PATH,
            "--cwd", str(workspace),
            "--evidence", str(artifact_dir / "mcp-tools-gate.json"),
            "--stderr", str(artifact_dir / "mcp-tools-gate.stderr"),
            "--timeout", "15",
            "--",
        )
    return (*command, "{image}")


class UbuntuLocalImageProvider:
    """Preflight and construct a question-mapped Ubuntu Docker driver factory."""

    def __init__(
        self,
        config: UbuntuExecutionDriverConfig | Mapping[str, Any],
        image_manifest: Mapping[str, Any],
        *,
        docker_command: Sequence[str] = ("docker",),
        subprocess_adapter: SubprocessAdapter | None = None,
        mcp_signing_key_path: str | pathlib.Path | None = None,
        materialize_workspaces: bool = True,
    ) -> None:
        config_map: dict[str, UbuntuExecutionDriverConfig]
        if isinstance(config, UbuntuExecutionDriverConfig):
            config_map = {"": config}
        else:
            raw_config = _object(config, "driver config")
            if "question_configs" in raw_config:
                _strict(raw_config, {"schema", "question_configs"}, "question driver map")
                if raw_config.get("schema") != "super-dolphin/cross-file-question-drivers/v1":
                    raise UbuntuLocalProviderError("unsupported question driver map schema")
                mapped = _object(raw_config.get("question_configs"), "question_configs")
                config_map = {
                    _text(question_id, "question_configs key"): UbuntuExecutionDriverConfig.from_dict(
                        _object(item, f"question_configs[{question_id}]")
                    )
                    for question_id, item in mapped.items()
                }
                if not config_map:
                    raise UbuntuLocalProviderError("question driver map must not be empty")
            else:
                config_map = {"": UbuntuExecutionDriverConfig.from_dict(raw_config)}
        self.config = next(iter(config_map.values()))
        if self.config.grader_slots != REQUIRED_GRADER_SLOTS:
            raise UbuntuLocalProviderError(
                f"Ubuntu local provider requires exactly {REQUIRED_GRADER_SLOTS} grader slots"
            )
        mcp_public = dict(self.config.receipt_signing_key)
        grader_public = self.config.grader_receipt_signing_key
        if not isinstance(grader_public, Mapping) or not grader_public:
            raise UbuntuLocalProviderError("grader receipt public binding is required")
        if dict(grader_public) == mcp_public:
            raise UbuntuLocalProviderError("MCP and grader receipt public bindings must be distinct")
        executor_path = self.config.executor_attestation_signing_key_path
        if executor_path is None or not executor_path.is_file():
            raise UbuntuLocalProviderError("executor signing key must be an existing host-side file")
        try:
            executor_signer = Ed25519Signer.from_file(
                executor_path,
                key_id=self.config.executor_attestation_key_id or "",
            )
        except Exception as exc:  # noqa: BLE001 - role-key boundary must fail closed.
            raise UbuntuLocalProviderError("executor signing key is invalid") from exc
        if executor_signer.public_key.to_dict() in (mcp_public, dict(grader_public)):
            raise UbuntuLocalProviderError("executor signing key has the wrong receipt role")
        self.images = parse_question_image_manifest(image_manifest)
        configured_mcp_key = mcp_signing_key_path or self.config.environment.get(MCP_RECEIPT_KEY_ENV)
        self._mcp_signing_key_path = (
            pathlib.Path(configured_mcp_key).resolve() if configured_mcp_key else None
        )
        if self._mcp_signing_key_path is not None:
            if not self._mcp_signing_key_path.is_absolute() or not self._mcp_signing_key_path.is_file():
                raise UbuntuLocalProviderError("MCP receipt signing key path must be an existing absolute file")
            try:
                mcp_signer = Ed25519Signer.from_file(
                    self._mcp_signing_key_path,
                    key_id=str(self.config.receipt_signing_key.get("key_id", "")),
                )
            except Exception as exc:  # noqa: BLE001 - role-key boundary must fail closed.
                raise UbuntuLocalProviderError("MCP receipt signing key is invalid") from exc
            if mcp_signer.public_key.to_dict() != dict(self.config.receipt_signing_key):
                raise UbuntuLocalProviderError("MCP receipt signing key does not match its public binding")
        try:
            codex_binary = pathlib.Path(self.config.codex_exec_command[0]).resolve()
            grader_binary = pathlib.Path(self.config.grader_command[0]).resolve()
            mcp_binary = pathlib.Path(self.config.environment["MCP_LSP_BINARY"]).resolve()
        except (KeyError, IndexError) as exc:
            raise UbuntuLocalProviderError("codex, grader and MCP binary paths are required") from exc
        for name, binary in (("codex", codex_binary), ("grader", grader_binary), ("MCP", mcp_binary)):
            if not binary.is_file() or not os.access(binary, os.X_OK):
                raise UbuntuLocalProviderError(f"{name} binary must be an executable host file")
        codex_release_dir = codex_binary.parent.parent
        if not (codex_release_dir / "codex-resources" / "bwrap").is_file():
            raise UbuntuLocalProviderError(
                "Codex release root must contain codex-resources/bwrap"
            )
        mcp_gate_script = pathlib.Path(__file__).resolve().parent / "cross_file_mcp_tools_gate.py"
        if not mcp_gate_script.is_file():
            raise UbuntuLocalProviderError("MCP tools gate script is missing")
        auth_value = self.config.environment.get(CODEX_AUTH_PATH_ENV)
        if not auth_value:
            raise UbuntuLocalProviderError(f"{CODEX_AUTH_PATH_ENV} is required")
        self._codex_auth_path = pathlib.Path(auth_value).resolve()
        if not self._codex_auth_path.is_file():
            raise UbuntuLocalProviderError("Codex auth path must be an existing host file")
        if set(config_map) != {""} and set(config_map) != set(self.images):
            raise UbuntuLocalProviderError("question driver map and image manifest must have the same question IDs")
        self._inspector = LocalDockerImageInspector(
            docker_command=docker_command,
            cwd=self.config.workspace,
            subprocess_adapter=subprocess_adapter,
        )
        self.preflight = preflight_question_images(self.images, inspector=self._inspector)
        configs = {
            question_id: dataclasses.replace(
                config_map.get(question_id, self.config),
                container_image=image,
                artifact_dir=(
                    config_map[question_id].artifact_dir
                    if question_id in config_map
                    else self.config.artifact_dir / "questions" / question_id
                ),
                codex_exec_command=(
                    CODEX_CONTAINER_PATH,
                    "exec",
                    "--dangerously-bypass-approvals-and-sandbox",
                ),
                codex_resume_command=(
                    CODEX_CONTAINER_PATH,
                    "exec",
                    "resume",
                    "--dangerously-bypass-approvals-and-sandbox",
                ),
                # Each arm owns a distinct artifact directory, so one stable
                # append-only receipt name is collision-free and matches the
                # path injected into both Docker and the Codex MCP config.
                receipt_log_path_template="mcp-receipts.jsonl",
            )
            for question_id, image in self.images.items()
        }
        source_workspaces: dict[str, pathlib.Path] = {}
        if any(item.grader_slots != REQUIRED_GRADER_SLOTS for item in configs.values()):
            raise UbuntuLocalProviderError("all question driver configs must declare exactly 8 grader slots")
        if materialize_workspaces:
            materialized_configs: dict[str, UbuntuExecutionDriverConfig] = {}
            for question_id, question_config in configs.items():
                target = self._materialize_workspace(question_id, question_config)
                source_workspaces[question_id] = target
                materialized_configs[question_id] = dataclasses.replace(
                    question_config,
                    workspace=target,
                )
            configs = materialized_configs
        command_factory = lambda question_id, mode, arm_id, workspace, artifact, home: _local_container_command(
            docker_command,
            question_id,
            mode,
            arm_id,
            workspace,
            artifact,
            home,
            self._mcp_signing_key_path,
            codex_binary,
            grader_binary,
            mcp_binary,
            source_workspaces.get(question_id) or workspace,
            codex_release_dir,
            mcp_gate_script,
        )
        provision_factory = lambda question_id, mode, arm_id, workspace, artifact, home: self._provision_arm(
            question_id, mode, arm_id, workspace, artifact, home
        )
        self.factory = QuestionMappedExecutionDriverFactory(
            configs,
            container_command_factory=command_factory,
            arm_provision_factory=provision_factory,
        )

    def for_arm(self, question_id: str, mode: str, arm: Mapping[str, Any]):
        """Delegate arm creation to the existing shared-slot factory."""

        return self.factory.for_arm(question_id, mode, arm)

    def _provision_arm(self, question_id, mode, arm_id, workspace, artifact_dir, codex_home):
        """Install arm-local auth and mode-specific Codex config before launch."""
        auth_target = codex_home / "auth.json"
        try:
            shutil.copyfile(self._codex_auth_path, auth_target)
            auth_target.chmod(0o600)
        except OSError as exc:
            raise UbuntuLocalProviderError("copy arm-local Codex auth failed") from exc
        if auth_target.stat().st_size == 0 or auth_target.stat().st_mode & 0o777 != 0o600:
            raise UbuntuLocalProviderError("arm-local Codex auth must be non-empty mode 0600")
        model_cache_source = self._codex_auth_path.parent / "models_cache.json"
        if not model_cache_source.is_file() or model_cache_source.stat().st_size == 0:
            raise UbuntuLocalProviderError("host Codex models_cache.json is required")
        model_cache_target = codex_home / "models_cache.json"
        try:
            shutil.copyfile(model_cache_source, model_cache_target)
            model_cache_target.chmod(0o600)
        except OSError as exc:
            raise UbuntuLocalProviderError("copy arm-local Codex model cache failed") from exc
        lines = [
            'model = "gpt-5.6-luna"',
            'model_reasoning_effort = "max"',
            'approval_policy = "never"',
            'sandbox_mode = "workspace-write"',
            '',
            '[features]',
            'enable_mcp_apps = false',
            '',
            '[memories]',
            'use_memories = false',
            'generate_memories = false',
            '',
            '[shell_environment_policy]',
            'inherit = "all"',
        ]
        if mode == "with_lsp":
            lsp_roots = json.dumps([str(workspace)], separators=(",", ":"))
            lines += [
                '',
                '[mcp_servers.mcp_lsp]',
                'enabled = true',
                'required = true',
                f'command = "{MCP_CONTAINER_PATH}"',
                'args = []',
                f'cwd = "{workspace}"',
                'startup_timeout_sec = 30',
                '[mcp_servers.mcp_lsp.env]',
                'MCP_LSP_RECEIPT_SCHEMA = "super-dolphin/mcp-server-receipt/v2"',
                'MCP_LSP_RECEIPT_INCLUDE_CANONICAL_IO = "1"',
                f'MCP_LSP_RECEIPT_LOG_PATH = "{artifact_dir / "mcp-receipts.jsonl"}"',
                'MCP_LSP_RECEIPT_SIGNING_KEY_PATH = "/opt/benchmark/sidecar/mcp-server.key"',
                'SUPER_DOLPHIN_RUNTIME_MODE = "dev"',
                f'SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR = "{workspace}"',
                'SUPER_DOLPHIN_DEPENDENCY_PROFILE = "production"',
                f'PROJECT_ROOT = "{workspace}"',
                f'GO_AGENT_LSP_ROOT = "{workspace}"',
                f"GO_AGENT_LSP_ROOTS = '{lsp_roots}'",
            ]
        config_target = codex_home / "config.toml"
        try:
            config_target.write_text("\n".join(lines) + "\n", encoding="utf-8")
            config_target.chmod(0o600)
        except OSError as exc:
            raise UbuntuLocalProviderError("write arm-local Codex config failed") from exc
        if config_target.stat().st_mode & 0o777 != 0o600:
            raise UbuntuLocalProviderError("arm-local Codex config must be mode 0600")

    def _materialize_workspace(
        self, question_id: str, config: UbuntuExecutionDriverConfig
    ) -> pathlib.Path:
        """Copy /testbed from an immutable image before any arm can execute."""

        target = (config.artifact_dir / "question-workspace").resolve()
        target.parent.mkdir(parents=True, exist_ok=True)
        base = _object(config.identity_bindings.get("base"), "identity_bindings.base")
        expected_head = _text(base.get("commit_sha"), "identity_bindings.base.commit_sha")
        expected_tree = _text(base.get("tree_sha"), "identity_bindings.base.tree_sha")
        if target.exists():
            if not target.is_dir():
                raise UbuntuLocalProviderError(f"materialized workspace is not a directory: {target}")
            self._verify_workspace(target, expected_head, expected_tree, question_id)
            return target
        token = _arm_token(question_id, "materialize", question_id)
        container = f"sd-cross-file-materialize-{token}"
        image = config.container_image
        created = False
        try:
            self._docker_command(("create", "--pull=never", "--name", container, image), config.workspace)
            created = True
            self._docker_command(("cp", f"{container}:/testbed/.", str(target)), config.workspace)
        finally:
            if created:
                self._docker_command(("rm", container), config.workspace)
        self._verify_workspace(target, expected_head, expected_tree, question_id)
        return target

    def _docker_command(self, args: Sequence[str], cwd: pathlib.Path) -> None:
        result = self._inspector._subprocess.run(  # noqa: SLF001 - provider-owned Docker seam
            (*self._inspector._docker_command, *args),  # noqa: SLF001
            cwd=cwd.resolve(),
            env=self._inspector._environment,  # noqa: SLF001
        )
        if result.returncode != 0:
            raise UbuntuLocalProviderError(
                f"Docker materialization command failed: {args[0]}"
            )

    @staticmethod
    def _verify_workspace(
        workspace: pathlib.Path, expected_head: str, expected_tree: str, question_id: str
    ) -> None:
        try:
            head = __import__("subprocess").run(
                ("git", "-C", str(workspace), "rev-parse", "HEAD"),
                capture_output=True, text=True, check=False,
            )
            tree = __import__("subprocess").run(
                ("git", "-C", str(workspace), "rev-parse", "HEAD^{tree}"),
                capture_output=True, text=True, check=False,
            )
        except OSError as exc:
            raise UbuntuLocalProviderError(f"verify materialized workspace failed for {question_id}") from exc
        if head.returncode != 0 or tree.returncode != 0:
            raise UbuntuLocalProviderError(f"materialized workspace is not a Git repository: {question_id}")
        if head.stdout.strip() != expected_head or tree.stdout.strip() != expected_tree:
            raise UbuntuLocalProviderError(f"materialized workspace base identity mismatch: {question_id}")


def load_local_image_provider(
    config_path: str | pathlib.Path,
    manifest_path: str | pathlib.Path,
    *,
    docker_command: Sequence[str] = ("docker",),
    subprocess_adapter: SubprocessAdapter | None = None,
) -> UbuntuLocalImageProvider:
    """Load explicit files and perform the complete image preflight."""

    config_candidate = pathlib.Path(config_path)
    try:
        raw_config = json.loads(config_candidate.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise UbuntuLocalProviderError(f"read driver config {config_candidate}: {exc}") from exc
    return UbuntuLocalImageProvider(
        _object(raw_config, "driver config"),
        {
            "schema": IMAGE_MANIFEST_SCHEMA,
            "questions": load_question_image_manifest(manifest_path),
        },
        docker_command=docker_command,
        subprocess_adapter=subprocess_adapter,
    )


def build_local_provider_from_env() -> UbuntuLocalImageProvider:
    """Build the provider from two explicit environment paths."""

    config_path = os.environ.get("SUPER_DOLPHIN_CROSS_FILE_DRIVER_CONFIG")
    manifest_path = os.environ.get("SUPER_DOLPHIN_CROSS_FILE_IMAGE_MANIFEST")
    if not config_path or not manifest_path:
        raise UbuntuLocalProviderError(
            "SUPER_DOLPHIN_CROSS_FILE_DRIVER_CONFIG and "
            "SUPER_DOLPHIN_CROSS_FILE_IMAGE_MANIFEST are required"
        )
    return load_local_image_provider(config_path, manifest_path)


__all__ = [
    "DockerImageInspector",
    "DockerImagePreflight",
    "EXPECTED_QUESTION_COUNT",
    "MIN_VALIDATION_QUESTION_COUNT",
    "REQUIRED_GRADER_SLOTS",
    "GO_RECEIPT_LOG_ENV",
    "GO_RECEIPT_SCHEMA",
    "GO_RECEIPT_SCHEMA_ENV",
    "MCP_RECEIPT_KEY_ENV",
    "IMAGE_MANIFEST_SCHEMA",
    "LocalDockerImageInspector",
    "PROVIDER_SCHEMA",
    "UbuntuLocalImageProvider",
    "UbuntuLocalProviderError",
    "load_question_image_manifest",
    "load_local_image_provider",
    "build_local_provider_from_env",
    "parse_question_image_manifest",
    "preflight_question_images",
]
