#!/usr/bin/env python3
"""Compile every collector query against the migrated disposable CI database."""
import json
import os
import re
import subprocess
from qualification import queries
from qualification.core import Runtime

container = os.environ['DATABASE_CONTAINER']
assert re.fullmatch(r'[0-9a-f]{12,64}', container)
metadata = json.loads(subprocess.check_output(['docker','inspect',container],text=True))[0]
assert metadata['Config']['Labels']['tv.subcult.augr.ci-service'] == 'integration-postgres'
args = ['docker','exec','-i','-e','PGOPTIONS=-c default_transaction_read_only=on -c statement_timeout=15000',
        container,'psql','-X','-qAt','-v','ON_ERROR_STOP=1','-U','tradingagent','-d','tradingagent_test']
statements = [queries.COHORT, *queries.sections('2026-09-21T00:00:00+00:00').values(),
              queries.observed_runs('automation','options_scan','2026-09-22T02:00:00+00:00','2026-09-22T02:01:00+00:00'),
              queries.observed_runs('strategy','00000000-0000-4000-8000-000000000001',
                                    '2026-09-21T14:00:00+00:00','2026-09-21T14:01:00+00:00')]
for sql in statements:
    subprocess.run(args,input='BEGIN READ ONLY; EXPLAIN '+sql+'; COMMIT;',text=True,
                   stdout=subprocess.DEVNULL,check=True,timeout=25)
# Exercise the real PostgreSQL JSON filtering against a fixture provider payload.
sql = "SELECT " + queries.COUNTER_SQL + " FROM (VALUES ('{\"selected\":251,\"provider_recoveries\":30,\"secret\":\"do-not-retain\",\"failed\":\"raw-error\"}'::jsonb)) v(result)"
result = json.loads(subprocess.check_output(args+['-c',sql],text=True))
assert result == {'selected':251,'provider_recoveries':30}, result
runtime = Runtime({})
runtime.sql_output = lambda sql: subprocess.check_output(args+['-c',sql],text=True,timeout=25)
rows = runtime.db_section('SELECT n AS id FROM generate_series(1,1242) n ORDER BY n LIMIT 501')
assert [row['id'] for row in rows] == list(range(1,1243))
bounded = runtime.db_section('SELECT n AS id FROM generate_series(1,12000) n ORDER BY n LIMIT 501')
assert len(bounded) == 10001 and bounded[-1]['id'] == 10001
print(f'{len(statements)} schema-114 queries compiled read-only; numeric allowlist and paginated snapshot fixtures passed')
