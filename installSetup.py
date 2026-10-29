#!/usr/bin/env python3
"""Set up infra/terraform/secret.tfvars for a cloud deploy, then optionally deploy.

Every setting can be passed as a flag (or as an environment variable named
SETUP_<SETTING>, e.g. SETUP_CLOUDFLARE_API_TOKEN, which keeps secrets out of
the shell history). Anything still missing is asked for while the script runs,
unless --non-interactive is given. Values already in secret.tfvars are kept
unless a flag changes them.

    # site on profile.cadentic.dev, mail records (SPF, DKIM, DMARC) stay on cadentic.dev
    python3 installSetup.py --target vps --site-host profile --vps-ipv4 203.0.113.10

    # ask for everything that is missing, then run the deploy
    python3 installSetup.py --deploy

    # see what would change without writing anything
    python3 installSetup.py --site-host profile.cadentic.dev --dry-run

The secrets file is written with mode 0600; the previous one is kept next to
it as secret.backup-<time>.tfvars (both are git-ignored). The deploy runs
infra/ansible/deploy.yml, so it needs Linux or WSL (on Windows 11: run it in
WSL Ubuntu, from a clone in the WSL home folder). Writing the file works on
Windows too. Standard library only. See DEPLOY.md and docs/deploy.md.
"""
from __future__ import annotations

import argparse
import getpass
import ipaddress
import os
import re
import secrets
import shutil
import subprocess
import sys
import time
from dataclasses import dataclass
from pathlib import Path

REPO = Path(__file__).resolve().parent
TF_DIR = REPO / "infra" / "terraform"
EXAMPLE = TF_DIR / "secret.tfvars.example"
TARGETS = ("gcp", "azure", "vps")
MEDIA = ("local", "gcs", "azure")
# A site in the bucket's own cloud reaches it with its own identity; anywhere else it needs a key.
SAME_CLOUD = (("gcp", "gcs"), ("azure", "azure"))

sys.path.insert(0, str(REPO / "tools" / "cloudmigrate"))
from cloudmigrate import Fail, parse_tfvars  # noqa: E402  (the same secret.tfvars reader as cloudmigrate)


@dataclass(frozen=True)
class Setting:
    key: str
    prompt: str
    secret: bool = False
    flag: str = ""  # defaults to --key-with-dashes

    @property
    def option(self) -> str:
        return self.flag or "--" + self.key.replace("_", "-")


# Settings the script knows about, in the order it asks for them.
SETTINGS = [
    Setting("database_url", "Neon direct connection string (postgresql://..., without -pooler)", secret=True),
    Setting("firebase_project_id", "Firebase project id"),
    Setting("firebase_api_key", "Firebase Web API key (AIza...)"),
    Setting("firebase_auth_domain", "Firebase auth domain"),
    Setting("admin_emails", "Admin sign-in emails (comma-separated)"),
    Setting("admin_alert_email", "Private email for delivery alerts"),
    Setting("email_from_address", "Newsletter sender address"),
    Setting("email_brevo_api_key", "Brevo API key (xkeysib-...)", secret=True, flag="--brevo-api-key"),
    Setting("email_mailjet_api_key", "Mailjet API key", secret=True, flag="--mailjet-api-key"),
    Setting("email_mailjet_secret_key", "Mailjet secret key", secret=True, flag="--mailjet-secret-key"),
    Setting("cloudflare_api_token", "Cloudflare API token (Zone:DNS:Edit on the domain)", secret=True),
    Setting("cloudflare_zone_id", "Cloudflare zone id"),
    Setting("gcp_project_id", "Google Cloud project id"),
    Setting("gcp_region", "Cloud Run region"),
    Setting("azure_subscription_id", "Azure subscription id"),
    Setting("azure_location", "Azure region"),
    Setting("acr_name", "Azure Container Registry name (globally unique, lowercase letters and digits)"),
    Setting("vps_ipv4", "Server's public IPv4 address"),
    Setting("vps_ipv6", "Server's public IPv6 address (optional)"),
    Setting("vps_ssh_user", "SSH user on the server"),
    Setting("vps_ssh_port", "SSH port"),
    Setting("vps_db_password", "Password for the server's Postgres container", secret=True),
    Setting("media_gcs_bucket", "Google Cloud Storage bucket for media (globally unique)"),
    Setting("media_azure_account", "Azure storage account for media (3-24 lowercase letters/digits)"),
]
BY_KEY = {s.key: s for s in SETTINGS}

