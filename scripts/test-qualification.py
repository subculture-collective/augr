#!/usr/bin/env python3
"""Offline behavioral checks; no production services or waiting required."""
import copy
import datetime as dt
import json
import multiprocessing
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from qualification import core, monitor, observers, queries, session

CONFIG_PATH = core.ROOT / 'monitoring/qualification/schema114-1022b401.json'
NOW = core.instant('2026-09-21T13:55:00Z')
RUN_ID = '00000000-0000-4000-8000-000000000001'


class FakeClock:
    def __init__(self, now=NOW):
        self.value = now
    def now(self):
        return self.value
    def sleep(self, seconds):
        self.value += dt.timedelta(seconds=seconds)


class Fixture:
    def __init__(self, config, clock=None):
        self.config = config
        self.clock = clock or FakeClock()
        self.fail_query = None
        self.manual = False
        self.run_mode = 'ok'
        self.version = config['strategy']['execution_version_id']
        self.auth = True
        self.containers = {}
        self.calls = []
        for role, spec in config['containers'].items():
            self.containers[role] = {'id': role, 'image': spec.get('image', 'dependency-image'),
                'revision': config['source_revision'] if role in ('app','web') else None,
                'project':'augr','service':role,'running':True,'health':'healthy','restarts':0,
                'started_at':'2026-09-20T14:44:52Z'}
        self.containers['app']['database'] = config['database']
        self.containers['app']['flags'] = {k:{'value':v,'source':'environment'} for k,v in config['safety_flags'].items()}
        self.identity = {'database':config['database'],'schema':114,'dirty':False,'read_only':'on'}
        self.cohort = [{'id':config['strategy']['id'],'ticker':'SPY','market_type':'stock',
                       'execution_strategy_version_id':self.version,'skip_next_run':False,
                       'schedule_cron':config['strategy']['schedule']}]
        self.due = core.instant('2026-09-22T02:00:00Z')
        self.polls = 0
    def inspect(self):
        return copy.deepcopy(self.containers)
    def db(self, sql, aggregate=True):
        self.calls.append(sql)
        if self.fail_query and self.fail_query in sql:
            raise core.Refusal('query_failed')
        if sql == queries.IDENTITY:
            return copy.deepcopy(self.identity)
        if sql == queries.COHORT:
            return copy.deepcopy(self.cohort)
        if 'FROM agent_events' in sql:
            return []
        if 'LIMIT 3' in sql:
            if self.clock.now() < self.due or self.run_mode == 'missing':
                return []
            self.polls += 1
            row = {'id':RUN_ID,'status':'ok','started_at':core.stamp(self.due),
                   'completed_at':core.stamp(self.due),'error_present':False,
                   'execution_version_id':self.version,'counters':{}}
            if self.run_mode == 'failed': row['status']='error'
            if self.run_mode == 'running': row['completed_at']=None
            if self.run_mode == 'changed':
                row['completed_at']=None if self.polls == 1 else core.stamp(self.clock.now())
                row['id']=RUN_ID if self.polls == 1 else '00000000-0000-4000-8000-000000000002'
            if self.run_mode == 'ambiguous': return [row,row]
            return [row]
        if 'DISTINCT ON (job_name)' in sql:
            return [{'job_name':'alpaca_reconcile','status':'ok','completed_at':core.stamp(self.clock.now())}]
        if 'event_type=' in sql:
            return [{'entity_id':self.config['strategy']['id'],'created_at':core.stamp(self.due)}] if self.manual else []
        return []
    def http(self, url, authenticated=False):
        if authenticated:
            if not self.auth: raise core.Refusal('authenticated_session_unavailable')
            return json.dumps([{'name':n,'enabled':True,'schedule':s,'consecutive_failures':0,
                                'last_error':'SECRET','last_result':'ok'} for n,s in self.config['schedules'].items()])
        if url.endswith('healthz'): return json.dumps({'status':'ok','db':'ok','redis':'ok'})
        if url.endswith('targets'):
            return json.dumps({'data':{'activeTargets':[{'labels':{'job':'augr-api'},'health':'up',
                'scrapeUrl':'http://10.0.0.56:3030/metrics','lastError':''}]}})
        return 'tradingagent_alpaca_reconcile_runs_total{result="success"} 3'
    def logs(self, since):
        rows = [{'event':'registered','name':k,'cron':v} for k,v in self.config['schedules'].items()]
        rows += [{'event':'strategy_registered','strategy_id':self.config['strategy']['id'],
                  'schedule':self.config['strategy']['schedule']}]
        rows += [{'event':'starting','job':'options_scan','time':core.stamp(self.due)},
                 {'event':'strategy_triggered','strategy_id':self.config['strategy']['id'],'time':core.stamp(self.due)}]
        if self.manual: rows += [{'event':'manual','job':'options_scan','time':core.stamp(self.due)}]
        return rows


