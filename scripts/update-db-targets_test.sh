#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
updater_source="$repo_root/scripts/update-db-targets.sh"
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT
case_root="$test_root/repo"

old_app_url='postgres://augr_app_runtime:old-app-secret@augr-postgres:5432/tradingagent?sslmode=disable'
old_database_url='postgres://postgres:old-general-secret@augr-postgres:5432/tradingagent?sslmode=disable'
old_projection_url='postgres://augr_projection_writer:old-projection-secret@augr-postgres:5432/tradingagent?sslmode=disable'
new_db='tradingagent_canonical_20260827'
new_app_url="postgres://augr_app_runtime:new-app-secret@augr-postgres:5432/$new_db?sslmode=disable"
new_database_url="postgres://augr_app_runtime:new-general-secret@augr-postgres:5432/$new_db?sslmode=disable"
new_projection_url="postgres://augr_projection_writer:new-projection-secret@augr-postgres:5432/$new_db?sslmode=disable"

make_repo() {
  rm -rf "$case_root"
  mkdir -p "$case_root/scripts" "$case_root/bin"
  install -m 0755 "$updater_source" "$case_root/scripts/update-db-targets.sh"
  printf 'services:\n  app:\n    image: fixture\n' >"$case_root/docker-compose.nuc.yml"
  cat >"$case_root/.env" <<EOF
PRESERVED_VALUE=keep-this-exactly
POSTGRES_DB=tradingagent
APP_DATABASE_URL=$old_app_url
DATABASE_URL=$old_database_url
KALSHI_PROJECTION_DATABASE_URL=$old_projection_url
TRAILING_VALUE=also-preserved
EOF
  chmod 0600 "$case_root/.env"
  git -C "$case_root" init -q
  cat >"$case_root/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${FAKE_DOCKER_LOG:?}"
if [[ ${FAKE_DOCKER_PAUSE:-0} == 1 ]]; then sleep 2; fi
[[ $* == *'compose --env-file '*'-f '*"docker-compose.nuc.yml config --quiet"* ]]
EOF
  chmod +x "$case_root/bin/docker"
  export PATH="$case_root/bin:$ORIGINAL_PATH"
  export FAKE_DOCKER_LOG="$case_root/docker.log"
  : >"$FAKE_DOCKER_LOG"
}

ORIGINAL_PATH=$PATH

run_updater() {
  "$case_root/scripts/update-db-targets.sh" "$@"
}

prepare() {
  run_updater --prepare-rollback --env-file "$case_root/.env"
}

update() {
  local result
  exec 3<<<"$new_app_url"
  exec 4<<<"$new_database_url"
  exec 5<<<"$new_projection_url"
  if run_updater --env-file "$case_root/.env" --postgres-db "$new_db" \
    --app-database-url-fd 3 --database-url-fd 4 --kalshi-projection-database-url-fd 5; then
    result=0
  else
    result=$?
  fi
  exec 3<&- 4<&- 5<&-
  return "$result"
}

expect_failure() {
  if "$@" >"$test_root/stdout" 2>"$test_root/stderr"; then
    printf 'expected failure: %s\n' "$*" >&2
    exit 1
  fi
}

sha() {
  sha256sum "$1" | awk '{print $1}'
}

make_repo
expect_failure run_updater
expect_failure run_updater --validate --restore --env-file "$case_root/.env"
expect_failure run_updater --validate --env-file "$case_root/.env" --postgres-db wrong
expect_failure run_updater --prepare-rollback --env-file "$test_root/not-repository.env"

make_repo
cp "$case_root/.env" "$case_root/outside.env"
chmod 0600 "$case_root/outside.env"
expect_failure run_updater --prepare-rollback --env-file "$case_root/outside.env"

make_repo
printf 'POSTGRES_DB=duplicate\n' >>"$case_root/.env"
expect_failure prepare

make_repo
sed -i '/^DATABASE_URL=/d' "$case_root/.env"
expect_failure prepare

make_repo
chmod 0640 "$case_root/.env"
expect_failure prepare

make_repo
prepare
[[ $(stat -c '%a' "$case_root/.env.canonical-cutover.rollback") == 600 ]]
[[ $(stat -c '%a' "$case_root/.env.canonical-cutover.rollback.meta") == 600 ]]
expect_failure prepare
before_validate=$(sha "$case_root/.env")
run_updater --validate --env-file "$case_root/.env"
[[ $(sha "$case_root/.env") == "$before_validate" ]]

rollback_sha=$(sha "$case_root/.env.canonical-cutover.rollback")
metadata_sha=$(sha "$case_root/.env.canonical-cutover.rollback.meta")
update >"$test_root/update.stdout" 2>"$test_root/update.stderr"
[[ ! -s $test_root/update.stdout && ! -s $test_root/update.stderr ]]
for secret in old-app-secret old-general-secret old-projection-secret new-app-secret new-general-secret new-projection-secret; do
  if grep -Fq "$secret" "$test_root/update.stdout" "$test_root/update.stderr" "$case_root/docker.log"; then
    printf 'secret leaked to updater output or Compose argv\n' >&2
    exit 1
  fi
