"""Unit tests: python3 -m unittest discover -s tools/cloudmigrate

The database tests at the bottom run only with CLOUDMIGRATE_IT=1 and Docker;
integration_test.sh sets that up.
"""
import contextlib
import io
import os
import shutil
import subprocess
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).parent))
import cloudmigrate as cm  # noqa: E402

EXAMPLE = cm.REPO / "infra" / "terraform" / "secret.tfvars.example"


class TfvarsTest(unittest.TestCase):
    def test_example_file(self):
        cfg = cm.parse_tfvars(EXAMPLE.read_text())
        self.assertEqual(cfg["domain"], "cadentic.dev")
        self.assertTrue(cfg["database_url"].startswith("postgresql://"))
        self.assertIsNone(cfg["email_dns_records"])  # a list: skipped
        self.assertEqual(cfg["dmarc_policy"], "quarantine")
        self.assertIn("vps_ipv4", cfg)
        self.assertIs(cfg["vps_local_db"], False)
        self.assertEqual(cfg["media_storage"], "local")
        self.assertIs(cfg["media_access_key"], False)

    def test_values(self):
        cfg = cm.parse_tfvars('''
# comment with "quotes" = x
a = "x # not a comment" // trailing
b=true
c = 42
d = [
  { name = "q]", type = "TXT" }, # ]
]
/* block
   e = "no" */
e = "esc \\"q\\" \\\\ $${lit}"
f = <<EOT
line 1
EOT
g = null
h = -1.5
''')
        self.assertEqual(cfg, {"a": "x # not a comment", "b": True, "c": 42, "d": None,
                               "e": 'esc "q" \\ ${lit}', "f": "line 1\n", "g": None, "h": -1.5})

    def test_errors(self):
        for bad in ['a = "open', "a b", "a = [1", "= 3"]:
            with self.assertRaises(cm.Fail):
                cm.parse_tfvars(bad)


class UrlTest(unittest.TestCase):
    def test_pg_env(self):
        env = cm.pg_env("postgresql://neondb_owner:p%40ss%3Aw@ep-a-1.ap-southeast-1.aws.neon.tech:5433/portfolio"
                        "?sslmode=require&channel_binding=require")
        self.assertEqual(env, {"PGHOST": "ep-a-1.ap-southeast-1.aws.neon.tech", "PGPORT": "5433",
                               "PGUSER": "neondb_owner", "PGPASSWORD": "p@ss:w", "PGDATABASE": "portfolio",
                               "PGSSLMODE": "require", "PGCHANNELBINDING": "require"})
        with self.assertRaises(cm.Fail):
            cm.pg_env("mysql://x@y/z")
        with self.assertRaises(cm.Fail):
            cm.pg_env("postgres://x@y/z?bogus=1")

    def test_neon_direct(self):
        pooled = "postgresql://u:pw@ep-cool-1-pooler.ap-southeast-1.aws.neon.tech/portfolio?sslmode=require"
        self.assertEqual(cm.neon_direct(pooled),
                         "postgresql://u:pw@ep-cool-1.ap-southeast-1.aws.neon.tech/portfolio?sslmode=require")
        other = "postgres://u:pw@db.example.com-pooler.x/portfolio"
        self.assertEqual(cm.neon_direct(other), other)

    def test_redact(self):
        self.assertEqual(cm.redact("postgres://u:secret@h:5432/d"), "postgres://u:***@h:5432/d")