# Example values that only mark where a real value goes.
PLACEHOLDER_MARKS = ("REPLACE_", "...", "USER:PASSWORD", "ep-xxxx", "your-", "you@example.com",
                     "00000000-0000-0000-0000-000000000000")
# Settings whose example value is a real default, not a placeholder.
REAL_DEFAULTS = {"domain", "gcp_region", "azure_location", "acr_name", "vps_ssh_user", "vps_ssh_port",
                 "email_from_address", "dmarc_policy", "media_storage", "media_azure_container"}

HOST_RE = re.compile(r"^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9-]+$")


def say(msg: str = "") -> None:
    print(msg, flush=True)


# --- reading and writing secret.tfvars -------------------------------------------

def hcl_value(value) -> str:
    """A value as HCL: strings quoted and escaped (including ${ and %{ templates)."""
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, int):
        return str(value)
    s = str(value).replace("\\", "\\\\").replace('"', '\\"').replace("\n", "\\n")
    return '"' + s.replace("${", "$${").replace("%{", "%%{") + '"'


def _scalar_end(rest: str) -> int | None:
    """Where a one-line scalar value at the start of rest ends, or None if it is not one (a list, a heredoc)."""
    if rest.startswith('"'):
        i = 1
        while i < len(rest):
            if rest[i] == "\\":
                i += 2
                continue
            if rest[i] == '"':
                return i + 1
            i += 1
        return None
    m = re.match(r"(true|false|null|-?[0-9][0-9.eE+-]*)(?=\s|$|#|//)", rest)
    return m.end() if m else None


def set_values(text: str, changes: dict) -> tuple[str, list[str]]:
    """text with each key = value replaced in place (keeping trailing comments), a commented-out
    `# key = ...` line taken over, or the key appended. A value of None comments the line out."""
    lines = text.splitlines(keepends=True)
    problems: list[str] = []
    for key, value in changes.items():
        active = re.compile(rf"^(\s*){re.escape(key)}(\s*=\s*)(.*?)(\r?\n)?$")
        commented = re.compile(rf"^(\s*)#\s*{re.escape(key)}\s*=.*?(\r?\n)?$")
        done = False
        for i, line in enumerate(lines):
            m = active.match(line)
            if not m:
                continue
            rest = m.group(3)
            end = _scalar_end(rest)
            if end is None:
                problems.append(f"{key} spans several lines in secret.tfvars; change it by hand")
                done = True
                break
            tail, nl = rest[end:], m.group(4) or ""
            if value is None:
                lines[i] = f"{m.group(1)}# {key}{m.group(2)}{rest}{nl}"
            else:
                lines[i] = f"{m.group(1)}{key}{m.group(2)}{hcl_value(value)}{tail}{nl}"
            done = True
            break
        if done or value is None:
            continue
        for i, line in enumerate(lines):
            m = commented.match(line)
            if m:
                lines[i] = f"{m.group(1)}{key} = {hcl_value(value)}{m.group(2) or chr(10)}"
                done = True
                break
        if not done:
            if lines and not lines[-1].endswith("\n"):
                lines[-1] += "\n"
            if not any(line.startswith("# --- added by installSetup.py") for line in lines):
                lines.append("\n# --- added by installSetup.py ---\n")
            lines.append(f"{key} = {hcl_value(value)}\n")
    return "".join(lines), problems


def is_missing(key: str, cfg: dict, example: dict) -> bool:
    value = cfg.get(key)
    if value is None or value == "":
        return True
    if not isinstance(value, str):
        return False
    if any(mark in value for mark in PLACEHOLDER_MARKS):
        return True
    return key not in REAL_DEFAULTS and value == example.get(key)


def mask(key: str, value) -> str:
    if value is None:
        return "(unset)"
    setting = BY_KEY.get(key)
    if (setting and setting.secret) or key in ("app_secret",) or key.endswith("_webhook_secret"):
        s = str(value)
        return "(empty)" if not s else s[:4] + "…" + f" ({len(s)} characters)"
    return hcl_value(value)


# --- asking -------------------------------------------------------------------------

