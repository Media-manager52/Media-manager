"""Static checks for the public Cosmos install template.

The Cosmos market wizard requires lowercase Compose volume keys such as
``source``; the Docker API decodes those keys case-insensitively and accepts the
extra ``bindOptions``/``readOnly`` fields. These checks guard the wizard-facing
shape and the properties that matter before a disposable install is attempted.
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
        cls.api = cls.template["services"]["{ServiceName}-api"]
        cls.routes = cls.template["services"]["{ServiceName}"]["routes"]

    def test_altmount_parent_is_read_only_with_rslave_propagation(self):
        altmount = next(volume for volume in self.api["volumes"] if volume["target"] == "/mnt")
        self.assertEqual(altmount["type"], "bind")
        self.assertEqual(altmount["source"], "{Context.altmountParentPath}")
        self.assertIs(altmount["readOnly"], True)
        self.assertEqual(altmount["bindOptions"]["propagation"], "rslave")

    def test_wizard_compatible_volume_shape_and_one_writable_library_set(self):
        volumes = self.api["volumes"]
        self.assertEqual(len(volumes), 5)
        # The upstream market wizard calls volume.source.startsWith(...), so
        # PascalCase Source silently crashes the supported install path.
        allowed = {"type", "source", "target", "readOnly", "bindOptions"}
        for volume in volumes:
            self.assertEqual(set(volume), set(volume) & allowed, volume)
            self.assertIsInstance(volume["source"], str)
            self.assertTrue(volume["source"])
            self.assertIn(volume["target"], {"/mnt", "/libraries/movies", "/libraries/tv", "/srv/media", "/var/lib/media-manager/backups"})
        library_targets = {"/libraries/movies", "/libraries/tv"}
        for volume in volumes:
            if volume["target"] in library_targets:
                self.assertNotEqual(volume.get("readOnly"), True, f"{volume['target']} must remain writable for publication")
        for volume in volumes:
            if volume["target"] != "/mnt":
                self.assertNotIn("bindOptions", volume, f"unexpected propagation on {volume['target']}")

    def test_routes_are_declared_api_health_then_ui(self):
        # Cosmos prepends entries; this declaration order is intentionally the
        # reverse of the stored UI, health, API precedence.
        self.assertEqual([route["name"] for route in self.routes], [
            "{ServiceName}-api",
            "{ServiceName}-health",
            "{ServiceName}",
        ])
        api, health, ui = self.routes
        self.assertEqual(api["PathPrefix"], "/api")
        self.assertEqual(api["target"], "http://{ServiceName}-api:8080")
        self.assertIs(api["AuthEnabled"], True)
        self.assertEqual(api["ExtraHeaders"]["Authorization"], "Bearer {Context.ownerToken}")
        self.assertEqual(health["PathPrefix"], "/health")
        self.assertEqual(health["target"], "http://{ServiceName}-api:8080")
        self.assertIs(health["AuthEnabled"], True)
        self.assertEqual(ui["target"], "http://{ServiceName}:3000")
        self.assertEqual(ui["host"], api["host"])
        self.assertIs(ui["AuthEnabled"], True)

    def test_all_routes_are_host_bound_and_owner_only(self):
        for route in self.routes:
            self.assertIs(route["useHost"], True, route["name"])
            self.assertIs(route["AuthEnabled"], True, route["name"])
            self.assertIs(route["AdminOnly"], True, route["name"])
        self.assertIn("owner", self.routes[0]["description"].lower())

    def test_unintended_hosts_do_not_match_any_route(self):
        intended = self.routes[0]["host"]
        other = "other-cosmos-service.invalid"

        def matches(route, host, path):
            if route.get("useHost") and route["host"] != host:
                return False
            if route.get("usePathPrefix") and not path.startswith(route["PathPrefix"]):
                return False
            return True

        def first_match(host, path):
            # This is the effective API/health/UI order after Cosmos expands the
            # declarations in reverse. The API and health paths must win over
            # the UI catch-all on the intended host.
            return next((route["name"] for route in self.routes if matches(route, host, path)), None)

        self.assertEqual(first_match(intended, "/api/v1/dashboard"), "{ServiceName}-api")
        self.assertEqual(first_match(intended, "/health/ready"), "{ServiceName}-health")
        self.assertEqual(first_match(intended, "/library"), "{ServiceName}")
        for path in ("/api/v1/dashboard", "/health/ready", "/"):
            self.assertIsNone(first_match(other, path), path)

    def test_healthcheck_units_are_integer_seconds(self):
        api = self.api["healthcheck"]
        postgres = self.template["services"]["{ServiceName}-postgres"]["healthcheck"]
        for healthcheck in (api, postgres):
            for key in ("interval", "timeout"):
                self.assertIsInstance(healthcheck[key], int, (key, healthcheck))
        self.assertIsInstance(api["start_period"], int)

    def test_template_declares_only_the_published_architecture(self):
        self.assertEqual(self.description["supported_architectures"], ["amd64"])


if __name__ == "__main__":
    unittest.main()
