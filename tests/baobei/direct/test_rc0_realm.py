"""G5-RC0 release-chain checks for direct-deploy.py (no Docker, no production).

P0-DEPLOY-01: a standard direct deployment must be able to select the approved
dedicated realm explicitly; the realm overlay and the account-service secret must
be part of the verified release snapshot, the manifest and the config hash.
"""
import importlib.util
import json
from pathlib import Path
from tempfile import TemporaryDirectory
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[3]
ENTRY = ROOT / "deploy/baobei/direct-deploy.py"
SPEC = importlib.util.spec_from_file_location("direct_deploy_rc0", ENTRY)
direct = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(direct)
CURRENT = "a" * 40


class RealmSelectionTests(unittest.TestCase):
    def test_realm_overlay_is_in_the_release_snapshot(self):
        self.assertIn("compose.realm-baobei.yaml", direct.SNAPSHOT,
                      "the dedicated-realm overlay is not part of the verified release snapshot")

    def test_compose_command_loads_overlay_only_for_baobei(self):
        calls = []

        class Rec(direct.Docker):
            def __init__(self):
                self.env = {}

            def call(self, *args, capture=True, env=None, cwd=None):
                calls.append(args)
                return b"{}"
        images = {s: {"ref": s, "id": "sha256:" + "a" * 64} for s in direct.SERVICES}
        d = Rec()
        d.compose(Path("/r"), Path("/s"), images, "config", realm="crane")
        d.compose(Path("/r"), Path("/s"), images, "config", realm="baobei")
        crane, baobei = calls
        self.assertNotIn("compose.realm-baobei.yaml", crane)
        self.assertIn("compose.realm-baobei.yaml", baobei)

    def test_baobei_requires_account_service_secret(self):
        self.assertIn("account_client_secret", direct.realm_secrets("baobei"))
        self.assertNotIn("account_client_secret", direct.realm_secrets("crane"))

    def test_manifest_binds_realm(self):
        source = ENTRY.read_text(encoding="utf-8")
        self.assertIn('"identity_realm"', source, "the manifest does not record the identity realm")


if __name__ == "__main__":
    unittest.main()