class PlanTest(unittest.TestCase):
    def kinds(self, steps):
        return [(s.kind, s.args.get("target") or s.args.get("db") or s.args.get("src"),
                 s.args.get("switch_dns"), s.args.get("vps_up") or s.args.get("dst")) for s in steps]

    def test_cloud_to_cloud(self):
        self.assertEqual(self.kinds(cm.plan_move("gcp", "azure", None, None)),
                         [("deploy", "azure", True, "all")])

    def test_neon_to_vps(self):
        self.assertEqual(self.kinds(cm.plan_move("gcp", "vps", "neon", "vps")), [
            ("deploy", "vps", False, "db"), ("freeze", "neon", None, None),
            ("copy", "neon", None, "vps"), ("deploy", "vps", True, "all")])

    def test_vps_back_to_neon_on_azure(self):
        self.assertEqual(self.kinds(cm.plan_move("vps", "azure", "vps", "neon", stop_before_dns=True)), [
            ("freeze", "vps", None, None), ("copy", "vps", None, "neon"), ("deploy", "azure", False, "all")])

    def test_database_only_on_the_same_vps(self):
        self.assertEqual(self.kinds(cm.plan_move("vps", "vps", "neon", "vps", freeze_source=False)), [
            ("deploy", "vps", False, "db"), ("copy", "neon", None, "vps"), ("deploy", "vps", True, "all")])

    def test_invalid(self):
        for args in [("gcp", "gcp", None, None), ("gcp", "azure", "neon", "vps"), ("gcp", "vps", "vps", "vps"),
                     ("gcp", "aws", None, None)]:
            with self.assertRaises(cm.Fail, msg=args):
                cm.plan_move(*args)

    def test_config_checks(self):
        base = {"vps_ipv4": "203.0.113.10", "gcp_project_id": "p", "database_url": "postgres://x"}
        cm.check_config(base, "vps", None)
        cm.check_config({**base, "vps_local_db": True}, "vps", "vps")
        with self.assertRaises(cm.Fail):  # an empty VPS database would go live
            cm.check_config({**base, "vps_local_db": True}, "vps", None)
        with self.assertRaises(cm.Fail):
            cm.check_config(base, "vps", "vps")
        with self.assertRaises(cm.Fail):
            cm.check_config({**base, "vps_local_db": True}, "vps", "neon")
        with self.assertRaises(cm.Fail):
            cm.check_config({"gcp_project_id": "your-gcp-project"}, "gcp", None)
        # Leaving a VPS whose container holds the live data needs the data moved too.
        local = {**base, "vps_local_db": True, "media_storage": "gcs", "media_gcs_bucket": "b"}
        with self.assertRaisesRegex(cm.Fail, "--db-from vps --db-to neon"):
            cm.check_config(local, "gcp", None, "vps")
        cm.check_config(local, "gcp", "neon", "vps")
        cm.check_config(local, "gcp", None, "azure")
        with self.assertRaisesRegex(cm.Fail, "--from"):  # where the site runs is unknown
            cm.check_config(local, "gcp", None, None)

    def test_media_config_checks(self):
        base = {"vps_ipv4": "203.0.113.10", "gcp_project_id": "p", "azure_subscription_id": "s", "acr_name": "a"}
        gcs = {**base, "media_storage": "gcs", "media_gcs_bucket": "b"}
        cm.check_config(gcs, "gcp", None)  # Cloud Run uses its own service account
        for to in ("vps", "azure"):
            with self.assertRaisesRegex(cm.Fail, "media_access_key", msg=to):
                cm.check_config(gcs, to, None)
            cm.check_config({**gcs, "media_access_key": True}, to, None)
        with self.assertRaisesRegex(cm.Fail, "media_access_key"):
            cm.check_config({**base, "media_storage": "azure"}, "gcp", None)
        # Local media cannot follow the site off the VPS.
        with self.assertRaisesRegex(cm.Fail, "media copy vps"):
            cm.media_preflight(base, "vps", "gcp")
        with self.assertRaisesRegex(cm.Fail, "discards files"):
            cm.media_preflight(base, None, "azure")
        cm.media_preflight(base, "gcp", "vps")
        cm.media_preflight(gcs, "vps", "gcp")
        notes = cm.after_move_notes("azure", "gcp", None, None, {**gcs, "media_access_key": True})
        self.assertTrue(any("media_access_key = false" in n for n in notes))

    def test_frozen_neon_is_not_deployed_against(self):
        cfg = {"database_url": "postgres://u:pw@neon.example/portfolio"}
        with mock.patch.object(cm, "info", return_value={"read_only": True}):
            with self.assertRaisesRegex(cm.Fail, "read-only"):
                cm.neon_preflight(cfg, "local")
        with mock.patch.object(cm, "info", return_value={"read_only": False}):
            cm.neon_preflight(cfg, "local")

    def test_compare(self):
        a = {"tables": {"t": {"rows": 1, "md5": "x"}, "u": {"rows": 0, "md5": "y"}}, "sequences": {"s": 5}}
        b = {"tables": {"t": {"rows": 1, "md5": "z"}}, "sequences": {"s": 5}}
        self.assertEqual(cm.compare(a, b), [("t", "1", "1", False), ("u", "0", "-", False),
                                            ("(sequence) s", "5", "5", True)])


GCP_RECORDS = {f"A:cadentic.dev:{i}": f"216.239.3{2 + 2 * i}.21" for i in range(4)} | {"CNAME:www.cadentic.dev:0": "ghs.googlehosted.com"}
VPS_RECORDS = {"A:cadentic.dev:0": "203.0.113.10", "CNAME:www.cadentic.dev:0": "cadentic.dev"}


