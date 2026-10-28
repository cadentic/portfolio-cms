"""Unit tests: python3 -m unittest discover -s tools/cloudmigrate

The database tests at the bottom run only with CLOUDMIGRATE_IT=1 and Docker;
integration_test.sh sets that up.
"""
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
        cm.media_preflight(base, "gcp", "vps")
        cm.media_preflight(gcs, "vps", "gcp")
        notes = cm.after_move_notes("azure", "gcp", None, None, {**gcs, "media_access_key": True})
        self.assertTrue(any("media_access_key = false" in n for n in notes))

    def test_compare(self):
        a = {"tables": {"t": {"rows": 1, "md5": "x"}, "u": {"rows": 0, "md5": "y"}}, "sequences": {"s": 5}}
        b = {"tables": {"t": {"rows": 1, "md5": "z"}}, "sequences": {"s": 5}}
        self.assertEqual(cm.compare(a, b), [("t", "1", "1", False), ("u", "0", "-", False),
                                            ("(sequence) s", "5", "5", True)])


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
import base64, hashlib, json, os, shutil, sys
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
    for k in files(src):
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
        staging = cm.media_copy(gcs, azure)
        self.assertTrue((staging / "wordpress/2012/03/a.jpg").is_file())  # kept under backups/
        self.assertTrue(cm.media_verify(gcs, azure))
        out = self.tmp / "restored"
        cm.media_copy(azure, cm.DirStore(out))
        self.assertEqual((out / "uploads/2026/10/1-b c.png").read_bytes(), b"png" * 100)
        # A changed file is reported.
        (self.tmp / "cloud/az/acct/media/wordpress/2012/03/a.jpg").write_bytes(b"jpeg, but longer")
        self.assertFalse(cm.media_verify(gcs, azure))
        with self.assertRaisesRegex(cm.Fail, "no files"):
            cm.media_copy(cm.GcsStore("empty"), azure)

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
        self.assertEqual(src["migration"], "0003_wordpress_import")
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
