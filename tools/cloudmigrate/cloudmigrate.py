#!/usr/bin/env python3
"""Move portfolio-cms between hosts, and its database between Postgres servers.

Hosts: Google Cloud Run (gcp), Azure Container Apps (azure) or any VPS with
root SSH, such as a Bluehost VPS (vps). Databases: Neon (or any managed
Postgres), the Postgres container on the VPS, or the local Docker stack.
Media: a Google Cloud Storage bucket, an Azure Blob container, the media
volume on the VPS or a folder.

    python3 tools/cloudmigrate/cloudmigrate.py check
    python3 tools/cloudmigrate/cloudmigrate.py db info neon
    python3 tools/cloudmigrate/cloudmigrate.py db copy neon vps --freeze
    python3 tools/cloudmigrate/cloudmigrate.py media copy frontend/public/media gcs
    python3 tools/cloudmigrate/cloudmigrate.py move --to vps --db-to vps

Everything is read from infra/terraform/secret.tfvars. Standard library only.
See tools/cloudmigrate/README.md.
"""
from __future__ import annotations

import argparse
import base64
import contextlib
import hashlib
import json
import os
import re
import shlex
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import uuid
from dataclasses import dataclass, field
from pathlib import Path
from urllib.parse import parse_qsl, unquote, urlsplit, urlunsplit

REPO = Path(__file__).resolve().parents[2]
TFVARS = REPO / "infra" / "terraform" / "secret.tfvars"
BACKUPS = REPO / "backups"
PG_MAJOR = 18
PG_IMAGE = f"postgres:{PG_MAJOR}-alpine"
TARGETS = ("gcp", "azure", "vps")


class Fail(Exception):
    """An error to show the user without a traceback."""


def say(msg: str = "") -> None:
    try:
        print(msg, flush=True)
    except BrokenPipeError:  # output piped into head and the like: keep working, stop printing
        sys.stdout = open(os.devnull, "w")


# --- secret.tfvars ---------------------------------------------------------------

_IDENT = re.compile(r"[A-Za-z_][A-Za-z0-9_-]*")
_NUMBER = re.compile(r"-?[0-9]+(\.[0-9]+)?([eE][+-]?[0-9]+)?")
_ESCAPES = {"n": "\n", "r": "\r", "t": "\t", '"': '"', "\\": "\\"}


def parse_tfvars(text: str) -> dict:
    """Top-level assignments from a .tfvars file. Strings, numbers and booleans
    are returned as Python values; lists and objects as None (not needed here)."""
    out: dict = {}
    pos, n = 0, len(text)

    def line() -> int:
        return text.count("\n", 0, pos) + 1

    def skip(newlines: bool = True) -> None:
        nonlocal pos
        while pos < n:
            c = text[pos]
            if c in " \t\r" or (newlines and c == "\n"):
                pos += 1
            elif c == "#" or text.startswith("//", pos):
                end = text.find("\n", pos)
                pos = n if end < 0 else end
            elif text.startswith("/*", pos):
                end = text.find("*/", pos + 2)
                if end < 0:
                    raise Fail(f"secret.tfvars line {line()}: unclosed comment")
                pos = end + 2
            else:
                return

    def string() -> str:
        nonlocal pos
        pos += 1
        buf = []
        while pos < n:
            c = text[pos]
            if c == '"':
                pos += 1
                return "".join(buf)
            if c == "\n":
                break
            if c == "\\" and pos + 1 < n:
                e = text[pos + 1]
                if e == "u" and pos + 5 < n:
                    buf.append(chr(int(text[pos + 2:pos + 6], 16)))
                    pos += 6
                    continue
                buf.append(_ESCAPES.get(e, "\\" + e))
                pos += 2
                continue
            if text.startswith("$${", pos) or text.startswith("%%{", pos):
                buf.append(c + "{")
                pos += 3
                continue
            buf.append(c)
            pos += 1
        raise Fail(f"secret.tfvars line {line()}: unterminated string")

    def skip_block() -> None:
        nonlocal pos
        depth = 0
        while pos < n:
            c = text[pos]
            if c == '"':
                string()
                continue
            if c == "#" or text.startswith("//", pos) or text.startswith("/*", pos):
                skip()
                continue
            if c in "[{(":
                depth += 1
            elif c in "]})":
                depth -= 1
                if depth == 0:
                    pos += 1
                    return
            pos += 1
        raise Fail("secret.tfvars: unclosed list or object")

    def heredoc() -> str:
        nonlocal pos
        m = re.compile(r"<<(-?)([A-Za-z_][A-Za-z0-9_]*)[ \t]*\r?\n").match(text, pos)
        if not m:
            raise Fail(f"secret.tfvars line {line()}: bad heredoc")
        pos = m.end()
        lines = []
        while pos < n:
            end = text.find("\n", pos)
            end = n if end < 0 else end
            current = text[pos:end]
            pos = min(end + 1, n)
            if current.strip() == m.group(2):
                return "\n".join(lines) + "\n"
            lines.append(current)
        raise Fail("secret.tfvars: unterminated heredoc")

    while True:
        skip()
        if pos >= n:
            return out
        m = _IDENT.match(text, pos)
        if not m:
            raise Fail(f"secret.tfvars line {line()}: expected a setting name")
        key, pos = m.group(), m.end()
        skip(newlines=False)
        if pos >= n or text[pos] != "=":
            raise Fail(f"secret.tfvars line {line()}: expected '=' after {key}")
        pos += 1
        skip(newlines=False)
        if pos >= n:
            raise Fail(f"secret.tfvars: missing value for {key}")
        c = text[pos]
        if c == '"':
            out[key] = string()
        elif c in "[{":
            skip_block()
            out[key] = None
        elif text.startswith("<<", pos):
            out[key] = heredoc()
        elif text.startswith("true", pos) or text.startswith("false", pos):
            out[key] = text.startswith("true", pos)
            pos += 4 if out[key] else 5
        elif text.startswith("null", pos):
            out[key] = None
            pos += 4
        elif nm := _NUMBER.match(text, pos):
            out[key] = float(nm.group()) if any(x in nm.group() for x in ".eE") else int(nm.group())
            pos = nm.end()
        else:
            raise Fail(f"secret.tfvars line {line()}: cannot read the value of {key}")


def load_tfvars(path: Path = TFVARS) -> dict:
    if not path.exists():
        raise Fail(f"{path.relative_to(REPO)} not found. Copy secret.tfvars.example and fill it in.")
    return parse_tfvars(path.read_text(encoding="utf-8"))


# --- connection URLs ---------------------------------------------------------------

# libpq URL parameters and the environment variables that carry them, so
# passwords never appear on a command line.
_PARAM_ENV = {
    "sslmode": "PGSSLMODE", "sslrootcert": "PGSSLROOTCERT", "sslcert": "PGSSLCERT", "sslkey": "PGSSLKEY",
    "channel_binding": "PGCHANNELBINDING", "options": "PGOPTIONS", "connect_timeout": "PGCONNECT_TIMEOUT",
    "application_name": "PGAPPNAME", "target_session_attrs": "PGTARGETSESSIONATTRS", "gssencmode": "PGGSSENCMODE",
}


def pg_env(url: str) -> dict:
    u = urlsplit(url)
    if u.scheme not in ("postgres", "postgresql"):
        raise Fail("a database URL must start with postgres:// or postgresql://")
    env = {}
    if u.hostname:
        env["PGHOST"] = u.hostname
    if u.port:
        env["PGPORT"] = str(u.port)
    if u.username:
        env["PGUSER"] = unquote(u.username)
    if u.password is not None:
        env["PGPASSWORD"] = unquote(u.password)
    if db := unquote(u.path.lstrip("/")):
        env["PGDATABASE"] = db
    for k, v in parse_qsl(u.query, keep_blank_values=True):
        if k not in _PARAM_ENV:
            raise Fail(f"unsupported connection parameter {k!r} in the database URL")
        env[_PARAM_ENV[k]] = v
    return env


def neon_direct(url: str) -> str:
    """Neon's pooled host (ep-x-pooler.region...) goes through PgBouncer, which
    pg_dump and restores should avoid. Use the direct host instead."""
    u = urlsplit(url)
    host = u.hostname or ""
    if not (host.endswith(".neon.tech") and "-pooler." in host):
        return url
    userinfo, at, hostport = u.netloc.rpartition("@")
    hostport = hostport.replace(host, host.replace("-pooler.", ".", 1), 1)
    return urlunsplit(u._replace(netloc=userinfo + at + hostport))


