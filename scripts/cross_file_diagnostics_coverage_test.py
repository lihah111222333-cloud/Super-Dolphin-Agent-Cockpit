import importlib.util
import json
import pathlib
import sys
import unittest


SCRIPT = pathlib.Path(__file__).with_name("cross_file_diagnostics_coverage.py")
FIXTURE = pathlib.Path(__file__).with_name("testdata") / "cross_file_diagnostics_coverage"


def load_module():
    spec = importlib.util.spec_from_file_location("cross_file_diagnostics_coverage", SCRIPT)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load diagnostics coverage module")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class DiagnosticsCoverageTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.coverage = load_module()

    def fixture_diff(self):
        return (FIXTURE / "cds.diff").read_text(encoding="utf-8")

    def generated_registry(self):
        return [
            {
                "path": "cds_parsetab.py",
                "kind": "generated",
                "reason": "PLY parser table generated from cds.py",
                "evidence": "cds.py parser source and generated table fixture",
                "owner": "parser-maintainers",
            }
        ]

    def language_registry(self):
        return {
            "extensions": {".py": "python"},
            "generated": ["cds_parsetab.py"],
        }

    def real_v069_diagnostics_arguments(self):
        # Captured from the v069 representative24 astropy-14369 With-LSP
        # round-2 trajectory; this is the exact object shape emitted to MCP.
        return {
            "file_paths": [
                "astropy/units/format/cds.py",
                "astropy/units/format/cds_parsetab.py",
            ],
            "diff_filter": {
                "mode": "changed_lines",
                "changed_lines": {
                    "astropy/units/format/cds.py": [174, 184, 185, 192, 193, 194, 195, 196, 197, 198, 199, 200, 201],
                    "astropy/units/format/cds_parsetab.py": [20, 22, 31, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69],
                },
            },
        }

    def test_real_cds_and_parsetab_fixture_emits_exact_batch_arguments(self):
        plan = self.coverage.build_coverage(
            self.fixture_diff(),
            language_support=self.language_registry(),
            exemptions=self.generated_registry(),
        )
        self.assertEqual(
            plan.diagnostics_args,
            {
                "file_paths": ["cds.py"],
                "diff_filter": {
                    "mode": "changed_lines",
                    "changed_lines": {"cds.py": [1, 2, 6, 7, 8]},
                },
            },
        )
        self.assertEqual(plan.exemptions[0]["path"], "cds_parsetab.py")
        self.assertEqual(plan.exemptions[0]["kind"], "generated")
        self.assertEqual(plan.to_runner_payload()["changed_files"][0]["path"], "cds.py")

    def test_changed_lines_are_per_file_deduplicated_and_sorted(self):
        patch = """diff --git a/z.py b/z.py
--- a/z.py
+++ b/z.py
@@ -20,1 +30,3 @@
-old
+a
+b
+c
@@ -1,0 +2,2 @@
+d
+e
diff --git a/a.py b/a.py
--- a/a.py
+++ b/a.py
@@ -2,0 +2,1 @@
+x
@@ -9,0 +9,1 @@
+y
"""
        plan = self.coverage.build_coverage(
            patch,
            changed_files=["z.py", "a.py"],
            language_support={".py": "python"},
        )
        self.assertEqual(plan.diagnostics_args["file_paths"], ["a.py", "z.py"])
        self.assertEqual(
            plan.diagnostics_args["diff_filter"]["changed_lines"],
            {"a.py": [2, 9], "z.py": [2, 3, 30, 31, 32]},
        )

    def test_real_v069_object_schema_round_trips_and_requires_exact_files(self):
        arguments = self.real_v069_diagnostics_arguments()
        expected_files = arguments["file_paths"]
        normalized = self.coverage.normalize_diagnostics_arguments(
            arguments, target_files=expected_files
        )
        self.assertEqual(normalized, arguments)
        self.assertEqual(
            self.coverage.parse_diff_filter(
                arguments["diff_filter"], target_files=expected_files
            ),
            arguments["diff_filter"],
        )

        missing = json.loads(json.dumps(arguments))
        del missing["diff_filter"]["changed_lines"][expected_files[1]]
        with self.assertRaisesRegex(self.coverage.CoverageError, "missing target"):
            self.coverage.normalize_diagnostics_arguments(missing, target_files=expected_files)

        extra = json.loads(json.dumps(arguments))
        extra["diff_filter"]["changed_lines"]["astropy/units/format/extra.py"] = [1]
        with self.assertRaisesRegex(self.coverage.CoverageError, "extra file"):
            self.coverage.normalize_diagnostics_arguments(extra, target_files=expected_files)

    def test_diff_filter_rejects_legacy_shapes_unknown_fields_and_bad_lines(self):
        expected = {"src/a.py": [1, 2]}
        for value in (
            ["src/a.py"],
            "src/a.py",
            {"mode": "changed_lines", "changed_lines": expected, "extra": True},
            {"mode": "changed_lines", "changed_lines": {"src/a.py": [0]}},
            {"mode": "changed_lines", "changed_lines": {"src/a.py": [True]}},
            {"mode": "changed_lines", "changed_lines": {"src/a.py": [1, 1]}},
        ):
            with self.subTest(value=value):
                with self.assertRaises(self.coverage.CoverageError):
                    self.coverage.parse_diff_filter(value, target_files=("src/a.py",))

        with self.assertRaisesRegex(self.coverage.CoverageError, "extra file"):
            self.coverage.parse_diff_filter(
                {"mode": "changed_lines", "changed_lines": expected}, target_files=()
            )

    def test_diagnostics_cache_key_is_order_independent_and_content_scoped(self):
        arguments = self.real_v069_diagnostics_arguments()
        reordered = {
            "diff_filter": {
                "changed_lines": {
                    "astropy/units/format/cds_parsetab.py": list(
                        reversed(arguments["diff_filter"]["changed_lines"]["astropy/units/format/cds_parsetab.py"])
                    ),
                    "astropy/units/format/cds.py": list(
                        reversed(arguments["diff_filter"]["changed_lines"]["astropy/units/format/cds.py"])
                    ),
                },
                "mode": "changed_lines",
            },
            "file_paths": list(reversed(arguments["file_paths"])),
        }
        digests = {
            "astropy/units/format/cds.py": "digest-cds",
            "astropy/units/format/cds_parsetab.py": "digest-parsetab",
        }
        first = self.coverage.build_diagnostics_cache_key(
            arguments, digests, "pyright-generation-7"
        )
        equivalent = self.coverage.build_diagnostics_cache_key(
            reordered, dict(reversed(list(digests.items()))), "pyright-generation-7"
        )
        self.assertEqual(first, equivalent)
        self.assertEqual(
            self.coverage.decide_diagnostics_cache(None, first).reason,
            "no_cached_result",
        )
        self.assertTrue(self.coverage.should_reuse_diagnostics(first, equivalent))

        changed_digest = self.coverage.build_diagnostics_cache_key(
            arguments,
            {**digests, "astropy/units/format/cds.py": "digest-cds-new"},
            "pyright-generation-7",
        )
        self.assertEqual(
            self.coverage.diagnostics_cache_decision(first, changed_digest).reason,
            "file_digest_changed",
        )
        changed_generation = self.coverage.build_diagnostics_cache_key(
            arguments, digests, "pyright-generation-8"
        )
        self.assertEqual(
            self.coverage.diagnostics_cache_decision(first, changed_generation).reason,
            "server_generation_changed",
        )

    def test_deletion_rename_and_zero_length_hunks_are_parsed_without_fake_lines(self):
        patch = """diff --git a/deleted.py b/deleted.py
deleted file mode 100644
--- a/deleted.py
+++ /dev/null
@@ -1,2 +0,0 @@
-one
-two
diff --git a/old.py b/new.py
similarity index 100%
rename from old.py
rename to new.py
diff --git a/empty.py b/empty.py
--- a/empty.py
+++ b/empty.py
@@ -4,0 +4,0 @@
"""
        parsed = self.coverage.parse_unified_diff(patch)
        self.assertEqual([item.status for item in parsed], ["deleted", "renamed", "modified"])
        self.assertEqual(parsed[0].changed_lines, ())
        self.assertEqual(parsed[1].path, "new.py")
        self.assertEqual(parsed[2].changed_lines, ())
        plan = self.coverage.build_coverage(patch, language_support={".py": "python"})
        self.assertEqual(plan.diagnostics_args, None)
        self.assertEqual(plan.deleted_files, ("deleted.py",))
        self.assertEqual(plan.zero_line_files, ("deleted.py", "empty.py", "new.py"))

    def test_no_lsp_never_creates_a_call(self):
        plan = self.coverage.build_coverage(
            self.fixture_diff(),
            language_support={".py": "python"},
            exemptions=self.generated_registry(),
            mode="no_lsp",
        )
        self.assertIsNone(plan.diagnostics_args)
        self.assertIsNone(plan.diagnostics_call)

    def test_non_production_unsupported_document_needs_no_exemption(self):
        patch = """diff --git a/docs/ref/example.txt b/docs/ref/example.txt
--- a/docs/ref/example.txt
+++ b/docs/ref/example.txt
@@ -1 +1 @@
-old
+new
diff --git a/src/main.py b/src/main.py
--- a/src/main.py
+++ b/src/main.py
@@ -1 +1 @@
-old
+new
"""
        plan = self.coverage.build_coverage(
            patch,
            changed_files=[
                {"path": "docs/ref/example.txt", "production": False},
                {"path": "src/main.py", "production": True},
            ],
            language_support={".py": "python"},
        )
        self.assertEqual(plan.exemptions, ())
        self.assertEqual(plan.diagnostics_args["file_paths"], ["src/main.py"])

    def test_exemption_registry_is_fail_first(self):
        base = self.fixture_diff()
        valid = self.generated_registry()[0]
        for invalid in (
            {**valid, "reason": ""},
            {**valid, "evidence": ""},
            {**valid, "owner": ""},
        ):
            with self.subTest(invalid=invalid):
                with self.assertRaises(self.coverage.RegistryError):
                    self.coverage.build_coverage(
                        base,
                        language_support=self.language_registry(),
                        exemptions=[invalid],
                    )
        with self.assertRaisesRegex(self.coverage.RegistryError, "missing"):
            self.coverage.build_coverage(base, language_support=self.language_registry())
        with self.assertRaisesRegex(self.coverage.RegistryError, "stale"):
            self.coverage.build_coverage(
                base,
                language_support=self.language_registry(),
                exemptions=[
                    valid,
                    {
                        "path": "old.py",
                        "kind": "generated",
                        "reason": "stale",
                        "evidence": "old",
                        "owner": "old-owner",
                    },
                ],
            )
        with self.assertRaisesRegex(self.coverage.RegistryError, "duplicate"):
            self.coverage.build_coverage(
                base,
                language_support=self.language_registry(),
                exemptions=[valid, valid],
            )

    def test_unknown_binary_and_escaping_inputs_fail_fast(self):
        with self.assertRaises(self.coverage.CoverageError):
            self.coverage.parse_unified_diff("not a git diff")
        with self.assertRaises(self.coverage.DiffParseError):
            self.coverage.parse_unified_diff(
                "diff --git a/x.py b/x.py\nBinary files a/x.py and b/x.py differ\n"
            )
        with self.assertRaises(self.coverage.CoverageError):
            self.coverage.parse_unified_diff(
                "diff --git a/../x.py b/../x.py\n--- a/../x.py\n+++ b/../x.py\n"
            )

    def test_codex_file_change_and_patch_text_entry(self):
        file_change = {"type": "file_change", "path": "cds.py", "kind": "update"}
        patch = """*** Begin Patch
*** Update File: cds.py
@@ -1 +1 @@
-old
+new
*** End Patch
"""
        plan = self.coverage.from_codex_file_change(
            file_change,
            patch,
            language_support={".py": "python"},
        )
        self.assertEqual(plan.diagnostics_args["file_paths"], ["cds.py"])
        self.assertEqual(plan.diagnostics_args["diff_filter"]["changed_lines"]["cds.py"], [1])

    def test_payload_is_json_serialisable(self):
        plan = self.coverage.build_coverage(
            self.fixture_diff(),
            language_support={".py": "python"},
            exemptions=self.generated_registry(),
        )
        json.dumps(plan.to_dict(), ensure_ascii=False)


if __name__ == "__main__":
    unittest.main()
