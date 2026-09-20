"""Staged monitor: local JSON notifications and explicit GO-gated activation."""
import datetime as dt
import json
from pathlib import Path
from zoneinfo import ZoneInfo

from .core import (UTC, assess, digest, instant, lock, require, stamp,
                   tool_identity, verify_go, verify_ledger, write_json)


def recent_due(cron, now, config):
    """Bounded lookup for the pinned simple cron expressions, in Eastern time.

    The reviewed week calendar is explicit, not an invented holiday algorithm.
    New dates require regeneration/review against the application's calendar.
    """
    def matches(field, value):
        for part in field.split(','):
            base, _, step = part.partition('/')
            if base == '*':
                if value % int(step or 1) == 0:
                    return True
            elif '-' in base:
                low, high = map(int, base.split('-'))
                if low <= value <= high and (value - low) % int(step or 1) == 0:
                    return True
            elif value == int(base):
                return True
        return False
    minute, hour, day, month, weekday = cron.split()
    require(day == month == '*', 'unsupported_monitor_cron')
    candidate = now.astimezone(ZoneInfo(config['timezone'])).replace(second=0, microsecond=0)
    for _ in range(48 * 60):
        if (matches(minute, candidate.minute) and matches(hour, candidate.hour)
                and matches(weekday, (candidate.weekday() + 1) % 7)):
            return candidate.astimezone(UTC)
        candidate -= dt.timedelta(minutes=1)
    return None


def check(report, config, now=None, expected_baseline=None, window_start=None):
    now = now or dt.datetime.now(UTC)
    findings = assess(report, config, now)
    age = (now - instant(report['observed_at'])).total_seconds()
    if not 0 <= age <= config['monitoring']['receipt_max_age_seconds']:
        findings.append('receipt_stale_or_future')
    if report.get('collection_status') != 'complete':
        findings.append('collection_incomplete')
    if report.get('config_sha256') != digest(config):
        findings.append('configuration_drift')
    if expected_baseline and report.get('baseline_sha256') != expected_baseline:
        findings.append('baseline_drift')
    jobs = {x['job_name']: x for x in report.get('sections', {}).get('latest_jobs', [])}
    start = instant(window_start) if window_start else now
    for name in ('options_scan', 'history_refresh', 'overnight_sweep'):
        due = recent_due(config['schedules'][name], now, config)
        if due and due >= start and (now - due).total_seconds() > config['monitoring']['job_timeout_seconds']:
            job = jobs.get(name, {})
            if (not job.get('started_at') or not 0 <= (instant(job['started_at']) - due).total_seconds() < 60
                    or not job.get('completed_at') or job.get('status') != 'ok'):
                findings.append('scheduled_deadline_missed_' + name)
    if window_start and now.astimezone(ZoneInfo(config['timezone'])).date().isoformat() not in config['monitoring']['reviewed_calendar_dates']:
        findings.append('monitor_calendar_review_required')
    return {'format': 1, 'checked_at': stamp(now), 'state': 'attention' if findings else 'observed_clear',
            'findings': sorted(set(findings)), 'receipt_sha256': digest(report),
            'qualification': 'monitoring_observation_not_go',
            'domain_review': 'Review provider timestamps and job-specific results against frozen gate evidence; no automatic GO.'}


def activate(ledger, report, config, now=None):
    ledger = Path(ledger)
    require(ledger == Path(config['monitoring']['future_ledger']), 'unexpected_monitor_ledger')
    with lock(str(ledger) + '.lock'):
        state = verify_ledger(ledger, report)
        go = json.loads((ledger / 'go.json').read_text())
        require(digest(go) == state['go_sha256'], 'ledger_go_changed')
        verify_go(go, report, config, now or dt.datetime.now(UTC))
        marker = {'baseline_sha256': state['baseline_sha256'], 'config_sha256': digest(config),
                  'tool_sha256': tool_identity()['sha256'], 'activated_at': stamp(now)}
        write_json(ledger / 'monitor-active.json', marker)
    return marker


def active_baseline(ledger, config):
    ledger = Path(ledger)
    require((ledger / 'monitor-active.json').is_file(), 'monitor_inactive')
    marker = json.loads((ledger / 'monitor-active.json').read_text())
    state = json.loads((ledger / 'baseline.json').read_text())
    require(marker['baseline_sha256'] == state['baseline_sha256'], 'activation_baseline_mismatch')
    require(marker['config_sha256'] == digest(config), 'activation_config_mismatch')
    require(marker['tool_sha256'] == tool_identity()['sha256'], 'activation_tool_mismatch')
    return state['baseline_sha256']


def disable(ledger):
    ledger = Path(ledger)
    with lock(str(ledger) + '.lock'):
        marker = ledger / 'monitor-active.json'
        require(marker.is_file(), 'monitor_inactive')
        archived = ledger / ('monitor-disabled-' + dt.datetime.now(UTC).strftime('%Y%m%dT%H%M%S%fZ') + '.json')
        marker.rename(archived)
    return archived
