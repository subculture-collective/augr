#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
scan_dir=$(mktemp -d /tmp/augr-gitleaks.XXXXXX)
chmod 700 "$scan_dir"
trap 'rm -rf "$scan_dir"' EXIT HUP INT TERM

image="${GITLEAKS_IMAGE:-zricethezav/gitleaks@sha256:cdbb7c955abce02001a9f6c9f602fb195b7fadc1e812065883f695d1eeaba854}"

if [ -f "$repo_dir/.git" ]; then
  # Linked worktrees store an absolute pointer in .git. Preserve that path in
  # the container and mount the common Git directory so history scanning cannot
  # silently degrade to a zero-commit filesystem scan.
  git_common_dir=$(git -C "$repo_dir" rev-parse --path-format=absolute --git-common-dir)
  docker run --rm \
    -v "$repo_dir:$repo_dir:ro" \
    -v "$git_common_dir:$git_common_dir:ro" \
    -v "$scan_dir:/out" \
    "$image" detect \
    --source="$repo_dir" \
    --no-banner \
    --exit-code=0 \
    --report-format=json \
    --report-path=/out/report.json >/dev/null
else
  docker run --rm \
    -v "$repo_dir:/repo:ro" \
    -v "$scan_dir:/out" \
    "$image" detect \
    --source=/repo \
    --no-banner \
    --exit-code=0 \
    --report-format=json \
    --report-path=/out/report.json >/dev/null
fi

# Commit hashes are not stable finding identities: gitleaks can report the same
# unchanged fixture in every later commit containing its blob. Hash the secret
# inside the mode-0700 temporary directory, then compare unique location/value
# fingerprints without ever printing plaintext findings.
python3 - "$scan_dir/report.json" >"$scan_dir/actual.tsv" <<'PY'
import hashlib
import json
import sys

with open(sys.argv[1], encoding="utf-8") as report:
    findings = json.load(report)

rows = {
    (
        finding["RuleID"],
        finding["File"],
        str(finding["StartLine"]),
        hashlib.sha256(finding["Secret"].encode()).hexdigest(),
    )
    for finding in findings
}
for row in sorted(rows):
    print("\t".join(row))
PY

cat >"$scan_dir/reviewed.tsv" <<'EOF'
generic-api-key	docs/Augr Trading Research/01 Synthesis/Final Combined Automated Trading Synthesis.md	976	71c1db4c0e9d093b769f54e8bd7d32b46ad4e3f2f5880f0bd06b5e5738815b39
generic-api-key	internal/config/validate_test.go	148	491719f3ac19ea286aaa207f9ecdf06b8de3014dd959df477dc16519cb2d09e1
generic-api-key	internal/config/validate_test.go	167	491719f3ac19ea286aaa207f9ecdf06b8de3014dd959df477dc16519cb2d09e1
generic-api-key	internal/config/validate_test.go	39	491719f3ac19ea286aaa207f9ecdf06b8de3014dd959df477dc16519cb2d09e1
generic-api-key	internal/config/validate_test.go	43	491719f3ac19ea286aaa207f9ecdf06b8de3014dd959df477dc16519cb2d09e1
generic-api-key	internal/domain/account_test.go	103	dc558448ab59c4481b8b469062c4372beae55653606659ad35f552e143b79247
generic-api-key	internal/execution/binance/client_test.go	54	23e9efa3ced6e70c7f2dbe0d08295f18f94b0cbd5c6026677e3bc51897ee31f2
generic-api-key	internal/execution/binance/client_test.go	55	23e9efa3ced6e70c7f2dbe0d08295f18f94b0cbd5c6026677e3bc51897ee31f2
EOF

LC_ALL=C sort -o "$scan_dir/reviewed.tsv" "$scan_dir/reviewed.tsv"
if ! diff -u "$scan_dir/reviewed.tsv" "$scan_dir/actual.tsv"; then
  echo "Secret-history verification failed: findings differ from the exact reviewed fingerprints." >&2
  exit 1
fi

echo "Secret-history verification passed: no unreviewed findings."
