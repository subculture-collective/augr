#!/usr/bin/env python3
"""Split a streamed old-DB snapshot and hash large table row streams."""

from __future__ import annotations

import hashlib
import pathlib
import sys


SECTION_PREFIX = "__AUGR_SECTION_"
TABLE_BEGIN_PREFIX = "__AUGR_TABLE_BEGIN__"
TABLE_END_PREFIX = "__AUGR_TABLE_END__"
BUCKET_PREFIX = "B\t"


def main() -> int:
    if len(sys.argv) != 2:
        raise SystemExit(f"usage: {sys.argv[0]} DESTINATION")

    destination = pathlib.Path(sys.argv[1])
    destination.mkdir(parents=True, exist_ok=True)
    current_handle = None
    handles = []
    table_name = None
    table_hash = None
    table_rows = 0
    fingerprints = (destination / "table_fingerprints.tsv").open("w", encoding="utf-8")
    handles.append(fingerprints)

    try:
        for raw_line in sys.stdin.buffer:
            line = raw_line.removesuffix(b"\n").removesuffix(b"\r").decode("utf-8")
            if line.startswith(SECTION_PREFIX) and line.endswith("__"):
                if table_name is not None:
                    raise SystemExit("section marker appeared inside table stream")
                name = line.removeprefix(SECTION_PREFIX).removesuffix("__")
                current_handle = (destination / name).open("w", encoding="utf-8")
                handles.append(current_handle)
                continue
            if line.startswith(TABLE_BEGIN_PREFIX):
                if table_name is not None:
                    raise SystemExit("nested table stream")
                table_name = line.removeprefix(TABLE_BEGIN_PREFIX)
                table_hash = hashlib.sha256()
                table_rows = 0
                continue
            if line.startswith(TABLE_END_PREFIX):
                ended_name = line.removeprefix(TABLE_END_PREFIX)
                if table_name is None or ended_name != table_name or table_hash is None:
                    raise SystemExit("mismatched table stream terminator")
                fingerprints.write(f"{table_name}\t{table_rows}\t{table_hash.hexdigest()}\n")
                table_name = None
                table_hash = None
                table_rows = 0
                continue
            if table_name is not None:
                if not line.startswith(BUCKET_PREFIX) or table_hash is None:
                    raise SystemExit(f"malformed bucket in table stream {table_name}")
                encoded = line.removeprefix(BUCKET_PREFIX).encode("utf-8")
                parts = encoded.split(b"\t")
                if (
                    len(parts) != 3
                    or len(parts[0]) != 2
                    or len(parts[2]) != 64
                    or any(value not in b"0123456789abcdef" for value in parts[0] + parts[2])
                ):
                    raise SystemExit(f"invalid bucket summary in table stream {table_name}")
                try:
                    bucket_rows = int(parts[1])
                except ValueError as error:
                    raise SystemExit(
                        f"invalid bucket row count in table stream {table_name}"
                    ) from error
                if bucket_rows <= 0:
                    raise SystemExit(f"empty bucket in table stream {table_name}")
                if table_rows:
                    table_hash.update(b"\n")
                table_hash.update(encoded)
                table_rows += bucket_rows
                continue
            if current_handle is not None:
                current_handle.write(line + "\n")
    finally:
        for handle in handles:
            handle.close()

    if table_name is not None:
        raise SystemExit(f"unterminated table stream {table_name}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
