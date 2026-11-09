// H2-1B plan analyser: runs EXPLAIN (ANALYZE, BUFFERS, VERBOSE, FORMAT JSON) of one summary variant
// and prints a compact metrics line. Usage: node analyse.mjs <db> <variant-file> <user> <label>
import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
const [db, file, user, label] = process.argv.slice(2);
const q = readFileSync(file, 'utf8');
const sql = `SET plan_cache_mode = force_custom_plan;
PREPARE s(text,timestamptz,timestamptz,int,int,int[]) AS ${q};
EXPLAIN (ANALYZE, BUFFERS, VERBOSE, FORMAT JSON) EXECUTE s('${user}','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,120,NULL);`;
const r = spawnSync(process.env.PGBIN + '/psql', ['-h', '127.0.0.1', '-p', '5434', '-U', 'a1test', '-d', db, '-At', '-v', 'ON_ERROR_STOP=1'], { input: sql, encoding: 'utf8', maxBuffer: 256 * 1024 * 1024 });
if (r.status !== 0) { console.error(label, r.stderr); process.exit(1); }
const json = r.stdout.slice(r.stdout.lastIndexOf('\n[') + 1); // the EXPLAIN JSON is the last output block
const plan = JSON.parse(json)[0];
const m = { cteScans: 0, ctes: [], alarmScans: [], removed: 0, tempRead: 0, tempWritten: 0 };
(function walk(n) {
  if (n['Node Type'] === 'CTE Scan') m.cteScans++;
  if (n['Subplan Name']?.startsWith('CTE ')) m.ctes.push(n['Subplan Name'].slice(4));
  if (n['Relation Name'] === 'alarms')
    m.alarmScans.push(`${n['Node Type']}${n['Index Name'] ? '(' + n['Index Name'] + ')' : ''} rows=${n['Actual Rows'] * n['Actual Loops']} removed=${(n['Rows Removed by Filter'] ?? 0) + (n['Rows Removed by Index Recheck'] ?? 0)} hit=${n['Shared Hit Blocks']} read=${n['Shared Read Blocks']}`);
  m.removed += (n['Rows Removed by Filter'] ?? 0) * (n['Actual Loops'] ?? 1);
  m.nodes = (m.nodes ?? 0) + 1;
  for (const c of n.Plans ?? []) walk(c);
})(plan.Plan);
const top = plan.Plan;
console.log(JSON.stringify({
  label, exec_ms: plan['Execution Time'], plan_ms: plan['Planning Time'],
  shared_hit: top['Shared Hit Blocks'], shared_read: top['Shared Read Blocks'],
  temp_read: top['Temp Read Blocks'], temp_written: top['Temp Written Blocks'],
  plan_nodes: m.nodes, cte_scans: m.cteScans, materialized_ctes: [...new Set(m.ctes)].join(','), alarm_scans: m.alarmScans, rows_removed_total: m.removed,
}));
