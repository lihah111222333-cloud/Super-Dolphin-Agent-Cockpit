#!/usr/bin/env python3
"""Black-box contract tests for the issue-ledger CLI.

Every test creates its own workbook and, where needed, its own Git checkout.
No project workbook or repository state is read or changed.
"""
from __future__ import annotations

import hashlib
import importlib
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from typing import Any


load_workbook: Any = importlib.import_module("openpyxl").load_workbook


SCRIPT = Path(__file__).with_name("issue_ledger.py")


class IssueLedgerCliTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.workbook = self.root / "ledger.xlsx"
        self.keys = 0

    def tearDown(self) -> None:
        self.temp.cleanup()

    def key(self, label: str) -> str:
        self.keys += 1
        return f"{label}-{self.keys}"

    def invoke(self, *args: str, expect: int = 0) -> dict:
        command = [sys.executable, str(SCRIPT), "--workbook", str(self.workbook), *args]
        completed = subprocess.run(command, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.assertEqual(completed.returncode, expect, completed.stderr + completed.stdout)
        try:
            data = json.loads(completed.stdout)
        except json.JSONDecodeError as exc:  # pragma: no cover - failure diagnostic
            self.fail(f"CLI did not return JSON: {completed.stdout!r}; {exc}")
        self.assertEqual(data["ok"], expect == 0, data)
        return data

    def fails(self, *args: str) -> dict:
        return self.invoke(*args, expect=2)

    def digest(self) -> str:
        return hashlib.sha256(self.workbook.read_bytes()).hexdigest()

    def init(self, repositories: list[dict] | None = None) -> None:
        payload: dict[str, object] = {"project_id": "proj", "name": "isolated test"}
        if repositories is not None:
            payload["repositories"] = repositories
        self.invoke("ledger", "init", "--payload-json", json.dumps(payload))

    def report(self, title: str = "Crash on launch", key: str | None = None) -> dict:
        result = self.invoke(
            "issue", "report", "--title", title, "--description", "reproducible symptom",
            "--severity", "HIGH", "--actor", "tester", "--idempotency-key", key or self.key("report"),
        )
        return result["result"]["issue"]

    def mutate(self, command: str, issue: dict, payload: dict | None = None, *, key: str | None = None) -> dict:
        result = self.invoke(
            "issue", command, "--issue-id", issue["issue_id"], "--expected-version", str(issue["version"]),
            "--actor", "tester", "--idempotency-key", key or self.key(command),
            "--payload-json", json.dumps(payload or {}),
        )
        return result["result"]["issue"]

    @staticmethod
    def repository(repo_id: str, url: str) -> dict:
        return {
            "repo_id": repo_id, "role": "source", "name": repo_id,
            "canonical_url": url, "default_branch": "main", "active": True,
        }

    def ready_to_verify(self) -> dict:
        issue = self.report()
        issue = self.mutate("record-history-search", issue, {"query": "launch"})
        issue = self.mutate("confirm", issue)
        issue = self.mutate("start-investigation", issue)
        issue = self.mutate("authorize-fix", issue, {"authorized_by": "owner", "authorization_scope": "launch only"})
        issue = self.mutate("start-fix", issue)
        return issue

    def create_git_repository(self) -> tuple[Path, str, str]:
        repo = self.root / "checkout"
        repo.mkdir()
        url = "https://example.test/acme/core.git"
        for command in (("init",), ("config", "user.email", "tester@example.test"), ("config", "user.name", "Tester"), ("remote", "add", "origin", url)):
            subprocess.run(["git", "-C", str(repo), *command], check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        (repo / "tracked.txt").write_text("evidence\n", encoding="utf-8")
        subprocess.run(["git", "-C", str(repo), "add", "tracked.txt"], check=True)
        subprocess.run(["git", "-C", str(repo), "commit", "-m", "test evidence"], check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        commit = subprocess.run(["git", "-C", str(repo), "rev-parse", "HEAD"], check=True, text=True, stdout=subprocess.PIPE).stdout.strip()
        return repo, url, commit

    def register_evidence(self, issue: dict, repo: Path, commit: str, evidence_type: str) -> dict:
        result = self.invoke(
            "--repo-path", f"core={repo}", "evidence", "register", "--issue-id", issue["issue_id"],
            "--evidence-type", evidence_type, "--repo-id", "core", "--commit", commit,
            "--relative-path", "tracked.txt", "--description", evidence_type, "--actor", "tester",
            "--expected-version", str(issue["version"]), "--idempotency-key", self.key(evidence_type),
        )
        return result["result"]["issue"]

    def test_init_validate_and_project_info(self) -> None:
        self.init()
        validation = self.invoke("ledger", "validate")["result"]
        self.assertTrue(validation["valid"])
        self.assertEqual(validation["counts"]["Project"], 1)
        self.assertEqual(self.invoke("ledger", "project-info")["result"]["project"]["project_id"], "proj")

    def test_report_idempotency_and_expected_version_conflict_are_atomic(self) -> None:
        self.init()
        first = self.report(key="same-report")
        again = self.report(key="same-report")
        self.assertEqual(first["issue_id"], again["issue_id"])
        history = self.invoke("issue", "history", "--issue-id", first["issue_id"])["result"]
        self.assertEqual(len(history), 1)
        before = self.digest()
        failure = self.fails(
            "issue", "confirm", "--issue-id", first["issue_id"], "--expected-version", "999",
            "--actor", "tester", "--idempotency-key", self.key("conflict"), "--payload-json", "{}",
        )
        self.assertIn("expected_version conflict", failure["error"])
        self.assertEqual(self.digest(), before)

    def test_state_flow_requires_authorization_and_complete_fix_commit(self) -> None:
        repo, url, commit = self.create_git_repository()
        self.init([self.repository("core", url)])
        issue = self.report()
        before = self.digest()
        denied = self.fails(
            "issue", "start-fix", "--issue-id", issue["issue_id"], "--expected-version", "1",
            "--actor", "tester", "--idempotency-key", self.key("unauthorized"), "--payload-json", "{}",
        )
        self.assertIn("authorization", denied["error"])
        self.assertEqual(self.digest(), before)
        issue = self.mutate("record-history-search", issue, {"query": "launch"})
        issue = self.mutate("confirm", issue)
        issue = self.mutate("start-investigation", issue)
        issue = self.mutate("authorize-fix", issue, {"authorized_by": "owner", "authorization_scope": "launch only"})
        issue = self.mutate("start-fix", issue)
        missing = self.fails(
            "issue", "complete-fix", "--issue-id", issue["issue_id"], "--expected-version", str(issue["version"]),
            "--actor", "tester", "--idempotency-key", self.key("missing-commit"), "--payload-json", "{}",
        )
        self.assertIn("FIX_COMMIT", missing["error"])
        issue = self.register_evidence(issue, repo, commit, "FIX_COMMIT")
        issue = self.mutate("complete-fix", issue)
        self.assertEqual(issue["status"], "READY_TO_VERIFY")

    def test_verification_pass_closes_and_failure_reopens(self) -> None:
        repo, url, commit = self.create_git_repository()
        self.init([self.repository("core", url)])
        issue = self.ready_to_verify()
        issue = self.register_evidence(issue, repo, commit, "FIX_COMMIT")
        issue = self.mutate("complete-fix", issue)
        issue = self.register_evidence(issue, repo, commit, "VERIFICATION")
        closed = self.mutate("record-verification", issue, {"verification_result": "PASS", "resolution": "verified"})
        self.assertEqual(closed["status"], "CLOSED")
        reopened = self.mutate("reopen", closed, {"reason": "new reproduction"})
        self.assertEqual(reopened["status"], "REOPENED")
        investigating = self.mutate("start-investigation", reopened)
        authorized = self.mutate("authorize-fix", investigating, {"authorized_by": "owner", "authorization_scope": "retry"})
        fixing = self.mutate("start-fix", authorized)
        fixing = self.register_evidence(fixing, repo, commit, "FIX_COMMIT")
        verify = self.mutate("complete-fix", fixing)
        verify = self.register_evidence(verify, repo, commit, "VERIFICATION")
        failed = self.mutate("record-verification", verify, {"verification_result": "FAIL", "resolution": "still broken"})
        self.assertEqual(failed["status"], "REOPENED")

    def test_invalid_foreign_key_and_transition_fail_without_changing_workbook(self) -> None:
        self.init()
        issue = self.report()
        before = self.digest()
        bad_transition = self.fails(
            "issue", "start-investigation", "--issue-id", issue["issue_id"], "--expected-version", "1",
            "--actor", "tester", "--idempotency-key", self.key("bad-transition"), "--payload-json", "{}",
        )
        self.assertIn("invalid state transition", bad_transition["error"])
        self.assertEqual(self.digest(), before)
        bad_fk = self.fails(
            "issue", "relate", "--issue-id", issue["issue_id"], "--target-issue-id", "does-not-exist",
            "--relation-type", "DUPLICATE_OF", "--expected-version", "1", "--actor", "tester",
            "--idempotency-key", self.key("bad-fk"), "--payload-json", "{}",
        )
        self.assertIn("found 0", bad_fk["error"])
        self.assertEqual(self.digest(), before)

    def test_history_search_and_duplicate_regression_relations(self) -> None:
        self.init()
        first, duplicate, regression = self.report("renderer crash"), self.report("same renderer crash"), self.report("renderer regression")
        found = self.invoke("issue", "search", "--query", "renderer")["result"]
        self.assertEqual({row["issue_id"] for row in found}, {first["issue_id"], duplicate["issue_id"], regression["issue_id"]})
        first = self.mutate("record-history-search", first, {"query": "renderer", "conclusion": "related reports found"})
        first = self.invoke(
            "issue", "relate", "--issue-id", first["issue_id"], "--target-issue-id", duplicate["issue_id"],
            "--relation-type", "DUPLICATE_OF", "--expected-version", str(first["version"]), "--actor", "tester",
            "--idempotency-key", self.key("duplicate"), "--payload-json", "{}",
        )["result"]["issue"]
        self.invoke(
            "issue", "relate", "--issue-id", first["issue_id"], "--target-issue-id", regression["issue_id"],
            "--relation-type", "REGRESSION_OF", "--expected-version", str(first["version"]), "--actor", "tester",
            "--idempotency-key", self.key("regression"), "--payload-json", "{}",
        )
        history = self.invoke("issue", "history", "--issue-id", first["issue_id"])["result"]
        self.assertGreaterEqual(len(history), 3)
        relations = list(load_workbook(self.workbook, data_only=True)["IssueRelations"].values)[1:]
        self.assertEqual({str(row[3]) for row in relations}, {"DUPLICATE_OF", "REGRESSION_OF"})

    def test_git_evidence_checks_commit_path_and_never_persists_checkout(self) -> None:
        repo, url, commit = self.create_git_repository()
        self.init([self.repository("core", url)])
        issue = self.report()
        before = self.digest()
        self.fails("--repo-path", f"core={repo}", "evidence", "register", "--issue-id", issue["issue_id"], "--evidence-type", "FIX_COMMIT", "--repo-id", "core", "--commit", "0" * 40, "--relative-path", "tracked.txt", "--description", "bad", "--actor", "tester", "--expected-version", "1", "--idempotency-key", self.key("missing-commit"))
        self.assertEqual(self.digest(), before)
        self.fails("--repo-path", f"core={repo}", "evidence", "register", "--issue-id", issue["issue_id"], "--evidence-type", "FIX_COMMIT", "--repo-id", "core", "--commit", commit, "--relative-path", "missing.txt", "--description", "bad", "--actor", "tester", "--expected-version", "1", "--idempotency-key", self.key("missing-path"))
        self.assertEqual(self.digest(), before)
        evidence = self.invoke("--repo-path", f"core={repo}", "evidence", "register", "--issue-id", issue["issue_id"], "--evidence-type", "FIX_COMMIT", "--repo-id", "core", "--commit", commit[:12], "--relative-path", "tracked.txt", "--description", "verified", "--actor", "tester", "--expected-version", "1", "--idempotency-key", self.key("evidence"))["result"]["evidence"]
        self.assertEqual(evidence["commit_hash"], commit)
        all_values = "\n".join(str(value) for ws in load_workbook(self.workbook, data_only=True).worksheets for row in ws.iter_rows(values_only=True) for value in row if value is not None)
        self.assertNotIn(str(repo), all_values)

    def test_validate_rejects_manual_event_tampering(self) -> None:
        self.init()
        self.report()
        workbook = load_workbook(self.workbook)
        events: Any = workbook["Events"]
        events.cell(2, 6).value = '{"tampered":true}'
        workbook.save(self.workbook)
        failure = self.fails("ledger", "validate")
        self.assertIn("event hash", failure["error"])


if __name__ == "__main__":
    unittest.main(verbosity=2)