done
grep -qx "POSTGRES_DB=$new_db" "$case_root/.env"
grep -qx "APP_DATABASE_URL=$new_app_url" "$case_root/.env"
grep -qx "DATABASE_URL=$new_database_url" "$case_root/.env"
grep -qx "KALSHI_PROJECTION_DATABASE_URL=$new_projection_url" "$case_root/.env"
grep -qx 'PRESERVED_VALUE=keep-this-exactly' "$case_root/.env"
grep -qx 'TRAILING_VALUE=also-preserved' "$case_root/.env"
[[ $(stat -c '%a' "$case_root/.env") == 600 ]]
[[ $(sha "$case_root/.env.canonical-cutover.rollback") == "$rollback_sha" ]]
[[ $(sha "$case_root/.env.canonical-cutover.rollback.meta") == "$metadata_sha" ]]
run_updater --validate --env-file "$case_root/.env"
updated_sha=$(sha "$case_root/.env")
run_updater --restore --env-file "$case_root/.env"
grep -qx 'POSTGRES_DB=tradingagent' "$case_root/.env"
[[ $(sha "$case_root/.env.canonical-cutover.pre-restore") == "$updated_sha" ]]
[[ $(stat -c '%a' "$case_root/.env.canonical-cutover.pre-restore") == 600 ]]
[[ $(sha "$case_root/.env.canonical-cutover.rollback") == "$rollback_sha" ]]
[[ $(sha "$case_root/.env.canonical-cutover.rollback.meta") == "$metadata_sha" ]]
expect_failure run_updater --restore --env-file "$case_root/.env"

make_repo
prepare
exec 3<<<"$new_app_url"
exec 4<<<"$new_database_url"
expect_failure run_updater --env-file "$case_root/.env" --postgres-db "$new_db" \
  --app-database-url-fd 3 --database-url-fd 4 --kalshi-projection-database-url-fd 99
exec 3<&- 4<&-

make_repo
prepare
bad_app_url="postgres://augr_app_runtime:x@augr-postgres:5432/wrong_database?sslmode=disable"
exec 3<<<"$bad_app_url"
exec 4<<<"$new_database_url"
exec 5<<<"$new_projection_url"
expect_failure run_updater --env-file "$case_root/.env" --postgres-db "$new_db" \
  --app-database-url-fd 3 --database-url-fd 4 --kalshi-projection-database-url-fd 5
exec 3<&- 4<&- 5<&-

make_repo
prepare
same_user_projection="postgres://augr_app_runtime:x@augr-postgres:5432/$new_db?sslmode=disable"
exec 3<<<"$new_app_url"
exec 4<<<"$new_database_url"
exec 5<<<"$same_user_projection"
expect_failure run_updater --env-file "$case_root/.env" --postgres-db "$new_db" \
  --app-database-url-fd 3 --database-url-fd 4 --kalshi-projection-database-url-fd 5
exec 3<&- 4<&- 5<&-

make_repo
prepare
original_sha=$(sha "$case_root/.env")
export AUGR_UPDATE_DB_TARGETS_FAIL_BEFORE_RENAME=1
expect_failure update
unset AUGR_UPDATE_DB_TARGETS_FAIL_BEFORE_RENAME
[[ $(sha "$case_root/.env") == "$original_sha" ]]

make_repo
prepare
chmod 0644 "$case_root/.env.canonical-cutover.rollback"
expect_failure run_updater --validate --env-file "$case_root/.env"

make_repo
prepare
printf 'tamper\n' >>"$case_root/.env.canonical-cutover.rollback"
expect_failure run_updater --validate --env-file "$case_root/.env"

for key in POSTGRES_DB APP_DATABASE_URL DATABASE_URL KALSHI_PROJECTION_DATABASE_URL; do
  make_repo
  prepare
  sed -i "s/^$key=/$key=tampered-/" "$case_root/.env.canonical-cutover.rollback"
  new_rollback_sha=$(sha "$case_root/.env.canonical-cutover.rollback")
  sed -i "s/^rollback_sha256=.*/rollback_sha256=$new_rollback_sha/" "$case_root/.env.canonical-cutover.rollback.meta"
  expect_failure run_updater --validate --env-file "$case_root/.env"
done

make_repo
prepare
export FAKE_DOCKER_PAUSE=1
exec 3<<<"$new_app_url"
exec 4<<<"$new_database_url"
exec 5<<<"$new_projection_url"
run_updater --env-file "$case_root/.env" --postgres-db "$new_db" \
  --app-database-url-fd 3 --database-url-fd 4 --kalshi-projection-database-url-fd 5 &
updater_pid=$!
sleep 0.2
ps -o args= -p "$updater_pid" >"$test_root/ps-output"
for secret in new-app-secret new-general-secret new-projection-secret; do
  if grep -Fq "$secret" "$test_root/ps-output"; then
    printf 'secret leaked to updater process argv\n' >&2
    exit 1
  fi
done
wait "$updater_pid"
exec 3<&- 4<&- 5<&-
unset FAKE_DOCKER_PAUSE

printf 'update-db-targets tests passed\n'
