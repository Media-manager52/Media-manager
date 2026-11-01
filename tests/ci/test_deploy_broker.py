import copy
import importlib.util
import json
import os
from pathlib import Path
import re
import tempfile
import unittest
from unittest import mock

source = Path(__file__).resolve().parents[2] / "deploy/github/deploy-broker.py"
spec = importlib.util.spec_from_file_location("deploy_broker", source)
broker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(broker)


class DeploymentBoundaryTests(unittest.TestCase):
    def setUp(self):
        self.sha = "a" * 40
        self.request = {"commit": self.sha, "verification_run": 123, "deployment_run": 456}
        common = {"head_sha": self.sha, "head_branch": "main",
                  "repository": {"full_name": broker.REPOSITORY},
                  "head_repository": {"full_name": broker.REPOSITORY}}
        self.verify = dict(copy.deepcopy(common), id=123, event="push", status="completed",
                           conclusion="success", path=".github/workflows/verify.yml")
        self.deploy = dict(copy.deepcopy(common), id=456, event="workflow_dispatch",
                           status="in_progress", path=".github/workflows/deploy.yml",
                           triggering_actor={"login": "Media-manager52"})
        # Shape and runner fields copied from the successful main Verify run
        # 41235746613 (commit 34478c4), as returned by the GitHub jobs API.
        hosted = {"runner_group_name": "GitHub Actions", "labels": ["ubuntu-24.04"]}
        vps = {"runner_name": "media-manager-vps", "runner_group_name": "Default",
               "labels": ["self-hosted", "linux", "x64", "media-manager-vps"]}
        self.jobs = [
            dict(name="frontend", conclusion="success", runner_name="GitHub Actions 1000000518", **copy.deepcopy(hosted)),
            dict(name="containers", conclusion="success", **copy.deepcopy(vps)),
            dict(name="browser", conclusion="success", runner_name="GitHub Actions 1000000517", **copy.deepcopy(hosted)),
            dict(name="backend", conclusion="success", **copy.deepcopy(vps)),
            dict(name="publish", conclusion="success", **copy.deepcopy(vps)),
        ]

    def job(self, name):
        return next(job for job in self.jobs if job["name"] == name)

    def check(self):
        broker.validate_evidence(self.request, self.sha, self.verify, self.jobs, self.deploy)

    def test_verified_current_main_can_deploy(self):
        self.check()

    def test_old_commit_cannot_deploy(self):
        with self.assertRaises(ValueError):
            broker.validate_evidence(self.request, "b" * 40, self.verify, self.jobs, self.deploy)

    def test_failed_or_incomplete_verification_cannot_deploy(self):
        for field, value in [("conclusion", "failure"), ("status", "in_progress"), ("event", "pull_request")]:
            with self.subTest(field=field):
                before = self.verify[field]
                self.verify[field] = value
                with self.assertRaises(ValueError):
                    self.check()
                self.verify[field] = before

    def test_fork_or_wrong_workflow_cannot_deploy(self):
        for field, value in [("head_repository", {"full_name": "external/fork"}),
                             ("repository", {"full_name": "external/repository"}),
                             ("path", ".github/workflows/other.yml"), ("head_branch", "feature")]:
            with self.subTest(field=field):
                before = self.verify[field]
                self.verify[field] = value
                with self.assertRaises(ValueError):
                    self.check()
                self.verify[field] = before

    def test_missing_image_publication_cannot_deploy(self):
        self.jobs = [job for job in self.jobs if job["name"] != "publish"]
        with self.assertRaises(ValueError):
            self.check()

    def test_any_failed_required_job_cannot_deploy(self):
        self.jobs[0]["conclusion"] = "failure"
        with self.assertRaises(ValueError):
            self.check()

    def test_checks_must_run_on_the_dedicated_vps_runner(self):
        self.job("backend")["runner_name"] = "unreviewed-runner"
        with self.assertRaises(ValueError):
            self.check()

    def test_mixed_hosted_and_vps_placement_from_real_verify_run_is_accepted(self):
        self.check()
        self.assertEqual({job["name"] for job in self.jobs}, broker.REQUIRED_JOBS)

    def test_image_building_jobs_cannot_move_to_hosted_runners(self):
        for name in ["backend", "containers", "publish"]:
            with self.subTest(job=name):
                job = self.job(name)
                before = copy.deepcopy(job)
                job.update(runner_name="GitHub Actions 1000000599",
                           runner_group_name="GitHub Actions", labels=["ubuntu-24.04"])
                with self.assertRaises(ValueError):
                    self.check()
                job.clear(); job.update(before)

    def test_hosted_jobs_cannot_run_on_another_self_hosted_runner(self):
        for change in [
                {"runner_name": "unreviewed-runner", "runner_group_name": "Default",
                 "labels": ["self-hosted", "linux", "x64"]},
                {"runner_name": "GitHub Actions 7", "runner_group_name": "GitHub Actions",
                 "labels": ["self-hosted", "ubuntu-24.04"]},
                {"runner_name": "GitHub Actions 7", "runner_group_name": "Default",
                 "labels": ["ubuntu-24.04"]},
                {"runner_name": "GitHub Actions 7", "runner_group_name": "GitHub Actions",
                 "labels": ["ubuntu-22.04"]},
                {"runner_name": "media-manager-vps", "runner_group_name": "GitHub Actions",
                 "labels": ["ubuntu-24.04"]}]:
            with self.subTest(change=change):
                job = self.job("frontend")
                before = copy.deepcopy(job)
                job.update(change)
                with self.assertRaises(ValueError):
                    self.check()
                job.clear(); job.update(before)

    def test_missing_runner_evidence_cannot_deploy(self):
        for name, fields in [("frontend", ["runner_name", "runner_group_name", "labels"]),
                             ("backend", ["runner_name", "labels"])]:
            for field in fields:
                with self.subTest(job=name, field=field):
                    job = self.job(name)
                    before = copy.deepcopy(job)
                    job.pop(field, None)
                    with self.assertRaises(ValueError):
                        self.check()
                    job.clear(); job.update(before)

    def test_skipped_required_job_cannot_deploy(self):
        self.job("publish")["conclusion"] = "skipped"
        with self.assertRaises(ValueError):
            self.check()

    def test_duplicate_required_job_cannot_deploy(self):
        self.jobs.append(dict(copy.deepcopy(self.job("browser")), conclusion="failure"))
        with self.assertRaises(ValueError):
            self.check()


    def test_only_active_owner_triggered_deployment_is_accepted(self):
        for field, value in [("status", "completed"), ("event", "push"), ("id", 789),
                             ("path", ".github/workflows/verify.yml"), ("head_sha", "b" * 40)]:
            with self.subTest(field=field):
                before = self.deploy[field]
                self.deploy[field] = value
                with self.assertRaises(ValueError):
                    self.check()
                self.deploy[field] = before

    def test_request_contains_only_exact_identities(self):
        for request in [{**self.request, "command": "sh"},
                        {**self.request, "commit": "main; sh"},
                        {**self.request, "deployment_run": True},
                        {**self.request, "verification_run": 0},
                        {**self.request, "verification_run": 2**63}, []]:
            with self.subTest(request=request):
                with self.assertRaises(ValueError):
                    broker.validate_request(request)

    def test_another_collaborator_cannot_request_production_deployment(self):
        self.deploy["triggering_actor"] = {"login": "another-collaborator"}
        with self.assertRaises(ValueError):
            self.check()