def redact(url: str) -> str:
    u = urlsplit(url)
    if u.password is None:
        return url
    userinfo, _, hostport = u.netloc.rpartition("@")
    return urlunsplit(u._replace(netloc=f"{userinfo.split(':')[0]}:***@{hostport}"))


# --- database endpoints ----------------------------------------------------------------
#
# Every database operation is a short sh script that runs where PostgreSQL 18
# client tools are: on this machine, in a postgres:18-alpine container, or
# inside the db container of a Compose stack (locally or on the VPS over SSH).
# Data and SQL always travel on stdin/stdout.

class Endpoint:
    label = "database"
    kind = "url"

    def argv(self, script: str) -> tuple[list[str], dict | None]:
        raise NotImplementedError

    def run(self, script: str, *, input: bytes | None = None, stdin=None, stdout=subprocess.PIPE) -> bytes:
        argv, env = self.argv(script)
        p = subprocess.run(argv, input=input, stdin=stdin, stdout=stdout, stderr=subprocess.PIPE, env=env)
        if p.returncode != 0:
            err = p.stderr.decode(errors="replace").strip()
            raise Fail(f"{self.label}: {err or 'command failed with exit code %d' % p.returncode}")
        return p.stdout or b""

    def sql(self, query: str) -> str:
        return self.run("psql -X -q -At -v ON_ERROR_STOP=1 -f -", input=query.encode()).decode().strip()

    def sql_json(self, query: str):
        out = self.sql(query)
        last = out.splitlines()[-1] if out else ""
        return json.loads(last)


class UrlDB(Endpoint):
    def __init__(self, label: str, url: str, client: str):
        self.label, self.url, self.client = label, url, client
        self.env = pg_env(url)

    def argv(self, script):
        env = {**os.environ, **self.env}
        if self.client == "local":
            return ["sh", "-c", script], env
        # Host networking reaches databases on localhost too; values come from
        # this process's environment (-e NAME), not the command line.
        args = ["docker", "run", "--rm", "-i", "--network", "host"]
        for k in self.env:
            args += ["-e", k]
        return args + [PG_IMAGE, "sh", "-c", script], env


_IN_DB = "exec -T -e PGUSER=portfolio -e PGDATABASE=portfolio db sh -c"


class LocalComposeDB(Endpoint):
    kind = "compose"

    def __init__(self, project_dir: Path = REPO):
        self.label, self.project_dir = "local Docker stack", project_dir

    def argv(self, script):
        return ["docker", "compose", "--project-directory", str(self.project_dir), *_IN_DB.split(), script], None


class VpsDB(Endpoint):
    kind = "compose"

    def __init__(self, host: str, user: str, port: int, directory: str):
        self.host, self.user, self.port, self.dir = host, user, port, directory
        self.label = f"VPS database ({user}@{host})"

    def ssh_prefix(self) -> list[str]:
        ssh = shlex.split(os.environ.get("CLOUDMIGRATE_SSH", "ssh"))
        return ssh + ["-o", "BatchMode=yes", "-p", str(self.port), f"{self.user}@{self.host}"]

    def ssh(self, remote: str) -> list[str]:
        return self.ssh_prefix() + [remote]

    def argv(self, script):
        return self.ssh(f"cd {shlex.quote(self.dir)} && docker compose {_IN_DB} {shlex.quote(script)}"), None


def pick_client(choice: str) -> str:
    if choice != "auto":
        return choice
    tools = [shutil.which(t) for t in ("psql", "pg_dump", "pg_restore")]
    if all(tools):
        out = subprocess.run(["pg_dump", "--version"], capture_output=True, text=True).stdout
        m = re.search(r"(\d+)(?:\.\d+)?", out)
        if m and int(m.group(1)) >= PG_MAJOR:
            return "local"
    if shutil.which("docker"):
        return "docker"
    raise Fail(f"Install the PostgreSQL {PG_MAJOR} client tools (psql, pg_dump, pg_restore) or Docker.")


def resolve(spec: str, cfg: dict | None, client: str) -> Endpoint:
    """neon | vps | local | postgres://... -> an endpoint."""
    if spec == "local":
        return LocalComposeDB()
    if spec.startswith(("postgres://", "postgresql://")):
        return UrlDB(redact(neon_direct(spec)), neon_direct(spec), pick_client(client))
    if cfg is None:
        cfg = load_tfvars()
    if spec == "neon":
        url = cfg.get("database_url")
        if not url or "USER:PASSWORD" in url:
            raise Fail("set database_url in secret.tfvars (the Neon connection string)")
        return UrlDB("Neon (database_url)", neon_direct(url), pick_client(client))
    if spec == "vps":
        host = cfg.get("vps_ssh_host") or cfg.get("vps_ipv4")
        if not host:
            raise Fail("set vps_ipv4 (and vps_ssh_user) in secret.tfvars")
        return VpsDB(host, cfg.get("vps_ssh_user") or "root", int(cfg.get("vps_ssh_port") or 22),
                     cfg.get("vps_dir") or "/opt/portfolio")
    raise Fail(f"unknown database {spec!r}: use neon, vps, local or a postgres:// URL")


# --- database operations ---------------------------------------------------------------

INFO_SQL = """
SELECT json_build_object(
  'version', current_setting('server_version'),
  'major', current_setting('server_version_num')::int / 10000,
  'database', current_database(),
  'user', current_user,
  'read_only', current_setting('default_transaction_read_only') = 'on',
  'size', pg_size_pretty(pg_database_size(current_database())),
  'tables', (SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
             WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p') AND NOT c.relispartition),
  'migration', CASE WHEN to_regclass('public.schema_migrations') IS NULL THEN NULL ELSE
    (xpath('/row/v/text()', query_to_xml('SELECT max(version) AS v FROM public.schema_migrations', false, true, '')))[1]::text END);
"""

# Row count and an order-independent checksum of every table in the public
# schema, plus sequence positions. Settings that change how values print are
# pinned, and rows sort by byte order, so equal data gives equal checksums on
# any server.
STATS_SQL = """
SET TimeZone = 'UTC'; SET DateStyle = 'ISO, YMD'; SET IntervalStyle = 'postgres';
SET extra_float_digits = 1; SET bytea_output = 'hex';
SELECT json_build_object(
  'tables', (SELECT coalesce(json_object_agg(relname, json_build_object('rows', n, 'md5', h) ORDER BY relname), '{}')
    FROM (SELECT c.relname,
                 (xpath('/row/n/text()', x))[1]::text::bigint AS n,
                 (xpath('/row/h/text()', x))[1]::text AS h
          FROM pg_class c JOIN pg_namespace ns ON ns.oid = c.relnamespace
          CROSS JOIN LATERAL query_to_xml(format(
            'SELECT count(*) AS n, md5(coalesce(string_agg(t::text, chr(10) ORDER BY t::text COLLATE "C"), '''')) AS h FROM %I.%I t',
            ns.nspname, c.relname), false, true, '') AS x
          WHERE ns.nspname = 'public' AND c.relkind IN ('r', 'p') AND NOT c.relispartition) s),
  'sequences', (SELECT coalesce(json_object_agg(sequencename, last_value ORDER BY sequencename), '{}')
    FROM pg_sequences WHERE schemaname = 'public'));
"""

# Stops writes on the whole database (the app keeps serving reads) and ends
# the sessions that started before, so nothing changes during the copy.
FREEZE_SQL = """
SET default_transaction_read_only = off;
DO $$BEGIN EXECUTE format('ALTER DATABASE %I SET default_transaction_read_only = on', current_database()); END$$;
SELECT count(pg_terminate_backend(pid)) FROM pg_stat_activity
WHERE datname = current_database() AND pid <> pg_backend_pid() AND usename = current_user;
"""

UNFREEZE_SQL = """
SET default_transaction_read_only = off;
DO $$BEGIN EXECUTE format('ALTER DATABASE %I RESET default_transaction_read_only', current_database()); END$$;
"""

DUMP_SH = "pg_dump --format=custom --no-owner --no-privileges"

