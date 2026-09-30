#!/usr/bin/env python3
"""Root-owned deployment broker: accept only a verified current-main commit."""
import datetime
import fcntl
import json
import os
from pathlib import Path
import pwd
import re
import shutil
import stat
import subprocess
import urllib.request

REPOSITORY = "Media-manager52/Media-manager"
CHECKOUT = Path("/home/vps/media-manager")
STATE = Path("/var/lib/media-manager-deploy")
REQUIRED_JOBS = {"frontend", "browser", "backend", "containers", "publish"}


def validate_request(request):
    if not isinstance(request, dict) or set(request) != {"commit", "verification_run", "deployment_run"}:
        raise ValueError("Invalid deployment request fields")
    if not isinstance(request["commit"], str) or not re.fullmatch(r"[0-9a-f]{40}", request["commit"]):
        raise ValueError("Invalid commit identity")
    for field in ["verification_run", "deployment_run"]:
        if type(request[field]) is not int or not 0 < request[field] < 2**63:
            raise ValueError("Invalid run identity")


def validate_evidence(request, main_sha, verification, jobs, deployment):
    validate_request(request)
    sha = request["commit"]
    if main_sha != sha:
        raise ValueError("Deployment must match current main")
    for run, workflow in [(verification, "verify.yml"), (deployment, "deploy.yml")]:
        if (run.get("head_sha") != sha or run.get("head_branch") != "main"
                or run.get("repository", {}).get("full_name") != REPOSITORY
                or run.get("head_repository", {}).get("full_name") != REPOSITORY
                or run.get("path") != ".github/workflows/" + workflow):
            raise ValueError("Workflow provenance does not match current main")
    if (verification.get("id") != request["verification_run"]
            or verification.get("event") not in ["push", "workflow_dispatch"]
            or verification.get("status") != "completed" or verification.get("conclusion") != "success"):
        raise ValueError("Verification run did not succeed")
    names = {job["name"] for job in jobs}
    if not REQUIRED_JOBS.issubset(names) or any(
            job.get("conclusion") != "success" or job.get("runner_name") != "media-manager-vps"
            for job in jobs if job["name"] in REQUIRED_JOBS):
        raise ValueError("Required VPS checks did not all succeed")
    if (deployment.get("id") != request["deployment_run"]
            or deployment.get("event") != "workflow_dispatch" or deployment.get("status") != "in_progress"):
        raise ValueError("An active owner-triggered deployment is required")
    actor = deployment.get("triggering_actor", deployment.get("actor", {}))
    if actor.get("login") != REPOSITORY.split("/")[0]:
        raise ValueError("Only the repository owner can request production deployment")


def operator(*args, **options):
    return subprocess.check_output(["runuser", "-u", "vps", "--", *args], **options)


def github(endpoint):
    return json.loads(operator("gh", "api", "repos/" + REPOSITORY + "/" + endpoint))


def evidence(request):
    validate_evidence(request, github("commits/main")["sha"],
        github("actions/runs/" + str(request["verification_run"])),
        github("actions/runs/" + str(request["verification_run"]) + "/jobs?per_page=100")["jobs"],
        github("actions/runs/" + str(request["deployment_run"])))


def image_names(sha):
    return [f"ghcr.io/{REPOSITORY.lower()}-{component}:sha-{sha}" for component in ["api", "ui"]]


