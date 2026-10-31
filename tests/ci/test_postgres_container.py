"""Regression checks for the disposable, socket-only PostgreSQL used by the backend gate.

`docker` is stubbed, so these tests need no container engine. They pin the
safety properties: rootless engine only, no published ports, no privileged
mode, a per-run random password that never appears on a command line, the
tuning the concurrent test groups rely on, and clean teardown.
"""
import os
import re
import stat
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "ci" / "postgres-container.sh"

STUB_DOCKER = r"""#!/usr/bin/env bash
printf '%s\n' "$*" >>"${STUB_DOCKER_LOG}"
case "$1" in
  ps) echo "${STUB_PS_IDS:-}" ;;
  inspect) echo "${STUB_INSPECT_CREATED:-}" ;;
  info) echo "${STUB_DOCKER_SECURITY:-[\"name=seccomp,profile=builtin\",\"name=rootless\"]}" ;;
  logs) [[ "${STUB_NEVER_READY:-}" == 1 ]] || echo "PostgreSQL init process complete; ready for start up." ;;
  exec) [[ "${STUB_NEVER_READY:-}" == 1 ]] && exit 2; exit 0 ;;
  run)
    # Capture the env file the real engine would read, then let the script delete it.
    for argument in "$@"; do
      if [[ "${previous:-}" == "--env-file" ]]; then cp "$argument" "${STUB_ENV_COPY}"; fi
      previous="$argument"
    done ;;
esac
exit 0
"""


class PostgresContainerTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        root = Path(self.temp.name)
        self.bin = root / "bin"
        self.bin.mkdir()
        docker = self.bin / "docker"
        docker.write_text(STUB_DOCKER, encoding="utf-8")
        docker.chmod(docker.stat().st_mode | stat.S_IXUSR)
        self.runner_temp = root / "runner-temp"
        self.runner_temp.mkdir()
        self.log = root / "docker.log"
        self.github_env = root / "github-env"
        self.env_copy = root / "env-copy"
        self.env = os.environ.copy()
        self.env.update(
            {
                "PATH": f"{self.bin}{os.pathsep}{self.env['PATH']}",
                "RUNNER_TEMP": str(self.runner_temp),
                "GITHUB_RUN_ID": "42",
                "GITHUB_RUN_ATTEMPT": "1",
                "GITHUB_JOB": "backend",
                "GITHUB_ENV": str(self.github_env),
                "STUB_DOCKER_LOG": str(self.log),
                "STUB_ENV_COPY": str(self.env_copy),
            }
        )

    def run_script(self, action, **overrides):
        env = dict(self.env)
        env.update(overrides)
        return subprocess.run(["bash", str(SCRIPT), action], env=env, text=True, capture_output=True)

    def docker_calls(self):
        return self.log.read_text(encoding="utf-8").splitlines() if self.log.exists() else []

    def test_start_runs_an_unprivileged_socket_only_container_with_the_tuning(self):
        result = self.run_script("start")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        run = next(call for call in self.docker_calls() if call.startswith("run "))
        for expected in (
            "--name mm-ci-pg-42-1-backend", "--label mm-ci=postgres", "postgres:18.6-alpine3.24",
            f"--volume {self.runner_temp}/mm-ci-pg-42-1-backend-socket:/var/run/postgresql",
            "-c listen_addresses=", "-c max_locks_per_transaction=256", "-c max_connections=200",
            "-c fsync=off", "-c synchronous_commit=off", "-c full_page_writes=off",
        ):
            self.assertIn(expected, run)
        self.assertNotRegex(run, r"(--publish|\s-p\s|--privileged|--network\s+host|--cap-add|docker\.sock)")
        socket_dir = self.runner_temp / "mm-ci-pg-42-1-backend-socket"
        self.assertEqual(stat.S_IMODE(socket_dir.stat().st_mode), 0o777)

    def test_password_is_random_masked_and_never_on_a_command_line(self):
        self.assertEqual(self.run_script("start").returncode, 0)
        env_text = self.env_copy.read_text(encoding="utf-8")
        password = re.search(r"POSTGRES_PASSWORD=([0-9a-f]{32})", env_text).group(1)
        self.assertIn("POSTGRES_USER=media_manager", env_text)
        self.assertIn("POSTGRES_DB=media_manager_test", env_text)
        for call in self.docker_calls():
            self.assertNotIn(password, call)
        # A second start must not reuse the credential.
        self.log.unlink()
        self.assertEqual(self.run_script("start").returncode, 0)
        self.assertNotIn(password, self.env_copy.read_text(encoding="utf-8"))
        self.assertFalse(any(self.runner_temp.glob("*.env")), "env file must be removed after docker run")

    def test_start_publishes_socket_urls_for_the_test_and_migrate_steps(self):
        result = self.run_script("start")
        self.assertRegex(result.stdout, r"::add-mask::[0-9a-f]{32}")
        exported = dict(line.split("=", 1) for line in self.github_env.read_text(encoding="utf-8").splitlines())
        self.assertEqual(exported["MEDIA_MANAGER_TEST_DATABASE_URL"], exported["DATABASE_URL"])
        self.assertRegex(
            exported["DATABASE_URL"],
            rf"^postgres://media_manager:[0-9a-f]{{32}}@/media_manager_test\?host={re.escape(str(self.runner_temp))}"
            r"/mm-ci-pg-42-1-backend-socket&sslmode=disable$",
        )
        self.assertIn("test", exported["DATABASE_URL"].lower())  # tests refuse URLs without it

    def test_wait_requires_the_init_marker_and_a_ready_server(self):
        self.assertEqual(self.run_script("wait").returncode, 0)
        self.assertTrue(any(call.startswith("exec mm-ci-pg-42-1-backend pg_isready") for call in self.docker_calls()))

    def test_wait_fails_loudly_when_the_server_never_becomes_ready(self):
        # Use a stub `sleep` so the 120-attempt loop does not take two minutes.
        fast = self.bin / "sleep"
        fast.write_text("#!/usr/bin/env bash\nexit 0\n", encoding="utf-8")
        fast.chmod(fast.stat().st_mode | stat.S_IXUSR)
        result = self.run_script("wait", STUB_NEVER_READY="1")
        self.assertEqual(result.returncode, 1)
        self.assertIn("did not become ready", result.stderr)

    def test_stop_removes_the_container_and_the_socket_directory(self):
        self.assertEqual(self.run_script("start").returncode, 0)
        result = self.run_script("stop")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        calls = self.docker_calls()
        self.assertTrue(any(call.startswith("stop --time 30 mm-ci-pg-42-1-backend") for call in calls))
        self.assertTrue(any(call.startswith("rm -f mm-ci-pg-42-1-backend") for call in calls))
        self.assertFalse((self.runner_temp / "mm-ci-pg-42-1-backend-socket").exists())

    def test_stop_cleans_files_left_by_the_container_user_through_a_throwaway_container(self):
        self.assertEqual(self.run_script("start").returncode, 0)
        (self.runner_temp / "mm-ci-pg-42-1-backend-socket" / ".s.PGSQL.5432.lock").write_text("stale")
        self.assertEqual(self.run_script("stop").returncode, 0)
        self.assertTrue(any(call.startswith("run --rm --volume") and "rm -rf /cleanup" in call for call in self.docker_calls()))

    def test_only_stale_labelled_containers_are_reaped_on_start(self):
        old = self.run_script("start", STUB_PS_IDS="oldid", STUB_INSPECT_CREATED="2020-01-01T00:00:00Z")
        self.assertEqual(old.returncode, 0, old.stderr)
        self.assertIn("rm -f oldid", self.docker_calls())
        self.log.unlink()
        recent = self.run_script("start", STUB_PS_IDS="freshid",
                                 STUB_INSPECT_CREATED=subprocess.check_output(["date", "-u", "+%Y-%m-%dT%H:%M:%SZ"], text=True).strip())
        self.assertEqual(recent.returncode, 0, recent.stderr)
        self.assertNotIn("rm -f freshid", self.docker_calls())

    def test_a_rootful_engine_is_refused(self):
        result = self.run_script("start", STUB_DOCKER_SECURITY='["name=seccomp,profile=builtin"]')
        self.assertEqual(result.returncode, 77, result.stderr)
        self.assertIn("rootless", result.stderr)
        self.assertFalse(any(call.startswith("run ") for call in self.docker_calls()))

    def test_unsafe_names_and_unknown_actions_are_rejected(self):
        self.assertEqual(self.run_script("start", GITHUB_JOB="back end; rm").returncode, 64)
        self.assertEqual(self.run_script("start", RUNNER_TEMP=str(self.runner_temp) + "/with space").returncode, 64)
        self.assertEqual(self.run_script("explode").returncode, 64)


if __name__ == "__main__":
    unittest.main()