# Turns the archive on stdin into SQL first (so a broken archive changes
# nothing), then replaces the public schema in one transaction.
RESTORE_SH = """set -e
t=$(mktemp)
trap 'rm -f "$t"' EXIT
pg_restore --no-owner --no-privileges -f "$t"
psql -X -q -v ON_ERROR_STOP=1 --single-transaction \
  -c 'DROP SCHEMA IF EXISTS public CASCADE' -c 'CREATE SCHEMA public' -f "$t"
"""


def info(db: Endpoint) -> dict:
    return db.sql_json(INFO_SQL)


def stats(db: Endpoint) -> dict:
    return db.sql_json(STATS_SQL)


def freeze(db: Endpoint) -> None:
    ended = db.sql(FREEZE_SQL).splitlines()
    say(f"  {db.label} is now read-only (ended {ended[-1] if ended else 0} open sessions).")


def unfreeze(db: Endpoint) -> None:
    db.sql(UNFREEZE_SQL)
    say(f"  {db.label} accepts writes again.")


def backup_path(label: str) -> Path:
    BACKUPS.mkdir(exist_ok=True)
    slug = re.sub(r"[^a-z0-9]+", "-", label.lower()).strip("-")[:40]
    return BACKUPS / f"{time.strftime('%Y%m%d-%H%M%S')}-{slug}.dump"


def dump(db: Endpoint, path: Path | None = None) -> Path:
    path = path or backup_path(db.label)
    tmp = path.with_suffix(path.suffix + ".part")
    with open(tmp, "wb") as f:
        db.run(DUMP_SH, stdout=f)
    if tmp.stat().st_size == 0:
        tmp.unlink()
        raise Fail(f"{db.label}: the dump is empty")
    tmp.replace(path)
    say(f"  Saved {db.label} to {path.relative_to(REPO) if path.is_relative_to(REPO) else path} "
        f"({path.stat().st_size // 1024} KiB).")
    return path


def restore(path: Path, db: Endpoint) -> None:
    with open(path, "rb") as f:
        db.run(RESTORE_SH, stdin=f)
    say(f"  Restored {path.name} into {db.label}.")


def compare(a: dict, b: dict) -> list[tuple[str, str, str, bool]]:
    rows = []
    for name in sorted(set(a["tables"]) | set(b["tables"])):
        x, y = a["tables"].get(name), b["tables"].get(name)
        rows.append((name, str(x["rows"]) if x else "-", str(y["rows"]) if y else "-", x == y))
    for name in sorted(set(a["sequences"]) | set(b["sequences"])):
        x, y = a["sequences"].get(name), b["sequences"].get(name)
        rows.append((f"(sequence) {name}", str(x), str(y), x == y))
    return rows


def verify(src: Endpoint, dst: Endpoint, before: dict | None = None) -> bool:
    a, b = before or stats(src), stats(dst)
    rows = compare(a, b)
    width = max([len(r[0]) for r in rows] + [5])
    say(f"  {'table':<{width}}  {'source':>8}  {'target':>8}")
    for name, x, y, ok in rows:
        say(f"  {name:<{width}}  {x:>8}  {y:>8}  {'ok' if ok else 'DIFFERENT'}")
    bad = [r[0] for r in rows if not r[3]]
    if bad:
        say(f"  {len(bad)} of {len(rows)} differ (rows or checksums): {', '.join(bad)}")
    else:
        say(f"  All {len(rows)} tables and sequences match (row counts and checksums).")
    return not bad


def confirm(question: str, yes: bool) -> None:
    if yes:
        return
    if not sys.stdin.isatty():
        raise Fail(f"{question} Re-run with --yes to confirm.")
    if input(f"{question} Type yes to continue: ").strip().lower() != "yes":
        raise Fail("stopped; nothing more was changed")


def copy(src: Endpoint, dst: Endpoint, *, replace: bool, do_freeze: bool, yes: bool,
         unfreeze_target: bool = False) -> None:
    s = info(src)
    say(f"  Source: {src.label}, PostgreSQL {s['version']}, {s['tables']} tables, "
        f"migration {s['migration'] or 'none'}, {s['size']}{', read-only' if s['read_only'] else ''}.")
    if not s["tables"]:
        raise Fail(f"{src.label} has no tables; nothing to copy")
    d = info(dst)
    say(f"  Target: {dst.label}, PostgreSQL {d['version']}, {d['tables']} tables.")
    if d["major"] < s["major"]:
        raise Fail(f"the target runs PostgreSQL {d['major']}, older than the source ({s['major']})")
    if d["read_only"]:
        if not unfreeze_target:
            raise Fail(f"{dst.label} is read-only (frozen). Run `db unfreeze` on it first if that is intended.")
        unfreeze(dst)
    if d["tables"]:
        if not replace:
            raise Fail(f"{dst.label} already has {d['tables']} tables. Pass --replace to overwrite them "
                       "(a backup is saved first).")
        confirm(f"This replaces every table in {dst.label}.", yes)
    if do_freeze and not s["read_only"]:
        confirm(f"{src.label} will become read-only: the site keeps serving pages, but edits, sign-ups "
                "and email sending stop until the move is done.", yes)
        freeze(src)
    elif not s["read_only"]:
        say("  Warning: the source still accepts writes; anything written after the dump is not copied.")
    before = stats(src)
    archive = dump(src)
    if d["tables"]:
        dump(dst, backup_path(dst.label + " before restore"))
    restore(archive, dst)
    if not verify(src, dst, before):
        raise Fail("the copy does not match the source. If the source changed meanwhile, copy again with --freeze.")


# --- media ---------------------------------------------------------------------------------
#
# Media files are kept under keys like wordpress/2012/03/a.jpg and
# uploads/2026/10/<id>-name.png in one of: a Google Cloud Storage bucket, an
# Azure Blob container, the "media" volume of a Compose stack (on the VPS or
# here), or a plain folder. Copies go through a folder under backups/, which
# is kept as a backup. Bucket access uses your own gcloud or az sign-in.

MEDIA_DIR = "/data/media"  # inside the api container
Listing = dict  # key -> (size, base64 md5 or None)
# Keys with a part starting with "." (.DS_Store, the app's half-written upload
# temp files) are not media: every store leaves them out.
_HIDDEN_RE = r"(^|.*/)\."


def hidden(key: str) -> bool:
    return any(part.startswith(".") for part in key.split("/"))


def _cli(argv: list[str], what: str, *, ok_empty: str | None = None) -> str:
    if not shutil.which(argv[0]):
        raise Fail(f"{what} needs the {argv[0]} command line tool (see DEPLOY.md)")
    p = subprocess.run(argv, capture_output=True, text=True)
    if p.returncode != 0:
        if ok_empty and ok_empty in p.stderr:
            return ""
        raise Fail(f"{what}: {p.stderr.strip() or 'exit code %d' % p.returncode}")
    return p.stdout


def _md5_b64(path: Path) -> str:
    h = hashlib.md5()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return base64.b64encode(h.digest()).decode()


class MediaStore:
    label = "media"

    def list(self) -> Listing:
        raise NotImplementedError

    def pull(self, dest: Path) -> None:
        """Download every file into dest."""
        raise NotImplementedError

    def push(self, src: Path) -> None:
        """Upload every file under src. Nothing is deleted at the target."""
        raise NotImplementedError


class DirStore(MediaStore):
    def __init__(self, path: Path):
        self.path = path
        self.label = f"folder {path}"

    def list(self) -> Listing:
        if not self.path.is_dir():
            return {}
        out = {}
        for f in sorted(self.path.rglob("*")):
            rel = f.relative_to(self.path)
            if f.is_file() and not hidden(rel.as_posix()):
                out[rel.as_posix()] = (f.stat().st_size, _md5_b64(f))
        return out

    def pull(self, dest: Path) -> None:
        for key in self.list():
            (dest / key).parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(self.path / key, dest / key)

    def push(self, src: Path) -> None:
        self.path.mkdir(parents=True, exist_ok=True)
        DirStore(src).pull(self.path)