class DeployTest(unittest.TestCase):
    """deploy() tells a failure before the DNS change from one after it by the records in the dns/ state."""

    def deploy(self, before, after, *, switch_dns=True):
        failed = subprocess.CompletedProcess([], 2)
        with mock.patch.object(cm.subprocess, "run", return_value=failed), \
                mock.patch.object(cm, "current_host", return_value="gcp"), \
                mock.patch.object(cm, "dns_site_records", side_effect=[before, after]), \
                mock.patch.object(cm, "target_site_records", return_value=VPS_RECORDS), mock.patch.object(cm, "say"):
            cm.deploy("vps", switch_dns=switch_dns)

    def test_failure_after_the_switch(self):
        with self.assertRaisesRegex(cm.SwitchedFail, "vps is now the live site"):
            self.deploy(GCP_RECORDS, VPS_RECORDS)

    def test_failure_during_the_switch(self):
        # Terraform changed the A record, then Cloudflare refused the CNAME.
        partly = {**GCP_RECORDS, "A:cadentic.dev:0": "203.0.113.10"}
        with self.assertRaisesRegex(cm.SwitchedFail, "some DNS records point at vps and some still at gcp"):
            self.deploy(GCP_RECORDS, partly)

    def test_old_records_left_beside_the_new_ones(self):
        # The updates went through, deleting Google's other addresses did not: visitors reach both hosts.
        both = {**GCP_RECORDS, **VPS_RECORDS}
        with self.assertRaisesRegex(cm.SwitchedFail, "some still at gcp"):
            self.deploy(GCP_RECORDS, both)

    def test_old_records_removed_but_none_added(self):
        only_deletes = {"A:cadentic.dev:0": GCP_RECORDS["A:cadentic.dev:0"],
                        "CNAME:www.cadentic.dev:0": "ghs.googlehosted.com"}
        with self.assertRaisesRegex(cm.SwitchedFail, "none point at vps yet"):
            self.deploy(GCP_RECORDS, only_deletes)

    def test_verification_records_do_not_count(self):
        with_txt = {**VPS_RECORDS, "TXT:asuid.cadentic.dev:0": "left-over"}
        with self.assertRaisesRegex(cm.SwitchedFail, "vps is now the live site"):
            self.deploy(GCP_RECORDS, with_txt)

    def test_failure_before_the_switch(self):
        with self.assertRaisesRegex(cm.Fail, "still points at gcp") as e:
            self.deploy(GCP_RECORDS, dict(GCP_RECORDS))
        self.assertNotIsInstance(e.exception, cm.SwitchedFail)

    def test_unreadable_dns_state_is_treated_as_switched(self):
        with self.assertRaisesRegex(cm.SwitchedFail, "may already point at vps"):
            self.deploy(None, VPS_RECORDS)

    def test_failure_without_a_dns_change(self):
        with self.assertRaisesRegex(cm.Fail, "DNS was not changed") as e:
            failed = subprocess.CompletedProcess([], 2)
            with mock.patch.object(cm.subprocess, "run", return_value=failed), mock.patch.object(cm, "say"):
                cm.deploy("vps", switch_dns=False)
        self.assertNotIsInstance(e.exception, cm.SwitchedFail)

    def test_current_host_comes_from_the_records(self):
        targets = {"gcp": GCP_RECORDS, "vps": VPS_RECORDS, "azure": None}
        with mock.patch.object(cm, "target_site_records", side_effect=targets.get), \
                mock.patch.object(cm, "dns_target", return_value="vps"):
            for records, host in [(GCP_RECORDS, "gcp"), ({**GCP_RECORDS, **VPS_RECORDS}, None), ({}, None),
                                  (None, "vps")]:  # unreadable state: fall back to the site_target output
                with mock.patch.object(cm, "dns_site_records", return_value=records):
                    self.assertEqual(cm.current_host(), host, records)

    def test_current_host_on_a_subdomain(self):
        # site_host = "profile.cadentic.dev": the site records sit on the subdomain, the mail records on the apex.
        gcp = {"CNAME:profile.cadentic.dev:0": "ghs.googlehosted.com"}
        azure = {"CNAME:profile.cadentic.dev:0": "portfolio-web.example.azurecontainerapps.io",
                 "TXT:asuid.profile.cadentic.dev:0": "ABC123"}
        vps = {"A:profile.cadentic.dev:0": "203.0.113.10"}
        targets = {"gcp": gcp, "azure": azure, "vps": vps}
        with mock.patch.object(cm, "target_site_records", side_effect=targets.get), \
                mock.patch.object(cm, "dns_target", return_value=None):
            for records, host in [(gcp, "gcp"), (vps, "vps"), (azure, "azure"),
                                  # the asuid TXT record does not route visitors, so it does not decide the host
                                  ({**azure, "TXT:asuid.profile.cadentic.dev:0": "OLD"}, "azure"),
                                  # still on the apex while the targets already describe the subdomain: unknown
                                  (VPS_RECORDS, None)]:
                with mock.patch.object(cm, "dns_site_records", return_value=records):
                    self.assertEqual(cm.current_host(), host, records)

    def test_site_host(self):
        self.assertEqual(cm.site_host({"domain": "cadentic.dev"}), "cadentic.dev")
        self.assertEqual(cm.site_host({"domain": "cadentic.dev", "site_host": ""}), "cadentic.dev")
        self.assertEqual(cm.site_host({"domain": "cadentic.dev", "site_host": "Profile.cadentic.dev"}),
                         "profile.cadentic.dev")
        notes = cm.after_move_notes("gcp", "vps", None, None, {"domain": "cadentic.dev",
                                                              "site_host": "profile.cadentic.dev"})
        self.assertIn("https://profile.cadentic.dev/admin", notes[0])

    def test_records_from_terraform(self):
        show = {"values": {"root_module": {"child_modules": [{"address": "module.dns", "resources": [
            {"type": "cloudflare_dns_record", "name": "site", "index": "A:cadentic.dev:0",
             "values": {"content": "203.0.113.10"}},
            {"type": "cloudflare_dns_record", "name": "site", "index": "TXT:asuid.cadentic.dev:0",
             "values": {"content": '"ABC123"'}},
            {"type": "cloudflare_dns_record", "name": "spf", "values": {"content": "v=spf1"}}]}]}}}
        with mock.patch.object(cm.shutil, "which", return_value="/usr/bin/terraform"), \
                mock.patch.object(cm, "_tf_read", return_value=cm.json.dumps(show)):
            self.assertEqual(cm.dns_site_records(), {"A:cadentic.dev:0": "203.0.113.10",
                                                     "TXT:asuid.cadentic.dev:0": "ABC123"})
        with mock.patch.object(cm.shutil, "which", return_value="/usr/bin/terraform"), \
                mock.patch.object(cm, "_tf_read", return_value='{"format_version": "1.0"}'):
            self.assertEqual(cm.dns_site_records(), {})  # no state yet
        records = [{"name": "cadentic.dev", "type": "A", "content": "1.1.1.1"},
                   {"name": "cadentic.dev", "type": "AAAA", "content": "::1"},
                   {"name": "cadentic.dev", "type": "A", "content": "2.2.2.2"},
                   {"name": "asuid.cadentic.dev", "type": "TXT", "content": "ABC123"}]
        self.assertEqual(cm._site_keys(records), {"A:cadentic.dev:0": "1.1.1.1", "AAAA:cadentic.dev:0": "::1",
                                                  "A:cadentic.dev:1": "2.2.2.2", "TXT:asuid.cadentic.dev:0": "ABC123"})


