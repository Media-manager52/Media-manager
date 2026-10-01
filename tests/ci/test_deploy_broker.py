import copy
import importlib.util
import json
import os
from pathlib import Path
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
        self.jobs = [{"name": name, "conclusion": "success", "runner_name": "media-manager-vps"}
                     for name in broker.REQUIRED_JOBS]

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
        self.jobs[0]["runner_name"] = "unreviewed-runner"
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
