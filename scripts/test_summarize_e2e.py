import json
from pathlib import Path
import tempfile
import unittest

from summarize_e2e import summarize


class SummaryTest(unittest.TestCase):
    def report(self, events):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "default-1.jsonl"
            path.write_text("\n".join(json.dumps(event) for event in events))
            return summarize([path])

    def test_suite_duration_uses_package_time_and_sorts_tests(self):
        report = self.report([
            {"Action": "pass", "Test": "TestFast", "Elapsed": 2},
            {"Action": "skip", "Test": "TestSkipped"},
            {"Action": "pass", "Test": "TestSlow", "Elapsed": 8},
            {"Action": "pass", "Elapsed": 9},
        ])
        self.assertIn("| default-1 | pass | 9.00 | 2 | 0 | 1 |", report)
        self.assertLess(report.index("TestSlow"), report.index("TestFast"))

    def test_failure_is_visible_even_outside_slowest_twenty(self):
        report = self.report([
            *({"Action": "pass", "Test": f"TestSlow{i}", "Elapsed": 2} for i in range(21)),
            {"Action": "fail", "Test": "TestFailed/<tag>|case", "Elapsed": 0.1},
            {"Action": "fail", "Elapsed": 9},
        ])
        self.assertIn("| default-1 | fail | 9.00 | 21 | 1 | 0 |", report)
        self.assertIn("Failed tests: TestFailed/&lt;tag&gt;&#124;case", report)

    def test_interrupted_stream_is_not_reported_as_passed(self):
        report = self.report([
            {"Action": "pass", "Test": "TestFinished", "Elapsed": 2},
            {"Action": "run", "Test": "TestInterrupted"},
        ])
        self.assertIn("| default-1 | incomplete | unavailable |", report)

    def test_combined_report_keeps_each_run_result_and_duration(self):
        with tempfile.TemporaryDirectory() as directory:
            paths = []
            for name, action, elapsed in [
                ("default-1", "pass", 50), ("default-2", "pass", 48),
                ("parallel12-1", "pass", 40), ("parallel12-2", "fail", 42),
            ]:
                path = Path(directory) / f"{name}.jsonl"
                path.write_text(json.dumps({"Action": action, "Elapsed": elapsed}))
                paths.append(path)
            report = summarize(paths)
        self.assertIn("| default-1 | pass | 50.00 |", report)
        self.assertIn("| default-2 | pass | 48.00 |", report)
        self.assertIn("| parallel12-1 | pass | 40.00 |", report)
        self.assertIn("| parallel12-2 | fail | 42.00 |", report)


if __name__ == "__main__":
    unittest.main()
