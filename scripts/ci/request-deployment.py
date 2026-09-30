#!/usr/bin/env python3
"""Submit a commit/run identity; production credentials stay with the broker."""
import json
import os
from pathlib import Path
import subprocess
import time

repository = os.environ["GITHUB_REPOSITORY"]
sha = os.environ["GITHUB_SHA"]
assert repository == "Media-manager52/Media-manager"
assert os.environ["GITHUB_REF"] == "refs/heads/main"


def github(endpoint):
    return json.loads(subprocess.check_output(["gh", "api", "repos/" + repository + "/" + endpoint]))


run = os.environ.get("VERIFICATION_RUN", "").strip()
if not run:
    runs = github("actions/workflows/verify.yml/runs?branch=main&status=success&per_page=20")["workflow_runs"]
    run = next((str(item["id"]) for item in runs if item["head_sha"] == sha), "")
if not run.isdecimal() or not int(run):
    raise RuntimeError("No successful verification for this main commit; run Verify first")
deployment_run = int(os.environ["GITHUB_RUN_ID"])
state = Path("/var/lib/media-manager-deploy")
target = state / "inbox" / (str(deployment_run) + ".json")
temporary = target.with_suffix(".pending")
with temporary.open("x") as output:
    temporary.chmod(0o600)
    json.dump({"commit": sha, "verification_run": int(run), "deployment_run": deployment_run}, output)
temporary.rename(target)
result_path = state / "results" / target.name
deadline = time.monotonic() + 1200
while time.monotonic() < deadline:
    if result_path.exists():
        result = json.loads(result_path.read_text())
        with Path(os.environ["GITHUB_STEP_SUMMARY"]).open("a") as summary:
            summary.write("Deployment result: " + json.dumps(result) + "\n")
        if not result["success"]:
            raise RuntimeError(result["error"])
        print("Deployed tested commit " + sha + "; backup and health checks passed")
        break
    time.sleep(5)
else:
    raise RuntimeError("Deployment timed out; inspect server status before retrying")
