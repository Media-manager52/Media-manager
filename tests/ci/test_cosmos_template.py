"""Static checks for the public Cosmos install template.

The Cosmos market wizard requires lowercase Compose volume keys such as
``source``; the Docker API decodes those keys case-insensitively and accepts the
extra ``bindOptions``/``readOnly`` fields. These checks guard the wizard-facing
shape and the properties that matter before a disposable install is attempted.
"""
import json
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
TEMPLATE = ROOT / "deploy" / "cosmos" / "MediaManager" / "cosmos-compose.json"
DESCRIPTION = ROOT / "deploy" / "cosmos" / "MediaManager" / "description.json"


CONDITIONAL = re.compile(r"\{if (not )?Context\.(\w+)\}(.*?)\{/if\}", re.S)


def render(**context):
    """Applies Cosmos' {if Context.x}...{/if} blocks (not nested) for a form
    answer and parses the result; other placeholders stay as written."""
    text = TEMPLATE.read_text(encoding="utf-8")

    def apply(match):
        value = bool(context[match.group(2)])
        if match.group(1):
            value = not value
        return match.group(3) if value else ""

    rendered = CONDITIONAL.sub(apply, text)
    # Cosmos reads UID/GID as numbers, so they are written unquoted.
    rendered = re.sub(r'(": )\{Context\.media(Uid|Gid)\}', r"\g<1>1000", rendered)
    assert "{if" not in rendered and "{/if}" not in rendered, "nested or unclosed {if} block"
    return json.loads(rendered)


class CosmosTemplateTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.template = render(bundleAltmount=True, bundlePlex=False)
        cls.own_altmount = render(bundleAltmount=False, bundlePlex=False)
        cls.with_plex = render(bundleAltmount=True, bundlePlex=True)
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
        self.assertEqual(len(volumes), 6)
        # The upstream market wizard calls volume.source.startsWith(...), so
        # PascalCase Source silently crashes the supported install path.
        allowed = {"type", "source", "target", "readOnly", "bindOptions"}
        for volume in volumes:
            self.assertEqual(set(volume), set(volume) & allowed, volume)
            self.assertIsInstance(volume["source"], str)
            self.assertTrue(volume["source"])
            self.assertIn(volume["target"], {"/mnt", "/libraries/movies", "/libraries/tv", "/srv/media", "/var/lib/media-manager/backups", "/var/lib/media-manager/state"})
        library_targets = {"/libraries/movies", "/libraries/tv"}
        for volume in volumes:
            if volume["target"] in library_targets:
                self.assertNotEqual(volume.get("readOnly"), True, f"{volume['target']} must remain writable for publication")
        for volume in volumes:
            if volume["target"] != "/mnt":
                self.assertNotIn("bindOptions", volume, f"unexpected propagation on {volume['target']}")

    def test_only_the_four_generated_passwords_are_used(self):
        # The Cosmos wizard generates exactly four passwords (Passwords.0-3);
        # a higher index renders as an empty string and silently leaves a
        # secret unset.
        text = TEMPLATE.read_text(encoding="utf-8")
        indexes = {int(index) for index in re.findall(r"\{Passwords\.(\d+)\}", text)}
        self.assertTrue(indexes)
        self.assertLessEqual(max(indexes), 3, sorted(indexes))

    def test_secret_encryption_key_is_generated_in_a_private_api_volume(self):
        environment = self.api["environment"]
        self.assertNotIn("SECRET_ENCRYPTION_KEY", [entry.split("=", 1)[0] for entry in environment])
        self.assertIn("SECRET_ENCRYPTION_KEY_FILE=/var/lib/media-manager/state/secret-encryption.key", environment)
        state = next(volume for volume in self.api["volumes"] if volume["target"] == "/var/lib/media-manager/state")
        self.assertEqual(state, {"source": "{ServiceName}-api-state", "target": "/var/lib/media-manager/state", "type": "volume"})
        self.assertIn("{ServiceName}-api-state", self.template["volumes"])
        for name, service in self.template["services"].items():
            if name != "{ServiceName}-api":
                self.assertFalse(any(volume.get("source") == "{ServiceName}-api-state" for volume in service.get("volumes", [])), name)
        dockerfile = (ROOT / "Dockerfile").read_text(encoding="utf-8")
        self.assertIn("install -d -m 1777 /var/lib/media-manager/state", dockerfile)

    def test_backup_key_is_generated_in_the_same_private_volume(self):
        environment = self.api["environment"]
        self.assertNotIn("BACKUP_ENCRYPTION_KEY", [entry.split("=", 1)[0] for entry in environment])
        self.assertIn("BACKUP_ENCRYPTION_KEY_FILE=/var/lib/media-manager/state/backup-encryption.key", environment)

    def test_form_asks_only_for_what_the_website_cannot_set(self):
        # Plex, indexer and AltMount secrets are entered, sealed and tested on
        # the website; the backup key is generated. Only host paths, ids, the
        # network, the AltMount choice, the generated owner token and the
        # optional TMDB token remain.
        names = [field["name"] for field in self.template["cosmos-installer"]["form"]]
        for removed in ("plexToken", "newznabKey", "altmountSecret", "backupKey"):
            self.assertNotIn(removed, names)
        text = TEMPLATE.read_text(encoding="utf-8")
        for name in re.findall(r"\{Context\.(\w+)\}", text):
            self.assertIn(name, names, f"{{Context.{name}}} has no form field")

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
        hosts = set()
        for route in self.routes:
            self.assertIs(route["useHost"], True, route["name"])
            self.assertIs(route["AuthEnabled"], True, route["name"])
            self.assertIs(route["AdminOnly"], True, route["name"])
            self.assertIn("host", route, route["name"])
            self.assertTrue(route["host"], route["name"])
            hosts.add(route["host"])
        self.assertEqual(len(hosts), 1, hosts)
        api, health, ui = self.routes
        self.assertIn("ExtraHeaders", api)
        self.assertNotIn("ExtraHeaders", health)
        self.assertNotIn("ExtraHeaders", ui)

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

    def test_altmount_is_installed_by_default_on_the_private_network(self):
        form = {field["name"]: field for field in self.template["cosmos-installer"]["form"]}
        self.assertEqual(form["bundleAltmount"]["type"], "checkbox")
        self.assertIs(form["bundleAltmount"]["initialValue"], True)
        altmount = self.template["services"]["{ServiceName}-altmount"]
        self.assertEqual(altmount["image"], "ghcr.io/media-manager52/altmount:mm-stable")
        self.assertIn("ALTMOUNT_PRESET=media-manager", altmount["environment"])
        self.assertEqual(list(altmount["networks"]), ["{ServiceName}"])
        self.assertEqual(altmount["devices"], ["/dev/fuse:/dev/fuse:rwm"])
        self.assertEqual(altmount["cap_add"], ["SYS_ADMIN"])
        # AltMount mounts its drive inside the folder Media Manager reads at
        # /mnt; rshared lets the mount reach the host and the API container.
        mount = next(volume for volume in altmount["volumes"] if volume["target"] == "/mnt")
        self.assertEqual(mount["source"], "{Context.altmountParentPath}")
        self.assertEqual(mount["bindOptions"]["propagation"], "rshared")
        self.assertNotIn("readOnly", mount)
        config = next(volume for volume in altmount["volumes"] if volume["target"] == "/config")
        self.assertEqual(config, {"source": "{ServiceName}-altmount-config", "target": "/config", "type": "volume"})
        self.assertIn("{ServiceName}-altmount-config", self.template["volumes"])
        self.assertIn("BUNDLED_ALTMOUNT_URL=http://{ServiceName}-altmount:8080", self.api["environment"])

    def test_own_altmount_leaves_the_bundled_one_out(self):
        services = self.own_altmount["services"]
        self.assertEqual(set(services), {"{ServiceName}", "{ServiceName}-api", "{ServiceName}-postgres"})
        self.assertNotIn("{ServiceName}-altmount-config", self.own_altmount["volumes"])
        api = services["{ServiceName}-api"]
        self.assertFalse([entry for entry in api["environment"] if entry.startswith("BUNDLED_ALTMOUNT_URL=")])
        self.assertEqual(api["volumes"], self.api["volumes"])

    def test_folders_cosmos_creates_belong_to_the_media_user(self):
        # Cosmos creates a missing bind folder as root (mode 750) unless the
        # service names a user. AltMount and Plex run as root before they
        # drop privileges, so they name the media user: whichever service
        # Cosmos creates first, the API can then write the library folders.
        text = TEMPLATE.read_text(encoding="utf-8")
        self.assertEqual(text.count('"UID": {Context.mediaUid},'), 2)
        self.assertEqual(text.count('"GID": {Context.mediaGid},'), 2)
        for service in (self.with_plex["services"]["{ServiceName}-plex"], self.with_plex["services"]["{ServiceName}-altmount"]):
            self.assertEqual((service["UID"], service["GID"]), (1000, 1000))
        self.assertEqual(self.api["user"], "{Context.mediaUid}:{Context.mediaGid}")

    def test_plex_is_left_out_by_default(self):
        form = {field["name"]: field for field in self.template["cosmos-installer"]["form"]}
        self.assertIs(form["bundlePlex"]["initialValue"], False)
        self.assertNotIn("{ServiceName}-plex", self.template["services"])
        self.assertNotIn("{ServiceName}-plex-config", self.template["volumes"])

    def test_installed_plex_sees_altmount_and_the_libraries_at_media_managers_paths(self):
        plex = self.with_plex["services"]["{ServiceName}-plex"]
        self.assertIn("PLEX_CLAIM={Context.plexClaim}", plex["environment"])
        self.assertIn("PUID={Context.mediaUid}", plex["environment"])
        self.assertEqual(list(plex["networks"]), ["{ServiceName}"])
        volumes = {volume["target"]: volume for volume in plex["volumes"]}
        # The same paths as the API, so the recommended setup proves them and
        # can create Plex's libraries itself.
        api = {volume["target"]: volume for volume in self.with_plex["services"]["{ServiceName}-api"]["volumes"]}
        for target in ("/mnt", "/libraries/movies", "/libraries/tv", "/srv/media"):
            self.assertEqual(volumes[target]["source"], api[target]["source"], target)
            self.assertIs(volumes[target]["readOnly"], True, target)
        # rslave: Plex keeps AltMount's drive when AltMount restarts.
        self.assertEqual(volumes["/mnt"]["bindOptions"]["propagation"], "rslave")
        self.assertEqual(volumes["/config"], {"source": "{ServiceName}-plex-config", "target": "/config", "type": "volume"})
        self.assertIn("{ServiceName}-plex-config", self.with_plex["volumes"])

    def test_healthcheck_units_are_integer_seconds(self):
        api = self.api["healthcheck"]
        postgres = self.template["services"]["{ServiceName}-postgres"]["healthcheck"]
        altmount = self.template["services"]["{ServiceName}-altmount"]["healthcheck"]
        for healthcheck in (api, postgres, altmount):
            for key in ("interval", "timeout"):
                self.assertIsInstance(healthcheck[key], int, (key, healthcheck))
        self.assertIsInstance(api["start_period"], int)

    def test_template_declares_only_the_published_architecture(self):
        self.assertEqual(self.description["supported_architectures"], ["amd64"])


if __name__ == "__main__":
    unittest.main()
