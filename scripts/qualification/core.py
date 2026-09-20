"""Read-only qualification and explicitly gated, filesystem-only paper ledgers.

Production access is intentionally local to the verified NUC. Tests inject a
transport/clock in Python; the CLI has no fixture or identity-bypass option.
"""
import contextlib
import datetime as dt
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from zoneinfo import ZoneInfo

from . import queries

ROOT = Path(__file__).resolve().parents[2]
UTC = dt.timezone.utc


class Refusal(Exception):
    """Only fixed, non-sensitive codes may be persisted or printed."""


def require(condition, code):
    if not condition:
        raise Refusal(code)


def instant(value):
    result = dt.datetime.fromisoformat(value.replace('Z', '+00:00'))
    require(result.tzinfo is not None, 'timestamp_requires_timezone')
    return result.astimezone(UTC)


def stamp(value=None):
    return (value or dt.datetime.now(UTC)).astimezone(UTC).isoformat()


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


def file_hash(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def write_json(path, data):
    """Exclusive creation; failed evidence is never silently replaced."""
    with Path(path).open('x') as stream:
        os.chmod(path, 0o600)
        json.dump(data, stream, indent=2, sort_keys=True)
        stream.write('\n')
        stream.flush()
        os.fsync(stream.fileno())


def command(args, timeout=30):
    try:
        run = subprocess.run(args, capture_output=True, text=True, timeout=timeout, check=False)
    except (OSError, subprocess.TimeoutExpired):
        raise Refusal('command_unavailable_or_timeout') from None
    require(run.returncode == 0, 'command_failed')
    return run.stdout


def tool_identity():
    files = sorted((ROOT / 'scripts/qualification').glob('*.py'))
    files += [ROOT / 'scripts' / name for name in ('qualify-paper.py', 'paper-week.sh',
              'observe-automation-run.sh', 'observe-paper-boundary.sh')]
    files += sorted((ROOT / 'monitoring/qualification').glob('*'))
    hashes = {str(p.relative_to(ROOT)): file_hash(p) for p in files if p.is_file()}
    try:
        revision = command(['git', '-C', str(ROOT), 'rev-parse', 'HEAD']).strip()
        dirty = bool(command(['git', '-C', str(ROOT), 'status', '--porcelain']).strip())
    except Refusal:
        revision, dirty = None, None
        source = ROOT / 'qualification-source.json'
        if source.is_file():
            provenance = json.loads(source.read_text())
            require(provenance['files'] == hashes, 'staged_tool_checksum_mismatch')
            revision, dirty = provenance['revision'], provenance['dirty']
    return {'revision': revision, 'dirty': dirty, 'files': hashes, 'sha256': digest(hashes)}


def load_config(path):
    config = json.loads(Path(path).read_text())
    # This tool qualifies this canonical schema, not arbitrary databases.
    require(config['database'] == 'tradingagent_canonical_20260827', 'noncanonical_database')
    require(config['schema'] == 114 and config['host'] == 'nuc', 'unsupported_target')
    require(config['compose_project'] == 'augr', 'wrong_compose_project')
    require(re.fullmatch(r'[0-9a-f]{40}', config['source_revision']), 'invalid_revision')
    for role in ('app', 'web'):
        require(re.fullmatch(r'sha256:[0-9a-f]{64}', config['containers'][role]['image']), 'invalid_image')
    for role, spec in config['containers'].items():
        require(spec['name'] == f'augr-{role}-1', 'unexpected_container')
    require(config['timezone'] == 'America/New_York', 'wrong_cron_timezone')
    require(config['monitoring']['staged_state'] == 'inactive', 'configuration_must_be_staged')
    require(config['monitoring']['auto_disable_failure_threshold'] == 5, 'changed_failure_threshold')
    require(config['safety_flags'] == {
        'ENABLE_LIVE_TRADING': False, 'ENABLE_POLYMARKET_AUTOMATION': False,
        'AUTOMATIC_SHADOW_PROMOTION': False, 'RELEASE_DRILLS_VERIFIED': False,
        'ENABLE_SCHEDULER': True}, 'changed_safety_policy')
    return config


class Runtime:
    def __init__(self, config, token_file=None):
        self.config = config
        self.token_file = token_file

    def inspect(self):
        require(socket.gethostname().split('.')[0] == self.config['host'], 'wrong_host')
        names = [x['name'] for x in self.config['containers'].values()]
        raw = json.loads(command(['docker', 'inspect', *names]))
        result = {}
        for item in raw:
            role = next((k for k, v in self.config['containers'].items()
                         if '/' + v['name'] == item['Name']), None)
            require(role is not None and role not in result, 'ambiguous_container')
            labels = item['Config'].get('Labels') or {}
            result[role] = {
                'id': item['Id'], 'image': item['Image'],
                'revision': labels.get('org.opencontainers.image.revision'),
                'project': labels.get('com.docker.compose.project'),
                'service': labels.get('com.docker.compose.service'),
                'running': item['State']['Running'],
                'health': item['State'].get('Health', {}).get('Status', 'not_configured'),
                'restarts': item['RestartCount'], 'started_at': item['State']['StartedAt'],
            }
            if role == 'app':
                env = dict(x.split('=', 1) for x in item['Config']['Env'] if '=' in x)
                result[role]['database'] = urllib.parse.urlsplit(env.get('DATABASE_URL', '')).path.lstrip('/')
                result[role]['flags'] = {}
                for key in self.config['safety_flags']:
                    value = env.get(key)
                    if value is None and key in self.config['source_default_false_flags']:
                        result[role]['flags'][key] = {'value': False, 'source': 'pinned_source_default'}
                    else:
                        result[role]['flags'][key] = {'value': {'true': True, 'false': False}.get(value), 'source': 'environment'}
        require(len(result) == 4, 'missing_container')
        return result

    def db(self, sql, aggregate=True):
        if aggregate:
            sql = "SELECT COALESCE(json_agg(q),'[]'::json) FROM (" + sql + ') q'
        output = command([
            'docker', 'exec', '-e',
            'PGOPTIONS=-c default_transaction_read_only=on -c statement_timeout=15000 -c lock_timeout=2000',
            self.config['containers']['postgres']['name'], 'psql', '-X', '-qAt',
            '-v', 'ON_ERROR_STOP=1', '-U', self.config['database_user'], '-d', self.config['database'],
            '-c', 'BEGIN READ ONLY; ' + sql + '; COMMIT;'], timeout=25)
        try:
            return json.loads(output)
        except ValueError:
            raise Refusal('invalid_database_response') from None

    def http(self, url, authenticated=False):
        headers = {}
        if authenticated:
            require(self.token_file is not None, 'authenticated_session_unavailable')
            p = Path(self.token_file)
            require(not p.is_symlink() and p.stat().st_mode & 0o077 == 0, 'token_file_permissions')
            token = p.read_text().strip()
            require(token and '\n' not in token, 'invalid_token_file')
            headers['Authorization'] = 'Bearer ' + token
        # Never forward an operator token through a redirect.
        class NoRedirect(urllib.request.HTTPRedirectHandler):
            def redirect_request(self, req, fp, code, msg, headers, newurl):
                return None
        try:
            with urllib.request.build_opener(NoRedirect).open(
                    urllib.request.Request(url, headers=headers), timeout=10) as response:
                data = response.read(4_000_001)
                require(len(data) <= 4_000_000, 'response_too_large')
                return data.decode()
        except (urllib.error.URLError, TimeoutError, OSError):
            raise Refusal('http_unavailable_or_unauthorized') from None

    def logs(self, since):
        # Docker writes application stderr to its stderr. Parse both privately.
        try:
            run = subprocess.run(['docker', 'logs', '--since', since,
                                  self.config['containers']['app']['name']],
                                 capture_output=True, text=True, timeout=20)
        except (OSError, subprocess.TimeoutExpired):
            raise Refusal('logs_unavailable') from None
        require(run.returncode == 0, 'logs_unavailable')
        allowed = {
            'automation: scheduled job': 'registered',
            'automation: manual trigger': 'manual',
            'automation: job starting': 'starting',
            'automation: job completed': 'completed',
            'automation: job enabled state changed': 'enabled_changed',
            'scheduler: registered strategy schedule': 'strategy_registered',
            'scheduler: triggered strategy schedule': 'strategy_triggered',
        }
        result = []
        for line in (run.stdout + '\n' + run.stderr).splitlines():
            try:
                item = json.loads(line)
            except ValueError:
                continue
            if item.get('msg') in allowed:
                row = {k: item[k] for k in ('time', 'name', 'job', 'cron', 'schedule',
                                           'strategy_id', 'enabled') if k in item}
                row['event'] = allowed[item['msg']]
                result.append(row)
        return result


def validate_runtime(config, containers, identity, cohort):
    require(identity == {'database': config['database'], 'schema': config['schema'],
                         'dirty': False, 'read_only': 'on'}, 'database_identity_mismatch')
    for role, spec in config['containers'].items():
        actual = containers[role]
        require(actual['project'] == config['compose_project'] and actual['service'] == role,
                'compose_identity_mismatch')
        require(actual['running'] and actual['health'] in ('healthy', 'not_configured'), 'container_unhealthy')
        if role != 'web':
            require(actual['health'] == 'healthy', 'healthcheck_missing')
        if 'image' in spec:
            require(actual['image'] == spec['image'] and actual['revision'] == config['source_revision'], 'release_identity_mismatch')
    require(containers['app']['database'] == config['database'], 'app_database_mismatch')
    for key, expected in config['safety_flags'].items():
        require(containers['app']['flags'][key]['value'] is expected, 'safety_flag_mismatch')
    require(bool(cohort), 'empty_cohort')
    seen = set()
    for row in cohort:
        try:
            uuid.UUID(row['id'])
            uuid.UUID(row['execution_strategy_version_id'])
        except (ValueError, TypeError, KeyError):
            raise Refusal('invalid_cohort') from None
        require(row['id'] not in seen and bool(row['schedule_cron']) and not row['skip_next_run'], 'invalid_cohort')
        seen.add(row['id'])
    canary = [x for x in cohort if x['id'] == config['strategy']['id']]
    require(len(canary) == 1 and canary[0]['execution_strategy_version_id'] == config['strategy']['execution_version_id']
            and canary[0]['schedule_cron'] == config['strategy']['schedule'], 'strategy_binding_mismatch')


def baseline(config, containers, identity, cohort, controls):
    return {'config_sha256': digest(config), 'database': identity, 'cohort': cohort,
            'controls': controls, 'source_revision': config['source_revision'],
            'containers': {k: {f: v[f] for f in ('id', 'image', 'revision', 'started_at', 'restarts')}
                           for k, v in containers.items()},
            'flags': containers['app']['flags'], 'tool_sha256': tool_identity()['sha256']}


def collect(runtime, config, since, mode='dry-run', now=None):
    now = now or dt.datetime.now(UTC)
    since = stamp(instant(since))
    require(instant(since) <= now, 'future_collection_window')
    report = {'format': 1, 'mode': mode, 'observed_at': stamp(now),
              'chicago_time': now.astimezone(ZoneInfo('America/Chicago')).isoformat(),
              'since': since, 'tool': tool_identity(), 'config_sha256': digest(config),
              'expected_revision': config['source_revision'], 'sections': {}, 'blockers': [],
              'qualification': 'not_a_go_decision'}
    try:
        report['containers'] = runtime.inspect()
        report['database'] = runtime.db(queries.IDENTITY, aggregate=False)
        report['cohort'] = runtime.db(queries.COHORT)
        validate_runtime(config, report['containers'], report['database'], report['cohort'])
    except Refusal as error:
        report['blockers'].append(str(error))
        report['collection_status'] = 'refused'
        return report
    for name, sql in queries.sections(since).items():
        try:
            rows = runtime.db(sql)
            report['sections'][name] = rows
            if len(rows) >= 501:
                report['blockers'].append('truncated_' + name)
        except Refusal:
            report['blockers'].append('query_failed_' + name)
    for name, action in (
        ('health', lambda: sanitize_health(json.loads(runtime.http(config['api_url'] + '/healthz')))),
        ('scheduler', lambda: sanitize_scheduler(json.loads(runtime.http(config['api_url'] + '/api/v1/automation/status', True)))),
        ('prometheus', lambda: sanitize_targets(json.loads(runtime.http(config['prometheus_url'] + '/api/v1/targets')), config)),
        ('metrics', lambda: sanitize_metrics(runtime.http(config['api_url'] + '/metrics'))),
        ('scheduler_events', lambda: runtime.logs(report['containers']['app']['started_at'])),
    ):
        try:
            report['sections'][name] = action()
        except (Refusal, ValueError, KeyError, TypeError):
            report['blockers'].append('unavailable_' + name)
    controls = report['sections'].get('controls', [])
    report['baseline'] = baseline(config, report['containers'], report['database'], report['cohort'], controls)
    report['baseline_sha256'] = digest(report['baseline'])
    # Identity is re-read after collection to reject replacement/restart races.
    try:
        require(runtime.inspect() == report['containers'], 'runtime_changed_during_collection')
    except Refusal as error:
        report['blockers'].append(str(error))
    report['collection_status'] = 'complete' if not report['blockers'] else 'incomplete'
    report['findings'] = assess(report, config, now)
    return report


def sanitize_scheduler(rows):
    require(isinstance(rows, list), 'invalid_scheduler_response')
    fields = ('name', 'schedule', 'enabled', 'running', 'last_run', 'last_result',
              'consecutive_failures', 'run_count', 'error_count')
    result = []
    for row in rows:
        item = {k: row[k] for k in fields if k in row}
        # last_result may contain an arbitrary skip/error detail in old versions.
        if item.get('last_result') not in ('ok', 'error', 'running', 'skipped', 'degraded', ''):
            item['last_result'] = 'other'
        result.append(item)
    return result


def sanitize_health(data):
    return {key: 'ok' if data.get(key) == 'ok' else 'unhealthy' for key in ('status', 'db', 'redis')}


def sanitize_targets(data, config):
    rows = data['data']['activeTargets']
    return [{'job': row['labels'].get('job'), 'health': row['health'],
             'last_scrape': row.get('lastScrape'), 'error_present': bool(row.get('lastError'))}
            for row in rows if row['labels'].get('job') in config['prometheus_jobs']
            and '10.0.0.56:3030' in row.get('scrapeUrl', '')]


def sanitize_metrics(text):
    prefixes = ('tradingagent_data_source_', 'tradingagent_automation_job_',
                'tradingagent_alpaca_reconcile_', 'tradingagent_kill_switch_',
                'tradingagent_circuit_breaker_', 'tradingagent_polymarket_reconciliation_drift_',
                'tradingagent_kalshi_reconcile_')
    # Restrict both metric names and labels; never retain an unexpected label value.
    result = []
    for line in text.splitlines():
        if line.startswith(prefixes) and re.fullmatch(r'[A-Za-z0-9_]+(?:\{[A-Za-z0-9_=".,:/ -]*\})? [-+0-9.eE]+', line):
            result.append(line)
    return result


def assess(report, config, now):
    findings = list(report.get('blockers', []))
    sections = report.get('sections', {})
    if sections.get('health') != {'status': 'ok', 'db': 'ok', 'redis': 'ok'}:
        findings.append('dependency_health_unverified')
    targets = sections.get('prometheus', [])
    if not targets or any(x['health'] != 'up' or x['error_present'] for x in targets):
        findings.append('prometheus_target_unhealthy_or_missing')
    jobs = {x['name']: x for x in sections.get('scheduler', [])}
    for name in config['required_jobs']:
        job = jobs.get(name)
        if not job or job.get('enabled') is not True:
            findings.append('scheduler_not_enabled_' + name)
        if job and job.get('consecutive_failures', 0) > 0:
            findings.append('scheduler_failures_' + name)
    for role, container in report.get('containers', {}).items():
        if container['restarts']:
            findings.append('container_restarted_' + role)
    for row in sections.get('automation', []):
        if row['status'] in ('error', 'degraded', 'skipped') or row['error_present']:
            findings.append('automation_outcome_requires_review_' + row['id'])
        counters = row.get('counters', {})
        if any(counters.get(k, 0) > 0 for k in ('stale', 'price_stale', 'provider_failures', 'price_fetch_failed', 'persist_failed')):
            findings.append('provider_or_persistence_gate_' + row['id'])
    latest = {x['job_name']: x for x in sections.get('latest_jobs', [])}
    rec = latest.get('alpaca_reconcile')
    if not rec or rec['status'] != 'ok' or not rec['completed_at']:
        findings.append('reconciliation_unverified')
    elif (now - instant(rec['completed_at'])).total_seconds() > config['monitoring']['reconciliation_max_age_seconds']:
        findings.append('reconciliation_stale')
    for row in sections.get('queues', []):
        for key in ('oldest_automation', 'oldest_pipeline'):
            if row.get(key) and (now - instant(row[key])).total_seconds() > config['monitoring']['job_timeout_seconds']:
                findings.append('stuck_' + key)
    for row in sections.get('decisions', []):
        if row['missing_evidence'] or not row['replay_events'] or (row['status'] == 'paper_ordered' and row['linked_orders'] != 1):
            findings.append('decision_integrity_' + row['id'])
        if row['live_order_id']:
            findings.append('live_order_link_' + row['id'])
    if not sections.get('metrics'):
        findings.append('provider_metrics_missing')
    for metric in sections.get('metrics', []):
        if metric.startswith(('tradingagent_kill_switch_active ', 'tradingagent_circuit_breaker_state ')) and float(metric.split()[-1]) != 0:
            findings.append('runtime_risk_guard_active')
    return sorted(set(findings))


def save_receipt(parent, report):
    parent = Path(parent)
    parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    directory = Path(tempfile.mkdtemp(prefix='receipt-', dir=parent))
    write_json(directory / 'receipt.json', report)
    write_json(directory / 'manifest.json', {'receipt.json': file_hash(directory / 'receipt.json')})
    return directory


@contextlib.contextmanager
def lock(path):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    require(not path.is_symlink(), 'lock_symlink')
    with path.open('a') as stream:
        try:
            fcntl.flock(stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise Refusal('operation_already_owned') from None
        yield
        # Keep the inode: unlinking locks permits concurrent owners.


def verify_go(go, report, config, now):
    require(go.get('decision') == 'GO' and bool(go.get('reviewer')), 'go_required')
    require(report.get('collection_status') == 'complete' and not report.get('findings'), 'preflight_incomplete')
    require(go.get('baseline_sha256') == report.get('baseline_sha256'), 'go_baseline_mismatch')
    require(go.get('tool_sha256') == tool_identity()['sha256'], 'go_tool_mismatch')
    require(0 <= (now - instant(go['decided_at'])).total_seconds() <= 3600, 'go_expired_or_future')
    gates = go.get('gates', {})
    for name in config['monitoring']['required_gates']:
        gate = gates.get(name, {})
        require(gate.get('status') == 'pass' and bool(gate.get('evidence')), 'required_gate_incomplete')
        for artifact in gate['evidence']:
            p = Path(artifact['path'])
            require(p.is_absolute() and p.is_file() and not p.is_symlink(), 'gate_artifact_missing')
            require(file_hash(p) == artifact['sha256'], 'gate_artifact_changed')


def initialize(ledger, go, report, config, now=None):
    now = now or dt.datetime.now(UTC)
    ledger = Path(ledger)
    require(ledger.is_absolute(), 'ledger_path_must_be_absolute')
    with lock(str(ledger) + '.lock'):
        require(not ledger.exists() and not ledger.is_symlink(), 'ledger_already_exists')
        verify_go(go, report, config, now)
        tmp = Path(tempfile.mkdtemp(prefix='.' + ledger.name + '-', dir=ledger.parent))
        try:
            manifest = {'format': 1, 'started_at': stamp(now), 'baseline': report['baseline'],
                        'baseline_sha256': report['baseline_sha256'],
                        'cohort_sha256': digest(report['cohort']), 'go_sha256': digest(go)}
            write_json(tmp / 'baseline.json', manifest)
            write_json(tmp / 'go.json', go)
            write_json(tmp / 'initial.json', report)
            (tmp / 'snapshots').mkdir(mode=0o700)
            # All cooperating writers own this persistent lock; refuse even empty targets.
            require(not ledger.exists() and not ledger.is_symlink(), 'ledger_already_exists')
            os.rename(tmp, ledger)
        finally:
            if tmp.exists():
                shutil.rmtree(tmp)
    return manifest


def verify_ledger(ledger, report):
    ledger = Path(ledger)
    require(ledger.is_dir() and not ledger.is_symlink(), 'ledger_missing')
    state = json.loads((ledger / 'baseline.json').read_text())
    require(state['baseline_sha256'] == digest(state['baseline']), 'ledger_baseline_corrupt')
    require(state['baseline_sha256'] == report.get('baseline_sha256'), 'ledger_baseline_drift')
    require(state['cohort_sha256'] == digest(report.get('cohort')), 'ledger_cohort_drift')
    return state
