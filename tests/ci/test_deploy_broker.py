import copy
import importlib.util
from pathlib import Path
import unittest

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
                           status="in_progress", path=".github/workflows/deploy.yml")
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


if __name__ == "__main__":
    unittest.main()