class GcsStore(MediaStore):
    def __init__(self, bucket: str):
        self.bucket = bucket
        self.label = f"gs://{bucket}"

    def list(self) -> Listing:
        out = _cli(["gcloud", "storage", "objects", "list", f"gs://{self.bucket}/**", "--raw",
                    "--format=json(name,size,md5Hash)"], f"listing {self.label}", ok_empty="matched no objects")
        return {o["name"]: (int(o["size"]), o.get("md5Hash")) for o in json.loads(out or "[]")
                if not o["name"].endswith("/") and not hidden(o["name"])}

    def pull(self, dest: Path) -> None:
        _cli(["gcloud", "storage", "rsync", f"gs://{self.bucket}", str(dest), "--recursive", f"--exclude={_HIDDEN_RE}"],
             f"downloading {self.label}")

    def push(self, src: Path) -> None:
        _cli(["gcloud", "storage", "rsync", str(src), f"gs://{self.bucket}", "--recursive", f"--exclude={_HIDDEN_RE}"],
             f"uploading to {self.label}")


class AzureStore(MediaStore):
    def __init__(self, account: str, container: str):
        self.account, self.container = account, container
        self.label = f"az://{account}/{container}"

    def _az(self, *args: str) -> list[str]:
        return ["az", "storage", "blob", *args, "--account-name", self.account, "--auth-mode", "login"]

    def list(self) -> Listing:
        out = _cli(self._az("list", "--container-name", self.container, "--num-results", "*", "--query",
                            "[].{name:name,size:properties.contentLength,md5:properties.contentSettings.contentMd5}",
                            "-o", "json"), f"listing {self.label}")
        return {o["name"]: (int(o["size"]), o.get("md5") or None) for o in json.loads(out or "[]")
                if not hidden(o["name"])}

    def pull(self, dest: Path) -> None:
        _cli(self._az("download-batch", "--source", self.container, "--destination", str(dest), "-o", "none"),
             f"downloading {self.label}")

    def push(self, src: Path) -> None:
        _cli(self._az("upload-batch", "--destination", self.container, "--source", str(src),
                      "--overwrite", "true", "-o", "none"), f"uploading to {self.label}")


# Lists the volume as "key<TAB>size<TAB>hex md5" lines, with the tools of the
# api image (Alpine/BusyBox).
_LIST_SH = (f"cd {MEDIA_DIR} 2>/dev/null || exit 0; find . -type f | while IFS= read -r f; do "
            "printf '%s\\t%s\\t%s\\n' \"${f#./}\" \"$(stat -c %s \"$f\")\" \"$(md5sum \"$f\" | cut -d ' ' -f 1)\"; done")


class ComposeStore(MediaStore):
    """The media volume, through the running api container of a Compose stack."""

    def __init__(self, label: str, prefix: list[str] | None = None, remote_dir: str | None = None):
        self.label, self.prefix, self.remote_dir = label, prefix, remote_dir

    def argv(self, script: str) -> list[str]:
        if self.prefix is None:  # this machine
            return ["docker", "compose", "--project-directory", str(REPO), "exec", "-T", "api", "sh", "-c", script]
        return self.prefix + [f"cd {shlex.quote(self.remote_dir)} && docker compose exec -T api sh -c {shlex.quote(script)}"]

    def _run(self, script: str) -> str:
        p = subprocess.run(self.argv(script), capture_output=True, text=True)
        if p.returncode != 0:
            raise Fail(f"{self.label}: {p.stderr.strip() or 'exit code %d' % p.returncode} "
                       "(the site's api container must be running)")
        return p.stdout

    def list(self) -> Listing:
        out = {}
        for line in self._run(_LIST_SH).splitlines():
            key, size, md5 = line.split("\t")
            if not hidden(key):
                out[key] = (int(size), base64.b64encode(bytes.fromhex(md5)).decode())
        return out

    def pull(self, dest: Path) -> None:
        if not hasattr(tarfile, "data_filter"):
            raise Fail("copying media out of a container needs Python 3.11.4 or newer")
        proc = subprocess.Popen(self.argv(f"cd {MEDIA_DIR} && tar -cf - ."), stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE)
        try:
            with tarfile.open(fileobj=proc.stdout, mode="r|") as tar:
                for member in tar:
                    name = member.name.removeprefix("./")
                    if name not in (".", "") and not hidden(name):
                        tar.extract(member, dest, filter="data")
        finally:
            proc.stdout.close()
            with proc.stderr:
                err = proc.stderr.read().decode(errors="replace").strip()
            if proc.wait() != 0:
                raise Fail(f"{self.label}: {err or 'tar failed'}")

    def push(self, src: Path) -> None:
        keys = DirStore(src).list()
        proc = subprocess.Popen(self.argv(f"mkdir -p {MEDIA_DIR} && tar -C {MEDIA_DIR} -xf -"),
                                stdin=subprocess.PIPE, stderr=subprocess.PIPE)
        try:
            with tarfile.open(fileobj=proc.stdin, mode="w|") as tar:
                for key in keys:
                    info = tar.gettarinfo(src / key, arcname=key)
                    info.uid = info.gid = 0
                    info.uname = info.gname = ""
                    info.mode = 0o644
                    with open(src / key, "rb") as f:
                        tar.addfile(info, f)
        except BrokenPipeError:
            pass  # the remote side stopped reading; its exit status says why
        finally:
            with contextlib.suppress(BrokenPipeError):
                proc.stdin.close()
            with proc.stderr:
                err = proc.stderr.read().decode(errors="replace").strip()
            if proc.wait() != 0:
                raise Fail(f"{self.label}: {err or 'tar failed'}")


def resolve_media(spec: str, cfg: dict | None) -> MediaStore:
    """gcs | azure | vps | local | gs://BUCKET | az://ACCOUNT/CONTAINER | a folder -> a media store."""
    if spec.startswith("gs://"):
        return GcsStore(spec[5:].strip("/"))
    if spec.startswith("az://"):
        account, _, container = spec[5:].strip("/").partition("/")
        return AzureStore(account, container or "media")
    if spec == "local":
        return ComposeStore("media volume of the local Docker stack")
    if spec in ("gcs", "azure", "vps"):
        cfg = cfg if cfg is not None else load_tfvars()
        if spec == "gcs":
            if not cfg.get("media_gcs_bucket"):
                raise Fail("set media_gcs_bucket in secret.tfvars")
            return GcsStore(cfg["media_gcs_bucket"])
        if spec == "azure":
            if not cfg.get("media_azure_account"):
                raise Fail("set media_azure_account in secret.tfvars")
            return AzureStore(cfg["media_azure_account"], cfg.get("media_azure_container") or "media")
        vps = resolve("vps", cfg, "local")
        return ComposeStore(f"media volume on the VPS ({vps.user}@{vps.host})", vps.ssh_prefix(), vps.dir)
    return DirStore(Path(spec).expanduser().resolve())


def human(n: float) -> str:
    for unit in ("B", "KB", "MB", "GB"):
        if n < 1024 or unit == "GB":
            return f"{n:.0f} {unit}" if unit == "B" else f"{n:.1f} {unit}"
        n /= 1024
    return f"{n:.1f} TB"


def media_compare(a: Listing, b: Listing) -> tuple[list[str], list[str], list[str]]:
    """Keys of a that are missing in b, that differ, and keys only in b."""
    missing = [k for k in a if k not in b]
    differ = [k for k in a if k in b and (a[k][0] != b[k][0] or (a[k][1] and b[k][1] and a[k][1] != b[k][1]))]
    return missing, differ, [k for k in b if k not in a]


def media_verify(src: MediaStore, dst: MediaStore, before: Listing | None = None) -> bool:
    a, b = before if before is not None else src.list(), dst.list()
    missing, differ, extra = media_compare(a, b)
    say(f"  {src.label}: {len(a)} files, {human(sum(s for s, _ in a.values()))}")
    say(f"  {dst.label}: {len(b)} files, {human(sum(s for s, _ in b.values()))}")
    for title, keys in [("missing at the target", missing), ("different at the target", differ)]:
        if keys:
            say(f"  {len(keys)} {title}: {', '.join(keys[:10])}{' ...' if len(keys) > 10 else ''}")
    if extra:
        say(f"  ({len(extra)} files exist only at the target; they are left alone)")
    if not missing and not differ:
        say(f"  All {len(a)} files are at the target with the same size and checksum.")
    return not missing and not differ


