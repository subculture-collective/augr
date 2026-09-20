#!/usr/bin/env python3
"""Canonical paper qualification CLI. prepare/collect never initialize a ledger."""
import argparse
import datetime as dt
import json
import os
from pathlib import Path
import signal
import sys

from qualification.core import (ROOT, UTC, Refusal, Runtime, collect, digest, file_hash,
                                initialize, instant, load_config, require, save_receipt,
                                stamp, verify_ledger, write_json)
from qualification.observers import make_plan, observe
from qualification import monitor


def parser():
    result = argparse.ArgumentParser(description=__doc__)
    result.add_argument('command', choices=['prepare', 'collect', 'plan', 'observe', 'init',
                                          'status', 'snapshot', 'monitor-check', 'monitor-activate',
                                          'monitor-disable', 'monitor-run'])
    result.add_argument('--config', type=Path, default=ROOT / 'monitoring/qualification/schema114-1022b401.json')
    result.add_argument('--evidence-dir', type=Path, default=ROOT / 'var/qualification')
    result.add_argument('--since', help='ISO timestamp with timezone; defaults to the preceding 30 minutes')
    result.add_argument('--token-file', type=Path, help='mode-0600 token from normal operator login; never printed')
    result.add_argument('--ledger', type=Path)
    result.add_argument('--go', type=Path, dest='go_file')
    result.add_argument('--receipt', type=Path)
    result.add_argument('--kind', choices=['strategy', 'automation'])
    result.add_argument('--target')
    result.add_argument('--due')
    result.add_argument('--lead', type=int, default=120)
    result.add_argument('--timeout', type=int, default=7200)
    return result


def main():
    os.umask(0o077)
    args = parser().parse_args()
    config = load_config(args.config)
    runtime = Runtime(config, args.token_file)
    now = dt.datetime.now(UTC)
    since = stamp(instant(args.since)) if args.since else stamp(now - dt.timedelta(minutes=30))
    ledger = args.ledger or Path(config['monitoring']['future_ledger'])
    evidence = args.evidence_dir.resolve()
    forbidden = [ledger.resolve(), Path(config['monitoring']['future_ledger']).resolve(),
                 Path('/var/lib/augr-cutover'), (ROOT / 'var/paper-week').resolve()]
    require(not any(evidence == p or p in evidence.parents for p in forbidden), 'evidence_inside_ledger')
    if args.command == 'monitor-disable':
        print(monitor.disable(ledger))
        return 0
    if args.command == 'observe':
        require(all((args.kind, args.target, args.due)), 'observer_arguments_required')
        report, directory = observe(runtime, config, args.kind, args.target, args.due, evidence,
                                    lead=args.lead, timeout=args.timeout)
        print(directory)
        return 0 if report['outcome'] == 'natural_terminal_evidence_collected' else 2
    if args.receipt:
        require(args.command in ('plan', 'monitor-check'), 'saved_receipt_not_allowed_for_mutation')
        manifest = json.loads((args.receipt.parent / 'manifest.json').read_text())
        require(manifest.get(args.receipt.name) == file_hash(args.receipt), 'receipt_checksum_mismatch')
        report = json.loads(args.receipt.read_text())
        require(report.get('config_sha256') == digest(config), 'receipt_configuration_mismatch')
    else:
        report = collect(runtime, config, since, 'dry-run' if args.command in ('prepare', 'collect', 'plan') else 'monitoring')
    directory = save_receipt(evidence, report)
    print(directory)
    if args.command == 'plan':
        write_json(directory / 'observer-plan.json', make_plan(config, report))
    elif args.command == 'init':
        require(args.go_file, 'go_file_required')
        initialize(ledger, json.loads(args.go_file.read_text()), report, config)
    elif args.command in ('status', 'snapshot'):
        verify_ledger(ledger, report)
        if args.command == 'snapshot':
            save_receipt(ledger / 'snapshots', report)
    elif args.command == 'monitor-activate':
        monitor.activate(ledger, report, config)
    elif args.command in ('monitor-check', 'monitor-run'):
        expected = monitor.active_baseline(ledger, config) if args.command == 'monitor-run' else None
        start = json.loads((ledger / 'baseline.json').read_text())['started_at'] if expected else None
        notification = monitor.check(report, config, expected_baseline=expected, window_start=start)
        write_json(directory / 'notification.json', notification)
        print(json.dumps(notification, sort_keys=True))
        return 0 if notification['state'] == 'observed_clear' else 3
    return 0 if report['collection_status'] == 'complete' else 3


if __name__ == '__main__':
    signal.signal(signal.SIGTERM, lambda *_: sys.exit(130))
    try:
        sys.exit(main())
    except Refusal as error:
        print('qualification refused: ' + str(error), file=sys.stderr)
        sys.exit(2)
    except (OSError, ValueError, KeyError, TypeError):
        print('qualification refused: invalid_or_unavailable_input', file=sys.stderr)
        sys.exit(2)
