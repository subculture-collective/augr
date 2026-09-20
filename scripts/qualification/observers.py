"""Prospective, bounded observation. Completion is evidence, never a GO."""
import datetime as dt
import json
from pathlib import Path
import re
import time
from zoneinfo import ZoneInfo

from . import queries
from .core import (UTC, Refusal, collect, digest, instant, lock, require,
                   save_receipt, stamp, write_json)


def make_plan(config, report):
    eastern = ZoneInfo(config['timezone'])
    chicago = ZoneInfo(config['display_timezone'])
    registered = {x.get('name'): x.get('cron') for x in report['sections'].get('scheduler_events', [])
                  if x['event'] == 'registered'}
    jobs = {x['name']: x for x in report['sections'].get('scheduler', [])}
    controls = {x['job_name']: x['enabled'] for x in report['sections'].get('controls', [])}
    boundaries = [('strategy', '2026-09-21T10:00:00')]
    boundaries += [('options_scan', '2026-09-21T22:00:00'),
                   ('history_refresh', '2026-09-22T00:00:00'),
                   ('overnight_sweep', '2026-09-22T00:30:00')]
    boundaries += [('overnight_backtest', f'2026-09-22T{hour:02d}:{minute:02d}:00')
                   for hour in range(1, 6) for minute in (0, 30)]
    boundaries += [('overnight_generate', '2026-09-22T06:00:00'),
                   ('options_discovery', '2026-09-22T06:30:00')]
    result = []
    for name, local in boundaries:
        boundary = dt.datetime.fromisoformat(local).replace(tzinfo=eastern)
        target = config['strategy']['id'] if name == 'strategy' else name
        kind = 'strategy' if name == 'strategy' else 'automation'
        if name == 'strategy':
            matched = any(x.get('event') == 'strategy_registered'
                          and x.get('strategy_id') == target
                          and x.get('schedule') == config['strategy']['schedule']
                          for x in report['sections'].get('scheduler_events', []))
            status = 'registered' if matched else 'registration_unverified'
        elif registered.get(name) != config['schedules'][name]:
            status = 'absent_or_schedule_mismatch'
        elif controls.get(name) is False or jobs.get(name, {}).get('enabled') is False:
            status = 'disabled'
        elif jobs.get(name, {}).get('enabled') is True:
            status = 'enabled'
        else:
            status = 'registered_enabled_state_unverified'
        result.append({'job': name, 'kind': kind, 'target': target,
                       'due_at': stamp(boundary), 'eastern': boundary.isoformat(),
                       'chicago': boundary.astimezone(chicago).isoformat(),
                       'arm_by': stamp(boundary - dt.timedelta(minutes=5)),
                       'runtime_status': status,
                       'command': f'./scripts/qualify-paper.py observe --kind {kind} --target {target} --due {stamp(boundary)} --evidence-dir /var/lib/augr-qualification/observations',
                       'deadline': stamp(boundary + dt.timedelta(seconds=config['monitoring']['job_timeout_seconds']))})
    return {'format': 1, 'created_from_receipt_sha256': digest(report),
            'timezone': config['timezone'], 'boundaries': result,
            'calendar': '2026-09-21 NYSE trading day verified by pinned-source regression test; no runtime calendar endpoint',
            'capability_review': config['capability_review_jobs'],
            'state': 'prepared_not_armed',
            'instruction': 'Recollect before arming; absent, disabled or unverified jobs require explicit review. Never trigger them to supply evidence.'}


class Clock:
    def now(self):
        return dt.datetime.now(UTC)

    def sleep(self, seconds):
        time.sleep(seconds)