def media_copy(src: MediaStore, dst: MediaStore) -> Path | None:
    before = src.list()
    if not before:
        raise Fail(f"{src.label} has no files; nothing to copy")
    say(f"  {src.label}: {len(before)} files, {human(sum(s for s, _ in before.values()))}")
    if isinstance(src, DirStore):
        # Upload a clean copy, without hidden files such as .DS_Store.
        with tempfile.TemporaryDirectory() as tmp:
            src.pull(Path(tmp))
            say(f"  Uploading to {dst.label} ...")
            dst.push(Path(tmp))
        staging = None
    else:
        staging = backup_path("media " + src.label).with_suffix("")
        staging.mkdir(parents=True)
        say(f"  Downloading to {staging.relative_to(REPO) if staging.is_relative_to(REPO) else staging} "
            "(kept as a backup) ...")
        src.pull(staging)
        # az download-batch cannot leave out hidden blobs, so drop them here (children first).
        for f in sorted(staging.rglob("*"), reverse=True):
            if hidden(f.relative_to(staging).as_posix()):
                f.rmdir() if f.is_dir() and not f.is_symlink() else f.unlink()
        say(f"  Uploading to {dst.label} ...")
        dst.push(staging)
    if not media_verify(src, dst, before):
        raise Fail("the copy does not match the source")
    return staging


# --- terraform / ansible helpers ------------------------------------------------------------

def tf_root(name: str) -> Path:
    return REPO / "infra" / "terraform" / name


def _tf(root: Path, *args: str) -> str:
    p = subprocess.run(["terraform", f"-chdir={root}", *args], capture_output=True, text=True)
    if p.returncode != 0:
        err = re.sub(r"\x1b\[[0-9;]*m", "", p.stderr).strip()
        raise Fail(f"terraform {args[0]} in {root.relative_to(REPO)} failed:\n{err}")
    return p.stdout


def _tf_read(root: Path, *args: str) -> str:
    """A read-only terraform command, after `terraform init` if the root's backend is not set up here yet."""
    try:
        return _tf(root, *args)
    except Fail as e:
        if "terraform init" not in str(e):
            raise
        _tf(root, "init", "-input=false")
        return _tf(root, *args)


def dns_target() -> str | None:
    """Which target the dns/ root last pointed the domain at, if known."""
    if not shutil.which("terraform"):
        return None
    try:
        out = _tf_read(tf_root("dns"), "output", "-raw", "site_target").strip()
    except Fail:
        return None
    return out if out in TARGETS else None


def _site_keys(records: list[dict]) -> dict[str, str]:
    """{key: content} keyed as modules/cloudflare-dns keys the site records: type:name:position within them."""
    out: dict[str, str] = {}
    counts: dict[str, int] = {}
    for r in records:
        group = f"{r['type']}:{r['name']}"
        out[f"{group}:{counts.get(group, 0)}"] = str(r["content"]).strip('"')
        counts[group] = counts.get(group, 0) + 1
    return out


def dns_site_records() -> dict[str, str] | None:
    """The site records the dns/ state holds, {key: content}, or None if it cannot be read."""
    if not shutil.which("terraform"):
        return None
    try:
        out = _tf_read(tf_root("dns"), "show", "-json")
    except Fail:
        return None
    records: dict[str, str] = {}

    def walk(module: dict) -> None:
        for r in module.get("resources", []):
            if r.get("type") == "cloudflare_dns_record" and r.get("name") == "site":
                records[str(r.get("index"))] = str((r.get("values") or {}).get("content", "")).strip('"')
        for child in module.get("child_modules", []):
            walk(child)
    walk((json.loads(out or "{}").get("values") or {}).get("root_module") or {})
    return records


def target_site_records(target: str) -> dict[str, str] | None:
    """The records that point the domain at a deployed target, from its root's site_records output."""
    try:
        return _site_keys(json.loads(_tf_read(tf_root(target), "output", "-json", "site_records")))
    except (Fail, ValueError, KeyError, TypeError):
        return None


def _routing(records: dict[str, str]) -> dict[str, str]:
    """The records that send visitors somewhere (not Azure's asuid TXT verification records)."""
    return {k: v for k, v in records.items() if k.split(":", 1)[0] in ("A", "AAAA", "CNAME")}


def current_host() -> str | None:
    """Where the domain points: the target whose records the dns/ state holds. The site_target output alone
    is not enough, because Terraform updates it even when the DNS apply fails. None if unknown or mixed."""
    records = dns_site_records()
    if records is None:
        return dns_target()
    routed = _routing(records)
    for target in TARGETS:
        want = _routing(target_site_records(target) or {})
        if want and routed == want:
            return target
    return None


class SwitchedFail(Fail):
    """A deploy failed after the DNS records began to change."""


def deploy(target: str, *, switch_dns: bool, vps_up: str = "all", extra: list[str] | None = None) -> None:
    argv = ["ansible-playbook", "deploy.yml", "-e", f"target={target}",
            "-e", f"switch_dns={'true' if switch_dns else 'false'}", "-e", f"vps_up={vps_up}", *(extra or [])]
    host = current_host() if switch_dns else None
    before = dns_site_records() if switch_dns else None
    say(f"  $ (cd infra/ansible && {' '.join(argv)})")
    if subprocess.run(argv, cwd=REPO / "infra" / "ansible").returncode == 0:
        return
    finish = f"(cd infra/ansible && ansible-playbook deploy.yml -e target={target})"
    if not switch_dns:
        raise Fail(f"the deploy to {target} failed; DNS was not changed")
    # The site_target output changes even when the DNS apply fails, so look at the records themselves.
    now = dns_site_records()
    if before is None or now is None:
        raise SwitchedFail(f"the deploy to {target} failed, and the dns/ state could not be read, so the domain may "
                           f"already point at {target}. Check with `cloudmigrate.py check`, fix the error above and "
                           f"finish with: {finish}")
    if now == before:
        raise Fail(f"the deploy to {target} failed before DNS changed; the domain still points at "
                   f"{host or 'the same host as before'}")
    want = _routing(target_site_records(target) or {})
    got = _routing(now)
    if not want:
        raise SwitchedFail(f"the deploy to {target} failed after the DNS records changed, so the domain may already "
                           f"point at {target}. Fix the error above and finish with: {finish}")
    if got == want:
        raise SwitchedFail(f"the deploy to {target} failed after the domain was pointed at it, so {target} is now "
                           f"the live site. Fix the error above and finish with: {finish}")
    if any(got.get(k) == v and before.get(k) != v for k, v in want.items()):
        raise SwitchedFail(f"the deploy to {target} failed while the domain was being pointed at it: some DNS records "
                           f"point at {target} and some still at {host or 'the old host'}, so visitors reach both. "
                           f"Fix the error above and finish with: {finish}")
    raise SwitchedFail(f"the deploy to {target} failed while the domain was being moved: records of "
                       f"{host or 'the old host'} were changed or removed, but none point at {target} yet. Fix the "
                       f"error above and finish with: {finish}")


# --- dns adopt: records from an older gcp/ or azure/ state into dns/ ----------------------------

def tf_state_pull(root: Path) -> dict | None:
    out = _tf_read(root, "state", "pull")
    return json.loads(out) if out.strip() else None


def tf_state_push(root: Path, path: Path) -> None:
    _tf(root, "state", "push", str(path.resolve()))


def _in_dns_module(r: dict) -> bool:
    return r.get("module", "") == "module.dns" or r.get("module", "").startswith(("module.dns.", "module.dns["))


def _record_ids(resources: list[dict]) -> set:
    return {(r.get("module"), r.get("type"), r.get("name"), json.dumps(inst.get("index_key")),
             (inst.get("attributes") or {}).get("id")) for r in resources for inst in r.get("instances", [])}


def legacy_dns_roots() -> list[str]:
    """The gcp/ and azure/ roots whose state still holds Cloudflare records (deployed before the dns/ root).
    Raises Fail when a root's state exists but cannot be read, rather than taking it as clean."""
    if not shutil.which("terraform"):
        return []
    found = []
    for name in ("gcp", "azure"):
        if not tf_root(name).is_dir():
            continue
        try:
            out = _tf_read(tf_root(name), "state", "list")
        except Fail as e:
            if "No state file was found" in str(e):
                continue
            raise Fail(f"could not check whether the {name} Terraform state still holds the Cloudflare records:\n{e}\n"
                       f"Sign in to the cloud that keeps the {name} state and retry. If this site was never deployed "
                       f"to {name}, comment out the backend block in infra/terraform/{name}/main.tf again.") from None
        if any(line.startswith("module.dns.") for line in out.splitlines()):
            found.append(name)
    return found


