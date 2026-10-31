"""Regression checks for the parallel backend test runner.

The CI runner must keep every gate (full coverage, `-race`, `-count=1`), report
one timing line per group, and keep printing the original failure diagnostics.
These tests stub `go` so they need neither a database nor a Go toolchain.
"""
import os
import re
import stat
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "ci" / "test-backend.sh"

STUB_GO = r"""#!/usr/bin/env bash
set -u
fail_name="${STUB_GO_FAIL_NAME:-}"
status=0
emit() {
  printf '{"Time":"2026-01-01T00:00:00Z","Action":"%s","Package":"stub/pkg","Test":"%s","Elapsed":0.01}\n' "$1" "$2"
}
output() {
  printf '{"Time":"2026-01-01T00:00:00Z","Action":"output","Package":"stub/pkg","Output":"%s"}\n' "$1"
}
if [[ "$1" == "list" ]]; then
  printf 'example/pkg/one\nexample/pkg/two\n'
  exit 0
fi
for arg in "$@"; do
  if [[ "$arg" == "-list" ]]; then
    printf 'TestOne\nTestTwo\nTestThree\n'
    exit 0
  fi
done
run_pattern=""
previous=""
for arg in "$@"; do
  if [[ "$previous" == "-run" ]]; then run_pattern="$arg"; fi
  previous="$arg"
done
if [[ -n "${STUB_GO_ARGS_LOG:-}" ]]; then
  printf '%s\n' "$*" >>"${STUB_GO_ARGS_LOG}"
fi
if [[ -z "$run_pattern" ]]; then
  emit pass TestUnit
  emit pass "TestUnit/sub"
  emit skip TestSkipped
  output "ok  \tstub/pkg\t0.02s\n"
  exit 0
fi
if [[ "$run_pattern" == *TestOne* ]]; then
  emit pass TestOne
  emit pass "TestOne/sub"
fi
if [[ "$run_pattern" == *TestTwo* ]]; then
  if [[ "$fail_name" == "TestTwo" ]]; then
    emit fail TestTwo
    output "boom: TestTwo failed\n"
    status=1
  else
    emit pass TestTwo
  fi
fi
if [[ "$run_pattern" == *TestThree* ]]; then
  emit pass TestThree
fi
output "ok  \tstub/pkg\t0.02s\n"
exit "$status"
"""


class BackendTestGroupsTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        root = Path(self.temp.name)
        stub = root / "go"
        stub.write_text(STUB_GO, encoding="utf-8")
        stub.chmod(stub.stat().st_mode | stat.S_IXUSR)
        self.args_log = root / "go-args.log"
        self.env = os.environ.copy()
        self.env.update(
            {
                "PATH": f"{root}{os.pathsep}{self.env.get('PATH', '')}",
                "RUNNER_TEMP": str(root),
                "MEDIA_MANAGER_TEST_DATABASE_URL": "postgres://stub/db",
                "STUB_GO_ARGS_LOG": str(self.args_log),
            }
        )

    def run_script(self, **overrides):
        env = dict(self.env)
        env.update(overrides)
        return subprocess.run(
            ["bash", str(SCRIPT)], env=env, text=True, capture_output=True, cwd=str(ROOT)
        )

    def test_each_group_reports_name_status_count_and_elapsed(self):
        result = self.run_script()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        other = re.search(
            r"^=== other-packages: exit 0, 2 tests \(1 passed, 0 failed, 1 skipped\) in \d+s ===$",
            result.stdout,
            re.MULTILINE,
        )
        self.assertIsNotNone(other, result.stdout)
        for group in ("postgres-0", "postgres-1", "postgres-2"):
            line = re.search(
                rf"^=== {group}: exit 0, 1 tests \(1 passed, 0 failed, 0 skipped\) in \d+s ===$",
                result.stdout,
                re.MULTILINE,
            )
            self.assertIsNotNone(line, result.stdout)
        self.assertIn(
            "all backend tests passed in 4 parallel groups (3 PostgreSQL tests, 5 tests total) in ",
            result.stdout,
        )

    def test_every_group_keeps_race_detection_and_count_one(self):
        self.assertEqual(self.run_script().returncode, 0)
        invocations = self.args_log.read_text(encoding="utf-8").splitlines()
        self.assertEqual(len(invocations), 4, invocations)
        for invocation in invocations:
            self.assertIn("-count=1", invocation)
            self.assertIn("-race", invocation)
            self.assertIn("-timeout 20m", invocation)
            self.assertIn("-json", invocation)

    def test_failures_keep_the_plain_text_diagnostics(self):
        result = self.run_script(STUB_GO_FAIL_NAME="TestTwo")
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("=== postgres-1: exit 1, 1 tests (0 passed, 1 failed, 0 skipped) in ", result.stdout)
        self.assertIn("boom: TestTwo failed", result.stderr)

    def test_missing_database_and_invalid_shard_count_are_rejected(self):
        missing = self.run_script(MEDIA_MANAGER_TEST_DATABASE_URL="")
        self.assertEqual(missing.returncode, 64, missing.stderr)
        self.assertIn("MEDIA_MANAGER_TEST_DATABASE_URL is required", missing.stderr)
        invalid = self.run_script(BACKEND_TEST_SHARDS="9")
        self.assertEqual(invalid.returncode, 64, invalid.stderr)
        self.assertIn("BACKEND_TEST_SHARDS must be an integer from 1 through 8", invalid.stderr)


if __name__ == "__main__":
    unittest.main()