class MoveTest(unittest.TestCase):
    """What a failed move tells the user to do with the frozen database."""

    def move(self, argv, cfg, current, fail_at):
        calls = []

        def fake_deploy(target, *, switch_dns, vps_up, extra):
            calls.append((target, vps_up))
            if (target, vps_up) == fail_at:
                raise cm.Fail("the smoke test failed")
        err = io.StringIO()
        with mock.patch.object(cm, "load_tfvars", return_value=cfg), \
                mock.patch.object(cm, "current_host", return_value=current), \
                mock.patch.object(cm, "deploy", side_effect=fake_deploy), \
                mock.patch.object(cm, "resolve", return_value=mock.Mock(label="db")), \
                mock.patch.object(cm, "info", return_value={"read_only": False}), \
                mock.patch.object(cm, "freeze"), mock.patch.object(cm, "copy"), \
                mock.patch.object(cm, "legacy_dns_roots", return_value=[]), \
                mock.patch.object(cm.shutil, "which", return_value="/usr/bin/x"), mock.patch.object(cm, "say"), \
                contextlib.redirect_stderr(err):
            rc = cm.main(["move", *argv, "--yes"])
        return rc, err.getvalue(), calls

    def test_database_move_on_the_same_vps(self):
        cfg = {"vps_ipv4": "203.0.113.10", "vps_local_db": True, "database_url": "postgres://u:p@neon.example/p"}
        rc, err, calls = self.move(["--to", "vps", "--db-to", "vps"], cfg, "vps", ("vps", "all"))
        self.assertEqual((rc, calls), (1, [("vps", "db"), ("vps", "all")]))
        self.assertIn("may already run on the vps database", err)
        self.assertIn("Do not run the move again", err)
        self.assertNotIn("To reopen the old site", err)

    def test_database_move_to_another_host(self):
        cfg = {"vps_ipv4": "203.0.113.10", "vps_local_db": True, "gcp_project_id": "p", "media_storage": "gcs",
               "media_gcs_bucket": "b", "database_url": "postgres://u:p@neon.example/p"}
        rc, err, _ = self.move(["--to", "gcp", "--db-from", "vps", "--db-to", "neon"], cfg, "vps", ("gcp", "all"))
        self.assertEqual(rc, 1)
        self.assertIn("still read-only. To reopen the old site for writes", err)
        self.assertIn("db unfreeze vps", err)

    def test_leftover_dns_records_stop_the_move_first(self):
        cfg = {"vps_ipv4": "203.0.113.10", "database_url": "postgres://u:p@neon.example/p"}
        with mock.patch.object(cm, "legacy_dns_roots", return_value=["gcp"]), \
                mock.patch.object(cm, "load_tfvars", return_value=cfg), mock.patch.object(cm, "current_host", return_value=None), \
                mock.patch.object(cm, "neon_preflight") as neon, mock.patch.object(cm, "deploy") as deploy:
            with self.assertRaisesRegex(cm.Fail, "dns adopt gcp"):
                cm.cmd_move(cm.argparse.Namespace(to="vps", from_=None, db_to=None, db_from=None, no_freeze=False,
                                                  stop_before_dns=False, ansible_args="", dry_run=False, yes=True,
                                                  client="local"))
        neon.assert_not_called()
        deploy.assert_not_called()


class CommandTest(unittest.TestCase):
    def test_vps_ssh_command(self):
        db = cm.VpsDB("203.0.113.10", "deploy", 2222, "/opt/my app")
        with mock.patch.dict(os.environ, {"CLOUDMIGRATE_SSH": "ssh"}):
            argv, _ = db.argv("psql -c 'select 1'")
        self.assertEqual(argv[:6], ["ssh", "-o", "BatchMode=yes", "-p", "2222", "deploy@203.0.113.10"])
        self.assertEqual(argv[6], "cd '/opt/my app' && docker compose exec -T -e PGUSER=portfolio "
                                  "-e PGDATABASE=portfolio db sh -c 'psql -c '\"'\"'select 1'\"'\"''")

    def test_docker_client_keeps_password_off_the_command_line(self):
        db = cm.UrlDB("x", "postgres://u:secret@h/d", "docker")
        argv, env = db.argv("true")
        self.assertNotIn("secret", " ".join(argv))
        self.assertIn("PGPASSWORD", argv)
        self.assertEqual(env["PGPASSWORD"], "secret")


