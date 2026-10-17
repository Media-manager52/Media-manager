#!/usr/bin/env python3
"""Build and smoke only disposable containers on the CI Docker engine."""
import json
import os
from pathlib import Path
import secrets
import subprocess
import tempfile


# Probe from the UI's isolated frontend network, without depending on host
# port forwarding from an entirely internal stack.
HTTP_PROBE = r"""
const token = require('node:fs').readFileSync(0, 'utf8').trim();
async function expect(path, status, options = {}, base = 'http://api:8080') {
  const response = await fetch(base + path, {
    ...options, signal: AbortSignal.timeout(30000), redirect: 'error',
  });
  if (response.status !== status) throw new Error(`${path}: expected ${status}, got ${response.status}`);
  return response;
}
(async () => {
  const artwork = await expect('/media/movie-placeholder.svg', 200, {}, 'http://127.0.0.1:3000');
  if (!(await artwork.text()).trimStart().startsWith('<svg ')) throw new Error('UI static artwork is unavailable');
  const owner = {headers: {Authorization: 'Bearer ' + token}};
  await expect('/health/ready', 200, owner);
  await expect('/api/v1/dashboard', 200, owner);
  await expect('/api/v1/dashboard', 401);
  await expect('/api/v1/integrations/seerr/webhook', 503, {method: 'POST'});
  const response = await expect('/api/v1/system/info', 200, owner);
  process.stdout.write(JSON.stringify((await response.json()).data));
})().catch(error => { console.error(error.message); process.exit(1); });
"""


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
    with tempfile.TemporaryDirectory(prefix=project, dir=os.environ.get("RUNNER_TEMP")) as temp:
        root = Path(temp)
        root.chmod(0o755)  # Container UIDs must traverse the disposable mount roots.
        for name in ["altmount", "movies", "tv", "media", "backups"]:
            (root / name).mkdir(mode=0o755)
        token = secrets.token_urlsafe(32)
        commit = os.environ.get("GITHUB_SHA", "local-ci-validation")
        values = {
            "POSTGRES_PASSWORD": secrets.token_urlsafe(32), "OWNER_TOKEN": token,
            # Parent of the "altmount" directory; mounted at /mnt in the API.
            "ALTMOUNT_HOST_PARENT_PATH": str(root),
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
            try:
                subprocess.run(command + ["up", "-d", "--wait", "--wait-timeout", "180"],
                               cwd=repo, check=True)
            except subprocess.CalledProcessError:
                # This stack contains only disposable CI credentials and no
                # integration configuration. Capture startup errors before cleanup.
                subprocess.run(command + ["logs", "--no-color", "--tail", "80"], cwd=repo)
                raise
            info = json.loads(subprocess.check_output(
                command + ["exec", "-T", "ui", "node", "-e", HTTP_PROBE],
                input=token, text=True, cwd=repo))
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
