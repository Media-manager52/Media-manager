import os
import subprocess
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "validate-release-images.sh"
DIGEST_API = "ghcr.io/media-manager52/media-manager-api@sha256:" + "a" * 64
DIGEST_UI = "ghcr.io/media-manager52/media-manager-ui@sha256:" + "b" * 64


class ReleaseImageValidationTest(unittest.TestCase):
    def run_validator(self, api, ui):
        env = os.environ.copy()
        env["MEDIA_MANAGER_API_IMAGE"] = api
        env["MEDIA_MANAGER_UI_IMAGE"] = ui
        return subprocess.run(["bash", str(SCRIPT)], env=env, text=True, capture_output=True)

    def test_digest_and_immutable_sha_tags_pass(self):
        self.assertEqual(self.run_validator(DIGEST_API, DIGEST_UI).returncode, 0)
        api = "ghcr.io/media-manager52/media-manager-api:sha-" + "c" * 40
        ui = "ghcr.io/media-manager52/media-manager-ui:sha-" + "d" * 40
        self.assertEqual(self.run_validator(api, ui).returncode, 0)

    def test_missing_or_mutable_references_fail(self):
        for api, ui in [
            ("", DIGEST_UI),
            (DIGEST_API, ""),
            ("ghcr.io/media-manager52/media-manager-api:main", DIGEST_UI),
            (DIGEST_API, "ghcr.io/media-manager52/media-manager-ui:latest"),
            ("ghcr.io/media-manager52/media-manager-api:sha-short", DIGEST_UI),
        ]:
            result = self.run_validator(api, ui)
            self.assertNotEqual(result.returncode, 0, (api, ui, result.stdout, result.stderr))
            self.assertTrue("required" in result.stderr or "immutable" in result.stderr, result.stderr)


if __name__ == "__main__":
    unittest.main()