def adopt_dns_state(old: dict, dns: dict | None, target: str) -> tuple[dict, dict | None]:
    """Move the module.dns resources of an old gcp/ or azure/ state into a dns/
    state. Returns (the old state without them, the new dns state). The dns
    state is None when it already holds exactly these records: an earlier run
    stopped between its two pushes, and only the old state is left to change."""
    moved = [r for r in old.get("resources", []) if _in_dns_module(r)]
    if not moved:
        raise Fail("this state has no DNS records to move (module.dns); nothing to do")
    adopted = []
    for r in moved:
        r = json.loads(json.dumps(r))
        if r.get("type") == "cloudflare_dns_record" and r.get("name") == "site":
            # The old key's last part was the position in the whole list; dns/ counts within each type and name.
            groups: dict[str, list[tuple[int, dict]]] = {}
            for inst in r.get("instances", []):
                group, _, i = str(inst["index_key"]).rpartition(":")
                groups.setdefault(group, []).append((int(i), inst))
            for group, items in groups.items():
                for j, (_, inst) in enumerate(sorted(items, key=lambda x: x[0])):
                    inst["index_key"] = f"{group}:{j}"
        for inst in r.get("instances", []):
            if "dependencies" in inst:
                inst["dependencies"] = [d for d in inst["dependencies"] if d.startswith("module.dns.")]
        adopted.append(r)
    rest = dict(old, serial=old.get("serial", 0) + 1,
                resources=[r for r in old.get("resources", []) if not _in_dns_module(r)])
    held = [r for r in (dns or {}).get("resources", []) if _in_dns_module(r)]
    if held:
        if _record_ids(held) == _record_ids(adopted):
            return rest, None
        raise Fail("the dns/ state already holds other Cloudflare records than this state. Compare "
                   "`terraform -chdir=infra/terraform/dns state list` with the old root's, and the records in "
                   "Cloudflare, before changing either state by hand")
    new = dict(dns) if dns else {"version": 4, "terraform_version": old.get("terraform_version"), "serial": 0,
                                 "lineage": str(uuid.uuid4()), "outputs": {}, "resources": [], "check_results": None}
    new["serial"] = new.get("serial", 0) + 1
    new["resources"] = list(new.get("resources", [])) + adopted
    new["outputs"] = dict(new.get("outputs") or {}, site_target={"value": target, "type": "string"})
    return rest, new


