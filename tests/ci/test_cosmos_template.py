"""Static checks for the public Cosmos install template.

These checks prevent a market install from silently mounting AltMount's parent
read-write, dropping FUSE propagation, or using Docker-Compose-only key casing
that the Cosmos Docker API does not honour.
"""
import json
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
TEMPLATE = ROOT / "deploy" / "cosmos" / "MediaManager" / "cosmos-compose.json"
DESCRIPTION = ROOT / "deploy" / "cosmos" / "MediaManager" / "description.json"


class CosmosTemplateTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.template = json.loads(TEMPLATE.read_text(encoding="utf-8"))
        cls.description = json.loads(DESCRIPTION.read_text(encoding="utf-8"))

    def test_altmount_parent_is_read_only_with_rslave_propagation(self):
        volumes = self.template["services"]["{ServiceName}-api"]["volumes"]
        altmount = next(volume for volume in volumes if volume["Target"] == "/mnt")
        self.assertEqual(altmount["Type"], "bind")
        self.assertEqual(altmount["Source"], "{Context.altmountParentPath}")
        self.assertIs(altmount["ReadOnly"], True)
        self.assertEqual(altmount["BindOptions"]["Propagation"], "rslave")

    def test_cosmos_volume_shape_uses_docker_api_keys(self):
        volumes = self.template["services"]["{ServiceName}-api"]["volumes"]
        self.assertEqual(len(volumes), 5)
        allowed = {"Type", "Source", "Target", "ReadOnly", "BindOptions"}
        for volume in volumes:
            self.assertTrue(set(volume).issubset(allowed), volume)
            self.assertIn("Type", volume)
            self.assertIn("Source", volume)
            self.assertIn("Target", volume)
            self.assertFalse({"source", "target", "type", "readOnly"} & set(volume), volume)

    def test_routes_are_declared_api_health_then_ui(self):
        # Cosmos prepends entries; this declaration order is intentionally the
        # reverse of the stored UI, health, API precedence.
        routes = self.template["services"]["{ServiceName}"]["routes"]
        self.assertEqual([route["name"] for route in routes], [
            "{ServiceName}-api",
            "{ServiceName}-health",
            "{ServiceName}",
        ])
        self.assertEqual(routes[0]["PathPrefix"], "/api")
        self.assertEqual(routes[0]["ExtraHeaders"]["Authorization"], "Bearer {Context.ownerToken}")
        self.assertEqual(routes[1]["PathPrefix"], "/health")

    def test_healthcheck_units_are_integer_seconds(self):
        api = self.template["services"]["{ServiceName}-api"]["healthcheck"]
        postgres = self.template["services"]["{ServiceName}-postgres"]["healthcheck"]
        for healthcheck in (api, postgres):
            for key in ("interval", "timeout"):
                self.assertIsInstance(healthcheck[key], int, (key, healthcheck))
        self.assertIsInstance(api["start_period"], int)

    def test_template_declares_only_the_published_architecture(self):
        self.assertEqual(self.description["supported_architectures"], ["amd64"])


if __name__ == "__main__":
    unittest.main()
