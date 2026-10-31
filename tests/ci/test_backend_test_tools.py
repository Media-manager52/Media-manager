"""Unit checks for the backend test planning/report helper (no Go, database or cgroup needed)."""
import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("backend_test_tools", ROOT / "scripts" / "ci" / "backend_test_tools.py")
report = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = report
SPEC.loader.exec_module(report)


def event(action, test=None, elapsed=None, output=None):
    body = {"Time": "2026-01-01T00:00:00Z", "Action": action, "Package": "p"}
    if test:
        body["Test"] = test
    if elapsed is not None:
        body["Elapsed"] = elapsed
    if output is not None:
        body["Output"] = output
    return json.dumps(body)


class SummaryTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

    def write(self, name, lines):
        path = self.root / name
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        return path

    def test_subtests_and_package_events_are_not_counted_as_tests(self):
        log = self.write("g.json", [
            event("pass", "TestA", 1.5), event("pass", "TestA/sub", 1.0),
            event("skip", "TestB", 0.0), event("fail", "TestC", 2.0),
            event("pass", None, 4.0),
        ])
        counts, results = report.summarize_group(log)
        self.assertEqual(counts, {"pass": 1, "fail": 1, "skip": 1})
        self.assertEqual(sorted(results), ["TestA", "TestB", "TestC"])
        self.assertEqual(results["TestA"], ("pass", 1.5))

    def test_failure_rendering_restores_output_and_keeps_unparsable_lines(self):
        log = self.write("g.json", [
            event("output", "TestA", output="=== RUN   TestA\n"),
            "# example/pkg [build failed]",
            event("output", "TestA", output="boom\n"),
        ])
        self.assertEqual(report.render_failure(log), "=== RUN   TestA\n# example/pkg [build failed]\nboom\n")

    def test_missing_tests_reports_only_unreported_names(self):
        self.assertEqual(report.missing_tests(["TestA", "TestB", "TestC"], {"TestB": None}), ["TestA", "TestC"])
        self.assertEqual(report.missing_tests([], {"TestB": None}), [])

    def test_group_line_format(self):
        self.assertEqual(
            report.format_group_line("postgres-2", "0", {"pass": 5, "fail": 0, "skip": 1}, "42"),
            "=== postgres-2: exit 0, 6 tests (5 passed, 0 failed, 1 skipped) in 42s ===",
        )


class PlanTest(unittest.TestCase):
    def test_every_test_lands_in_exactly_one_group(self):
        names = [f"Test{index:03d}" for index in range(37)]
        buckets = report.plan_groups(names, 6, {})
        flattened = [name for bucket in buckets for name in bucket]
        self.assertEqual(sorted(flattened), sorted(names))
        self.assertEqual(len(buckets), 6)
        self.assertLessEqual(max(map(len, buckets)) - min(map(len, buckets)), 1)

    def test_known_slow_tests_are_spread_apart_and_balance_the_load(self):
        names = ["Slow1", "Slow2", "Slow3", "Slow4"] + [f"Fast{index}" for index in range(20)]
        weights = {"Slow1": 10.0, "Slow2": 9.0, "Slow3": 8.0, "Slow4": 7.0}
        buckets = report.plan_groups(names, 4, weights)
        self.assertEqual({sum(name.startswith("Slow") for name in bucket) for bucket in buckets}, {1})
        loads = [sum(weights.get(name, report.DEFAULT_WEIGHT) for name in bucket) for bucket in buckets]
        self.assertLess(max(loads) - min(loads), 3.0)

    def test_planning_is_deterministic_and_ignores_stale_weights(self):
        names = ["TestB", "TestA", "TestC"]
        stale = {"TestRemoved": 100.0, "TestA": 5.0}
        self.assertEqual(report.plan_groups(names, 2, stale), report.plan_groups(list(reversed(names)), 2, stale))
        self.assertEqual(report.plan_groups(names, 2, stale)[0], ["TestA"])

    def test_weight_file_skips_comments_and_malformed_lines(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "weights.txt"
            path.write_text("# comment\n12.5 TestSlow  # trailing note\nnot-a-number TestBad\njust-one-field\n\n3 TestOther\n")
            self.assertEqual(report.load_weights(path), {"TestSlow": 12.5, "TestOther": 3.0})
            self.assertEqual(report.load_weights(Path(temp) / "missing.txt"), {})


class ResourceTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / "cgroup"
        self.slice = self.root / "user.slice" / "user-997.slice"
        self.leaf = self.slice / "app.slice" / "runner.service"
        self.leaf.mkdir(parents=True)
        self.proc = Path(self.temp.name) / "proc-cgroup"
        self.proc.write_text("0::/user.slice/user-997.slice/app.slice/runner.service\n")

    def populate(self, usage, throttled):
        (self.leaf / "cpu.max").write_text("max 100000\n")
        (self.leaf / "cpu.stat").write_text(f"usage_usec {usage}\nnr_throttled 1\nthrottled_usec 2\n")
        (self.slice / "cpu.max").write_text("500000 100000\n")
        (self.slice / "cpu.stat").write_text(
            f"usage_usec {usage}\nnr_periods 100\nnr_throttled {throttled}\nthrottled_usec {throttled * 1000000}\n")
        (self.slice / "memory.current").write_text("1073741824\n")
        (self.slice / "memory.peak").write_text("2147483648\n")
        (self.slice / "memory.max").write_text("12884901888\n")
        (self.slice / "memory.events").write_text("low 0\nhigh 0\nmax 7\noom 0\n")

    def test_quota_is_found_on_the_nearest_limited_ancestor(self):
        self.populate(0, 0)
        self.assertEqual(report.limiting_cgroup(self.leaf, self.root), (self.slice, 5.0))
        self.assertIsNone(report.limiting_cgroup(self.root / "elsewhere", self.root))

    def test_snapshot_and_delta_describe_cpu_throttling_and_memory(self):
        self.populate(1_000_000, 3)
        before = report.snapshot(self.root, str(self.proc))
        self.populate(61_000_000, 5)
        (self.slice / "memory.events").write_text("low 0\nhigh 0\nmax 9\noom 0\n")
        after = report.snapshot(self.root, str(self.proc))
        after["time"] = before["time"] + 30
        lines = report.describe_resources(before, after)
        self.assertEqual(
            lines[0], "resources: cgroup user-997.slice: limit 5.00 CPUs; used 60 cpu-s in 30s (average 2.00 CPUs)")
        self.assertEqual(lines[1], "resources: CPU throttled in 2 periods (2.0 thread-s)")
        self.assertEqual(
            lines[2], "resources: memory now 1024 MiB, high-water 2048 MiB, limit 12288 MiB, memory limit reached 2 times")

    def test_missing_cgroup_data_degrades_to_a_single_notice(self):
        snapshot = report.snapshot(self.root / "absent", str(self.proc))
        lines = report.describe_resources(snapshot, dict(snapshot, time=snapshot["time"] + 1))
        self.assertEqual(len(lines), 1)
        self.assertIn("no cgroup accounting available", lines[0])


if __name__ == "__main__":
    unittest.main()