class VerifyWorkflowPlacementContractTests(unittest.TestCase):
    """The broker's approved placement must match what Verify actually declares."""

    EXPECTED_RUNS_ON = {
        "github-hosted": "ubuntu-24.04",
        "vps": "[self-hosted, linux, x64, media-manager-vps]",
    }

    def declared_runs_on(self):
        workflow = Path(__file__).resolve().parents[2] / ".github/workflows/verify.yml"
        declared, job, in_jobs = {}, None, False
        for line in workflow.read_text().splitlines():
            if line.startswith("jobs:"):
                in_jobs = True
                continue
            if not in_jobs:
                continue
            header = re.fullmatch(r"  ([A-Za-z0-9_-]+):\s*", line)
            if header:
                job = header.group(1)
                continue
            runs_on = re.fullmatch(r"    runs-on:\s*(.+?)\s*", line)
            if job and runs_on:
                declared[job] = runs_on.group(1)
        return declared

    def test_every_required_job_is_declared_on_its_approved_runner(self):
        declared = self.declared_runs_on()
        for name, placement in broker.JOB_PLACEMENT.items():
            with self.subTest(job=name):
                self.assertIn(name, declared, "Verify no longer declares a job the broker requires")
                self.assertEqual(declared[name], self.EXPECTED_RUNS_ON[placement])

    def test_hosted_label_matches_the_broker(self):
        self.assertEqual(broker.HOSTED_LABELS, [self.EXPECTED_RUNS_ON["github-hosted"]])


class DeploymentResultTests(unittest.TestCase):
    def test_polling_runner_sees_only_complete_readable_result(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "456.json"
            result = {"success": True, "commit": "a" * 40}
            replace = os.replace

            def publish(source, destination):
                self.assertFalse(path.exists())
                self.assertEqual(json.loads(source.read_text()), result)
                self.assertEqual(source.stat().st_mode & 0o777, 0o640)
                replace(source, destination)

            with mock.patch.object(broker.os, "chown") as chown, mock.patch.object(
                    broker.pwd, "getpwnam", return_value=mock.Mock(pw_gid=985)), mock.patch.object(
                    broker.os, "replace", side_effect=publish):
                broker.publish_result(path, result)
            chown.assert_called_once()
            self.assertEqual(chown.call_args.args[1:], (0, 985))
            self.assertEqual(json.loads(path.read_text()), result)
            self.assertEqual(list(Path(directory).iterdir()), [path])

    def test_permissions_failure_never_exposes_a_partial_result(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "456.json"
            with mock.patch.object(broker.os, "chown", side_effect=PermissionError), mock.patch.object(
                    broker.pwd, "getpwnam", return_value=mock.Mock(pw_gid=985)):
                with self.assertRaises(PermissionError):
                    broker.publish_result(path, {"success": False})
            self.assertEqual(list(Path(directory).iterdir()), [])


if __name__ == "__main__":
    unittest.main()
