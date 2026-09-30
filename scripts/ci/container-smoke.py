#!/usr/bin/env python3
"""Build and smoke only disposable containers on the CI Docker engine."""
import json
import os
from pathlib import Path
import secrets
import subprocess
import tempfile
import urllib.error
import urllib.request


def main():
    repo = Path(__file__).resolve().parents[2]
    security = json.loads(subprocess.check_output(
        ["docker", "info", "--format", "{{json .SecurityOptions}}"], text=True))
    if not any("rootless" in item for item in security):
        raise RuntimeError("CI requires its separate rootless Docker engine")
    integration_file = repo / ".env.integrations"
    if integration_file.exists():
        raise RuntimeError("Refusing to reuse an existing integration environment")
    project = "mm-ci-" + secrets.token_hex(6)
    client = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with tempfile.TemporaryDirectory(prefix=project, dir=os.environ.get("RUNNER_TEMP")) as temp:
        root = Path(temp)
        root.chmod(0o755)  # Container UIDs must traverse the disposable mount roots.
        for name in ["altmount", "movies", "tv", "media", "backups"]:
            (root / name).mkdir(mode=0o755)
        token = secrets.token_urlsafe(32)
        commit = os.environ.get("GITHUB_SHA", "local-ci-validation")
        values = {
            "POSTGRES_PASSWORD": secrets.token_urlsafe(32), "OWNER_TOKEN": token,
            "ALTMOUNT_HOST_PATH": str(root / "altmount"),
            "MOVIES_HOST_PATH": str(root / "movies"), "TV_HOST_PATH": str(root / "tv"),
            "MEDIA_LIBRARY_HOST_PATH": str(root / "media"), "MEDIA_NETWORK": project,
            "MEDIA_MANAGER_API_PORT": "38081", "MEDIA_MANAGER_COMMIT": commit,
            "MEDIA_MANAGER_VERSION": "ci", "MEDIA_MANAGER_UID": "65532",
            "MEDIA_MANAGER_GID": "65532", "MEDIA_LIBRARY_GID": "65532",
            "ACQUISITION_ATTEMPTS_PER_ROUND": "4", "ACQUISITION_RETAINED_OUTPUT_LIMIT": "8",
            "METADATA_TMDB_TOKEN_SOURCE_FILE": "/dev/null",
            "METADATA_TVDB_KEY_SOURCE_FILE": "/dev/null",
            "METADATA_SIMKL_CLIENT_ID_SOURCE_FILE": "/dev/null",
            "SEERR_BASE_URL": "", "SEERR_API_KEY": "", "SEERR_INSTANCE_ID": "",
            "SEERR_WEBHOOK_SECRET": "", "SERVARR_API_KEY": "",
            "COMMISSIONING_V2_OWNER_ORIGIN": "http://127.0.0.1:38081",
        }
        environment = root / "environment"
        environment.write_text("".join(f"{key}={value}\n" for key, value in values.items()))
        environment.chmod(0o600)
        # Override the one repository-relative writable volume with a disposable path.
        override = root / "compose.json"
        override.write_text(json.dumps({"services": {"api": {"volumes": [
            {"type": "bind", "source": str(root / "backups"),
             "target": "/var/lib/media-manager/backups"}]}}}))
        command = ["docker", "compose", "-p", project, "--env-file", str(environment),
                   "-f", str(repo / "compose.yaml"), "-f", str(override)]
        integration_file.write_text("# CI intentionally has no integration credentials.\n")
        integration_file.chmod(0o600)
        network_created = False
        try:
            subprocess.run(["docker", "network", "create", "--internal", project], check=True)
            network_created = True
            subprocess.run(command + ["config", "--quiet"], cwd=repo, check=True)
            subprocess.run(command + ["build", "api", "ui"], cwd=repo, check=True)
            subprocess.run(command + ["up", "-d", "--wait", "--wait-timeout", "180"],
                           cwd=repo, check=True)
            base = "http://127.0.0.1:38081"
            for path in ["/health/ready", "/api/v1/dashboard"]:
                request = urllib.request.Request(base + path, headers={"Authorization": "Bearer " + token})
                with client.open(request, timeout=30) as response:
                    assert response.status == 200
            for path, expected, method in [("/api/v1/dashboard", 401, "GET"),
                    ("/api/v1/integrations/seerr/webhook", 503, "POST")]:
                try:
                    client.open(urllib.request.Request(base + path, method=method), timeout=30)
                    raise AssertionError("Unexpected successful anonymous request")
                except urllib.error.HTTPError as error:
                    assert error.code == expected, (path, error.code)
            request = urllib.request.Request(base + "/api/v1/system/info",
                                             headers={"Authorization": "Bearer " + token})
            with client.open(request, timeout=30) as response:
                info = json.load(response)["data"]
            assert info["commit"] == commit
            assert info["acquisition_limits"] == {"attempts_per_round": 4, "retained_failed_outputs": 8}
            with (root / "test.dump").open("wb") as dump:
                subprocess.run(command + ["exec", "-T", "postgres", "sh", "-c",
                    'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc'], cwd=repo, stdout=dump, check=True)
            assert (root / "test.dump").stat().st_size > 0
            print("PASS: isolated API/UI health, authentication, configured limits and database dump")
        finally:
            subprocess.run(command + ["down", "--volumes", "--remove-orphans"], cwd=repo, check=True)
            if network_created:
                subprocess.run(["docker", "network", "rm", project], check=True)
            integration_file.unlink()


if __name__ == "__main__":
    main()