def observe(runtime, config, kind, target, due, evidence_dir, clock=None,
            lead=120, timeout=7200, poll=5):
    clock = clock or Clock()
    require(kind in ('strategy', 'automation'), 'invalid_observer_kind')
    require(target == config['strategy']['id'] if kind == 'strategy'
            else target in config['schedules'], 'unknown_observer_target')
    require(0 < lead <= 3600 and 0 < timeout <= 28800 and 0 < poll <= 30, 'invalid_observer_bounds')
    boundary = instant(due)
    armed = clock.now()
    parent = Path(evidence_dir)
    parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    key = digest([kind, target, stamp(boundary)])
    report = {'format': 1, 'mode': 'prospective', 'kind': kind, 'target': target,
              'due_at': stamp(boundary), 'armed_at': stamp(armed), 'events': [],
              'outcome': 'incomplete', 'qualification': 'not_a_go_decision'}
    # A persistent journal survives SIGKILL/host loss. Recovery is a failed/missed
    # observation, never a backdated restart. Re-arming the same slot is refused.
    journal = parent / (key + '.journal.ndjson')
    with lock(parent / (key + '.lock')):
        require(not journal.exists(), 'observation_slot_already_owned')
        with journal.open('x') as stream:
            import os
            os.chmod(journal, 0o600)

            def event(event_name, **fields):
                item = {'at': stamp(clock.now()), 'event': event_name, **fields}
                report['events'].append(item)
                stream.write(json.dumps(item, sort_keys=True) + '\n')
                stream.flush()
                os.fsync(stream.fileno())

            try:
                event('armed')
                require(armed < boundary - dt.timedelta(seconds=lead), 'late_arm')
                while clock.now() < boundary - dt.timedelta(seconds=lead):
                    clock.sleep(min(30, (boundary - dt.timedelta(seconds=lead) - clock.now()).total_seconds()))
                report['precheck'] = collect(runtime, config, stamp(armed), 'prospective_precheck', clock.now())
                require(report['precheck'].get('baseline_sha256'), 'precheck_identity_failed')
                require(clock.now() < boundary, 'missed_precheck_boundary')
                require(report['precheck'].get('collection_status') == 'complete', 'precheck_evidence_incomplete')
                if kind == 'automation':
                    jobs = {x['name']: x for x in report['precheck']['sections']['scheduler']}
                    require(jobs.get(target, {}).get('enabled') is True, 'observed_job_not_enabled')
                    require(any(x['event'] == 'registered' and x.get('name') == target
                                and x.get('cron') == config['schedules'][target]
                                for x in report['precheck']['sections']['scheduler_events']), 'schedule_unverified')
                event('precheck_complete', baseline_sha256=report['precheck']['baseline_sha256'])
                # A run must start in this slot, not at any later recurrence.
                admission_end = boundary + dt.timedelta(seconds=60)
                deadline = boundary + dt.timedelta(seconds=timeout)
                pinned = None
                while True:
                    require(clock.now() <= deadline, 'terminal_timeout')
                    rows = runtime.db(queries.observed_runs(kind, target, stamp(boundary), stamp(admission_end)))
                    require(len(rows) <= 1, 'ambiguous_run_identity')
                    if rows:
                        row = rows[0]
                        require(pinned is None or row['id'] == pinned, 'run_identity_changed')
                        pinned = row['id']
                        report['run'] = row
                        if kind == 'strategy':
                            require(row['execution_version_id'] == config['strategy']['execution_version_id'], 'execution_version_changed')
                        event('run_state', run_id=pinned, status=row['status'])
                        if row['completed_at']:
                            require(instant(row['completed_at']) <= deadline, 'terminal_after_deadline')
                            require(row['status'] in ('ok', 'completed') and not row['error_present'], 'natural_run_failed')
                            break
                    elif clock.now() >= admission_end:
                        raise Refusal('missed_natural_run')
                    clock.sleep(poll)
                logs = runtime.logs(stamp(armed))
                report['scheduler_events'] = logs
                if kind == 'automation':
                    require(not any(x['event'] == 'manual' and x.get('job') == target for x in logs), 'manual_run_rejected')
                    require(any(x['event'] == 'starting' and x.get('job') == target
                                and abs((instant(x['time']) - instant(row['started_at'])).total_seconds()) < 5
                                for x in logs), 'natural_trigger_unverified')
                else:
                    manual = runtime.db(queries.sections(stamp(armed))['manual_strategy_runs'])
                    require(not any(x['entity_id'] == target for x in manual), 'manual_run_rejected')
                    require(any(x['event'] == 'strategy_triggered' and x.get('strategy_id') == target
                                and abs((instant(x['time']) - instant(row['started_at'])).total_seconds()) < 10
                                for x in logs), 'natural_trigger_unverified')
                report['postcheck'] = collect(runtime, config, stamp(armed), 'prospective_postcheck', clock.now())
                require(report['postcheck'].get('baseline_sha256') == report['precheck']['baseline_sha256'], 'observation_baseline_drift')
                require(report['postcheck']['collection_status'] == 'complete', 'postcheck_evidence_incomplete')
                report['outcome'] = 'natural_terminal_evidence_collected'
                report['domain_review'] = 'pending: review inputs, result counters, persistence and downstream effects; terminal success alone is insufficient'
                event('terminal_evidence_collected', run_id=pinned)
            except Refusal as error:
                report['outcome'] = str(error)
                event('failed', code=str(error))
            except (KeyboardInterrupt, SystemExit):
                report['outcome'] = 'observer_interrupted'
                event('failed', code='observer_interrupted')
            except Exception:
                report['outcome'] = 'observer_internal_error'
                event('failed', code='observer_internal_error')
            finally:
                report['finished_at'] = stamp(clock.now())
                directory = save_receipt(parent, report)
    return report, directory
