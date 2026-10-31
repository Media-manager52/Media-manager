"""Structural guards for .github/workflows/verify.yml.

The workflow is parsed as text (no YAML dependency) so these checks run on any
runner. They pin the properties that must survive CI performance work: the job
names the deployment broker requires, which jobs may run on the owner's
self-hosted runner, the absence of a pull_request trigger, complete browser
shards, and the database settings the concurrent backend groups depend on.
"""
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
TEXT = (ROOT / ".github" / "workflows" / "verify.yml").read_text(encoding="utf-8")
REQUIRED_JOBS = {"frontend", "browser", "backend", "containers", "publish"}  # deploy/github/deploy-broker.py


def split_jobs(text):
    body = text.split("\njobs:\n", 1)[1]
    jobs, name, lines = {}, None, []
    for line in body.splitlines():
        header = re.match(r"^  ([a-z][a-z0-9-]*):\s*$", line)
        if header:
            if name:
                jobs[name] = "\n".join(lines)
            name, lines = header.group(1), []
        elif name:
            lines.append(line)
    if name:
        jobs[name] = "\n".join(lines)
    return jobs


JOBS = split_jobs(TEXT)


def runs_on(job):
    return re.search(r"^    runs-on: (.+)$", JOBS[job], re.MULTILINE).group(1).strip()


class VerifyWorkflowTest(unittest.TestCase):
    def test_required_job_names_exist_for_the_deployment_broker(self):
        self.assertTrue(REQUIRED_JOBS.issubset(JOBS), sorted(JOBS))

    def test_no_pull_request_trigger_so_fork_code_never_reaches_the_runner(self):
        triggers = TEXT.split("\npermissions:", 1)[0]
        self.assertRegex(triggers, r"(?m)^  push:")
        self.assertNotRegex(triggers, r"(?m)^  pull_request(_target)?:")

    def test_only_container_and_backend_and_publish_use_the_self_hosted_runner(self):
        for job in JOBS:
            expected = job in {"backend", "containers", "publish"}
            self.assertEqual("self-hosted" in runs_on(job), expected, f"{job}: {runs_on(job)}")
        for job in ("backend", "containers", "publish"):
            self.assertIn("media-manager-vps", runs_on(job))

    def test_publish_waits_for_every_required_check(self):
        needs = re.search(r"needs: \[([^\]]+)\]", JOBS["publish"]).group(1)
        self.assertEqual({item.strip() for item in needs.split(",")}, {"frontend", "browser", "backend", "containers"})

    def test_browser_shards_are_complete_and_the_aggregate_requires_all_of_them(self):
        shards = re.search(r'shard: \[([^\]]+)\]', JOBS["browser-shard"]).group(1)
        parsed = [tuple(map(int, item.strip().strip('"').split("/"))) for item in shards.split(",")]
        total = parsed[0][1]
        self.assertEqual(sorted(parsed), [(index, total) for index in range(1, total + 1)])
        self.assertIn("--shard=${{ matrix.shard }}", JOBS["browser-shard"])
        self.assertIn("fail-fast: false", JOBS["browser-shard"])
        aggregate = JOBS["browser"]
        self.assertIn("needs: browser-shard", aggregate)
        self.assertIn("always()", aggregate)
        self.assertIn('test "${SHARDS_RESULT}" = "success"', aggregate)

    def test_browser_image_stays_pinned(self):
        self.assertIn("container: mcr.microsoft.com/playwright:v1.62.1-noble", JOBS["browser-shard"])

    def test_backend_database_is_socket_only_and_always_removed(self):
        backend = JOBS["backend"]
        shards = int(re.search(r'BACKEND_TEST_SHARDS: "(\d+)"', backend).group(1))
        self.assertTrue(1 <= shards <= 8)
        self.assertNotIn("services:", backend)  # no published host port
        self.assertNotIn("35432", backend)
        start = backend.index("postgres-container.sh start")
        wait = backend.index("postgres-container.sh wait")
        tests = backend.index("bash scripts/ci/test-backend.sh")
        migrate = backend.index("go run ./cmd/media-manager migrate")
        self.assertLess(start, wait)
        self.assertLess(wait, tests)
        self.assertLess(tests, migrate)
        self.assertRegex(backend, r"- if: always\(\)\n\s+run: bash scripts/ci/postgres-container.sh stop")
        # The job must not pin its own database URLs over the ones the start step exports.
        self.assertNotIn("DATABASE_URL:", backend)

    def test_the_container_script_keeps_the_settings_the_concurrent_groups_need(self):
        script = (ROOT / "scripts" / "ci" / "postgres-container.sh").read_text(encoding="utf-8")
        self.assertGreaterEqual(int(re.search(r"max_locks_per_transaction=(\d+)", script).group(1)), 256)
        self.assertGreaterEqual(int(re.search(r"max_connections=(\d+)", script).group(1)), 100)
        self.assertIn("listen_addresses=", script)
        self.assertNotIn("--publish", script)
        self.assertNotIn("--privileged", script)

    def test_backend_keeps_every_gate(self):
        backend = JOBS["backend"]
        for step in ('test -z "$(gofmt -l cmd internal)"', "go vet ./cmd/... ./internal/...",
                     "bash scripts/ci/test-backend.sh", "go run ./cmd/media-manager migrate",
                     "git diff --exit-code -- go.mod go.sum"):
            self.assertIn(step, backend)
        self.assertIn("python3 scripts/ci/container-smoke.py", JOBS["containers"])


if __name__ == "__main__":
    unittest.main()
