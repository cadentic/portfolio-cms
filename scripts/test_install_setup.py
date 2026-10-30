"""Unit tests for installSetup.py: python3 -m unittest discover -s scripts -p 'test_*.py'"""
import contextlib
import io
import os
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

REPO = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(REPO))
import installSetup as setup  # noqa: E402

# Everything a VPS deploy needs, as flags. Dummy values, never secrets.
VPS_FLAGS = [
    "--target", "vps", "--vps-ipv4", "203.0.113.10",
    "--database-url", "postgresql://u:p@ep-dummy.neon.example.invalid/portfolio?sslmode=require",
    "--firebase-project-id", "dummy-project", "--firebase-api-key", "AIzaDummyKey",
    "--admin-emails", "owner@example.invalid", "--brevo-api-key", "xkeysib-dummy",
    "--mailjet-api-key", "mj-dummy", "--mailjet-secret-key", "mj-secret-dummy",
    "--cloudflare-api-token", "cf-dummy", "--cloudflare-zone-id", "zone-dummy",
]


class SetValuesTest(unittest.TestCase):
    def test_replace_keeps_comments_and_layout(self):
        text = 'vps_ipv4     = "" # public IPv4\nvps_local_db = false\n'
        out, problems = setup.set_values(text, {"vps_ipv4": "203.0.113.10", "vps_local_db": True})
        self.assertEqual(problems, [])
        self.assertEqual(out, 'vps_ipv4     = "203.0.113.10" # public IPv4\nvps_local_db = true\n')

    def test_commented_line_taken_over_then_appended(self):
        out, _ = setup.set_values('site_host = ""\n# site_www = false\n', {"site_www": True, "new_key": "x"})
        self.assertEqual(out, 'site_host = ""\nsite_www = true\n\n# --- added by installSetup.py ---\nnew_key = "x"\n')

    def test_none_comments_out(self):
        out, _ = setup.set_values('site_www = true # x\n', {"site_www": None})
        self.assertEqual(out, '# site_www = true # x\n')

    def test_escaping_round_trips(self):
        value = 'a "b" \\ ${c} %{d}'
        out, _ = setup.set_values('k = "old"\n', {"k": value})
        self.assertEqual(setup.parse_tfvars(out)["k"], value)

    def test_multiline_value_refused(self):
        _, problems = setup.set_values('email_dns_records = [\n]\n', {"email_dns_records": "x"})
        self.assertTrue(problems)

    def test_does_not_match_a_longer_key(self):
        out, _ = setup.set_values('media_gcs_bucket_x = "a"\nmedia_gcs_bucket = ""\n', {"media_gcs_bucket": "b"})
        self.assertEqual(out, 'media_gcs_bucket_x = "a"\nmedia_gcs_bucket = "b"\n')


class SiteHostTest(unittest.TestCase):
    def test_forms(self):
        for raw, want in [("", ""), ("@", ""), ("apex", ""), ("cadentic.dev", ""), ("profile", "profile.cadentic.dev"),
                          ("Profile.Cadentic.dev.", "profile.cadentic.dev"), ("a.b.cadentic.dev", "a.b.cadentic.dev")]:
            self.assertEqual(setup.normalize_site_host(raw, "cadentic.dev"), want, raw)

    def test_outside_the_domain(self):
        for raw in ("profile.example.com", "notcadentic.dev", "bad_name"):
            with self.assertRaises(setup.Fail):
                setup.normalize_site_host(raw, "cadentic.dev")


class MainTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.path = Path(self.dir.name) / "secret.tfvars"
        env = {k: v for k, v in os.environ.items() if not k.startswith("SETUP_")}
        patcher = mock.patch.dict(os.environ, env, clear=True)
        patcher.start()
        self.addCleanup(patcher.stop)
        self.addCleanup(self.dir.cleanup)

    def run_main(self, *flags):
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            code = setup.main([*flags, "--tfvars", str(self.path), "--non-interactive", "--no-deploy"])
        return code, out.getvalue()

    def cfg(self):
        return setup.parse_tfvars(self.path.read_text())

    def test_vps_on_a_subdomain(self):
        code, out = self.run_main(*VPS_FLAGS, "--site-host", "profile")
        self.assertEqual(code, 0, out)
        cfg = self.cfg()
        self.assertEqual(cfg["domain"], "cadentic.dev")
        self.assertEqual(cfg["site_host"], "profile.cadentic.dev")
        self.assertEqual(cfg["vps_ipv4"], "203.0.113.10")
        self.assertEqual(cfg["firebase_auth_domain"], "dummy-project.firebaseapp.com")
        self.assertEqual(cfg["admin_alert_email"], "owner@example.invalid")
        self.assertGreaterEqual(len(cfg["app_secret"]), 32)
        self.assertNotIn("REPLACE_", cfg["email_brevo_webhook_secret"] + cfg["email_mailjet_webhook_secret"])
        self.assertEqual(self.path.stat().st_mode & 0o777, 0o600)
        self.assertIn("https://profile.cadentic.dev", out)
        self.assertIn("Authorized domains: add profile.cadentic.dev", out)
        self.assertNotIn("cf-dummy", out)  # secrets are masked in the summary

        # A second run keeps everything, including the generated secrets.
        secret = cfg["app_secret"]
        code, out = self.run_main("--target", "vps")
        self.assertEqual(code, 0, out)
        self.assertIn("nothing to change", out)
        self.assertEqual(self.cfg()["app_secret"], secret)

        # Back to the domain itself, with a backup of the previous file.
        code, _ = self.run_main("--target", "vps", "--site-host", "@", "--www", "no")
        self.assertEqual(code, 0)
        self.assertEqual(self.cfg()["site_host"], "")
        self.assertIs(self.cfg()["site_www"], False)
        self.assertTrue(list(Path(self.dir.name).glob("secret.backup-*.tfvars")))

    def test_missing_settings_are_listed(self):
        with self.assertRaisesRegex(setup.Fail, r"--vps-ipv4.*--cloudflare-zone-id|--database-url"):
            self.run_main("--target", "vps")
        self.assertFalse(self.path.exists())

    def test_dry_run_writes_nothing(self):
        code, out = self.run_main(*VPS_FLAGS, "--dry-run")
        self.assertEqual(code, 0)
        self.assertIn("Dry run", out)
        self.assertFalse(self.path.exists())

    def test_cloud_run_needs_a_bucket(self):
        flags = [f for f in VPS_FLAGS[4:]] + ["--target", "gcp", "--gcp-project-id", "dummy-gcp"]
        with self.assertRaisesRegex(setup.Fail, "--media-storage"):
            self.run_main(*flags)
        code, out = self.run_main(*flags, "--media-storage", "gcs", "--media-gcs-bucket", "dummy-media")
        self.assertEqual(code, 0, out)
        self.assertIs(self.cfg()["media_access_key"], False)  # same cloud: the site's own identity

    def test_bucket_in_another_cloud_gets_a_key(self):
        code, out = self.run_main(*VPS_FLAGS, "--media-storage", "gcs", "--media-gcs-bucket", "dummy-media")
        self.assertEqual(code, 0, out)
        self.assertIs(self.cfg()["media_access_key"], True)

    def test_local_database_password(self):
        flags = [f for f in VPS_FLAGS if not f.startswith("postgresql://") and f != "--database-url"]
        code, out = self.run_main(*flags, "--vps-local-db")
        self.assertEqual(code, 0, out)
        self.assertRegex(self.cfg()["vps_db_password"], r"^[0-9a-f]{48}$")

    def test_bad_values_refused(self):
        with self.assertRaisesRegex(setup.Fail, "IPv4"):
            self.run_main(*VPS_FLAGS[:2], "--vps-ipv4", "not-an-ip")
        with self.assertRaisesRegex(setup.Fail, "pooler"):
            self.run_main("--database-url", "postgresql://u:p@ep-x-pooler.neon.tech/db")
        with self.assertRaisesRegex(setup.Fail, "under it"):
            self.run_main(*VPS_FLAGS, "--site-host", "profile.example.com")

    def test_google_contacts_on_and_off(self):
        code, out = self.run_main(*VPS_FLAGS)
        self.assertEqual(code, 0, out)
        self.assertIs(self.cfg()["google_contacts_enabled"], False)  # off by default
        with self.assertRaisesRegex(setup.Fail, "--google-oauth-client-id.*--google-oauth-client-secret"):
            self.run_main("--target", "vps", "--google-contacts")
        code, out = self.run_main("--target", "vps", "--google-contacts", "--google-oauth-client-id",
                                  "123.apps.googleusercontent.com", "--google-oauth-client-secret", "GOCSPX-dummy")
        self.assertEqual(code, 0, out)
        cfg = self.cfg()
        self.assertIs(cfg["google_contacts_enabled"], True)
        self.assertEqual(cfg["google_oauth_client_secret"], "GOCSPX-dummy")
        self.assertNotIn("GOCSPX-dummy", out)
        self.assertIn("/api/google/oauth/callback", out)
        code, out = self.run_main("--target", "vps", "--no-google-contacts")
        self.assertEqual(code, 0, out)
        self.assertIs(self.cfg()["google_contacts_enabled"], False)
        self.assertEqual(self.cfg()["google_oauth_client_id"], "123.apps.googleusercontent.com")  # kept for later

    def test_deploy_refused_on_windows(self):
        with mock.patch.object(setup.os, "name", "nt"), self.assertRaisesRegex(setup.Fail, "WSL"):
            setup.deploy("vps", True, False)


if __name__ == "__main__":
    unittest.main()