def write_private(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n")
    path.chmod(0o600)


def deploy(request, log):
    ci_socket = "unix:///run/user/" + str(pwd.getpwnam("mm-ci").pw_uid) + "/docker.sock"
    evidence(request)
    if operator("git", "-C", str(CHECKOUT), "status", "--porcelain", text=True).strip():
        raise RuntimeError("Production checkout has local changes")
    if operator("git", "-C", str(CHECKOUT), "branch", "--show-current", text=True).strip() != "main":
        raise RuntimeError("Production checkout is not on main")
    images = image_names(request["commit"])
    for image in images:
        config = json.loads(subprocess.check_output(["docker", "--host", ci_socket, "image", "inspect", image]))[0]
        labels = config["Config"].get("Labels", {})
        if (labels.get("org.opencontainers.image.revision") != request["commit"]
                or labels.get("org.opencontainers.image.source") != "https://github.com/" + REPOSITORY):
            raise RuntimeError("Built image provenance mismatch")
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%d-%H%M%S")
    backup = Path("/home/vps/media-manager-backups") / ("github-" + stamp + "-" + str(request["deployment_run"]))
    backup.mkdir(mode=0o700)
    for name in [".env", ".env.integrations"]:
        shutil.copy2(CHECKOUT / name, backup / name)
        (backup / name).chmod(0o600)
    previous = {"checkout": operator("git", "-C", str(CHECKOUT), "rev-parse", "HEAD", text=True).strip()}
    previous["images"] = [subprocess.check_output(["docker", "inspect", "media-manager-" + service + "-1",
                                                 "--format", "{{.Image}}"], text=True).strip()
                          for service in ["api", "ui"]]
    write_private(backup / "previous.json", previous)
    previous_override = CHECKOUT / ".sites-runtime/github-deploy-images.json"
    if previous_override.exists():
        shutil.copy2(previous_override, backup / "previous-compose-override.json")
    dump = backup / "media-manager.dump"
    with dump.open("xb") as target:
        dump.chmod(0o600)
        subprocess.run(["docker", "compose", "exec", "-T", "postgres", "sh", "-c",
            'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc'], cwd=CHECKOUT, stdout=target, stderr=log, check=True)
    with dump.open("rb") as source:
        toc = subprocess.check_output(["docker", "compose", "exec", "-T", "postgres", "pg_restore", "-l"],
                                      cwd=CHECKOUT, stdin=source, stderr=log)
    if not dump.stat().st_size or b"TABLE DATA" not in toc:
        raise RuntimeError("Backup failed verification")
    old_archive = backup / "previous-images.tar"
    with old_archive.open("xb") as target:
        old_archive.chmod(0o600)
        subprocess.run(["docker", "image", "save", *previous["images"]], stdout=target, stderr=log, check=True)
    archive = backup / "tested-images.tar"
    with archive.open("xb") as target:
        archive.chmod(0o600)
        subprocess.run(["docker", "--host", ci_socket, "image", "save", *images], stdout=target, stderr=log, check=True)
    with archive.open("rb") as source:
        subprocess.run(["docker", "image", "load"], stdin=source, stdout=log, stderr=log, check=True)
    operator("git", "-C", str(CHECKOUT), "fetch", "github", "main", stderr=log)
    if operator("git", "-C", str(CHECKOUT), "rev-parse", "github/main", text=True).strip() != request["commit"]:
        raise RuntimeError("Main changed during deployment preparation")
    evidence(request)  # Recheck after backup/image transfer, before touching production.
    operator("git", "-C", str(CHECKOUT), "merge", "--ff-only", request["commit"], stderr=log)
    environment_file = CHECKOUT / ".env"
    body = environment_file.read_text()
    for key, value in [("MEDIA_MANAGER_COMMIT", request["commit"]),
                       ("MEDIA_MANAGER_VERSION", "github-" + request["commit"][:12])]:
        pattern = r"(?m)^" + key + r"=.*$"
        body = re.sub(pattern, key + "=" + value, body) if re.search(pattern, body) else body + "\n" + key + "=" + value + "\n"
    environment_file.write_text(body)
    environment_file.chmod(0o600)
    write_private(previous_override, {"services": {service: {"image": image, "pull_policy": "never"}
        for service, image in zip(["api", "ui"], images)}})
    compose = ["docker", "compose", "-f", str(CHECKOUT / "compose.yaml"), "-f", str(previous_override)]
    subprocess.run(compose + ["config", "--quiet"], cwd=CHECKOUT, check=True, stdout=log, stderr=log)
    subprocess.run(compose + ["up", "-d", "--no-build", "--no-deps", "--wait", "--wait-timeout", "180", "api", "ui"],
                   cwd=CHECKOUT, check=True, stdout=log, stderr=log)
    config = json.loads(subprocess.check_output(["docker", "inspect", "media-manager-api-1"]))[0]
    env = dict(item.split("=", 1) for item in config["Config"]["Env"] if "=" in item)
    client = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    request_info = urllib.request.Request("http://127.0.0.1:28081/api/v1/system/info",
                                         headers={"Authorization": "Bearer " + env["OWNER_TOKEN"]})
    with client.open(request_info, timeout=30) as response:
        info = json.load(response)["data"]
    if info["commit"] != request["commit"]:
        raise RuntimeError("Running API source identity mismatch")
    write_private(backup / "deployment.json", {"request": request, "info": info})
    return {"success": True, "commit": request["commit"], "schema": info["schema_version"], "backup_created": True}


def main():
    os.umask(0o077)
    with (STATE / "deployment.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        for path in sorted((STATE / "inbox").glob("*.json")):
            if not re.fullmatch(r"[1-9][0-9]{0,18}\.json", path.name):
                path.unlink()
                continue
            result_file = STATE / "results" / path.name
            log_file = STATE / (path.stem + ".log")
            result = {"success": False, "error": "Deployment rejected or failed; inspect the protected server log."}
            with log_file.open("a") as log:
                log_file.chmod(0o600)
                try:
                    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
                    with os.fdopen(descriptor) as source:
                        metadata = os.fstat(source.fileno())
                        if (not stat.S_ISREG(metadata.st_mode) or metadata.st_size > 2048
                                or metadata.st_uid != pwd.getpwnam("mm-ci").pw_uid or metadata.st_nlink != 1):
                            raise ValueError("Unsafe request file")
                        request = json.load(source)
                    validate_request(request)
                    if str(request["deployment_run"]) != path.stem or result_file.exists():
                        raise ValueError("Duplicate or mismatched deployment request")
                    result = deploy(request, log)
                except Exception as error:
                    log.write(type(error).__name__ + ": " + str(error) + "\n")
            if not result_file.exists():
                write_private(result_file, result)
                os.chown(result_file, 0, pwd.getpwnam("mm-ci").pw_gid)
                result_file.chmod(0o640)
            path.unlink(missing_ok=True)
            print("Deployment run " + path.stem + ": " + ("success" if result["success"] else "failed"), flush=True)


if __name__ == "__main__":
    main()