def try_lock(path, queue):
    try:
        with core.lock(path): queue.put('acquired')
    except core.Refusal as e: queue.put(str(e))


class QualificationTests(unittest.TestCase):
    def setUp(self):
        self.config = core.load_config(CONFIG_PATH)
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.config['monitoring']['future_ledger'] = str(self.root/'ledger')
        self.runtime = Fixture(self.config)
    def report(self):
        return core.collect(self.runtime,self.config,core.stamp(NOW-dt.timedelta(minutes=30)),now=NOW)
    def go(self, report):
        artifact = self.root/'gate.txt'
        artifact.write_text('explicit reviewed fixture only')
        return {'decision':'GO','reviewer':'test','decided_at':core.stamp(NOW),
                'baseline_sha256':report['baseline_sha256'],'tool_sha256':core.tool_identity()['sha256'],
                'gates':{k:{'status':'pass','evidence':[{'path':str(artifact),'sha256':core.file_hash(artifact)}]}
                         for k in self.config['monitoring']['required_gates']}}
    def test_prepare_creates_no_ledger_and_records_distinct_tool_identity(self):
        report=self.report()
        self.assertEqual(report['collection_status'],'complete')
        self.assertEqual(report['findings'],[])
        core.save_receipt(self.root/'evidence',report)
        self.assertFalse((self.root/'ledger').exists())
        self.assertIn('files',report['tool'])
        self.assertEqual(report['baseline']['source_revision'],self.config['source_revision'])
        self.assertNotIn('SECRET',json.dumps(report))
    def test_database_identity_refusals(self):
        for field,value in [('database','tradingagent'),('schema',113),('dirty',True),('read_only','off')]:
            with self.subTest(field=field):
                old=self.runtime.identity[field]; self.runtime.identity[field]=value
                report=self.report(); self.assertEqual(report['collection_status'],'refused')
                self.assertIn('database_identity_mismatch',report['blockers'])
                self.runtime.identity[field]=old
    def test_runtime_identity_and_safety_refusals(self):
        for field,value in [('image','wrong'),('revision','wrong'),('project','other'),('database','tradingagent')]:
            with self.subTest(field=field):
                old=self.runtime.containers['app'][field]; self.runtime.containers['app'][field]=value
                self.assertEqual(self.report()['collection_status'],'refused')
                self.runtime.containers['app'][field]=old
        self.runtime.containers['app']['flags']['ENABLE_LIVE_TRADING']['value']=True
        self.assertIn('safety_flag_mismatch',self.report()['blockers'])
    def test_empty_invalid_and_unscheduled_cohorts(self):
        original=copy.deepcopy(self.runtime.cohort)
        for rows in [[],[dict(original[0],id='bad')],[dict(original[0],execution_strategy_version_id=None)],
                     [dict(original[0],schedule_cron='')],[dict(original[0],skip_next_run=True)]]:
            self.runtime.cohort=rows
            self.assertEqual(self.report()['collection_status'],'refused')
    def test_failed_query_retains_other_sections(self):
        self.runtime.fail_query='FROM trades'
        report=self.report(); self.assertEqual(report['collection_status'],'incomplete')
        self.assertIn('query_failed_trades',report['blockers']); self.assertIn('cohort',report)
    def test_auth_unavailable_is_not_pass(self):
        self.runtime.auth=False
        report=self.report()
        self.assertIn('unavailable_scheduler',report['blockers'])
        self.assertIn('scheduler_not_enabled_options_scan',report['findings'])
    def test_section_pages_share_one_read_only_snapshot(self):
        output = '\n'.join(json.dumps({'id': n}) for n in range(1242))
        with patch('qualification.core.command', return_value=output) as run:
            rows = core.Runtime(self.config).db_section(
                'SELECT id FROM automation_job_runs ORDER BY started_at DESC LIMIT 501')
        self.assertEqual(len(rows), 1242)
        sql = run.call_args.args[0][-1]
        self.assertIn('BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY', sql)
        self.assertEqual(sql.count('FETCH FORWARD 500'), 20)
        self.assertIn('FETCH FORWARD 1', sql)
        self.assertNotIn('LIMIT 501', sql)
        self.assertEqual(run.call_args.kwargs['timeout'], 25)

    def test_large_complete_section_and_explicit_total_bound(self):
        original = self.runtime.db
        for count, expected in ((1242, 'complete'), (10001, 'incomplete')):
            self.runtime.db_section = lambda sql: ([
                {'id': str(n), 'status': 'ok', 'error_present': False}
                for n in range(count)] if 'FROM automation_job_runs WHERE' in sql else original(sql))
            report = self.report()
            self.assertEqual(report['collection_status'], expected)
            self.assertEqual(len(report['sections']['automation']), count)
            self.assertEqual('truncated_automation' in report['blockers'], count == 10001)

    def test_long_prearmed_wait_uses_fresh_precheck_window(self):
        due = core.instant('2026-09-22T02:00:00Z')
        clock = FakeClock(due-dt.timedelta(hours=27))
        self.runtime.clock = clock
        self.runtime.due = due
        report, _ = observers.observe(self.runtime, self.config, 'automation',
            'options_scan', core.stamp(due), self.root/'long-wait', clock=clock)
        self.assertEqual(report['outcome'], 'natural_terminal_evidence_collected')
        self.assertEqual(core.instant(report['precheck']['since']), due-dt.timedelta(minutes=32))
        self.assertEqual(report['postcheck']['since'], report['precheck']['since'])

    def test_strategy_preparation_rejection_is_retained_without_pipeline(self):
        original = self.runtime.db
        def rejected(sql, aggregate=True):
            if 'FROM agent_events' in sql and 'LIMIT 3' in sql:
                return [{'id': RUN_ID, 'reason_code': 'fundamentals_incomplete'}]
            return original(sql, aggregate)
        self.runtime.db = rejected
        report, _ = self.observe(mode='missing', kind='strategy')
        self.assertEqual(report['outcome'], 'strategy_preparation_rejected')
        self.assertEqual(report['preparation_rejections'][0]['reason_code'], 'fundamentals_incomplete')

    def test_archived_registration_never_replays_run_or_enabled_events(self):
        report = self.report()
        directory = core.save_receipt(self.root, report)
        runtime = core.Runtime(self.config, registration_receipt=directory/'receipt.json')
        with patch.object(runtime, 'inspect', return_value=self.runtime.inspect()):
            rows = runtime.archived_registrations()
        self.assertTrue(rows)
        self.assertTrue(all(x['event'] in ('registered','strategy_registered') for x in rows))
        self.assertTrue(all(x['registration_receipt_sha256'] for x in rows))
        changed = self.runtime.inspect()
        changed['app']['restarts'] += 1
        with patch.object(runtime, 'inspect', return_value=changed):
            with self.assertRaisesRegex(core.Refusal, 'registration_runtime_changed'):
                runtime.archived_registrations()
        (directory/'receipt.json').write_text('{}')
        with self.assertRaisesRegex(core.Refusal, 'registration_receipt_checksum_mismatch'):
            runtime.archived_registrations()

    def test_new_plan_dates_require_review_and_preserve_archive_argument(self):
        plan = observers.make_plan(self.config, self.report(), '/tmp/session',
                                   '2026-09-23', '/tmp/old receipt/receipt.json')
        self.assertEqual(plan['boundaries'][0]['due_at'], '2026-09-23T14:00:00+00:00')
        self.assertEqual(plan['boundaries'][1]['due_at'], '2026-09-24T02:00:00+00:00')
        import shlex
        args = shlex.split(plan['boundaries'][0]['command'])
        self.assertEqual(args[args.index('--registration-receipt')+1], '/tmp/old receipt/receipt.json')
        for date in ('2026-09-26', '2026-10-01'):
            with self.assertRaisesRegex(core.Refusal, 'session_calendar_review_required'):
                observers.make_plan(self.config, self.report(), session_date=date)
    def test_database_command_is_read_only_explicit_and_bounded(self):
        with patch('qualification.core.command',return_value='[]') as run:
            core.Runtime(self.config).db('SELECT 1')
        args=run.call_args.args[0]
        self.assertIn(self.config['database'],args)
        self.assertIn('BEGIN READ ONLY;',args[-1])
        self.assertTrue(any('default_transaction_read_only=on' in x for x in args))
        self.assertEqual(run.call_args.kwargs['timeout'],25)
    def test_wrong_host_refuses_before_docker(self):
        with patch('qualification.core.socket.gethostname',return_value='other'), patch('qualification.core.command') as run:
            with self.assertRaisesRegex(core.Refusal,'wrong_host'): core.Runtime(self.config).inspect()
            run.assert_not_called()
    def test_go_missing_or_failed_cannot_initialize(self):
        report=self.report()
        for go in [{},{'decision':'NO-GO'}]:
            with self.assertRaises(core.Refusal): core.initialize(self.root/'ledger',go,report,self.config,NOW)
            self.assertFalse((self.root/'ledger').exists())
    def test_initialization_atomic_complete_and_no_overwrite(self):
        report=self.report(); go=self.go(report); ledger=self.root/'ledger'
        core.initialize(ledger,go,report,self.config,NOW)
        before=(ledger/'baseline.json').read_bytes()
        self.assertTrue((ledger/'snapshots').is_dir())
        self.assertFalse((ledger/'start.env').exists())
        with self.assertRaisesRegex(core.Refusal,'ledger_already_exists'):
            core.initialize(ledger,go,report,self.config,NOW)
        self.assertEqual((ledger/'baseline.json').read_bytes(),before)
    def test_invalid_gate_hash_and_drift_refuse_initialization(self):
        report=self.report(); go=self.go(report)
        (self.root/'gate.txt').write_text('changed')
        with self.assertRaisesRegex(core.Refusal,'gate_artifact_changed'):
            core.initialize(self.root/'ledger',go,report,self.config,NOW)
        self.assertFalse((self.root/'ledger').exists())
        go['baseline_sha256']='wrong'
        with self.assertRaisesRegex(core.Refusal,'go_baseline_mismatch'):
            core.initialize(self.root/'ledger',go,report,self.config,NOW)
    def test_concurrent_lock_refuses_second_process(self):
        path=self.root/'ledger.lock'; ctx=multiprocessing.get_context('spawn'); queue=ctx.Queue()
        with core.lock(path):
            child=ctx.Process(target=try_lock,args=(path,queue)); child.start(); child.join(5)
            self.assertEqual(child.exitcode,0); self.assertEqual(queue.get(timeout=1),'operation_already_owned')
    def test_failed_initial_publish_leaves_no_partial_ledger(self):
        report=self.report(); go=self.go(report)
        with patch('qualification.core.os.rename',side_effect=OSError('fixture')):
            with self.assertRaises(OSError): core.initialize(self.root/'ledger',go,report,self.config,NOW)
        self.assertFalse((self.root/'ledger').exists())
        self.assertEqual(list(self.root.glob('.ledger-*')),[])
    def test_evidence_is_exclusive_and_checksummed(self):
        p=core.save_receipt(self.root,self.report())
        self.assertEqual(json.loads((p/'manifest.json').read_text())['receipt.json'],core.file_hash(p/'receipt.json'))
        with self.assertRaises(FileExistsError): core.write_json(p/'receipt.json',{})
    def test_timezone_plan_uses_eastern_not_utc(self):
        plan=observers.make_plan(self.config,self.report())
        rows={x['job']:x for x in plan['boundaries']}
        self.assertEqual(rows['options_scan']['due_at'],'2026-09-22T02:00:00+00:00')
        self.assertEqual(rows['history_refresh']['chicago'],'2026-09-21T23:00:00-05:00')
        self.assertEqual(rows['options_discovery']['due_at'],'2026-09-22T10:30:00+00:00')
        self.assertEqual(len([x for x in plan['boundaries'] if x['job']=='overnight_backtest']),10)
    def test_plan_commands_carry_session_path_without_losing_argument_boundaries(self):
        import shlex
        plan=observers.make_plan(self.config,self.report(),'/tmp/operator session')
        for row in plan['boundaries']:
            args=shlex.split(row['command'])
            self.assertEqual(args[args.index('--token-file')+1],'/tmp/operator session')
    def test_login_refuses_noninteractive_password_fallback(self):
        import subprocess
        result=subprocess.run(['python3',str(core.ROOT/'scripts/qualify-paper.py'),'login',
                               '--token-file',str(self.root/'session')],capture_output=True,text=True)
        self.assertEqual(result.returncode,2)
        self.assertIn('login_requires_interactive_terminal',result.stderr)
        self.assertFalse((self.root/'session').exists())
    def observe(self,mode='ok',kind='automation'):
        due=self.runtime.due if kind=='automation' else core.instant('2026-09-21T14:00:00Z')
        clock=FakeClock(due-dt.timedelta(minutes=5) if due != NOW else NOW)
        self.runtime.due=due; self.runtime.clock=clock; self.runtime.run_mode=mode; self.runtime.polls=0
        target='options_scan' if kind=='automation' else self.config['strategy']['id']
        return observers.observe(self.runtime,self.config,kind,target,core.stamp(self.runtime.due),
                                  self.root/'observations',clock=clock,timeout=120)
    def test_successful_natural_observation_still_requires_domain_review(self):
        report,path=self.observe()
        self.assertEqual(report['outcome'],'natural_terminal_evidence_collected')
        self.assertIn('pending',report['domain_review']); self.assertTrue((path/'manifest.json').exists())
    def test_strategy_observation_pins_version(self):
        report,_=self.observe(kind='strategy')
        self.assertEqual(report['outcome'],'natural_terminal_evidence_collected')
    def test_wrong_strategy_version_is_retained(self):
        self.runtime.version='00000000-0000-4000-8000-000000000099'
        report,_=self.observe(kind='strategy')
        self.assertEqual(report['outcome'],'execution_version_changed')
    def test_off_schedule_future_boundary_is_rejected(self):
        self.runtime.due=core.instant('2026-09-22T02:03:00Z')
        report,_=self.observe()
        self.assertEqual(report['outcome'],'not_a_scheduled_boundary')
        self.assertEqual(self.runtime.calls,[])
    def test_malformed_identity_retains_refusal_receipt(self):
        with patch.object(self.runtime,'inspect',side_effect=ValueError('SECRET')):
            report=self.report()
        self.assertEqual(report['collection_status'],'refused')
        self.assertNotIn('SECRET',json.dumps(report))
    def test_manual_observation_rejected(self):
        self.runtime.manual=True
        report,_=self.observe()
        self.assertEqual(report['outcome'],'manual_run_rejected')
    def test_late_start_is_recorded_and_does_not_poll(self):
        self.runtime.due=NOW
        report,path=self.observe()
        self.assertEqual(report['outcome'],'late_arm'); self.assertEqual(self.runtime.calls,[])
        self.assertTrue((path/'receipt.json').exists())
    def test_missing_failed_timeout_and_changed_runs_retain_receipts(self):
        for mode,want in [('missing','missed_natural_run'),('failed','natural_run_failed'),
                          ('running','terminal_timeout'),('changed','run_identity_changed'),('ambiguous','ambiguous_run_identity')]:
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as td:
                original=self.root; self.root=Path(td)
                report,path=self.observe(mode)
                self.assertEqual(report['outcome'],want)
                self.assertTrue((path/'receipt.json').is_file())
                self.assertTrue(list((self.root/'observations').glob('*.journal.ndjson')))
                self.root=original
    def test_rearming_owned_slot_refused(self):
        self.observe()
        with self.assertRaisesRegex(core.Refusal,'observation_slot_already_owned'): self.observe()
    def test_monitor_pass_fail_missing_stale_and_identity_mismatch(self):
        report=self.report(); self.assertEqual(monitor.check(report,self.config,NOW)['state'],'observed_clear')
        self.assertIn('receipt_stale_or_future',monitor.check(report,self.config,NOW+dt.timedelta(hours=1))['findings'])
        self.assertIn('baseline_drift',monitor.check(report,self.config,NOW,expected_baseline='wrong')['findings'])
        report['sections']['prometheus']=[]
        self.assertEqual(monitor.check(report,self.config,NOW)['state'],'attention')
    def test_monitor_activation_requires_ledger_go_and_exact_baseline(self):
        report=self.report(); ledger=self.root/'ledger'
        with self.assertRaises(core.Refusal): monitor.activate(ledger,report,self.config,NOW)
        core.initialize(ledger,self.go(report),report,self.config,NOW)
        monitor.activate(ledger,report,self.config,NOW)
        self.assertEqual(monitor.active_baseline(ledger,self.config),report['baseline_sha256'])
        archived=monitor.disable(ledger); self.assertTrue(archived.exists())
        with self.assertRaisesRegex(core.Refusal,'monitor_inactive'): monitor.active_baseline(ledger,self.config)
    def test_notification_is_local_and_sanitized(self):
        report=self.report(); report['blockers']=['unavailable_scheduler']
        notification=monitor.check(report,self.config,NOW)
        core.write_json(self.root/'notification.json',notification)
        self.assertNotIn('SECRET',(self.root/'notification.json').read_text())
    def test_expected_job_deadlines_report_missing_natural_run(self):
        report=self.report()
        now=core.instant('2026-09-22T07:00:00Z')
        report['observed_at']=core.stamp(now)
        result=monitor.check(report,self.config,now,window_start='2026-09-21T20:00:00Z')
        self.assertIn('scheduled_deadline_missed_history_refresh',result['findings'])
        self.assertIn('scheduled_deadline_missed_options_scan',result['findings'])
    def test_provider_stale_counter_and_risk_guard_raise_findings(self):
        report=self.report()
        report['sections']['automation']=[{'id':RUN_ID,'status':'ok','error_present':False,'counters':{'price_stale':1}}]
        report['sections']['metrics'].append('tradingagent_kill_switch_active 1')
        result=monitor.check(report,self.config,NOW)
        self.assertIn('provider_or_persistence_gate_'+RUN_ID,result['findings'])
        self.assertIn('runtime_risk_guard_active',result['findings'])
    def test_observer_interruption_preserves_failure_and_journal(self):
        with patch.object(self.runtime,'db',side_effect=KeyboardInterrupt):
            report,path=self.observe()
        self.assertEqual(report['outcome'],'observer_interrupted')
        self.assertTrue((path/'receipt.json').exists())
    def test_slow_precheck_cannot_be_called_prospective(self):
        real=self.runtime.inspect
        def slow():
            self.runtime.clock.sleep(180)
            return real()
        with patch.object(self.runtime,'inspect',side_effect=slow): report,_=self.observe()
        self.assertEqual(report['outcome'],'missed_precheck_boundary')
    def test_activation_refuses_changed_tool_or_baseline(self):
        report=self.report(); ledger=self.root/'ledger'
        core.initialize(ledger,self.go(report),report,self.config,NOW)
        drift=copy.deepcopy(report); drift['baseline_sha256']='different'
        with self.assertRaisesRegex(core.Refusal,'ledger_baseline_drift'): monitor.activate(ledger,drift,self.config,NOW)
        self.assertFalse((ledger/'monitor-active.json').exists())
    def test_health_allowlist_drops_provider_error_text(self):
        result=core.sanitize_health({'status':'bad SECRET','db':'SECRET','redis':'ok','error':'SECRET'})
        self.assertNotIn('SECRET',json.dumps(result))
    def test_headless_login_uses_normal_endpoint_and_private_session(self):
        data={'access_token':'SECRET_ACCESS','refresh_token':'SECRET_REFRESH','expires_at':'2026-09-22T03:00:00Z'}
        path=self.root/'operator-token'
        with patch('builtins.input',return_value='operator'), patch('getpass.getpass',return_value='SECRET_PASSWORD'), patch.object(session,'auth_request',return_value=data) as request:
            result=session.login(self.config['api_url'],path)
        self.assertEqual(request.call_args.args[1],'login')
        self.assertEqual(path.stat().st_mode & 0o777,0o600)
        self.assertNotIn('SECRET',json.dumps(result))
        self.assertEqual(json.loads(path.read_text()),data)
    def test_normal_session_renewal_is_separate_from_read_only_sql(self):
        path=self.root/'operator-token'
        session.store_session(path,{'access_token':'old','refresh_token':'renew','expires_at':core.stamp(NOW)})
        replacement={'access_token':'new','refresh_token':'next','expires_at':core.stamp(NOW+dt.timedelta(hours=1))}
        with patch.object(session,'auth_request',return_value=replacement) as request:
            self.assertEqual(session.access_token(path,self.config['api_url'],NOW),'new')
        self.assertEqual(request.call_args.args[1:3],('refresh',{'refresh_token':'renew'}))
        self.assertEqual(json.loads(path.read_text()),replacement)
    def test_failed_refresh_preserves_session_and_does_not_invent_token(self):
        path=self.root/'operator-token'
        session.store_session(path,{'access_token':'old','refresh_token':'renew','expires_at':core.stamp(NOW)})
        before=path.read_bytes()
        with patch.object(session,'auth_request',side_effect=core.Refusal('normal_login_or_refresh_failed')):
            with self.assertRaises(core.Refusal): session.access_token(path,self.config['api_url'],NOW)
        self.assertEqual(path.read_bytes(),before)
    def test_session_rejects_world_readable_and_symlink_files(self):
        path=self.root/'operator-token'; path.write_text('SECRET'); path.chmod(0o644)
        with self.assertRaisesRegex(core.Refusal,'session_file_permissions'): session.access_token(path,self.config['api_url'])
        link=self.root/'token-link'; link.symlink_to(path)
        with self.assertRaisesRegex(core.Refusal,'session_symlink'): session.access_token(link,self.config['api_url'])
    def test_config_refuses_legacy_database_and_weakened_safety(self):
        for field,value in [('database','tradingagent'),('timezone','UTC'),('schema',113)]:
            c=copy.deepcopy(self.config); c[field]=value; p=self.root/'config.json'; p.write_text(json.dumps(c))
            with self.assertRaises(core.Refusal): core.load_config(p)


if __name__ == '__main__':
    unittest.main(verbosity=2)
