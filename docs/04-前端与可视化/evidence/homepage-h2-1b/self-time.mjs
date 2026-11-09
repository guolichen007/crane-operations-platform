// self (exclusive) time per node from EXPLAIN JSON: node self = total*loops - Σ children total*loops (CTE Scan of an already-materialized CTE counts its own read time)
import { spawnSync } from 'node:child_process'; import { readFileSync } from 'node:fs';
const [db, file, user] = process.argv.slice(2);
const sql = `SET plan_cache_mode = force_custom_plan;\nPREPARE s(text,timestamptz,timestamptz,int,int,int[]) AS ${readFileSync(file,'utf8')};\nEXPLAIN (ANALYZE, FORMAT JSON) EXECUTE s('${user}','2026-11-06T08:00:00Z','2026-11-07T08:00:00Z',2100,120,NULL);`;
const r = spawnSync(process.env.PGBIN + '/psql', ['-h','127.0.0.1','-p','5434','-U','a1test','-d',db,'-At'], { input: sql, encoding: 'utf8', maxBuffer: 1 << 28 });
const plan = JSON.parse(r.stdout.slice(r.stdout.lastIndexOf('\n[') + 1))[0];
const out = [];
(function walk(n, path) {
  const tot = n['Actual Total Time'] * n['Actual Loops'];
  const kids = (n.Plans ?? []).reduce((s, c) => s + c['Actual Total Time'] * c['Actual Loops'], 0);
  out.push([Math.max(0, tot - kids), (n['Subplan Name'] ? n['Subplan Name'] + ' / ' : '') + n['Node Type'] + (n['Relation Name'] ? ' ' + n['Relation Name'] : '') + (n['CTE Name'] ? ' ' + n['CTE Name'] : ''), n['Actual Rows'] * n['Actual Loops']]);
  for (const c of n.Plans ?? []) walk(c);
})(plan.Plan);
out.sort((a, b) => b[0] - a[0]);
console.log('exec', plan['Execution Time']);
for (const [t, name, rows] of out.slice(0, 12)) console.log(t.toFixed(1).padStart(7), 'ms self ', name, 'rows=' + rows);
