#!/usr/bin/env python3
"""Find a successful Verify run of a branch whose tree equals this main commit's.

Prints run=<id> to GITHUB_OUTPUT when one exists, so the test jobs can be
skipped; prints run= otherwise and the tests run as usual. This only saves
time: the deployment broker repeats the search and the checks independently,
and refuses a deployment whose skipped tests have no such run."""
import json
import os
import re
import subprocess

repository = os.environ["GITHUB_REPOSITORY"]
sha = os.environ["GITHUB_SHA"]
TESTED_JOBS = {"frontend", "browser", "backend"}


def github(endpoint):
    return json.loads(subprocess.check_output(["gh", "api", "repos/" + repository + "/" + endpoint]))


def tree_of(commit):
    return github("git/commits/" + commit)["tree"]["sha"]


def find():
    tree = tree_of(sha)
    runs = github("actions/workflows/verify.yml/runs?status=success&event=push&per_page=50")["workflow_runs"]
    for run in runs:
        head = run.get("head_sha")
        if (head == sha or run.get("head_branch") == "main" or not isinstance(head, str)
                or not re.fullmatch(r"[0-9a-f]{40}", head)
                or run.get("head_repository", {}).get("full_name") != repository
                or run.get("conclusion") != "success"):
            continue
        if tree_of(head) != tree:
            continue
        jobs = github("actions/runs/" + str(run["id"]) + "/jobs?per_page=100")["jobs"]
        passed = {job["name"] for job in jobs if job.get("conclusion") == "success"}
        if TESTED_JOBS <= passed:
            return run
    return None


run = find()
with open(os.environ["GITHUB_OUTPUT"], "a") as output:
    output.write("run=" + (str(run["id"]) if run else "") + "\n")
with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as summary:
    if run:
        summary.write(f"Tests skipped: Verify run [{run['id']}]({run['html_url']}) already passed "
                      f"commit {run['head_sha']} on {run['head_branch']}, whose tree is identical to {sha}.\n")
    else:
        summary.write("No passing run of an identical tree; every test runs for this commit.\n")
print("reused run:", run["id"] if run else "none")
