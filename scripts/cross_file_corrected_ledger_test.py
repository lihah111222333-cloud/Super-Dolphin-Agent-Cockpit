import copy
import unittest

from scripts.cross_file_corrected_ledger import (
    AmbiguousCorrectionError,
    CorrectionLinkError,
    EvidenceDriftError,
    EvidenceHashError,
    INPUT_SCHEMA,
    LedgerCycleError,
    QUALITY_POINTS,
    SCHEMA,
    build_corrected_ledger,
    evidence_digest,
)


def _record(
    attempt_id,
    *,
    instance="task-01",
    arm="with_lsp",
    version="v082",
    classification="infra",
    status="infrastructure_failure",
    parent=None,
    supersedes=None,
    quality_round=None,
    score=None,
    correction_link=None,
    evidence=None,
):
    evidence = evidence if evidence is not None else {"attempt": attempt_id, "status": status}
    result = {
        "attempt_id": attempt_id,
        "parent": parent,
        "supersedes": list(supersedes or []),
        "instance": instance,
        "arm": arm,
        "version": version,
        "classification": classification,
        "status": status,
        "evidence": evidence,
        "evidence_hash": evidence_digest(evidence),
        "correction_link": correction_link,
    }
    if quality_round is not None:
        result["quality_round"] = quality_round
    if score is not None:
        result["score"] = score
    return result


def _link(parent, evidence_hash, reason="official recovery evidence"):
    return {
        "from_attempt_id": parent,
        "relation": "corrects",
        "evidence_hash": evidence_hash,
        "reason": reason,
    }