class Asker:
    def __init__(self, interactive: bool):
        self.interactive = interactive
        self.missing: list[str] = []

    def text(self, key: str, prompt: str, default: str = "", secret: bool = False, check=None) -> str | None:
        """Ask for one value. Non-interactive: record it as missing and return None."""
        if not self.interactive:
            self.missing.append(key)
            return None
        while True:
            shown = f" [{'keep current' if secret else default}]" if default else ""
            raw = (getpass.getpass(f"{prompt}{shown}: ") if secret else input(f"{prompt}{shown}: ")).strip()
            value = raw or default
            if not value:
                say("  A value is needed.")
                continue
            error = check(value) if check else None
            if error:
                say(f"  {error}")
                continue
            return value

    def choice(self, key: str, prompt: str, options: tuple, default: str | None) -> str | None:
        return self.text(key, f"{prompt} ({'/'.join(options)})", default or "",
                         check=lambda v: None if v in options else f"Choose one of: {', '.join(options)}.")

    def yes(self, prompt: str, default: bool) -> bool:
        if not self.interactive:
            return default
        raw = input(f"{prompt} [{'Y/n' if default else 'y/N'}]: ").strip().lower()
        return default if not raw else raw in ("y", "yes")


# --- checks -------------------------------------------------------------------------

def check_domain(v: str) -> str | None:
    return None if HOST_RE.match(v.lower()) else "Not a valid domain name."


def normalize_site_host(raw: str, domain: str) -> str:
    """'' / '@' / 'apex' / the domain -> '' (the domain itself); 'profile' -> 'profile.<domain>'."""
    v = raw.strip().lower().rstrip(".")
    if v in ("", "@", "apex", domain):
        return ""
    if "." not in v:
        v = f"{v}.{domain}"
    if not v.endswith("." + domain) or not HOST_RE.match(v):
        raise Fail(f"site host {raw!r} must be the domain {domain} or a name under it, such as profile.{domain}")
    return v


def site_host_error(v: str, domain: str) -> str | None:
    try:
        normalize_site_host(v, domain)
        return None
    except Fail as e:
        return str(e)


def check_ipv4(v: str) -> str | None:
    try:
        ipaddress.IPv4Address(v)
        return None
    except ValueError:
        return "Not an IPv4 address."


def check_ipv6(v: str) -> str | None:
    try:
        ipaddress.IPv6Address(v)
        return None
    except ValueError:
        return "Not an IPv6 address."


CHECKS = {
    "database_url": lambda v: (None if re.match(r"^postgres(ql)?://", v) and "-pooler" not in v
                               else "Use the direct postgresql:// string from Neon (pooling off, no -pooler)."),
    "admin_emails": lambda v: None if all("@" in e for e in v.split(",")) else "Comma-separated email addresses.",
    "admin_alert_email": lambda v: None if "@" in v else "An email address.",
    "email_from_address": lambda v: None if "@" in v else "An email address.",
    "vps_ipv4": check_ipv4,
    "vps_ipv6": check_ipv6,
    "vps_ssh_port": lambda v: None if v.isdigit() and 0 < int(v) < 65536 else "A port number.",
    "vps_db_password": lambda v: (None if re.match(r"^[A-Za-z0-9._~-]{16,}$", v)
                                  else "16+ characters from A-Z a-z 0-9 . _ ~ -"),
    "acr_name": lambda v: None if re.match(r"^[a-z0-9]{5,50}$", v) else "5-50 lowercase letters and digits.",
    "media_gcs_bucket": lambda v: (None if re.match(r"^[a-z0-9][a-z0-9_-]{1,61}[a-z0-9]$", v)
                                   else "3-63 lowercase letters, digits, - and _."),
    "media_azure_account": lambda v: None if re.match(r"^[a-z0-9]{3,24}$", v) else "3-24 lowercase letters and digits.",
}


# --- the plan ----------------------------------------------------------------------

def guess_target(cfg: dict, example: dict) -> str | None:
    if not is_missing("vps_ipv4", cfg, example):
        return "vps"
    if not is_missing("gcp_project_id", cfg, example):
        return "gcp"
    if not is_missing("azure_subscription_id", cfg, example):
        return "azure"
    return None


