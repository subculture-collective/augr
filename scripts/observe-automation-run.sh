#!/bin/sh
set -eu
repo_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
if [ "$#" -lt 2 ]; then
  echo "usage: $0 job due-at [qualification CLI options]" >&2
  exit 2
fi
job=$1
due=$2
shift 2
exec python3 "$repo_dir/scripts/qualify-paper.py" observe --kind automation --target "$job" --due "$due" "$@"
