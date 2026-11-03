"""The Go toolchain is pinned to one exact version everywhere it is chosen.

Tests in CI, the production image and local builds must use the same Go
release: a floating tag silently changes the standard library (and its
security fixes) between a tested commit and the image that ships.
"""
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


class ToolchainPinTest(unittest.TestCase):
    def test_go_version_matches_everywhere(self):
        go_mod = (ROOT / "go.mod").read_text()
        toolchain = re.search(r"^toolchain go(\d+\.\d+\.\d+)$", go_mod, re.M)
        self.assertIsNotNone(toolchain, "go.mod must pin an exact toolchain")
        version = toolchain.group(1)

        dockerfile = (ROOT / "Dockerfile").read_text()
        images = re.findall(r"^FROM golang:(\S+)", dockerfile, re.M)
        self.assertTrue(images, "Dockerfile must build the API with a golang image")
        for image in images:
            self.assertTrue(image.startswith(version + "-"), f"golang:{image} is not pinned to Go {version}")

        workflows = "".join(p.read_text() for p in (ROOT / ".github" / "workflows").glob("*.yml"))
        pins = re.findall(r"go-version:\s*([0-9.]+)", workflows)
        self.assertTrue(pins, "CI must pin go-version")
        for pin in pins:
            self.assertEqual(pin, version, f"CI go-version {pin} differs from go.mod toolchain {version}")

    def test_analyzers_are_pinned(self):
        workflow = (ROOT / ".github" / "workflows" / "verify.yml").read_text()
        for tool in ("honnef.co/go/tools/cmd/staticcheck", "golang.org/x/vuln/cmd/govulncheck"):
            match = re.search(re.escape(tool) + r"@(\S+)", workflow)
            self.assertIsNotNone(match, f"{tool} must run in Verify")
            self.assertRegex(match.group(1), r"^v\d+\.\d+\.\d+$", f"{tool} must be pinned to a release")


if __name__ == "__main__":
    unittest.main()