def required_keys(target: str, cfg: dict) -> list[str]:
    keys = ["firebase_project_id", "firebase_api_key", "admin_emails", "email_brevo_api_key",
            "email_mailjet_api_key", "email_mailjet_secret_key", "cloudflare_api_token", "cloudflare_zone_id"]
    if not (target == "vps" and cfg.get("vps_local_db")):
        keys.insert(0, "database_url")
    keys += {"gcp": ["gcp_project_id", "gcp_region"], "azure": ["azure_subscription_id", "azure_location", "acr_name"],
             "vps": ["vps_ipv4", "vps_ssh_user"]}[target]
    media = cfg.get("media_storage") or "local"
    if media == "gcs":
        keys.append("media_gcs_bucket")
    if media == "azure":
        keys.append("media_azure_account")
    return keys


def env_or_flag(args, key: str):
    value = getattr(args, key, None)
    if value is None:
        value = os.environ.get("SETUP_" + key.upper())
    return value


def build_changes(args, cfg: dict, example: dict, ask: Asker) -> tuple[str, dict, list[str]]:
    """Work out the target and every value to write. Returns (target, changes, notes)."""
    changes: dict = {}
    notes: list[str] = []

    def put(key: str, value) -> None:
        if cfg.get(key) != value:
            changes[key] = value
        cfg[key] = value

    # Values given on the command line (or in SETUP_* variables) win.
    for s in SETTINGS:
        value = env_or_flag(args, s.key)
        if value is not None:
            error = CHECKS[s.key](value) if s.key in CHECKS and value != "" else None
            if error:
                raise Fail(f"{s.option}: {error}")
            put(s.key, int(value) if s.key == "vps_ssh_port" else value)
    for item in args.set or []:
        key, sep, value = item.partition("=")
        if not sep or not re.match(r"^[a-z_][a-z0-9_]*$", key):
            raise Fail(f"--set {item!r}: use --set key=value")
        lowered = value.lower()
        put(key, True if lowered == "true" else False if lowered == "false" else
            int(value) if re.match(r"^-?[0-9]+$", value) else value)
    for key in ("vps_local_db", "media_access_key"):
        if getattr(args, key) is not None:
            put(key, getattr(args, key))
    if args.media_storage:
        put("media_storage", args.media_storage)

    # Where the site runs.
    domain = (args.domain or os.environ.get("SETUP_DOMAIN") or cfg.get("domain") or "cadentic.dev").lower()
    if check_domain(domain):
        raise Fail(f"--domain {domain!r}: not a valid domain name")
    put("domain", domain)
    raw_host = args.site_host if args.site_host is not None else os.environ.get("SETUP_SITE_HOST")
    if raw_host is None and ask.interactive:
        raw_host = ask.text("site_host", f"Site hostname: {domain} itself, or a subdomain such as profile.{domain}",
                            default=cfg.get("site_host") or domain, check=lambda v: site_host_error(v, domain))
    if raw_host is not None:
        put("site_host", normalize_site_host(raw_host, domain))
    if args.www is not None:
        put("site_www", {"yes": True, "no": False, "auto": None}[args.www])

    target = args.target or os.environ.get("SETUP_TARGET")
    if target is None and ask.interactive:
        target = ask.choice("target", "Deploy target: gcp = Cloud Run, azure = Container Apps, vps = your server",
                            TARGETS, guess_target(cfg, example))
    if target is None:  # non-interactive: the target whose settings are filled in
        target = guess_target(cfg, example)
        if target is None:
            raise Fail("say where the site runs with --target gcp|azure|vps")
    if target not in TARGETS:
        raise Fail(f"--target must be one of {', '.join(TARGETS)}")

    # Media: Cloud Run and Container Apps discard local files.
    media = cfg.get("media_storage") or "local"
    if media == "local" and target != "vps":
        notes.append(f"{target} cannot keep media on local disk, so media_storage needs a bucket.")
        picked = ask.choice("media_storage", "Media storage", ("gcs", "azure"), "gcs" if target == "gcp" else "azure")
        if picked:
            put("media_storage", picked)
            media = picked
    if media != "local":
        need_key = (target, media) not in SAME_CLOUD
        if need_key and not cfg.get("media_access_key"):
            put("media_access_key", True)
            notes.append(f"media_access_key = true: a {target} site reaches a {media} bucket with a key limited to it.")

    # Fill in what is still missing.
    for key in required_keys(target, cfg):
        if not is_missing(key, cfg, example):
            continue
        s = BY_KEY[key]
        default = "" if is_missing(key, cfg, {}) else str(cfg.get(key))
        value = ask.text(key, s.prompt, default=default, secret=s.secret, check=CHECKS.get(key))
        if value is not None:
            put(key, int(value) if key == "vps_ssh_port" else value)

    # Values that follow from others.
    if is_missing("firebase_auth_domain", cfg, example) and not is_missing("firebase_project_id", cfg, example):
        put("firebase_auth_domain", f"{cfg['firebase_project_id']}.firebaseapp.com")
    if is_missing("admin_alert_email", cfg, example) and not is_missing("admin_emails", cfg, example):
        put("admin_alert_email", str(cfg["admin_emails"]).split(",")[0].strip())

    # Random secrets, generated once and never replaced.
    for key, make in (("app_secret", lambda: secrets.token_urlsafe(48)),
                      ("email_brevo_webhook_secret", lambda: secrets.token_urlsafe(32)),
                      ("email_mailjet_webhook_secret", lambda: secrets.token_urlsafe(32))):
        if is_missing(key, cfg, example):
            put(key, make())
            notes.append(f"Generated {key}.")
    if target == "vps" and cfg.get("vps_local_db") and is_missing("vps_db_password", cfg, example):
        put("vps_db_password", secrets.token_hex(24))
        notes.append("Generated vps_db_password.")

    return target, changes, notes