# Stand-ins for gcloud and az that keep "buckets" in folders under $FAKE_CLOUD.
FAKE_GCLOUD = r"""
import base64, hashlib, json, os, re, shutil, sys
root = os.environ["FAKE_CLOUD"]
def path(u):
    return os.path.join(root, "gcs", u[5:].split("/")[0]) if u.startswith("gs://") else u
def files(d):
    for base, _, names in os.walk(d):
        for n in names:
            yield os.path.relpath(os.path.join(base, n), d).replace(os.sep, "/")
a = sys.argv[1:]
if a[:3] == ["storage", "objects", "list"]:
    assert a[4] == "--raw" and a[3].endswith("/**"), a
    d = path(a[3])
    if not os.path.isdir(d) or not os.listdir(d):
        sys.exit("ERROR: (gcloud.storage.objects.list) One or more URLs matched no objects.")
    out = []
    for k in files(d):
        data = open(os.path.join(d, k), "rb").read()
        out.append({"name": k, "size": str(len(data)), "md5Hash": base64.b64encode(hashlib.md5(data).digest()).decode()})
    print(json.dumps(out))
elif a[:2] == ["storage", "rsync"] and a[4] == "--recursive":
    src, dst = path(a[2]), path(a[3])
    exclude = [x.partition("=")[2] for x in a[5:] if x.startswith("--exclude=")]
    for k in files(src):
        if any(re.match(x, k) for x in exclude):
            continue
        os.makedirs(os.path.dirname(os.path.join(dst, k)), exist_ok=True)
        shutil.copyfile(os.path.join(src, k), os.path.join(dst, k))
else:
    sys.exit("fake gcloud: unexpected %r" % a)
"""

FAKE_AZ = r"""
import json, os, shutil, sys
root = os.environ["FAKE_CLOUD"]
a = sys.argv[1:]
opt = {a[i]: a[i + 1] for i in range(3, len(a) - 1) if a[i].startswith("-")}
assert a[:3][:2] == ["storage", "blob"] and opt["--auth-mode"] == "login", a
def container(name):
    return os.path.join(root, "az", opt["--account-name"], name)
def files(d):
    for base, _, names in os.walk(d):
        for n in names:
            yield os.path.relpath(os.path.join(base, n), d).replace(os.sep, "/")
def copy(src, dst):
    for k in files(src):
        os.makedirs(os.path.dirname(os.path.join(dst, k)), exist_ok=True)
        shutil.copyfile(os.path.join(src, k), os.path.join(dst, k))
if a[2] == "list":
    d = container(opt["--container-name"])
    print(json.dumps([{"name": k, "size": os.path.getsize(os.path.join(d, k)), "md5": None} for k in files(d)]))
elif a[2] == "upload-batch":
    assert opt["--overwrite"] == "true"
    copy(opt["--source"], container(opt["--destination"]))
elif a[2] == "download-batch":
    copy(container(opt["--source"]), opt["--destination"])
else:
    sys.exit("fake az: unexpected %r" % a)
"""


