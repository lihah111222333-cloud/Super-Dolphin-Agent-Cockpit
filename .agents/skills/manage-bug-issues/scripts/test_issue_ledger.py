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

    @staticmethod
    def rehash_event(workbook: Any, row: int) -> None:
        events: Any = workbook["Events"]
        headers = [cell.value for cell in events[1]]
        columns = {name: index + 1 for index, name in enumerate(headers)}
        event = {
            name: "" if events.cell(row, column).value is None else events.cell(row, column).value
            for name, column in columns.items()
        }
        body = {key: value for key, value in event.items() if key != "event_hash"}
        events.cell(row, columns["event_hash"]).value = hashlib.sha256(
            json.dumps(body, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
        ).hexdigest()

    @staticmethod
    def set_schema_version(workbook: Any, version: int) -> None:
        project: Any = workbook["Project"]
        headers = [cell.value for cell in project[1]]
        columns = {name: index + 1 for index, name in enumerate(headers)}
        project.cell(2, columns["version"]).value = version

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
        project = self.invoke("ledger", "project-info")["result"]["project"]
        self.assertEqual(project["project_id"], "proj")
        self.assertEqual(project["version"], 2)

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

    def test_idempotency_key_is_bound_to_the_exact_mutation_request(self) -> None:
        self.init()
        first = self.report(key="bound-report")
        before = self.digest()
        report_conflict = self.fails(
            "issue", "report", "--title", "different report", "--description", "reproducible symptom",
            "--severity", "HIGH", "--actor", "tester", "--idempotency-key", "bound-report",
        )
        self.assertIn("idempotency_key conflict", report_conflict["error"])
        self.assertEqual(self.digest(), before)

        second = self.report("second issue")
        third = self.report("third issue")
        relation_key = self.key("bound-relation")
        relation_args = (
            "issue", "relate", "--issue-id", first["issue_id"], "--target-issue-id", second["issue_id"],
            "--relation-type", "RELATED_TO", "--expected-version", str(first["version"]), "--actor", "tester",
            "--idempotency-key", relation_key, "--payload-json", "{}",
        )
        relation_result = self.invoke(*relation_args)["result"]
        after_relation = self.digest()
        retried = self.invoke(*relation_args)["result"]
        self.assertTrue(retried["idempotent"])
        self.assertEqual(retried["relation"], relation_result["relation"])
        self.assertEqual(self.digest(), after_relation)

        changed_target = self.fails(
            "issue", "relate", "--issue-id", first["issue_id"], "--target-issue-id", third["issue_id"],
            "--relation-type", "RELATED_TO", "--expected-version", str(first["version"]), "--actor", "tester",
            "--idempotency-key", relation_key, "--payload-json", "{}",
        )
        self.assertIn("idempotency_key conflict", changed_target["error"])
        self.assertEqual(self.digest(), after_relation)
        changed_issue = self.fails(
            "issue", "relate", "--issue-id", third["issue_id"], "--target-issue-id", second["issue_id"],
            "--relation-type", "RELATED_TO", "--expected-version", str(third["version"]), "--actor", "tester",
            "--idempotency-key", relation_key, "--payload-json", "{}",
        )
        self.assertIn("idempotency_key conflict", changed_issue["error"])
        self.assertEqual(self.digest(), after_relation)
        changed_command = self.fails(
            "issue", "confirm", "--issue-id", first["issue_id"],
            "--expected-version", str(relation_result["issue"]["version"]), "--actor", "tester",
            "--idempotency-key", relation_key, "--payload-json", "{}",
        )
        self.assertIn("idempotency_key conflict", changed_command["error"])
        self.assertEqual(self.digest(), after_relation)

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
        target = self.report("target issue")
        before = self.digest()
        bad_transition = self.fails(
            "issue", "start-investigation", "--issue-id", issue["issue_id"], "--expected-version", "1",
            "--actor", "tester", "--idempotency-key", self.key("bad-transition"), "--payload-json", "{}",
        )
        self.assertIn("invalid state transition", bad_transition["error"])
        self.assertEqual(self.digest(), before)
        untriaged_merge = self.fails(
            "issue", "resolve-without-fix", "--issue-id", issue["issue_id"],
            "--target-issue-id", target["issue_id"], "--resolution", "MERGED",
            "--expected-version", "1", "--actor", "tester",
            "--idempotency-key", self.key("untriaged-merge"), "--payload-json", "{}",
        )
        self.assertIn("invalid state transition", untriaged_merge["error"])
        self.assertEqual(self.digest(), before)
        bad_fk = self.fails(
            "issue", "relate", "--issue-id", issue["issue_id"], "--target-issue-id", "does-not-exist",
            "--relation-type", "RELATED_TO", "--expected-version", "1", "--actor", "tester",
            "--idempotency-key", self.key("bad-fk"), "--payload-json", "{}",
        )
        self.assertIn("found 0", bad_fk["error"])
        self.assertEqual(self.digest(), before)
        bad_type = self.fails(
            "issue", "relate", "--issue-id", issue["issue_id"], "--target-issue-id", target["issue_id"],
            "--relation-type", "FREE_FORM", "--expected-version", "1", "--actor", "tester",
            "--idempotency-key", self.key("bad-type"), "--payload-json", "{}",
        )
        self.assertIn("relation_type must be one of", bad_type["error"])
        self.assertEqual(self.digest(), before)
        reserved_type = self.fails(
            "issue", "relate", "--issue-id", issue["issue_id"], "--target-issue-id", target["issue_id"],
            "--relation-type", "DUPLICATE_OF", "--expected-version", "1", "--actor", "tester",
            "--idempotency-key", self.key("reserved-type"), "--payload-json", "{}",
        )
        self.assertIn("must be created by resolve-without-fix", reserved_type["error"])
        self.assertEqual(self.digest(), before)

    def test_history_search_and_nonterminal_relations(self) -> None:
        self.init()
        first, duplicate, regression = self.report("renderer crash"), self.report("same renderer crash"), self.report("renderer regression")
        found = self.invoke("issue", "search", "--query", "renderer")["result"]
        self.assertEqual({row["issue_id"] for row in found}, {first["issue_id"], duplicate["issue_id"], regression["issue_id"]})
        first = self.mutate("record-history-search", first, {"query": "renderer", "conclusion": "related reports found"})
        related_result = self.invoke(
            "issue", "relate", "--issue-id", first["issue_id"], "--target-issue-id", duplicate["issue_id"],
            "--relation-type", "RELATED_TO", "--expected-version", str(first["version"]), "--actor", "tester",
            "--idempotency-key", self.key("related"), "--payload-json", "{}",
        )["result"]
        first = related_result["issue"]
        regression_result = self.invoke(
            "issue", "relate", "--issue-id", first["issue_id"], "--target-issue-id", regression["issue_id"],
            "--relation-type", "REGRESSION_OF", "--expected-version", str(first["version"]), "--actor", "tester",
            "--idempotency-key", self.key("regression"), "--payload-json", "{}",
        )["result"]
        history = self.invoke("issue", "history", "--issue-id", first["issue_id"])["result"]
        self.assertGreaterEqual(len(history), 3)
        relation_events = [json.loads(event["payload_json"]) for event in history if event["event_type"] == "RELATED"]
        self.assertEqual({payload["relation_type"] for payload in relation_events}, {"RELATED_TO", "REGRESSION_OF"})
        relations = self.invoke("issue", "relations", "--issue-id", first["issue_id"])["result"]
        self.assertEqual({row["relation_type"] for row in relations}, {"RELATED_TO", "REGRESSION_OF"})
        self.assertEqual(
            self.invoke("issue", "relation-get", "--relation-id", related_result["relation"]["relation_id"])["result"],
            related_result["relation"],
        )
        self.assertEqual(regression_result["relation"]["target_issue_id"], regression["issue_id"])

    def test_mature_issues_can_be_atomically_merged_or_marked_duplicate(self) -> None:
        self.init()
        canonical = self.report("canonical issue")
        merged = self.report("mature merge candidate")
        merged = self.mutate("record-history-search", merged)
        merged = self.mutate("confirm", merged)
        merged = self.mutate("start-investigation", merged)
        merged = self.mutate("authorize-fix", merged, {"authorized_by": "owner", "authorization_scope": "candidate only"})
        merged = self.mutate("start-fix", merged)
        before = self.digest()
        missing_target = self.fails(
            "issue", "resolve-without-fix", "--issue-id", merged["issue_id"],
            "--expected-version", str(merged["version"]), "--actor", "tester",
            "--resolution", "MERGED", "--idempotency-key", self.key("missing-target"),
        )
        self.assertIn("target_issue_id", missing_target["error"])
        self.assertEqual(self.digest(), before)

        merge_key = self.key("merge")
        merge_args = (
            "issue", "resolve-without-fix", "--issue-id", merged["issue_id"],
            "--target-issue-id", canonical["issue_id"], "--expected-version", str(merged["version"]),
            "--actor", "tester", "--resolution", "MERGED", "--idempotency-key", merge_key,
            "--payload-json", json.dumps({"basis": "same root cause"}),
        )
        merge_result = self.invoke(*merge_args)["result"]
        self.assertEqual(merge_result["issue"]["status"], "MERGED")
        self.assertEqual(merge_result["relation"]["relation_type"], "MERGED_INTO")
        payload = json.loads(merge_result["event"]["payload_json"])
        self.assertEqual(payload["target_issue_id"], canonical["issue_id"])
        self.assertEqual(payload["relation_id"], merge_result["relation"]["relation_id"])
        after_merge = self.digest()
        retried = self.invoke(*merge_args)["result"]
        self.assertTrue(retried["idempotent"])
        self.assertEqual(retried["relation"], merge_result["relation"])
        self.assertEqual(self.digest(), after_merge)
        self.assertEqual(self.invoke("issue", "get", "--issue-id", canonical["issue_id"])["result"]["version"], 1)

        duplicate = self.report("mature duplicate candidate")
        duplicate = self.mutate("record-history-search", duplicate)
        duplicate = self.mutate("confirm", duplicate)
        duplicate = self.mutate("start-investigation", duplicate)
        duplicate_result = self.invoke(
            "issue", "resolve-without-fix", "--issue-id", duplicate["issue_id"],
            "--target-issue-id", canonical["issue_id"], "--expected-version", str(duplicate["version"]),
            "--actor", "tester", "--resolution", "DUPLICATE", "--idempotency-key", self.key("resolve-duplicate"),
        )["result"]
        self.assertEqual(duplicate_result["issue"]["status"], "DUPLICATE")
        self.assertEqual(duplicate_result["relation"]["relation_type"], "DUPLICATE_OF")
        relations = self.invoke("issue", "relations", "--issue-id", canonical["issue_id"])["result"]
        self.assertEqual({row["relation_type"] for row in relations}, {"MERGED_INTO", "DUPLICATE_OF"})

        invalid = self.report("invalid report")
        invalid = self.mutate("record-history-search", invalid)
        invalid_result = self.invoke(
            "issue", "resolve-without-fix", "--issue-id", invalid["issue_id"],
            "--expected-version", str(invalid["version"]), "--actor", "tester",
            "--resolution", "INVALID", "--idempotency-key", self.key("resolve-invalid"),
        )["result"]
        self.assertEqual(invalid_result["issue"]["status"], "INVALID")
        self.assertNotIn("relation", invalid_result)

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

    def test_validate_rejects_rehashed_illegal_state_transition(self) -> None:
        self.init()
        issue = self.report()
        self.mutate("record-history-search", issue)
        workbook = load_workbook(self.workbook)
        events: Any = workbook["Events"]
        event_headers = [cell.value for cell in events[1]]
        event_columns = {name: index + 1 for index, name in enumerate(event_headers)}
        events.cell(3, event_columns["event_type"]).value = "START_FIX"
        events.cell(3, event_columns["to_status"]).value = "FIXING"
        event = {
            name: "" if events.cell(3, column).value is None else events.cell(3, column).value
            for name, column in event_columns.items()
        }
        body = {key: value for key, value in event.items() if key != "event_hash"}
        events.cell(3, event_columns["event_hash"]).value = hashlib.sha256(
            json.dumps(body, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
        ).hexdigest()
        issues: Any = workbook["Issues"]
        issue_headers = [cell.value for cell in issues[1]]
        issue_columns = {name: index + 1 for index, name in enumerate(issue_headers)}
        issues.cell(2, issue_columns["status"]).value = "FIXING"
        workbook.save(self.workbook)
        failure = self.fails("ledger", "validate")
        self.assertIn("invalid state transition", failure["error"])

    def test_validate_rejects_rehashed_event_type_mismatch(self) -> None:
        self.init()
        issue = self.report()
        self.mutate("record-history-search", issue)
        workbook = load_workbook(self.workbook)
        events: Any = workbook["Events"]
        event_headers = [cell.value for cell in events[1]]
        event_columns = {name: index + 1 for index, name in enumerate(event_headers)}
        events.cell(3, event_columns["event_type"]).value = "START_FIX"
        self.rehash_event(workbook, 3)
        workbook.save(self.workbook)
        failure = self.fails("ledger", "validate")
        self.assertIn("event type has invalid target status", failure["error"])

    def test_validate_cross_checks_consolidation_events_and_relations(self) -> None:
        self.init()
        canonical = self.report("canonical")
        other = self.report("other")
        merged = self.report("merge candidate")
        merged = self.mutate("record-history-search", merged)
        merged = self.mutate("confirm", merged)
        merged = self.mutate("start-investigation", merged)
        self.invoke(
            "issue", "resolve-without-fix", "--issue-id", merged["issue_id"],
            "--target-issue-id", canonical["issue_id"], "--expected-version", str(merged["version"]),
            "--actor", "tester", "--resolution", "MERGED", "--idempotency-key", self.key("merge"),
        )
        valid_bytes = self.workbook.read_bytes()

        workbook = load_workbook(self.workbook)
        relations: Any = workbook["IssueRelations"]
        relations.delete_rows(2)
        workbook.save(self.workbook)
        missing = self.fails("ledger", "validate")
        self.assertIn("missing relation", missing["error"])

        self.workbook.write_bytes(valid_bytes)
        workbook = load_workbook(self.workbook)
        relations = workbook["IssueRelations"]
        relation_headers = [cell.value for cell in relations[1]]
        relation_columns = {name: index + 1 for index, name in enumerate(relation_headers)}
        relations.cell(2, relation_columns["target_issue_id"]).value = other["issue_id"]
        workbook.save(self.workbook)
        wrong_target = self.fails("ledger", "validate")
        self.assertIn("target does not match", wrong_target["error"])

        self.workbook.write_bytes(valid_bytes)
        workbook = load_workbook(self.workbook)
        relations = workbook["IssueRelations"]
        relation_headers = [cell.value for cell in relations[1]]
        relation_columns = {name: index + 1 for index, name in enumerate(relation_headers)}
        relations.cell(2, relation_columns["relation_type"]).value = "DUPLICATE_OF"
        workbook.save(self.workbook)
        wrong_type = self.fails("ledger", "validate")
        self.assertIn("does not match terminal status", wrong_type["error"])

        self.workbook.write_bytes(valid_bytes)
        workbook = load_workbook(self.workbook)
        events: Any = workbook["Events"]
        event_headers = [cell.value for cell in events[1]]
        event_columns = {name: index + 1 for index, name in enumerate(event_headers)}
        merge_row = next(
            row for row in range(2, events.max_row + 1)
            if events.cell(row, event_columns["event_type"]).value == "RESOLVE_WITHOUT_FIX"
        )
        payload = json.loads(events.cell(merge_row, event_columns["payload_json"]).value)
        payload["resolution"] = "INVALID"
        events.cell(merge_row, event_columns["payload_json"]).value = json.dumps(payload)
        self.rehash_event(workbook, merge_row)
        workbook.save(self.workbook)
        wrong_resolution = self.fails("ledger", "validate")
        self.assertIn("resolution payload does not match", wrong_resolution["error"])

    def test_validate_rejects_rehashed_consolidation_without_relation(self) -> None:
        self.init()
        issue = self.report()
        issue = self.mutate("record-history-search", issue)
        self.mutate("confirm", issue)
        workbook = load_workbook(self.workbook)
        events: Any = workbook["Events"]
        event_headers = [cell.value for cell in events[1]]
        event_columns = {name: index + 1 for index, name in enumerate(event_headers)}
        events.cell(4, event_columns["event_type"]).value = "RESOLVE_WITHOUT_FIX"
        events.cell(4, event_columns["to_status"]).value = "MERGED"
        forged_payload = json.loads(events.cell(4, event_columns["payload_json"]).value)
        forged_payload["idempotency_key"] = "forged"
        forged_payload["resolution"] = "MERGED"
        events.cell(4, event_columns["payload_json"]).value = json.dumps(forged_payload)
        event = {
            name: "" if events.cell(4, column).value is None else events.cell(4, column).value
            for name, column in event_columns.items()
        }
        body = {key: value for key, value in event.items() if key != "event_hash"}
        events.cell(4, event_columns["event_hash"]).value = hashlib.sha256(
            json.dumps(body, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
        ).hexdigest()
        issues: Any = workbook["Issues"]
        issue_headers = [cell.value for cell in issues[1]]
        issue_columns = {name: index + 1 for index, name in enumerate(issue_headers)}
        issues.cell(2, issue_columns["status"]).value = "MERGED"
        workbook.save(self.workbook)
        failure = self.fails("ledger", "validate")
        self.assertIn("must reference a relation_id", failure["error"])

    def test_validate_requires_reported_genesis_event(self) -> None:
        self.init()
        self.report()
        workbook = load_workbook(self.workbook)
        events: Any = workbook["Events"]
        event_headers = [cell.value for cell in events[1]]
        event_columns = {name: index + 1 for index, name in enumerate(event_headers)}
        events.cell(2, event_columns["event_type"]).value = "START_FIX"
        events.cell(2, event_columns["from_status"]).value = "FIXING"
        events.cell(2, event_columns["to_status"]).value = "FIXING"
        event = {
            name: "" if events.cell(2, column).value is None else events.cell(2, column).value
            for name, column in event_columns.items()
        }
        body = {key: value for key, value in event.items() if key != "event_hash"}
        events.cell(2, event_columns["event_hash"]).value = hashlib.sha256(
            json.dumps(body, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
        ).hexdigest()
        issues: Any = workbook["Issues"]
        issue_headers = [cell.value for cell in issues[1]]
        issue_columns = {name: index + 1 for index, name in enumerate(issue_headers)}
        issues.cell(2, issue_columns["status"]).value = "FIXING"
        workbook.save(self.workbook)
        failure = self.fails("ledger", "validate")
        self.assertIn("first issue event", failure["error"])

    def test_validate_binds_verification_result_to_target_status(self) -> None:
        repo, url, commit = self.create_git_repository()
        self.init([self.repository("core", url)])
        issue = self.ready_to_verify()
        issue = self.register_evidence(issue, repo, commit, "FIX_COMMIT")
        issue = self.mutate("complete-fix", issue)
        issue = self.register_evidence(issue, repo, commit, "VERIFICATION")
        self.mutate("record-verification", issue, {"verification_result": "PASS", "resolution": "verified"})
        workbook = load_workbook(self.workbook)
        events: Any = workbook["Events"]
        event_headers = [cell.value for cell in events[1]]
        event_columns = {name: index + 1 for index, name in enumerate(event_headers)}
        verification_row = next(
            row for row in range(2, events.max_row + 1)
            if events.cell(row, event_columns["event_type"]).value == "RECORD_VERIFICATION"
        )
        payload = json.loads(events.cell(verification_row, event_columns["payload_json"]).value)
        payload["verification_result"] = "FAIL"
        events.cell(verification_row, event_columns["payload_json"]).value = json.dumps(payload)
        self.rehash_event(workbook, verification_row)
        workbook.save(self.workbook)
        failure = self.fails("ledger", "validate")
        self.assertIn("verification_result does not match", failure["error"])

    def test_schema_v1_grandfathers_legacy_consolidation_without_relation(self) -> None:
        self.init()
        issue = self.report()
        self.mutate("record-history-search", issue)
        workbook = load_workbook(self.workbook)
        self.set_schema_version(workbook, 1)
        events: Any = workbook["Events"]
        event_headers = [cell.value for cell in events[1]]
        event_columns = {name: index + 1 for index, name in enumerate(event_headers)}
        events.cell(3, event_columns["event_type"]).value = "RESOLVE_WITHOUT_FIX"
        events.cell(3, event_columns["to_status"]).value = "DUPLICATE"
        legacy_payload = json.loads(events.cell(3, event_columns["payload_json"]).value)
        legacy_payload.pop("request_fingerprint")
        events.cell(3, event_columns["payload_json"]).value = json.dumps(legacy_payload)
        self.rehash_event(workbook, 3)
        issues: Any = workbook["Issues"]
        issue_headers = [cell.value for cell in issues[1]]
        issue_columns = {name: index + 1 for index, name in enumerate(issue_headers)}
        issues.cell(2, issue_columns["status"]).value = "DUPLICATE"
        workbook.save(self.workbook)
        self.assertTrue(self.invoke("ledger", "validate")["result"]["valid"])

    def test_schema_v1_grandfathers_legacy_relation_but_replay_fails_closed(self) -> None:
        self.init()
        source = self.report("source")
        target = self.report("target")
        relation_key = self.key("legacy-relation")
        self.invoke(
            "issue", "relate", "--issue-id", source["issue_id"], "--target-issue-id", target["issue_id"],
            "--relation-type", "RELATED_TO", "--expected-version", str(source["version"]), "--actor", "tester",
            "--idempotency-key", relation_key, "--payload-json", "{}",
        )
        workbook = load_workbook(self.workbook)
        self.set_schema_version(workbook, 1)
        events: Any = workbook["Events"]
        event_headers = [cell.value for cell in events[1]]
        event_columns = {name: index + 1 for index, name in enumerate(event_headers)}
        legacy_payload = json.loads(events.cell(4, event_columns["payload_json"]).value)
        for name in ("request_fingerprint", "relation_id", "target_issue_id", "relation_type"):
            legacy_payload.pop(name)
        events.cell(4, event_columns["payload_json"]).value = json.dumps(legacy_payload)
        self.rehash_event(workbook, 4)
        relations: Any = workbook["IssueRelations"]
        relation_headers = [cell.value for cell in relations[1]]
        relation_columns = {name: index + 1 for index, name in enumerate(relation_headers)}
        relations.cell(2, relation_columns["created_at"]).value = "2099-01-01T00:00:00+00:00"
        relations.cell(2, relation_columns["relation_type"]).value = "CUSTOM_LEGACY"
        workbook.save(self.workbook)
        self.assertTrue(self.invoke("ledger", "validate")["result"]["valid"])
        before = self.digest()
        replay = self.fails(
            "issue", "relate", "--issue-id", source["issue_id"], "--target-issue-id", target["issue_id"],
            "--relation-type", "RELATED_TO", "--expected-version", str(source["version"]), "--actor", "tester",
            "--idempotency-key", relation_key, "--payload-json", "{}",
        )
        self.assertIn("legacy idempotency_key cannot be replayed", replay["error"])
        self.assertEqual(self.digest(), before)


if __name__ == "__main__":
    unittest.main(verbosity=2)
