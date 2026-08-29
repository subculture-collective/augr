#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
usage:
  update-db-targets.sh --prepare-rollback --env-file .env
  update-db-targets.sh --validate --env-file .env
  update-db-targets.sh --restore --env-file .env
  update-db-targets.sh --env-file .env --postgres-db NAME \
    --app-database-url-fd FD --database-url-fd FD --kalshi-projection-database-url-fd FD
EOF
  exit 2
}

mode=update
mode_set=false
env_file=
postgres_db=
app_database_url_fd=
database_url_fd=
projection_database_url_fd=

while (($#)); do
  case $1 in
    --prepare-rollback|--validate|--restore)
      $mode_set && usage
      mode_set=true
      mode=${1#--}
      shift
      ;;
    --env-file)
      (($# >= 2)) || usage
      env_file=$2
      shift 2
      ;;
    --postgres-db)
      (($# >= 2)) || usage
      postgres_db=$2
      shift 2
      ;;
    --app-database-url-fd)
      (($# >= 2)) || usage
      app_database_url_fd=$2
      shift 2
      ;;
    --database-url-fd)
      (($# >= 2)) || usage
      database_url_fd=$2
      shift 2
      ;;
    --kalshi-projection-database-url-fd)
      (($# >= 2)) || usage
      projection_database_url_fd=$2
      shift 2
      ;;
    *) usage ;;
  esac
done

[[ -n $env_file ]] || usage
if [[ $mode == update ]]; then
  [[ -n $postgres_db && -n $app_database_url_fd && -n $database_url_fd && -n $projection_database_url_fd ]] || usage
  [[ $postgres_db =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || usage
else
  [[ -z $postgres_db && -z $app_database_url_fd && -z $database_url_fd && -z $projection_database_url_fd ]] || usage
fi

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(git -C "$script_dir/.." rev-parse --show-toplevel)
expected_env=$(realpath -- "$repo_root/.env")
resolved_env=$(realpath -- "$env_file")
[[ $resolved_env == "$expected_env" ]] || {
  printf 'refusing env file outside repository root: expected %s\n' "$expected_env" >&2
  exit 1
}

docker compose --env-file "$repo_root/.env" -f "$repo_root/docker-compose.nuc.yml" config --quiet

rollback_file="$resolved_env.canonical-cutover.rollback"
metadata_file="$rollback_file.meta"
pre_restore_file="$resolved_env.canonical-cutover.pre-restore"

run_core() {
  python3 - "$mode" "$resolved_env" "$rollback_file" "$metadata_file" "$pre_restore_file" "$postgres_db" <<'PY'
import hashlib
import os
import re
import stat
import sys
import tempfile
from urllib.parse import unquote, urlsplit

TARGET_KEYS = ("POSTGRES_DB", "APP_DATABASE_URL", "DATABASE_URL", "KALSHI_PROJECTION_DATABASE_URL")
META_KEYS = ("mode", "rollback_sha256", "non_target_sha256") + tuple(f"{key}_sha256" for key in TARGET_KEYS)

def fail(message):
    print(message, file=sys.stderr)
    raise SystemExit(1)

def digest(data):
    return hashlib.sha256(data).hexdigest()

def parse_env(data):
    lines = data.splitlines(keepends=True)
    values, indexes = {}, {}
    for index, line in enumerate(lines):
        raw = line[:-1] if line.endswith(b"\n") else line
        raw = raw[:-1] if raw.endswith(b"\r") else raw
        for key in TARGET_KEYS:
            prefix = key.encode() + b"="
            if raw.startswith(prefix):
                if key in values:
                    fail(f"target key {key} must occur exactly once")
                values[key], indexes[key] = raw[len(prefix):], index
    missing = [key for key in TARGET_KEYS if key not in values]
    if missing:
        fail("missing target key: " + ", ".join(missing))
    non_target = b"".join(line for index, line in enumerate(lines) if index not in indexes.values())
    return lines, values, indexes, non_target

def validate_url(label, raw, database):
    try:
        parsed = urlsplit(raw.decode("utf-8"))
    except (UnicodeDecodeError, ValueError):
        fail(f"{label} is not a valid PostgreSQL URL")
    if parsed.scheme not in ("postgres", "postgresql") or not parsed.hostname or not parsed.username:
        fail(f"{label} is not a complete PostgreSQL URL")
    if unquote(parsed.path.lstrip("/")) != database:
        fail(f"{label} does not target POSTGRES_DB")
    return unquote(parsed.username)

def validate_values(values, expected_database=None, require_shared_general_user=True):
    try:
        database = values["POSTGRES_DB"].decode("utf-8")
    except UnicodeDecodeError:
        fail("POSTGRES_DB is not valid UTF-8")
    if not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", database):
        fail("POSTGRES_DB is not a valid database name")
    if expected_database is not None and database != expected_database:
        fail("POSTGRES_DB does not match --postgres-db")
    app_user = validate_url("APP_DATABASE_URL", values["APP_DATABASE_URL"], database)
    general_user = validate_url("DATABASE_URL", values["DATABASE_URL"], database)
    projection_user = validate_url("KALSHI_PROJECTION_DATABASE_URL", values["KALSHI_PROJECTION_DATABASE_URL"], database)
    if require_shared_general_user and app_user != general_user:
        fail("APP_DATABASE_URL and DATABASE_URL must use the same general username")
    if projection_user in (app_user, general_user):
        fail("general and projection database usernames must differ")

def read_regular_0600(path, label):
    try:
        fd = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
    except OSError as error:
        fail(f"cannot open {label}: {error.strerror}")
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode):
            fail(f"{label} must be a regular file")
        if stat.S_IMODE(info.st_mode) != 0o600:
            fail(f"{label} mode must be 600")
        chunks = []
        while True:
            chunk = os.read(fd, 1024 * 1024)
            if not chunk:
                return b"".join(chunks)
            chunks.append(chunk)
    finally:
        os.close(fd)

def parse_metadata(data):
    try:
        lines = data.decode("ascii").splitlines()
    except UnicodeDecodeError:
        fail("rollback metadata must be ASCII")
    metadata = {}
    for line in lines:
        if "=" not in line:
            fail("rollback metadata is malformed")
        key, value = line.split("=", 1)
        if key in metadata or key not in META_KEYS:
            fail("rollback metadata contains duplicate or unknown fields")
        metadata[key] = value
    if set(metadata) != set(META_KEYS):
        fail("rollback metadata is incomplete")
    return metadata

def validate_artifacts(rollback_path, metadata_path):
    rollback = read_regular_0600(rollback_path, "rollback artifact")
    metadata = parse_metadata(read_regular_0600(metadata_path, "rollback metadata"))
    if metadata["mode"] != "600" or metadata["rollback_sha256"] != digest(rollback):
        fail("rollback artifact mode or checksum mismatch")
    _, values, _, non_target = parse_env(rollback)
    if metadata["non_target_sha256"] != digest(non_target):
        fail("rollback non-target digest mismatch")
    for key in TARGET_KEYS:
        if metadata[f"{key}_sha256"] != digest(values[key]):
            fail(f"rollback {key} hash mismatch")
    validate_values(values, require_shared_general_user=False)
    return rollback, values, non_target

def write_exclusive(path, data):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0), 0o600)
    try:
        view = memoryview(data)
        while view:
            view = view[os.write(fd, view):]
        os.fsync(fd)
    finally:
        os.close(fd)

def fsync_directory(path):
    fd = os.open(path, os.O_RDONLY | getattr(os, "O_DIRECTORY", 0))
    try:
        os.fsync(fd)
    finally:
        os.close(fd)

def atomic_replace(path, data):
    parent = os.path.dirname(path)
    fd, temporary = tempfile.mkstemp(prefix=".canonical-cutover.", dir=parent)
    try:
        os.fchmod(fd, 0o600)
        view = memoryview(data)
        while view:
            view = view[os.write(fd, view):]
        os.fsync(fd)
        os.close(fd)
        fd = -1
        if os.environ.get("AUGR_UPDATE_DB_TARGETS_FAIL_BEFORE_RENAME") == "1":
            fail("injected failure before atomic rename")
        os.replace(temporary, path)
        fsync_directory(parent)
    finally:
        if fd >= 0:
            os.close(fd)
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass

mode, env_path, rollback_path, metadata_path, pre_restore_path, requested_database = sys.argv[1:]
env_data = read_regular_0600(env_path, "environment file")
env_lines, env_values, env_indexes, env_non_target = parse_env(env_data)

if mode == "prepare-rollback":
    validate_values(env_values, require_shared_general_user=False)
    if os.path.lexists(rollback_path) or os.path.lexists(metadata_path):
        fail("rollback artifacts already exist")
    metadata = {"mode": "600", "rollback_sha256": digest(env_data), "non_target_sha256": digest(env_non_target)}
    metadata.update({f"{key}_sha256": digest(env_values[key]) for key in TARGET_KEYS})
    metadata_data = "".join(f"{key}={metadata[key]}\n" for key in META_KEYS).encode("ascii")
    write_exclusive(rollback_path, env_data)
    write_exclusive(metadata_path, metadata_data)
    fsync_directory(os.path.dirname(env_path))
elif mode == "validate":
    _, rollback_values, rollback_non_target = validate_artifacts(rollback_path, metadata_path)
    targets_match_rollback = all(digest(env_values[key]) == digest(rollback_values[key]) for key in TARGET_KEYS)
    validate_values(env_values, require_shared_general_user=not targets_match_rollback)
    if digest(env_non_target) != digest(rollback_non_target):
        fail("environment non-target digest changed")
elif mode == "restore":
    rollback, _, rollback_non_target = validate_artifacts(rollback_path, metadata_path)
    validate_values(env_values)
    if digest(env_non_target) != digest(rollback_non_target):
        fail("environment non-target digest changed")
    if os.path.lexists(pre_restore_path):
        fail("pre-restore artifact already exists")
    write_exclusive(pre_restore_path, env_data)
    fsync_directory(os.path.dirname(env_path))
    atomic_replace(env_path, rollback)
    if read_regular_0600(env_path, "restored environment file") != rollback:
        fail("restored environment does not match rollback artifact")
elif mode == "update":
    _, original_values, rollback_non_target = validate_artifacts(rollback_path, metadata_path)
    validate_values(env_values, require_shared_general_user=False)
    if digest(env_non_target) != digest(rollback_non_target):
        fail("environment non-target digest changed")
    if any(digest(env_values[key]) != digest(original_values[key]) for key in TARGET_KEYS):
        fail("environment target values no longer match the rollback baseline")
    supplied = os.fdopen(3, "rb", closefd=False).read().splitlines()
    if len(supplied) != 3:
        fail("expected exactly three database URLs on protected input")
    replacement_values = {
        "POSTGRES_DB": requested_database.encode("utf-8"),
        "APP_DATABASE_URL": supplied[0],
        "DATABASE_URL": supplied[1],
        "KALSHI_PROJECTION_DATABASE_URL": supplied[2],
    }
    validate_values(replacement_values, requested_database)
    for key in TARGET_KEYS:
        newline = b"\n" if env_lines[env_indexes[key]].endswith(b"\n") else b""
        env_lines[env_indexes[key]] = key.encode() + b"=" + replacement_values[key] + newline
    updated = b"".join(env_lines)
    _, updated_values, _, updated_non_target = parse_env(updated)
    validate_values(updated_values, requested_database)
    if digest(updated_non_target) != digest(env_non_target):
        fail("non-target digest changed during update")
    atomic_replace(env_path, updated)
else:
    fail("unsupported mode")
PY
}

if [[ $mode == update ]]; then
  [[ $app_database_url_fd =~ ^[0-9]+$ && $database_url_fd =~ ^[0-9]+$ && $projection_database_url_fd =~ ^[0-9]+$ ]] || usage
  IFS= read -r app_database_url <&"$app_database_url_fd" || {
    printf 'could not read APP_DATABASE_URL descriptor\n' >&2
    exit 1
  }
  IFS= read -r database_url <&"$database_url_fd" || {
    printf 'could not read DATABASE_URL descriptor\n' >&2
    exit 1
  }
  IFS= read -r projection_database_url <&"$projection_database_url_fd" || {
    printf 'could not read KALSHI_PROJECTION_DATABASE_URL descriptor\n' >&2
    exit 1
  }
  run_core 3< <(printf '%s\n%s\n%s\n' "$app_database_url" "$database_url" "$projection_database_url")
else
  run_core 3</dev/null
fi