# --- deploy -------------------------------------------------------------------------

def deploy(target: str, switch_dns: bool, allow_dirty: bool) -> int:
    if os.name == "nt":
        raise Fail("the deploy needs Linux. On Windows 11 open WSL Ubuntu, cd to the clone in your WSL home "
                   f"folder and run: python3 installSetup.py --target {target} --deploy")
    tools = ["terraform", "ansible-playbook", "docker", "git"] + {"gcp": ["gcloud"], "azure": ["az"], "vps": ["ssh"]}[target]
    missing = [t for t in tools if not shutil.which(t)]
    if missing:
        raise Fail(f"missing tools for a {target} deploy: {', '.join(missing)} (see docs/deploy.md)")
    argv = ["ansible-playbook", "deploy.yml", "-e", f"target={target}",
            "-e", f"switch_dns={'true' if switch_dns else 'false'}"]
    if allow_dirty:
        argv += ["-e", "allow_dirty=true"]
    say(f"\n$ (cd infra/ansible && {' '.join(argv)})")
    return subprocess.run(argv, cwd=REPO / "infra" / "ansible").returncode


def next_steps(target: str, cfg: dict) -> list[str]:
    domain = cfg["domain"]
    host = cfg.get("site_host") or domain
    steps = [
        f"Firebase console > Authentication > Settings > Authorized domains: add {host}.",
        f"Brevo and Mailjet webhooks: https://webhook:<secret>@{host}/api/webhooks/brevo and .../mailjet "
        "(the secrets are email_brevo_webhook_secret and email_mailjet_webhook_secret in secret.tfvars).",
        f"Mail DNS stays on {domain}: SPF and DMARC are created for you; paste the Brevo and Mailjet DKIM "
        "records into email_dns_records in secret.tfvars.",
    ]
    if host != domain:
        steps.append(f"Only the site records move to {host}. Anything already on {domain} itself (another site, "
                     "a redirect) is not touched by Terraform.")
    if target == "gcp":
        steps.append(f"Cloud Run domain mapping: verify {domain} in Google Search Console with the account that "
                     "deploys (it covers subdomains).")
    return steps


# --- main --------------------------------------------------------------------------

