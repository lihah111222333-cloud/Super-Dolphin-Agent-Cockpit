import pathlib
import unittest
from unittest import mock
from types import SimpleNamespace

from scripts import cross_file_benchmark_launcher as launcher


class CrossFileBenchmarkLauncherTest(unittest.TestCase):
    def test_source_is_bound_to_benchmark_runner_and_ubuntu_driver(self):
        source = pathlib.Path(launcher.__file__).read_text(encoding="utf-8")
        self.assertIn("cross_file_benchmark_runner", source)
        self.assertIn("cross_file_ubuntu_local_provider", source)
        self.assertIn("execute_plan", source)
        self.assertIn("load_local_image_provider", source)
        self.assertIn("max_concurrency=40", source)
        self.assertIn("exactly 8 grader slots", source)
        self.assertNotIn("ClosedLoopGuard", source)
        self.assertNotIn("resume --last", source)
        self.assertNotIn('"resolved_ids"', source)

    def test_run_remote_plan_constructs_driver_and_calls_runner(self):
        questions = [
            {"question_id": f"question-{index:02d}"}
            for index in range(20)
        ]
        plan = {"questions": questions, "max_concurrency": 40}
        images = {question["question_id"]: "ubuntu@sha256:" + str(index % 10) * 64 for index, question in enumerate(questions)}
        driver = SimpleNamespace(config=SimpleNamespace(grader_slots=8))
        with mock.patch.object(launcher, "_read_plan", return_value=plan), \
             mock.patch.object(launcher, "load_question_image_manifest", return_value=images), \
             mock.patch.object(launcher, "load_local_image_provider", return_value=driver) as provider, \
             mock.patch.object(launcher, "execute_plan", return_value="execution") as execute:
            result = launcher.run_remote_plan(
                "/tmp/plan.json",
                state_dir="/tmp/state",
                driver_config_path="/tmp/driver.json",
                image_manifest_path="/tmp/images.json",
            )
        self.assertEqual(result, "execution")
        provider.assert_called_once_with("/tmp/driver.json", "/tmp/images.json")
        execute.assert_called_once_with(
            plan,
            state_dir="/tmp/state",
            driver=driver,
        )

    def test_production_launcher_rejects_non_twenty_question_plan_before_driver(self):
        plan = {"questions": [{"question_id": "only-one"}], "max_concurrency": 40}
        with mock.patch.object(launcher, "_read_plan", return_value=plan), \
             mock.patch.object(launcher, "load_local_image_provider") as load_provider:
            with self.assertRaisesRegex(RuntimeError, "exactly 20"):
                launcher.run_remote_plan(
                    "/tmp/plan.json",
                    state_dir="/tmp/state",
                    driver_config_path="/tmp/driver.json",
                    image_manifest_path="/tmp/images.json",
                )
        load_provider.assert_not_called()

    def test_production_launcher_rejects_wrong_concurrency_before_image_preflight(self):
        questions = [{"question_id": f"question-{index:02d}"} for index in range(20)]
        with mock.patch.object(launcher, "_read_plan", return_value={"questions": questions, "max_concurrency": 1}), \
             mock.patch.object(launcher, "load_local_image_provider") as load_provider:
            with self.assertRaisesRegex(RuntimeError, "max_concurrency=40"):
                launcher.run_remote_plan(
                    "/tmp/plan.json",
                    state_dir="/tmp/state",
                    driver_config_path="/tmp/driver.json",
                    image_manifest_path="/tmp/images.json",
                )
        load_provider.assert_not_called()


if __name__ == "__main__":
    unittest.main()