class CorrectedLedgerTests(unittest.TestCase):
    def test_raw_is_immutable_and_recovery_chain_produces_diff_and_score(self):
        root = _record(
            "v082-pytest-with",
            evidence={"guard": "collected_count disagrees", "grader_started": False},
        )
        transport = _record(
            "v083-pytest-with",
            version="v083",
            classification="infra",
            status="pending",
            parent=root["attempt_id"],
            supersedes=[root["attempt_id"]],
            correction_link=_link(root["attempt_id"], "placeholder"),
            evidence={"transport": "stream disconnected"},
        )
        transport["correction_link"]["evidence_hash"] = transport["evidence_hash"]
        recovered = _record(
            "v087-pytest-with",
            version="v087",
            classification="model",
            status="passed",
            parent=transport["attempt_id"],
            supersedes=[transport["attempt_id"]],
            quality_round=1,
            score=QUALITY_POINTS[0],
            correction_link=_link(transport["attempt_id"], "placeholder"),
            evidence={"passed": 432, "skipped": 2, "xfailed": 2},
        )
        recovered["correction_link"]["evidence_hash"] = recovered["evidence_hash"]
        raw = [root, transport, recovered]
        before = copy.deepcopy(raw)

        result = build_corrected_ledger(raw)
        self.assertEqual(raw, before)
        report = result.to_dict()
        self.assertEqual(report["schema"], SCHEMA)
        self.assertEqual(report["authority"], "BENCHMARK_EVIDENCE_ONLY")
        self.assertEqual(report["raw"]["schema"], INPUT_SCHEMA)
        self.assertEqual(report["raw"]["records"], before)
        self.assertEqual(report["current"][0]["attempt_id"], "v082-pytest-with")
        self.assertEqual(report["corrected"][0]["attempt_id"], "v087-pytest-with")
        self.assertEqual(report["differences"][0]["correction_chain"], [
            "v082-pytest-with",
            "v083-pytest-with",
            "v087-pytest-with",
        ])
        self.assertEqual(report["score_comparison"]["current_total"], 0)
        self.assertEqual(report["score_comparison"]["corrected_total"], 10)
        self.assertEqual(report["final_3_round_scores"][0]["first_passing_round"], 1)
        self.assertEqual(
            [item["score"] for item in report["final_3_round_scores"][0]["rounds"]],
            [10, 10, 10],
        )

    def test_version_only_overwrite_is_rejected(self):
        first = _record("v082-task", version="v082")
        second = _record("v087-task", version="v087", classification="model", status="passed", quality_round=1)
        with self.assertRaisesRegex(AmbiguousCorrectionError, "explicit correction links"):
            build_corrected_ledger([first, second])

    def test_missing_parent_link_and_cross_edge_fail_fast(self):
        root = _record("root")
        child = _record(
            "child",
            parent=root["attempt_id"],
            supersedes=[root["attempt_id"]],
            evidence={"result": "recovered"},
        )
        with self.assertRaises(CorrectionLinkError):
            build_corrected_ledger([root, child])

        other = _record("other", instance="other-task")
        child = _record(
            "child",
            parent=root["attempt_id"],
            supersedes=[root["attempt_id"], other["attempt_id"]],
            correction_link=_link(root["attempt_id"], "placeholder"),
            evidence={"result": "recovered"},
        )
        child["correction_link"]["evidence_hash"] = child["evidence_hash"]
        with self.assertRaises(CorrectionLinkError):
            build_corrected_ledger([root, other, child])

    def test_evidence_hash_and_correction_hash_are_bound(self):
        root = _record("root")
        tampered = copy.deepcopy(root)
        tampered["evidence"]["tampered"] = True
        with self.assertRaises(EvidenceHashError):
            build_corrected_ledger([tampered])

        child = _record(
            "child",
            parent=root["attempt_id"],
            supersedes=[root["attempt_id"]],
            correction_link=_link(root["attempt_id"], "sha256:" + "f" * 64),
            evidence={"result": "recovered"},
        )
        with self.assertRaises(EvidenceDriftError):
            build_corrected_ledger([root, child])

    def test_three_round_score_uses_first_explicit_passing_round(self):
        r1 = _record(
            "r1",
            classification="model",
            status="failed",
            quality_round=1,
            score=0,
            evidence={"assertion": "wrong at round 1"},
        )
        r2 = _record(
            "r2",
            version="v083",
            classification="model",
            status="failed",
            parent="r1",
            supersedes=["r1"],
            quality_round=2,
            score=0,
            correction_link=_link("r1", "placeholder"),
            evidence={"assertion": "wrong at round 2"},
        )
        r2["correction_link"]["evidence_hash"] = r2["evidence_hash"]
        r3 = _record(
            "r3",
            version="v087",
            classification="model",
            status="passed",
            parent="r2",
            supersedes=["r2"],
            quality_round=3,
            score=QUALITY_POINTS[2],
            correction_link=_link("r2", "placeholder"),
            evidence={"passed": True},
        )
        r3["correction_link"]["evidence_hash"] = r3["evidence_hash"]

        report = build_corrected_ledger([r1, r2, r3]).to_dict()
        scores = report["final_3_round_scores"][0]
        self.assertEqual(scores["first_passing_round"], 3)
        self.assertEqual(scores["final_score"], 4)
        self.assertEqual([item["score"] for item in scores["rounds"]], [0, 0, 4])
        self.assertEqual(report["score_comparison"]["corrected_round_totals"], {"1": 0, "2": 0, "3": 4})

    def test_input_envelope_and_selector_are_hash_bound(self):
        root = _record("root", instance="task-selector", arm="no_lsp")
        child = _record(
            "child",
            instance="task-selector",
            arm="no_lsp",
            version="v087",
            classification="classifier",
            status="pending",
            parent="root",
            supersedes=["root"],
            correction_link=_link("root", "placeholder"),
            evidence={"pending": "awaiting authoritative report"},
        )
        child["correction_link"]["evidence_hash"] = child["evidence_hash"]
        envelope = {
            "schema": INPUT_SCHEMA,
            "raw_records": [root, child],
            "current_attempt_ids": {"task-selector": {"no_lsp": "root"}},
        }
        direct = build_corrected_ledger(envelope).to_dict()
        envelope["raw_ledger_hash"] = direct["raw_ledger_hash"]
        report = build_corrected_ledger(envelope).to_dict()
        self.assertEqual(report["raw_ledger_hash"], direct["raw_ledger_hash"])
        self.assertEqual(report["raw"]["current_attempt_ids"], {"task-selector": {"no_lsp": "root"}})
        self.assertFalse(report["integrity"]["version_only_selection"])

    def test_authoritative_raw_and_paired_scores_keep_both_arms(self):
        no_lsp = _record(
            "no-lsp-pass",
            instance="paired-task",
            arm="no_lsp",
            classification="model",
            status="passed",
            quality_round=1,
            score=10,
            evidence={"resolved": True},
        )
        with_lsp = _record(
            "with-lsp-fail",
            instance="paired-task",
            arm="with_lsp",
            classification="model",
            status="failed",
            quality_round=1,
            score=0,
            evidence={"resolved": False, "completed": True},
        )
        raw = [no_lsp, with_lsp]
        report = build_corrected_ledger(raw).to_dict()
        self.assertEqual(report["raw"]["records"], raw)
        self.assertEqual(
            [row["attempt_id"] for row in report["authoritative"]["records"]],
            ["no-lsp-pass", "with-lsp-fail"],
        )
        pair = report["authoritative"]["pairs"][0]
        self.assertTrue(pair["complete"])
        self.assertEqual(pair["arms"]["no_lsp"]["final_score"], 10)
        self.assertEqual(pair["arms"]["with_lsp"]["final_score"], 0)
        self.assertEqual(pair["final_score"], 10)
        self.assertEqual(pair["round_totals"], {"1": 10, "2": 10, "3": 10})
        self.assertEqual(report["final"]["raw"]["records"], raw)
        self.assertEqual(report["final"]["authoritative"]["pairs"][0]["final_score"], 10)

    def test_paired_quality_scores_are_10_7_4_per_first_passing_round(self):
        records = []
        for arm, round_number, score in (
            ("no_lsp", 2, 7),
            ("with_lsp", 3, 4),
        ):
            previous = None
            for number in range(1, round_number + 1):
                attempt_id = f"{arm}-r{number}"
                status = "passed" if number == round_number else "failed"
                item = _record(
                    attempt_id,
                    instance="paired-rounds",
                    arm=arm,
                    classification="model",
                    status=status,
                    parent=previous,
                    supersedes=[previous] if previous else [],
                    quality_round=number,
                    score=score if number == round_number else 0,
                    correction_link=(
                        _link(previous, "placeholder") if previous else None
                    ),
                    evidence={"round": number, "status": status},
                )
                if previous:
                    item["correction_link"]["evidence_hash"] = item["evidence_hash"]
                records.append(item)
                previous = attempt_id
        report = build_corrected_ledger(records).to_dict()
        pair = report["pair_final_3_round_scores"][0]
        self.assertEqual(pair["quality_points_by_first_passing_round"], [10, 7, 4])
        self.assertEqual(pair["arms"]["no_lsp"]["final_score"], 7)
        self.assertEqual(pair["arms"]["with_lsp"]["final_score"], 4)
        self.assertEqual(pair["final_score"], 11)

    def test_cycle_and_incomplete_selector_fail_fast(self):
        first = _record("first")
        second = _record(
            "second",
            parent="first",
            supersedes=["first"],
            correction_link=_link("first", "placeholder"),
            evidence={"step": 2},
        )
        second["correction_link"]["evidence_hash"] = second["evidence_hash"]
        first["parent"] = "second"
        first["supersedes"] = ["second"]
        first["correction_link"] = _link("second", first["evidence_hash"])
        with self.assertRaises(LedgerCycleError):
            build_corrected_ledger([first, second])

        no_lsp = _record("no-lsp", instance="task-b", arm="no_lsp")
        with_lsp = _record("with-lsp", instance="task-b", arm="with_lsp")
        raw = [no_lsp, with_lsp]
        with self.assertRaises(AmbiguousCorrectionError):
            build_corrected_ledger(
                {
                    "schema": INPUT_SCHEMA,
                    "raw_records": raw,
                    "current_attempt_ids": {"task-b": {"no_lsp": "no-lsp"}},
                }
            )


if __name__ == "__main__":
    unittest.main()