def parse_args(argv: list[str] | None) -> argparse.Namespace:
    p = argparse.ArgumentParser(description=__doc__.split("\n\n")[0],
                                formatter_class=argparse.RawDescriptionHelpFormatter,
                                epilog="Any setting can also come from SETUP_<SETTING> (e.g. SETUP_DATABASE_URL).")
    p.add_argument("--target", choices=TARGETS, help="where the site runs: gcp, azure or vps")
    p.add_argument("--domain", help="DNS zone and email sending domain (default: cadentic.dev)")
    p.add_argument("--site-host", dest="site_host",
                   help="hostname that serves the site: 'profile' or 'profile.cadentic.dev' for a subdomain, "
                        "'@' for the domain itself")
    p.add_argument("--www", choices=("yes", "no", "auto"),
                   help="also serve www.<site host> (auto: yes on the domain itself, no on a subdomain)")
    for s in SETTINGS:
        p.add_argument(s.option, dest=s.key, metavar="SECRET" if s.secret else "VALUE",
                       help=s.prompt + (" (prefer SETUP_" + s.key.upper() + ")" if s.secret else ""))
    p.add_argument("--vps-local-db", dest="vps_local_db", action=argparse.BooleanOptionalAction,
                   help="run PostgreSQL on the VPS instead of Neon")
    p.add_argument("--media-storage", choices=MEDIA, help="where uploads live: local (VPS only), gcs or azure")
    p.add_argument("--media-access-key", dest="media_access_key", action=argparse.BooleanOptionalAction,
                   help="key limited to the media bucket, for a site outside the bucket's cloud")
    p.add_argument("--set", action="append", metavar="KEY=VALUE", help="any other secret.tfvars setting (repeatable)")
    p.add_argument("--tfvars", type=Path, default=TF_DIR / "secret.tfvars", help="file to write")
    p.add_argument("--non-interactive", action="store_true", help="never ask; fail if a setting is missing")
    p.add_argument("--dry-run", action="store_true", help="show the changes, write nothing, deploy nothing")
    p.add_argument("--deploy", action=argparse.BooleanOptionalAction, default=None,
                   help="run the deploy playbook afterwards (asked if not given)")
    p.add_argument("--switch-dns", action=argparse.BooleanOptionalAction, default=True,
                   help="point the site host at the target after the deploy (default: yes)")
    p.add_argument("--allow-dirty", action="store_true", help="deploy with uncommitted changes")
    return p.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    interactive = not args.non_interactive and sys.stdin.isatty()
    example_text = EXAMPLE.read_text(encoding="utf-8")
    example = parse_tfvars(example_text)
    path: Path = args.tfvars
    exists = path.exists()
    text = path.read_text(encoding="utf-8") if exists else example_text
    cfg = parse_tfvars(text)
    say(f"{'Updating' if exists else 'Creating'} {path} {'' if exists else '(from secret.tfvars.example)'}")

    ask = Asker(interactive)
    target, changes, notes = build_changes(args, cfg, example, ask)
    if ask.missing:
        raise Fail("missing settings: " + ", ".join(BY_KEY[k].option if k in BY_KEY else "--" + k.replace("_", "-")
                                                    for k in ask.missing)
                   + ". Pass them as flags or SETUP_<SETTING> variables, or run without --non-interactive.")

    new_text, problems = set_values(text, changes)
    if problems:
        raise Fail("; ".join(problems))
    host = cfg.get("site_host") or cfg["domain"]
    say(f"\nTarget: {target}. Site: https://{host}. Mail domain (SPF, DKIM, DMARC): {cfg['domain']}.")
    for note in notes:
        say(f"  {note}")
    if changes:
        say("\nChanges:")
        for key, value in changes.items():
            say(f"  {key} = {mask(key, value)}")
    else:
        say("\nsecret.tfvars already holds everything; nothing to change.")

    if args.dry_run:
        say("\nDry run: nothing written.")
        return 0
    if changes or not exists:
        path.parent.mkdir(parents=True, exist_ok=True)
        if exists:
            backup = path.with_name(f"secret.backup-{time.strftime('%Y%m%d-%H%M%S')}.tfvars")
            shutil.copy2(path, backup)
            os.chmod(backup, 0o600)
            say(f"\nPrevious file kept as {backup.name}.")
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8", newline="") as f:
            f.write(new_text)
        os.chmod(path, 0o600)
        say(f"Wrote {path}.")

    say("\nBefore or after the first deploy:")
    for step in next_steps(target, cfg):
        say(f"  - {step}")

    run = args.deploy
    if run is None:
        run = ask.yes(f"\nRun the {target} deploy now?", False)
    if not run:
        say(f"\nDeploy later with: (cd infra/ansible && ansible-playbook deploy.yml -e target={target})")
        return 0
    return deploy(target, args.switch_dns, args.allow_dirty)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Fail as e:
        print(f"installSetup: {e}", file=sys.stderr)
        sys.exit(1)
    except KeyboardInterrupt:
        print("\ninstallSetup: stopped; nothing written.", file=sys.stderr)
        sys.exit(130)
