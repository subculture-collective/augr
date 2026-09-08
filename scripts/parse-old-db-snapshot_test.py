#!/usr/bin/env python3

from __future__ import annotations

import hashlib
import pathlib
import subprocess
import sys
import tempfile
import unittest


SCRIPT = pathlib.Path(__file__).with_name("parse-old-db-snapshot.py")


class ParseOldDatabaseSnapshotTest(unittest.TestCase):
    def test_streams_sections_and_hashes_rows_without_a_server_aggregate(self) -> None:
        buckets = [
            b"00\t1\t" + hashlib.sha256(b"first").hexdigest().encode(),
            b"ff\t2\t" + hashlib.sha256(b"second").hexdigest().encode(),
        ]
        stream = b"\n".join(
            [
                b"__AUGR_SECTION_schema_catalog.tsv__",
                b"107\tf",
                b"__AUGR_SECTION_table_fingerprints.tsv__",
                b"__AUGR_TABLE_BEGIN__public.empty",
                b"__AUGR_TABLE_END__public.empty",
                b"__AUGR_TABLE_BEGIN__public.large",
                *(b"B\t" + bucket for bucket in buckets),
                b"__AUGR_TABLE_END__public.large",
                b"__AUGR_SECTION_protected_snapshots.json__",
                b'{}',
                b"",
            ]
        )
        with tempfile.TemporaryDirectory() as directory:
            completed = subprocess.run(
                [sys.executable, SCRIPT, directory], input=stream, check=False
            )
            self.assertEqual(completed.returncode, 0)
            destination = pathlib.Path(directory)
            self.assertEqual(
                (destination / "schema_catalog.tsv").read_text(), "107\tf\n"
            )
            large_digest = hashlib.sha256(b"\n".join(buckets)).hexdigest()
            expected = "\n".join(
                [
                    f"public.empty\t0\t{hashlib.sha256().hexdigest()}",
                    f"public.large\t3\t{large_digest}",
                    "",
                ]
            )
            self.assertEqual(
                (destination / "table_fingerprints.tsv").read_text(), expected
            )
            self.assertEqual(
                (destination / "protected_snapshots.json").read_text(), "{}\n"
            )

    def test_rejects_unterminated_table_stream(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            completed = subprocess.run(
                [sys.executable, SCRIPT, directory],
                input=b"__AUGR_TABLE_BEGIN__public.broken\nB\t00\t1\tnot-a-digest\n",
                check=False,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )
            self.assertNotEqual(completed.returncode, 0)


if __name__ == "__main__":
    unittest.main()