def _write_private(path: Path, text: str) -> Path:
    """Write a new file only its owner can read (state files hold secrets); never overwrite one."""
    for n in range(100):
        candidate = path if n == 0 else path.with_name(f"{path.stem}-{n}{path.suffix}")
        try:
            fd = os.open(candidate, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        except FileExistsError:
            continue
        with os.fdopen(fd, "w") as f:
            f.write(text)
        return candidate
    raise Fail(f"could not create a backup file next to {path}")


def cmd_dns(args) -> int:
    if not shutil.which("terraform"):
        raise Fail("install Terraform first (see DEPLOY.md)")
    old_root, dns_root = tf_root(args.frm), tf_root("dns")
    old = tf_state_pull(old_root)
    if not old:
        raise Fail(f"infra/terraform/{args.frm} has no state on this machine or in its backend")
    dns = tf_state_pull(dns_root)
    rest, new = adopt_dns_state(old, dns, args.frm)
    count = sum(len(r.get("instances", [])) for r in old["resources"] if _in_dns_module(r))
    paths = {}
    for label, state in [(f"{args.frm} state before dns adopt", old), ("dns state before dns adopt", dns),
                         (f"{args.frm} state after dns adopt", rest), ("dns state after dns adopt", new)]:
        if state is not None:
            paths[label] = _write_private(backup_path(label).with_suffix(".tfstate"), json.dumps(state, indent=2) + "\n")
    rel = BACKUPS.relative_to(REPO) if BACKUPS.is_relative_to(REPO) else BACKUPS
    if new is None:
        say(f"infra/terraform/dns already holds these {count} records (an earlier run stopped half way); "
            f"removing them from infra/terraform/{args.frm} (backups in {rel}/).")
    else:
        say(f"Moving {count} Cloudflare records from infra/terraform/{args.frm} to infra/terraform/dns "
            f"(backups in {rel}/).")
    confirm("Records are not changed in Cloudflare, only which Terraform state owns them.", args.yes)
    # dns/ first: if the second push never happens, both states hold the records, the deploy playbook
    # refuses to run, and running this again finishes the job.
    if new is not None:
        tf_state_push(dns_root, paths["dns state after dns adopt"])
    try:
        tf_state_push(old_root, paths[f"{args.frm} state after dns adopt"])
    except BaseException:
        say(f"\ninfra/terraform/dns holds the records, but they are still in infra/terraform/{args.frm} too. "
            f"Run `cloudmigrate.py dns adopt {args.frm}` again to finish.")
        raise
    say(f"Done. Deploy as usual: (cd infra/ansible && ansible-playbook deploy.yml -e target={args.frm})")
    return 0


# --- move plan ---------------------------------------------------------------------------

@dataclass
class Step:
    kind: str  # deploy | freeze | copy
    text: str
    args: dict = field(default_factory=dict)


def plan_move(frm: str | None, to: str, db_from: str | None, db_to: str | None, *,
              freeze_source: bool = True, stop_before_dns: bool = False) -> list[Step]:
    if to not in TARGETS:
        raise Fail(f"--to must be one of {', '.join(TARGETS)}")
    if db_to == "vps" and to != "vps":
        raise Fail("the VPS database container only runs with the vps target")
    if db_to and db_from == db_to:
        raise Fail("the database source and target are the same")
    if frm == to and not db_to:
        raise Fail(f"the site already runs on {to}; to move only the database, add --db-to")
    steps = []
    if db_to == "vps":
        steps.append(Step("deploy", "Start the database container on the VPS (the app and DNS stay as they are)",
                          {"target": "vps", "switch_dns": False, "vps_up": "db"}))
    if db_to:
        if freeze_source:
            steps.append(Step("freeze", f"Make the {db_from} database read-only (the old site keeps serving pages)",
                              {"db": db_from}))
        steps.append(Step("copy", f"Copy the {db_from} database to {db_to}, then compare every table",
                          {"src": db_from, "dst": db_to}))
    if stop_before_dns:
        steps.append(Step("deploy", f"Deploy to {to} and smoke-test it, without changing DNS",
                          {"target": to, "switch_dns": False, "vps_up": "all"}))
    else:
        steps.append(Step("deploy", f"Deploy to {to}, smoke-test it, then point the domain at it",
                          {"target": to, "switch_dns": True, "vps_up": "all"}))
    return steps


def check_config(cfg: dict, to: str, db_to: str | None, frm: str | None = None) -> None:
    need = {"gcp": ["gcp_project_id"], "azure": ["azure_subscription_id", "acr_name"], "vps": ["vps_ipv4"]}[to]
    missing = [k for k in need if not cfg.get(k) or str(cfg[k]).startswith(("your-", "0000"))]
    if missing:
        raise Fail(f"set {', '.join(missing)} in secret.tfvars for the {to} target")
    local_db = bool(cfg.get("vps_local_db"))
    if db_to == "vps" and not local_db:
        raise Fail("set vps_local_db = true and vps_db_password in secret.tfvars to run the database on the VPS")
    if to == "vps" and local_db and db_to != "vps":
        raise Fail("secret.tfvars has vps_local_db = true, so the VPS would start with an empty database. "
                   "Add --db-to vps to copy the data, or set vps_local_db = false to keep using Neon.")
    if db_to == "neon" and to == "vps" and local_db:
        raise Fail("set vps_local_db = false in secret.tfvars so the VPS uses database_url (Neon)")
    if frm == "vps" and to != "vps" and local_db and db_to != "neon":
        raise Fail(f"the live database is the VPS container (vps_local_db = true), and {to} uses database_url (Neon). "
                   "Move the data too: --db-from vps --db-to neon.")
    if frm is None and to != "vps" and local_db and db_to != "neon":
        raise Fail("secret.tfvars has vps_local_db = true, so the live data may be in the VPS container, and where "
                   "the domain points cannot be read here. Say where the site runs now with --from, or move the "
                   "data with --db-from vps --db-to neon.")
    media = cfg.get("media_storage") or "local"
    if media != "local" and (to, media) not in SAME_CLOUD and not cfg.get("media_access_key"):
        raise Fail(f"a site on {to} reaches the {media} media bucket with a key limited to it: "
                   "set media_access_key = true in secret.tfvars")


# A site in the bucket's own cloud reaches it with its managed identity, no key.
SAME_CLOUD = (("gcp", "gcs"), ("azure", "azure"))


def neon_preflight(cfg: dict, client: str) -> None:
    """The new host will use Neon as it is. A frozen Neon was left behind by an
    earlier move, so the live data is somewhere else."""
    if info(resolve("neon", cfg, client))["read_only"]:
        raise Fail("Neon (database_url) is read-only: an earlier move left it behind, so the live data is elsewhere "
                   "(probably the VPS container). Copy it back with --db-from vps --db-to neon, or, if Neon really is "
                   "current, run `cloudmigrate.py db unfreeze neon` first.")


def media_preflight(cfg: dict, frm: str | None, to: str) -> None:
    if (cfg.get("media_storage") or "local") != "local" or to == "vps":
        return
    hint = (" Copy the files from the VPS volume first: `cloudmigrate.py media copy vps gcs` (or azure)."
            if frm == "vps" else "")
    raise Fail(f"media_storage = \"local\", but {to} discards files written to its containers. Set media_storage = "
               "\"gcs\" or \"azure\" and the bucket in secret.tfvars (see docs/deploy.md#media)." + hint)


def after_move_notes(frm: str | None, to: str, db_from: str | None, db_to: str | None, cfg: dict) -> list[str]:
    domain = cfg.get("domain") or "cadentic.dev"
    notes = [f"Open https://{domain} and https://{domain}/admin, and send a test email "
             "(Subscribers & Email > Providers > Test send). DNS changes can take a few minutes."]
    if db_to:
        notes.append(f"The {db_from} database is read-only and no longer used. Its dump is in backups/. "
                     "Delete it only after the new site has run well for a while.")
    if frm and frm != to:
        if frm == "vps":
            notes.append("Stop the old VPS stack when you are happy: "
                         f"ssh {cfg.get('vps_ssh_user') or 'root'}@{cfg.get('vps_ssh_host') or cfg.get('vps_ipv4')} "
                         f"'cd {cfg.get('vps_dir') or '/opt/portfolio'} && docker compose down'")
        else:
            notes.append(f"Remove the old {frm} deployment when you are happy: "
                         f"terraform -chdir=infra/terraform/{frm} destroy -var-file=../secret.tfvars")
    media = cfg.get("media_storage") or "local"
    if media != "local" and cfg.get("media_access_key") and (to, media) in SAME_CLOUD:
        notes.append(f"The site now reaches the {media} bucket with its own identity. Once the old host is gone, "
                     "set media_access_key = false and deploy again to delete the bucket key.")
    if db_from == "vps" and frm == "vps" and to == "vps":
        notes.append("The old database container keeps running; stop it with "
                     "`docker compose --profile localdb stop db` on the server.")
    back = []
    if db_to:
        back.append(f"python3 tools/cloudmigrate/cloudmigrate.py db unfreeze {db_from}")
    if frm:
        back.append(f"undo your secret.tfvars change, then (cd infra/ansible && ansible-playbook deploy.yml -e target={frm})")
    if back:
        notes.append("To go back: " + "; ".join(back))
    return notes


# --- commands ---------------------------------------------------------------------------------

def cmd_check(args) -> int:
    say("Tools:")
    for tool, why in [("terraform", "deploys"), ("ansible-playbook", "deploys"), ("docker", "images, Postgres tools"),
                      ("ssh", "VPS"), ("psql", "optional"), ("pg_dump", "optional"), ("git", "deploys")]:
        path = shutil.which(tool)
        version = ""
        if path and tool in ("pg_dump", "terraform", "docker"):
            version = subprocess.run([tool, "--version"], capture_output=True, text=True).stdout.splitlines()[0:1]
            version = f" ({version[0]})" if version else ""
        say(f"  {'ok     ' if path else 'missing'} {tool:<17} {why}{version}")
    client = pick_client(args.client)
    say(f"  PostgreSQL {PG_MAJOR} client tools: {'on this machine' if client == 'local' else 'from ' + PG_IMAGE}")
    cfg = load_tfvars()
    say("\nsecret.tfvars:")
    say(f"  domain           {cfg.get('domain')}")
    say(f"  database_url     {redact(cfg['database_url']) if cfg.get('database_url') else 'not set'}")
    for target, keys in [("gcp", ["gcp_project_id", "gcp_region"]), ("azure", ["azure_subscription_id", "azure_location"]),
                         ("vps", ["vps_ipv4", "vps_ssh_user", "vps_local_db"])]:
        values = ", ".join(f"{k}={cfg[k]!r}" for k in keys if k in cfg)
        say(f"  {target:<16} {values or 'not configured'}")
    media = cfg.get("media_storage") or "local"
    where = {"gcs": f"gs://{cfg.get('media_gcs_bucket')}", "azure": f"az://{cfg.get('media_azure_account')}/"
             f"{cfg.get('media_azure_container') or 'media'}", "local": "the server's media volume"}.get(media, media)
    say(f"  media            {media}: {where}{', with a bucket key' if cfg.get('media_access_key') else ''}")
    host = current_host()
    say(f"\nThe domain points at: {host or 'unknown (the dns/ state is not readable here, or its records match no deployed target)'}")
    try:
        for name in legacy_dns_roots():
            say(f"The {name} state still holds the Cloudflare records: run `cloudmigrate.py dns adopt {name}`.")
    except Fail as e:
        say(str(e))
    return 0


def cmd_db(args) -> int:
    cfg = None if all(s in ("local",) or s.startswith("postgres") for s in args.dbs) else load_tfvars()
    dbs = [resolve(s, cfg, args.client) for s in args.dbs]
    if args.action == "info":
        i = info(dbs[0])
        say(f"{dbs[0].label}: PostgreSQL {i['version']}, database {i['database']} as {i['user']}, {i['size']}, "
            f"{i['tables']} tables, migration {i['migration'] or 'none'}{', READ-ONLY' if i['read_only'] else ''}")
        for name, t in stats(dbs[0])["tables"].items():
            say(f"  {name:<34} {t['rows']:>8}")
    elif args.action == "dump":
        dump(dbs[0], Path(args.output).resolve() if args.output else None)
    elif args.action == "restore":
        path = Path(args.file)
        d = info(dbs[0])
        if d["tables"]:
            if not args.replace:
                raise Fail(f"{dbs[0].label} already has {d['tables']} tables. Pass --replace (a backup is saved first).")
            confirm(f"This replaces every table in {dbs[0].label}.", args.yes)
            dump(dbs[0], backup_path(dbs[0].label + " before restore"))
        restore(path, dbs[0])
    elif args.action == "copy":
        copy(dbs[0], dbs[1], replace=args.replace, do_freeze=args.freeze, yes=args.yes)
    elif args.action == "verify":
        return 0 if verify(dbs[0], dbs[1]) else 1
    elif args.action == "freeze":
        confirm(f"{dbs[0].label} will become read-only.", args.yes)
        freeze(dbs[0])
    elif args.action == "unfreeze":
        unfreeze(dbs[0])
    return 0


def cmd_media(args) -> int:
    cfg = load_tfvars() if any(s in ("gcs", "azure", "vps") for s in args.stores) else None
    stores = [resolve_media(s, cfg) for s in args.stores]
    if len(stores) == 2 and stores[0].label == stores[1].label:
        raise Fail("the source and target are the same")
    if args.action == "ls":
        listing = stores[0].list()
        if args.long:
            for key, (size, _) in sorted(listing.items()):
                say(f"  {size:>10}  {key}")
        say(f"{stores[0].label}: {len(listing)} files, {human(sum(s for s, _ in listing.values()))}")
    elif args.action == "copy":
        media_copy(stores[0], stores[1])
    elif args.action == "verify":
        return 0 if media_verify(stores[0], stores[1]) else 1
    return 0


def cmd_move(args) -> int:
    cfg = load_tfvars()
    current = current_host()
    frm = args.from_ or current
    if args.from_ and current and args.from_ != current:
        say(f"Note: the DNS records point at {current}, not {args.from_}.")
    db_from = args.db_from or ({"vps": "neon", "neon": "vps"}.get(args.db_to) if args.db_to else None)
    steps = plan_move(frm, args.to, db_from, args.db_to, freeze_source=not args.no_freeze,
                      stop_before_dns=args.stop_before_dns)
    check_config(cfg, args.to, args.db_to, frm)
    media_preflight(cfg, frm, args.to)
    if legacy := legacy_dns_roots():
        raise Fail(f"the {' and '.join(legacy)} Terraform state still holds the Cloudflare records (a deployment "
                   f"made before infra/terraform/dns). Run `cloudmigrate.py dns adopt {legacy[0]}` first.")
    if not args.db_to and (args.to != "vps" or not cfg.get("vps_local_db")):
        neon_preflight(cfg, args.client)
    tools = ["terraform", "ansible-playbook", "docker"] + (["ssh"] if "vps" in (args.to, frm, db_from, args.db_to) else [])
    if missing := [t for t in tools if not shutil.which(t)]:
        raise Fail(f"install {', '.join(missing)} first (see DEPLOY.md)")

    say(f"Move {frm or '(current host)'} -> {args.to}" + (f", database {db_from} -> {args.db_to}" if args.db_to else ""))
    for i, s in enumerate(steps, 1):
        say(f"  {i}. {s.text}")
    if args.dry_run:
        return 0
    confirm("Start?", args.yes)

    frozen = False
    for i, s in enumerate(steps, 1):
        say(f"\n[{i}/{len(steps)}] {s.text}")
        try:
            if s.kind == "deploy":
                deploy(s.args["target"], switch_dns=s.args["switch_dns"], vps_up=s.args["vps_up"],
                       extra=shlex.split(args.ansible_args))
            elif s.kind == "freeze":
                db = resolve(s.args["db"], cfg, args.client)
                if not info(db)["read_only"]:
                    freeze(db)
                frozen = True
            elif s.kind == "copy":
                # The target is replaced (after a backup); an old frozen copy is reopened first.
                copy(resolve(s.args["src"], cfg, args.client), resolve(s.args["dst"], cfg, args.client),
                     replace=True, do_freeze=False, yes=True, unfreeze_target=True)
        except SwitchedFail as e:
            if frozen:
                raise Fail(f"{e}\nThe data is in the {args.db_to} database now, which {args.to} uses. Leave {db_from} "
                           "read-only until that deploy has finished, and do not run the move again: that would copy "
                           "the old data over the new.") from None
            raise
        except Fail as e:
            if frozen and s.kind == "deploy" and frm == args.to and s.args["vps_up"] == "all":
                # Same host, new database: the stack restarts on the new DATABASE_URL before the checks that failed.
                raise Fail(f"{e}\nThe site on {args.to} may already run on the {args.db_to} database. Fix the error "
                           f"and finish with: (cd infra/ansible && ansible-playbook deploy.yml -e target={args.to}). "
                           "Do not run the move again: that would copy the old data over the new. To go back "
                           f"instead: undo the secret.tfvars change, deploy, and only then run "
                           f"`cloudmigrate.py db unfreeze {db_from}`.") from None
            if frozen:
                raise Fail(f"{e}\nThe {db_from} database is still read-only. To reopen the old site for writes: "
                           f"python3 tools/cloudmigrate/cloudmigrate.py db unfreeze {db_from}") from None
            raise
    say("\nDone." if not args.stop_before_dns else
        f"\nThe new host is ready. Point the domain at it with: (cd infra/ansible && ansible-playbook deploy.yml -e target={args.to})")
    for note in after_move_notes(frm, args.to, db_from, args.db_to, cfg):
        say(f"- {note}")
    return 0


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(prog="cloudmigrate", description=__doc__.split("\n\n")[0])
    ap.add_argument("--client", choices=["auto", "local", "docker"], default="auto",
                    help=f"where the PostgreSQL client tools run: this machine (needs version {PG_MAJOR}+) or {PG_IMAGE}")
    sub = ap.add_subparsers(dest="cmd", required=True)

    sub.add_parser("check", help="show tools, settings and where the domain points")

    db = sub.add_parser("db", help="database info, backup, copy and freeze",
                        description="DB is neon (database_url in secret.tfvars), vps (the Postgres container on "
                                    "the VPS, over SSH), local (the docker compose stack here) or a postgres:// URL.")
    dbs = db.add_subparsers(dest="action", required=True)
    for name, nargs, text in [("info", 1, "version, migration, row counts"),
                              ("dump", 1, "save a backup to backups/ (or -o FILE)"),
                              ("restore", 1, "load a dump file into DB"),
                              ("copy", 2, "copy FROM to TO, then compare every table"),
                              ("verify", 2, "compare row counts and checksums of two databases"),
                              ("freeze", 1, "make DB read-only (the app keeps serving reads)"),
                              ("unfreeze", 1, "allow writes again")]:
        p = dbs.add_parser(name, help=text)
        if name == "restore":
            p.add_argument("file")
        p.add_argument("dbs", nargs=nargs, metavar="DB" if nargs == 1 else "FROM TO")
        if name == "dump":
            p.add_argument("-o", "--output")
        if name in ("restore", "copy"):
            p.add_argument("--replace", action="store_true", help="overwrite a target that already has tables")
        if name == "copy":
            p.add_argument("--freeze", action="store_true", help="make the source read-only first (recommended for a live site)")
        if name in ("restore", "copy", "freeze"):
            p.add_argument("--yes", action="store_true", help="do not ask for confirmation")

    md = sub.add_parser("media", help="list, copy and compare media (uploads and WordPress images)",
                        description="STORE is gcs or azure (the bucket in secret.tfvars), vps (the media volume on "
                                    "the VPS, over SSH), local (the docker compose stack here), gs://BUCKET, "
                                    "az://ACCOUNT/CONTAINER or a folder such as frontend/public/media.")
    mds = md.add_subparsers(dest="action", required=True)
    for name, nargs, text in [("ls", 1, "count the files (--long lists them)"),
                              ("copy", 2, "copy every file from FROM to TO (nothing is deleted), then compare"),
                              ("verify", 2, "check that every file of FROM is at TO with the same size and checksum")]:
        p = mds.add_parser(name, help=text)
        p.add_argument("stores", nargs=nargs, metavar="STORE" if nargs == 1 else "FROM TO")
        if name == "ls":
            p.add_argument("--long", "-l", action="store_true")

    mv = sub.add_parser("move", help="move the site to another host, optionally with its database")
    mv.add_argument("--to", required=True, choices=TARGETS)
    mv.add_argument("--from", dest="from_", choices=TARGETS, help="current host (default: read from the dns/ state)")
    mv.add_argument("--db-to", choices=["vps", "neon"], help="also move the database (vps = container on the VPS)")
    mv.add_argument("--db-from", help="database to copy from (default: the other one of neon/vps, or a postgres:// URL)")
    mv.add_argument("--no-freeze", action="store_true", help="do not make the old database read-only during the copy")
    mv.add_argument("--stop-before-dns", action="store_true", help="deploy and test the new host but leave DNS alone")
    mv.add_argument("--ansible-args", default="", help="extra ansible-playbook arguments, e.g. \"-K\" for a sudo password")
    mv.add_argument("--dry-run", action="store_true", help="only print the steps")
    mv.add_argument("--yes", action="store_true", help="do not ask for confirmation")

    dn = sub.add_parser("dns", help="Cloudflare records in Terraform")
    dns = dn.add_subparsers(dest="action", required=True)
    p = dns.add_parser("adopt", help="move the records of a deployment made before the dns/ root into it",
                       description="Deployments made before infra/terraform/dns kept the Cloudflare records in the "
                                   "gcp/ or azure/ state. This moves them into the dns/ state without touching "
                                   "Cloudflare; the deploy playbook stops until it has run.")
    p.add_argument("frm", choices=["gcp", "azure"], metavar="FROM", help="gcp or azure")
    p.add_argument("--yes", action="store_true", help="do not ask for confirmation")

    args = ap.parse_args(argv)
    try:
        return {"check": cmd_check, "db": cmd_db, "media": cmd_media, "move": cmd_move, "dns": cmd_dns}[args.cmd](args)
    except Fail as e:
        print(f"error: {e}", file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        print("\ninterrupted", file=sys.stderr)
        return 130


if __name__ == "__main__":
    sys.exit(main())
