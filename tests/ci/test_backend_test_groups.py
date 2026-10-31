"""Regression checks for the parallel backend test runner.

The runner must keep every gate (full coverage, `-race`, `-count=1`), give each
group its own disposable database, drop those databases again, report one timing
line per group, fail when a discovered test reports nothing, and keep printing
the original failure diagnostics. These tests stub `go` and `psql`, so they need
neither a database nor a Go toolchain.
"""
import os
import re
import shutil
import stat
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "ci" / "test-backend.sh"
ADMIN_URL = "postgres://ci_user:s3cret@127.0.0.1:35432/admin_db?sslmode=disable"

STUB_GO = r"""#!/usr/bin/env bash
set -u
emit() {
  printf '{"Time":"2026-01-01T00:00:00Z","Action":"%s","Package":"stub/pkg","Test":"%s","Elapsed":%s}\n' "$1" "$2" "${3:-0.01}"
}
output() {
  printf '{"Time":"2026-01-01T00:00:00Z","Action":"output","Package":"stub/pkg","Output":"%s"}\n' "$1"
}
if [[ "$1" == "list" ]]; then
  root="${STUB_REPO_ROOT}"
  printf 'example/pkg/one /nonexistent/internal/one\nexample/pkg/two /nonexistent/internal/two\n'
  printf 'github.com/media-manager/media-manager/internal/postgres %s/internal/postgres\n' "$root"
  printf 'github.com/media-manager/media-manager/internal/httpapi %s/internal/httpapi\n' "$root"
  exit 0
fi
for arg in "$@"; do
  if [[ "$arg" == "-list" ]]; then
    printf 'TestOne\nTestTwo\nTestThree\nok  \tstub/pkg\t0.01s\n'
    exit 0
  fi
done
run_pattern=""
previous=""
for arg in "$@"; do
  if [[ "$previous" == "-run" ]]; then run_pattern="$arg"; fi
  previous="$arg"
done
if [[ -n "${STUB_GO_LOG:-}" ]]; then
  printf '%s|%s\n' "${MEDIA_MANAGER_TEST_DATABASE_URL:-}" "$*" >>"${STUB_GO_LOG}"
fi
status=0
if [[ -z "$run_pattern" ]]; then
  emit pass TestUnit
  emit pass "TestUnit/sub"
  emit skip TestSkipped
  output "ok  \tstub/pkg\t0.02s\n"
  exit 0
fi
if [[ "$run_pattern" == *TestOne* && "${STUB_GO_OMIT_NAME:-}" != "TestOne" ]]; then
  emit pass TestOne 2.50
  emit pass "TestOne/sub"
fi
if [[ "$run_pattern" == *TestTwo* ]]; then
  if [[ "${STUB_GO_FAIL_NAME:-}" == "TestTwo" ]]; then
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

STUB_PSQL = r"""#!/usr/bin/env bash
printf '%s|PGPASSWORD=%s|PGHOST=%s|PGPORT=%s|PGUSER=%s|PGSSLMODE=%s\n' \
  "$*" "${PGPASSWORD:-}" "${PGHOST:-}" "${PGPORT:-}" "${PGUSER:-}" "${PGSSLMODE:-}" >>"${STUB_PSQL_LOG}"