class MediaTest(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp)
        bin_dir = self.tmp / "bin"
        bin_dir.mkdir()
        for name, code in [("gcloud", FAKE_GCLOUD), ("az", FAKE_AZ)]:
            (bin_dir / name).write_text(f"#!{sys.executable}\n" + textwrap.dedent(code))
            (bin_dir / name).chmod(0o755)
        env = {"PATH": f"{bin_dir}{os.pathsep}{os.environ['PATH']}", "FAKE_CLOUD": str(self.tmp / "cloud")}
        patcher = mock.patch.dict(os.environ, env)
        patcher.start()
        self.addCleanup(patcher.stop)
        self.backups = mock.patch.object(cm, "BACKUPS", self.tmp / "backups")
        self.backups.start()
        self.addCleanup(self.backups.stop)
        self.src = self.tmp / "media"
        for key, data in [("wordpress/2012/03/a.jpg", b"jpeg"), ("uploads/2026/10/1-b c.png", b"png" * 100),
                          (".DS_Store", b"x"), ("wordpress/.hidden/x", b"x")]:
            (self.src / key).parent.mkdir(parents=True, exist_ok=True)
            (self.src / key).write_bytes(data)

    def test_folder_listing_skips_hidden_files(self):
        listing = cm.DirStore(self.src).list()
        self.assertEqual(sorted(listing), ["uploads/2026/10/1-b c.png", "wordpress/2012/03/a.jpg"])
        self.assertEqual(listing["wordpress/2012/03/a.jpg"], (4, "q088y6dIV8Xyug1bfb9l4Q=="))  # md5 of b"jpeg"

    def test_compare(self):
        a = {"x": (1, "m1"), "y": (2, "m2"), "z": (3, None)}
        b = {"x": (1, "m1"), "y": (2, "other"), "z": (3, "m3"), "w": (9, None)}
        self.assertEqual(cm.media_compare(a, b), ([], ["y"], ["w"]))
        self.assertEqual(cm.media_compare(a, {}), (["x", "y", "z"], [], []))

    def test_resolve(self):
        cfg = {"media_gcs_bucket": "media-b", "media_azure_account": "acct", "vps_ipv4": "203.0.113.10",
               "vps_ssh_user": "deploy", "vps_dir": "/srv/p"}
        self.assertEqual(cm.resolve_media("gcs", cfg).label, "gs://media-b")
        self.assertEqual(cm.resolve_media("azure", cfg).label, "az://acct/media")
        self.assertEqual(cm.resolve_media("az://other/files", cfg).label, "az://other/files")
        self.assertEqual(cm.resolve_media("gs://x/", None).bucket, "x")
        with mock.patch.dict(os.environ, {"CLOUDMIGRATE_SSH": "ssh"}):
            argv = cm.resolve_media("vps", cfg).argv("ls")
        self.assertEqual(argv[:6], ["ssh", "-o", "BatchMode=yes", "-p", "22", "deploy@203.0.113.10"])
        self.assertEqual(argv[6], "cd /srv/p && docker compose exec -T api sh -c ls")
        self.assertIsInstance(cm.resolve_media(str(self.src), None), cm.DirStore)
        with self.assertRaisesRegex(cm.Fail, "media_gcs_bucket"):
            cm.resolve_media("gcs", {})

    def test_copy_folder_to_gcs_to_azure_and_back(self):
        gcs, azure = cm.GcsStore("bucket-1"), cm.AzureStore("acct", "media")
        self.assertEqual(gcs.list(), {})  # an empty bucket is not an error
        self.assertIsNone(cm.media_copy(cm.DirStore(self.src), gcs))
        self.assertEqual(len(gcs.list()), 2)
        # Hidden objects (an interrupted upload's temp file) are neither listed nor copied.
        for where in ("gcs/bucket-1/uploads/.tmp-1", "az/acct/media/.tmp-2"):
            (self.tmp / "cloud" / where).parent.mkdir(parents=True, exist_ok=True)
            (self.tmp / "cloud" / where).write_bytes(b"partial")
        self.assertEqual(len(gcs.list()), 2)
        staging = cm.media_copy(gcs, azure)
        self.assertTrue((staging / "wordpress/2012/03/a.jpg").is_file())  # kept under backups/
        self.assertFalse((staging / "uploads/.tmp-1").exists())
        self.assertEqual(len(azure.list()), 2)
        self.assertTrue(cm.media_verify(gcs, azure))
        out = self.tmp / "restored"
        cm.media_copy(azure, cm.DirStore(out))
        self.assertEqual((out / "uploads/2026/10/1-b c.png").read_bytes(), b"png" * 100)
        self.assertFalse((out / ".tmp-2").exists())
        # A changed file is reported.
        (self.tmp / "cloud/az/acct/media/wordpress/2012/03/a.jpg").write_bytes(b"jpeg, but longer")
        self.assertFalse(cm.media_verify(gcs, azure))
        with self.assertRaisesRegex(cm.Fail, "no files"):
            cm.media_copy(cm.GcsStore("empty"), azure)

    def test_volume_upload_failure_is_reported(self):
        # The remote tar reads a little, then fails (a full disk, say): no BrokenPipeError escapes.
        (self.src / "big.bin").write_bytes(os.urandom(4 << 20))
        stop = [sys.executable, "-c", "import sys; sys.stdin.buffer.read(1000); sys.exit('tar: write error')"]
        with self.assertRaisesRegex(cm.Fail, "write error"):
            cm.ComposeStore("volume", stop, "/srv").push(self.src)

    def test_cli(self):
        def run(*args):
            return subprocess.run([sys.executable, str(Path(cm.__file__)), *args], capture_output=True, text=True)
        p = run("media", "copy", str(self.src), "gs://cli-bucket")
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertIn("All 2 files are at the target", p.stdout)
        p = run("media", "ls", "--long", "gs://cli-bucket")
        self.assertIn("gs://cli-bucket: 2 files, 304 B", p.stdout)
        self.assertEqual(run("media", "verify", "gs://cli-bucket", str(self.src)).returncode, 0)
        self.assertEqual(run("media", "verify", str(self.src), "gs://other").returncode, 1)
        p = run("media", "copy", "gs://cli-bucket", "gs://cli-bucket")
        self.assertIn("the same", p.stderr)


CF = 'provider["registry.terraform.io/cloudflare/cloudflare"]'


def tf_resource(module, rtype, name, keys, deps=()):
    provider = CF if rtype.startswith("cloudflare") else 'provider["registry.terraform.io/hashicorp/google"]'
    r = {"mode": "managed", "type": rtype, "name": name, "provider": provider,
         "instances": [{"schema_version": 0, "attributes": {"id": f"{name}-{k}"}, "sensitive_attributes": [],
                        "dependencies": list(deps), **({"index_key": k} if k is not None else {})} for k in keys]}
    return {"module": module, **r} if module else r


def old_gcp_state():
    """A gcp/ state from before the dns/ root: the records sit in module.dns, keyed by their place in the list."""
    site = [f"A:cadentic.dev:{i}" for i in range(4)] + [f"AAAA:cadentic.dev:{i}" for i in range(4, 8)] \
        + ["CNAME:www.cadentic.dev:8"]
    return {"version": 4, "terraform_version": "1.9.8", "serial": 7, "lineage": "old-lineage", "outputs": {},
            "check_results": None, "resources": [
                tf_resource(None, "google_cloud_run_v2_service", "web", [None]),
                tf_resource("module.dns", "cloudflare_dns_record", "site", site,
                            ["google_cloud_run_v2_service.web", "module.dns.cloudflare_dns_record.spf"]),
                tf_resource("module.dns", "cloudflare_dns_record", "spf", [None]),
                tf_resource("module.dns", "cloudflare_dns_record", "email", ["CNAME:brevo1._domainkey.cadentic.dev:0"]),
            ]}


