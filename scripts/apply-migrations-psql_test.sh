#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
runner="$repo_root/scripts/apply-migrations-psql.sh"
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT
mkdir -p "$test_root/bin" "$test_root/state"
real_git=$(command -v git)

cat >"$test_root/bin/git" <<EOF
#!/usr/bin/env bash
set -euo pipefail
if [[ \${1:-} == ls-files && -n \${FAKE_MISSING_VERSION:-} && \${*: -1} == *"\${FAKE_MISSING_VERSION}"* ]]; then
  exit 0
fi
exec "$real_git" "\$@"
EOF

cat >"$test_root/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
: "${FAKE_STATE_DIR:?}"
db=
query=
single=false
while (($#)); do
  case $1 in
    -d) db=$2; shift 2 ;;
    -c) query=$2; shift 2 ;;
    --single-transaction) single=true; shift ;;
    *) shift ;;
  esac
done
[[ -n $db ]] || { printf 'fake docker did not receive -d\n' >&2; exit 1; }
state="$FAKE_STATE_DIR/$db"
if [[ -n $query ]]; then
  if [[ $query == *"SELECT count(*)::text"* ]]; then
    [[ -f $state ]] || exit 1
    sed -n '1p' "$state"
    exit 0
  fi
  exit 0
fi
input=$(sed -n '1,$p')
if $single; then
  [[ $input == SET\ ROLE\ augr_db_owner\;* ]] || { printf 'migration omitted SET ROLE\n' >&2; exit 1; }
  printf '%s\n' "${input%%$'\n'*}" >>"$FAKE_STATE_DIR/migration_calls"
  if [[ -f $FAKE_STATE_DIR/fail_next ]]; then
    rm "$FAKE_STATE_DIR/fail_next"
    exit 1
  fi
  exit 0
fi
if [[ $input == *"INSERT INTO schema_migrations(version,dirty) VALUES(0,false)"* ]]; then
  if [[ -f $state ]]; then
    IFS='|' read -r count _ _ <"$state"
    [[ $count == 0 ]] || exit 1
  fi
  printf '1|0|f\n' >"$state"
  exit 0
fi
[[ -f $state ]] || exit 1
IFS='|' read -r count version dirty <"$state"
if [[ $input == *"UPDATE schema_migrations SET dirty=true"* ]]; then
  [[ $count == 1 && $dirty == f ]] || exit 1
  printf '1|%s|t\n' "$version" >"$state"
  exit 0
fi
if [[ $input =~ UPDATE[[:space:]]schema_migrations[[:space:]]SET[[:space:]]version=([0-9]+),dirty=false ]]; then
  [[ $count == 1 && $dirty == t ]] || exit 1
  printf '1|%s|f\n' "${BASH_REMATCH[1]}" >"$state"
  exit 0
fi
exit 0
EOF
chmod +x "$test_root/bin/git" "$test_root/bin/docker"

run_runner() {
  PATH="$test_root/bin:$PATH" POSTGRES_USER=test_owner FAKE_STATE_DIR="$test_root/state" "$runner" "$@"
}

expect_failure() {
  if "$@" >"$test_root/stdout" 2>"$test_root/stderr"; then
    printf 'expected failure: %s\n' "$*" >&2
    exit 1
  fi
}

expect_failure run_runner
expect_failure run_runner --database valid --from 0
expect_failure run_runner --database tradingagent --from 0 --to 109
expect_failure run_runner --database 'bad-name' --from 0 --to 109

run_runner --database zero_init --from 0 --to 0
grep -qx '1|0|f' "$test_root/state/zero_init"

printf '0||f\n' >"$test_root/state/empty_metadata"
run_runner --database empty_metadata --from 0 --to 0
grep -qx '1|0|f' "$test_root/state/empty_metadata"

printf '2|108|f\n' >"$test_root/state/duplicate_metadata"
expect_failure run_runner --database duplicate_metadata --from 108 --to 109
printf '1|107|f\n' >"$test_root/state/wrong_from"
expect_failure run_runner --database wrong_from --from 108 --to 109
printf '1|108|t\n' >"$test_root/state/dirty"
expect_failure run_runner --database dirty --from 108 --to 109
grep -q 'database dirty is dirty at version 108' "$test_root/stderr"

FAKE_MISSING_VERSION=000109 expect_failure run_runner --database missing --from 0 --to 109

run_runner --database full --from 0 --to 109
grep -qx '1|109|f' "$test_root/state/full"
[[ $(wc -l <"$test_root/state/migration_calls") -eq 109 ]]

run_runner --database full --from 109 --to 107
grep -qx '1|107|f' "$test_root/state/full"
run_runner --database full --from 107 --to 108
grep -qx '1|108|f' "$test_root/state/full"

run_runner --database injected_failure --from 0 --to 108
touch "$test_root/state/fail_next"
expect_failure run_runner --database injected_failure --from 108 --to 109
grep -qx '1|108|t' "$test_root/state/injected_failure"
expect_failure run_runner --database injected_failure --from 108 --to 109
grep -q 'database injected_failure is dirty at version 108' "$test_root/stderr"
rm "$test_root/state/injected_failure"
run_runner --database injected_failure --from 0 --to 109
grep -qx '1|109|f' "$test_root/state/injected_failure"

exec 8>"$repo_root/.git/augr-migration-runner.lock"
flock -n 8
expect_failure run_runner --database locked --from 0 --to 109
flock -u 8

grep -q '^SET ROLE augr_db_owner;$' "$test_root/state/migration_calls"
printf 'apply-migrations-psql tests passed\n'