exit 0
"""


def write_stub(directory, name, body):
    path = directory / name
    path.write_text(body, encoding="utf-8")
    path.chmod(path.stat().st_mode | stat.S_IXUSR)


class BackendTestGroupsTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        write_stub(self.bin, "go", STUB_GO)
        write_stub(self.bin, "psql", STUB_PSQL)
        self.go_log = self.root / "go.log"
        self.psql_log = self.root / "psql.log"
        self.env = os.environ.copy()
        self.env.update(
            {
                "PATH": f"{self.bin}{os.pathsep}{self.env.get('PATH', '')}",
                "RUNNER_TEMP": str(self.root),
                "MEDIA_MANAGER_TEST_DATABASE_URL": ADMIN_URL,
                "STUB_GO_LOG": str(self.go_log),
                "STUB_PSQL_LOG": str(self.psql_log),
                "STUB_REPO_ROOT": str(ROOT),
            }
        )
        self.env.pop("BACKEND_TEST_SHARDS", None)
        self.env.pop("BACKEND_TEST_DATABASE_ISOLATION", None)

    def run_script(self, **overrides):
        env = dict(self.env)
        env.update(overrides)
        return subprocess.run(
            ["bash", str(SCRIPT)], env=env, text=True, capture_output=True, cwd=str(ROOT)
        )

    def go_runs(self):
        return self.go_log.read_text(encoding="utf-8").splitlines()

    def psql_calls(self):
        return self.psql_log.read_text(encoding="utf-8").splitlines() if self.psql_log.exists() else []

    def test_each_group_reports_name_status_count_and_elapsed(self):
        result = self.run_script(BACKEND_TEST_SHARDS="3")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        for group in ("other-packages", "pkg-httpapi"):
            self.assertRegex(
                result.stdout,
                rf"(?m)^=== {group}: exit 0, 2 tests \(1 passed, 0 failed, 1 skipped\) in \d+s ===$",
            )
        for group in ("postgres-0", "postgres-1", "postgres-2"):
            self.assertRegex(
                result.stdout,
                rf"(?m)^=== {group}: exit 0, 1 tests \(1 passed, 0 failed, 0 skipped\) in \d+s ===$",
            )
        self.assertIn("slowest:    2.5s TestOne (postgres-0)", result.stdout)
        self.assertIn("3 of 3 PostgreSQL tests accounted for", result.stdout)
        self.assertRegex(
            result.stdout,
            r"all backend tests passed in 5 parallel groups \(3 PostgreSQL tests, database databases\) in \d+s",
        )

    def test_every_group_keeps_race_detection_and_count_one(self):
        self.assertEqual(self.run_script(BACKEND_TEST_SHARDS="3").returncode, 0)
        invocations = self.go_runs()
        self.assertEqual(len(invocations), 5, invocations)
        for invocation in invocations:
            self.assertIn("-count=1", invocation)
            self.assertIn("-race", invocation)
            self.assertIn("-timeout 20m", invocation)
            self.assertIn("-json", invocation)

    def test_default_shard_count_never_creates_empty_groups(self):
        result = self.run_script()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        # The stub has three tests, so only three non-empty PostgreSQL groups run.
        self.assertEqual(len(self.go_runs()), 5)

    def test_database_packages_run_in_their_own_group_and_leave_other_packages(self):
        self.assertEqual(self.run_script(BACKEND_TEST_SHARDS="3").returncode, 0)
        package_runs = [line for line in self.go_runs() if "-run" not in line]
        self.assertEqual(len(package_runs), 2, package_runs)
        by_target = {("internal/httpapi" in line): line for line in package_runs}
        self.assertNotIn("internal/httpapi", by_target[False])
        self.assertIn("example/pkg/one example/pkg/two", by_target[False])
        self.assertTrue(by_target[True].endswith("github.com/media-manager/media-manager/internal/httpapi"))
        # Neither group may include the PostgreSQL package, which has its own groups.
        for line in package_runs:
            self.assertNotIn("internal/postgres", line)
        self.assertNotEqual(by_target[True].split("|", 1)[0], by_target[False].split("|", 1)[0])

    def test_groups_get_distinct_databases_and_they_are_dropped(self):
        self.assertEqual(self.run_script(BACKEND_TEST_SHARDS="3").returncode, 0)
        urls = [line.split("|", 1)[0] for line in self.go_runs()]
        self.assertEqual(len(set(urls)), 5, urls)
        for url in urls:
            self.assertRegex(
                url,
                r"^postgres://ci_user:s3cret@127\.0\.0\.1:35432/mm_ci_[0-9a-f]{12}_[a-z0-9_]+\?sslmode=disable$",
            )
            self.assertNotIn("/admin_db", url)
        calls = self.psql_calls()
        created = [c for c in calls if "CREATE DATABASE" in c]
        dropped = [c for c in calls if "DROP DATABASE IF EXISTS" in c]
        self.assertEqual(len(created), 5, calls)
        self.assertEqual(len(dropped), 5, calls)
        for call in calls:
            self.assertIn("PGPASSWORD=s3cret", call)
            self.assertIn("PGHOST=127.0.0.1|PGPORT=35432|PGUSER=ci_user|PGSSLMODE=disable", call)
            # The password must never appear on the psql command line.
            self.assertNotIn("s3cret", call.split("|", 1)[0])
            self.assertIn("-d admin_db", call)

    def test_failures_keep_the_plain_text_diagnostics_and_still_drop_databases(self):
        result = self.run_script(BACKEND_TEST_SHARDS="3", STUB_GO_FAIL_NAME="TestTwo")
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("=== postgres-2: exit 1, 1 tests (0 passed, 1 failed, 0 skipped) in ", result.stdout)
        self.assertIn("boom: TestTwo failed", result.stderr)
        self.assertEqual(len([c for c in self.psql_calls() if "DROP DATABASE" in c]), 5)

    def test_a_discovered_test_that_reports_nothing_fails_the_run(self):
        result = self.run_script(BACKEND_TEST_SHARDS="3", STUB_GO_OMIT_NAME="TestOne")
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("1 discovered PostgreSQL tests reported no result: TestOne", result.stderr)

    def test_unix_socket_urls_keep_the_socket_directory_for_psql_and_every_group(self):
        socket_url = "postgres://ci_user:s3cret@/admin_db?host=/tmp/pg-socket&sslmode=disable"
        result = self.run_script(BACKEND_TEST_SHARDS="3", MEDIA_MANAGER_TEST_DATABASE_URL=socket_url)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        for line in self.go_runs():
            self.assertRegex(
                line.split("|", 1)[0],
                r"^postgres://ci_user:s3cret@/mm_ci_[0-9a-f]{12}_[a-z0-9_]+\?host=/tmp/pg-socket&sslmode=disable$",
            )
        for call in self.psql_calls():
            self.assertIn("PGHOST=/tmp/pg-socket|PGPORT=5432|PGUSER=ci_user|PGSSLMODE=disable", call)

    def test_socket_url_port_comes_from_the_query_and_a_missing_host_is_rejected(self):
        with_port = "postgres://ci_user@/admin_db?host=/tmp/pg-socket&port=45432"
        self.assertEqual(self.run_script(BACKEND_TEST_SHARDS="3", MEDIA_MANAGER_TEST_DATABASE_URL=with_port).returncode, 0)
        self.assertTrue(all("PGPORT=45432" in call for call in self.psql_calls()))
        self.assertTrue(all("@/mm_ci_" in line.split("|", 1)[0] for line in self.go_runs()))
        missing = self.run_script(MEDIA_MANAGER_TEST_DATABASE_URL="postgres://ci_user@/admin_db?sslmode=disable")
        self.assertEqual(missing.returncode, 64, missing.stderr)
        self.assertIn("needs a host", missing.stderr)

    def test_shared_isolation_reuses_the_admin_database_without_psql(self):
        result = self.run_script(BACKEND_TEST_SHARDS="3", BACKEND_TEST_DATABASE_ISOLATION="shared")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual({line.split("|", 1)[0] for line in self.go_runs()}, {ADMIN_URL})
        self.assertEqual(self.psql_calls(), [])

    def test_missing_psql_is_rejected_unless_databases_are_shared(self):
        tools = self.root / "tools"
        tools.mkdir()
        for name in ("bash", "python3", "mktemp", "od", "tr", "seq", "grep", "cat", "date", "dirname", "env", "rm"):
            located = shutil.which(name)
            self.assertIsNotNone(located, name)
            (tools / name).symlink_to(located)
        shutil.copy(self.bin / "go", tools / "go")
        result = subprocess.run(
            [shutil.which("bash"), str(SCRIPT)],
            env={**self.env, "PATH": str(tools), "BACKEND_TEST_SHARDS": "3"},
            text=True, capture_output=True, cwd=str(ROOT),
        )
        self.assertEqual(result.returncode, 69, result.stdout + result.stderr)
        self.assertIn("psql is required", result.stderr)

    def test_invalid_inputs_are_rejected(self):
        missing = self.run_script(MEDIA_MANAGER_TEST_DATABASE_URL="")
        self.assertEqual(missing.returncode, 64, missing.stderr)
        self.assertIn("MEDIA_MANAGER_TEST_DATABASE_URL is required", missing.stderr)
        invalid = self.run_script(BACKEND_TEST_SHARDS="9")
        self.assertEqual(invalid.returncode, 64, invalid.stderr)
        self.assertIn("BACKEND_TEST_SHARDS must be an integer from 1 through 8", invalid.stderr)
        isolation = self.run_script(BACKEND_TEST_DATABASE_ISOLATION="none")
        self.assertEqual(isolation.returncode, 64, isolation.stderr)
        malformed = self.run_script(MEDIA_MANAGER_TEST_DATABASE_URL="not-a-url")
        self.assertEqual(malformed.returncode, 64, malformed.stderr)
        self.assertIn("must look like postgres://", malformed.stderr)


if __name__ == "__main__":
    unittest.main()