class DnsAdoptTest(unittest.TestCase):
    def test_gcp_records_are_rekeyed_within_each_name(self):
        rest, new = cm.adopt_dns_state(old_gcp_state(), None, "gcp")
        self.assertEqual([r["type"] for r in rest["resources"]], ["google_cloud_run_v2_service"])
        self.assertEqual((rest["serial"], rest["lineage"]), (8, "old-lineage"))
        site = next(r for r in new["resources"] if r["name"] == "site")
        self.assertEqual([i["index_key"] for i in site["instances"]],
                         [f"A:cadentic.dev:{i}" for i in range(4)] + [f"AAAA:cadentic.dev:{i}" for i in range(4)]
                         + ["CNAME:www.cadentic.dev:0"])
        self.assertEqual([i["attributes"]["id"] for i in site["instances"]][4:], [f"site-AAAA:cadentic.dev:{i}" for i in range(4, 8)]
                         + ["site-CNAME:www.cadentic.dev:8"])  # the same Cloudflare records, new keys
        self.assertEqual(site["instances"][0]["dependencies"], ["module.dns.cloudflare_dns_record.spf"])
        email = next(r for r in new["resources"] if r["name"] == "email")
        self.assertEqual(email["instances"][0]["index_key"], "CNAME:brevo1._domainkey.cadentic.dev:0")
        self.assertEqual((new["version"], new["serial"], new["terraform_version"]), (4, 1, "1.9.8"))
        self.assertNotEqual(new["lineage"], "old-lineage")
        self.assertEqual(new["outputs"]["site_target"], {"value": "gcp", "type": "string"})

    def test_azure_records(self):
        keys = ["A:cadentic.dev:0", "TXT:asuid.cadentic.dev:1", "CNAME:www.cadentic.dev:2", "TXT:asuid.www.cadentic.dev:3"]
        old = {"version": 4, "serial": 1, "lineage": "l", "resources": [
            tf_resource("module.dns", "cloudflare_dns_record", "site", keys)]}
        dns = {"version": 4, "serial": 3, "lineage": "dns-lineage", "outputs": {}, "resources": []}
        _, new = cm.adopt_dns_state(old, dns, "azure")
        self.assertEqual([i["index_key"] for i in new["resources"][0]["instances"]],
                         ["A:cadentic.dev:0", "TXT:asuid.cadentic.dev:0", "CNAME:www.cadentic.dev:0",
                          "TXT:asuid.www.cadentic.dev:0"])
        self.assertEqual((new["serial"], new["lineage"]), (4, "dns-lineage"))

    def test_refusals(self):
        with self.assertRaisesRegex(cm.Fail, "nothing to do"):
            cm.adopt_dns_state({"resources": [tf_resource(None, "x", "y", [None])]}, None, "gcp")
        taken = {"resources": [tf_resource("module.dns", "cloudflare_dns_record", "spf", [None])]}
        with self.assertRaisesRegex(cm.Fail, "already holds other"):
            cm.adopt_dns_state(old_gcp_state(), taken, "gcp")

    def test_finishing_an_interrupted_run(self):
        # The dns/ state was pushed, the old state was not: only the old state is left to change.
        rest, new = cm.adopt_dns_state(old_gcp_state(), None, "gcp")
        rest2, new2 = cm.adopt_dns_state(old_gcp_state(), new, "gcp")
        self.assertIsNone(new2)
        self.assertEqual(rest2, rest)

    @unittest.skipUnless(shutil.which("terraform"), "needs terraform")
    def test_with_terraform(self):
        tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, tmp)
        for root in ("gcp", "dns"):
            (tmp / "infra/terraform" / root).mkdir(parents=True)
            (tmp / "infra/terraform" / root / "main.tf").write_text("terraform {}\n")
        (tmp / "infra/terraform/gcp/terraform.tfstate").write_text(cm.json.dumps(old_gcp_state()))
        push = cm.tf_state_push

        def interrupted(root, path):  # Ctrl-C after the dns/ push, before the old root's
            if root.name == "gcp":
                raise KeyboardInterrupt
            push(root, path)
        with mock.patch.object(cm, "REPO", tmp), mock.patch.object(cm, "BACKUPS", tmp / "backups"), \
                mock.patch.object(cm, "say"), contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(cm.legacy_dns_roots(), ["gcp"])
            with mock.patch.object(cm, "tf_state_push", side_effect=interrupted):
                self.assertEqual(cm.main(["dns", "adopt", "gcp", "--yes"]), 130)
            self.assertEqual(cm.legacy_dns_roots(), ["gcp"])  # both states hold them: deploys stay blocked
            self.assertEqual(cm.main(["dns", "adopt", "gcp", "--yes"]), 0)  # and running it again finishes
            self.assertEqual(cm.legacy_dns_roots(), [])
            self.assertEqual(cm.main(["dns", "adopt", "gcp", "--yes"]), 1)  # nothing left to move
            # A root whose state lives in a backend this machine cannot reach is not taken as clean.
            (tmp / "infra/terraform/azure").mkdir()
            (tmp / "infra/terraform/azure/main.tf").write_text(
                'terraform {\n  backend "gcs" {\n    bucket = "no-such-bucket-for-tests"\n  }\n}\n')
            with mock.patch.dict(os.environ, {"GOOGLE_APPLICATION_CREDENTIALS": str(tmp / "missing.json")}):
                with self.assertRaisesRegex(cm.Fail, "could not check whether the azure"):
                    cm.legacy_dns_roots()

        def state_list(root):
            p = subprocess.run(["terraform", f"-chdir={tmp / 'infra/terraform' / root}", "state", "list"],
                               capture_output=True, text=True, check=True)
            return p.stdout.split()
        self.assertEqual(state_list("gcp"), ["google_cloud_run_v2_service.web"])
        dns = state_list("dns")
        self.assertIn('module.dns.cloudflare_dns_record.site["CNAME:www.cadentic.dev:0"]', dns)
        self.assertIn("module.dns.cloudflare_dns_record.spf", dns)
        self.assertEqual(len(dns), 11)
        backups = list((tmp / "backups").glob("*.tfstate"))
        self.assertEqual(len(backups), 6)  # gcp before/after and dns after, then gcp before/after and dns before
        self.assertTrue(all(b.stat().st_mode & 0o077 == 0 for b in backups))


