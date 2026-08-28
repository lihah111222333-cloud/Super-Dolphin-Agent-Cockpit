import unittest

from scripts.cross_file_ubuntu_local_provider_test import CrossFileUbuntuLocalProviderTest
from scripts.generate_multilingual_validation_bundle import (
    MultilingualBundleError,
    build_bundle,
)


class FakeResolver:
    def __init__(self):
        self.calls = []

    def resolve(self, image, base_commit):
        self.calls.append((image, base_commit))
        return "registry.example/validation@sha256:" + "a" * 64, "b" * 40


def _selection_and_dataset(count=100):
    selection = []
    official = {}
    for index in range(count):
        question_id = f"org__repo-{index:03d}"
        commit = f"{index + 1:040x}"
        selection.append(
            {
                "instance_id": question_id,
                "repo": "org/repo",
                "base_commit": commit,
                "image": f"swebench/validation-{index}:latest",
            }
        )
        official[question_id] = {
            "instance_id": question_id,
            "repo": "org/repo",
            "base_commit": commit,
            "image": f"swebench/validation-{index}:latest",
            "problem_statement": f"Problem statement {index}",
        }
    return selection, official


class GenerateMultilingualValidationBundleTest(unittest.TestCase):
    def setUp(self):
        self.fixture = CrossFileUbuntuLocalProviderTest()
        self.fixture.setUp()
        self.addCleanup(self.fixture.tearDown)

    def test_builds_100_question_plan_images_and_drivers(self):
        selection, official = _selection_and_dataset()
        resolver = FakeResolver()
        bundle = build_bundle(
            selection,
            official,
            self.fixture.config_data,
            resolver=resolver,
            artifact_root=self.fixture.fixture.root / "multilingual",
        )
        self.assertEqual(bundle["metadata"]["question_count"], 100)
        self.assertEqual(bundle["metadata"]["arm_count"], 200)
        self.assertEqual(len(bundle["plan"]["questions"]), 100)
        self.assertEqual(len(bundle["images"]["questions"]), 100)
        self.assertEqual(len(bundle["drivers"]["question_configs"]), 100)
        self.assertEqual(len(resolver.calls), 100)
        self.assertEqual(bundle["plan"]["max_concurrency"], 40)
        self.assertEqual(
            bundle["drivers"]["question_configs"]["org__repo-099"]["identity_bindings"]["base"]["tree_sha"],
            "b" * 40,
        )

    def test_missing_official_record_fails_closed(self):
        selection, official = _selection_and_dataset()
        official.pop("org__repo-099")
        with self.assertRaisesRegex(MultilingualBundleError, "no org__repo-099"):
            build_bundle(
                selection,
                official,
                self.fixture.config_data,
                resolver=FakeResolver(),
                artifact_root=self.fixture.fixture.root / "multilingual",
            )


if __name__ == "__main__":
    unittest.main()