@unittest.skipUnless(os.environ.get("CLOUDMIGRATE_IT") == "1", "set CLOUDMIGRATE_IT=1 (see integration_test.sh)")
class DatabaseTest(unittest.TestCase):
    """Runs against the databases integration_test.sh starts."""

    def setUp(self):
        self.client = os.environ.get("CLOUDMIGRATE_CLIENT", "docker")
        self.a = cm.resolve(os.environ["IT_SOURCE_URL"], None, self.client)
        self.b = cm.resolve(os.environ["IT_TARGET_URL"], None, self.client)
        self.vps = cm.VpsDB("it-host", "it", 22, os.environ["IT_VPS_DIR"])
        cm.BACKUPS = Path(os.environ["IT_BACKUPS"])

    def test_1_copy_to_vps_and_back(self):
        src = cm.info(self.a)
        self.assertGreater(src["tables"], 20)
        latest = max(p.stem for p in (Path(__file__).resolve().parents[2] / "backend/internal/store/migrations").glob("*.sql"))
        self.assertEqual(src["migration"], latest)
        # Neon -> VPS container, replacing what the fresh stack created.
        with self.assertRaises(cm.Fail):
            cm.copy(self.a, self.vps, replace=False, do_freeze=False, yes=True)
        cm.copy(self.a, self.vps, replace=True, do_freeze=True, yes=True)
        self.assertTrue(cm.info(self.a)["read_only"])
        with self.assertRaisesRegex(cm.Fail, "read-only transaction"):  # writes are refused while frozen
            self.a.sql("INSERT INTO contact_messages (name, email, message) VALUES ('x', 'x@example.com', 'xxxxxxxxxxxx')")
        self.assertTrue(cm.verify(self.a, self.vps))
        # VPS -> another server, then the frozen source opens again.
        cm.copy(self.vps, self.b, replace=True, do_freeze=False, yes=True)
        self.assertTrue(cm.verify(self.a, self.b))
        cm.unfreeze(self.a)
        self.assertFalse(cm.info(self.a)["read_only"])

    def test_2_verify_spots_changes(self):
        self.b.sql("UPDATE cms_pages SET title = title || '!' WHERE ctid = (SELECT min(ctid) FROM cms_pages)")
        self.assertFalse(cm.verify(self.a, self.b))

    def test_3_dump_and_restore_file(self):
        path = cm.dump(self.a)
        with self.assertRaises(cm.Fail):  # refuses to restore into a frozen database
            cm.freeze(self.b)
            cm.copy(self.a, self.b, replace=True, do_freeze=False, yes=True)
        cm.unfreeze(self.b)
        cm.restore(path, self.b)
        self.assertTrue(cm.verify(self.a, self.b))


@unittest.skipUnless(os.environ.get("CLOUDMIGRATE_IT") == "1", "set CLOUDMIGRATE_IT=1 (see integration_test.sh)")
class MediaVolumeTest(unittest.TestCase):
    """Copies media into and out of a running api container's volume, over the stand-in ssh."""

    def test_copy_in_and_out(self):
        here = os.environ["IT_MEDIA_DIR"]
        volume = cm.ComposeStore("media volume", cm.VpsDB("it-host", "it", 22, here).ssh_prefix(), here)
        wordpress = cm.DirStore(cm.REPO / "frontend" / "public" / "media")
        out = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, out)
        with mock.patch.object(cm, "BACKUPS", Path(os.environ["IT_BACKUPS"])):
            cm.media_copy(wordpress, volume)
            cm.media_copy(volume, cm.DirStore(out))
        self.assertTrue(cm.media_verify(wordpress, cm.DirStore(out)))


if __name__ == "__main__":
    unittest.main()
