// Runs internal/operations/summary.sql with the same bindings as internal/operations.Summary:
// region map from internal/geo/index.gen.json (code → direct child of the scope), read roles,
// sampledAt = 2026-11-07T08:00:00Z (dataset reference), 24 h window, stale 2100 s, skew 120 s.
// Prints one JSON line: plan metrics (EXPLAIN ANALYZE, BUFFERS) + 7 timed executions (median/min/max).
// Usage: node run.mjs <repoRoot> <db> <user> <country|adcode> [label]
import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';

const [root, db, user, region, label = ''] = process.argv.slice(2);
const sql = readFileSync(`${root}/internal/operations/summary.sql`, 'utf8');
const index = JSON.parse(readFileSync(`${root}/internal/geo/index.gen.json`, 'utf8'));
const parent = new Map(index.regions.map(([c, p]) => [c, p]));
const children = new Map();
for (const [c, p] of index.regions) if (p) (children.get(p) ?? children.set(p, []).get(p)).push(c);
const scope = region === 'country' ? 100000 : Number(region);
const desc = [];
for (const stack = [scope]; stack.length; ) {
  const c = stack.pop();
  desc.push(c);
  stack.push(...(children.get(c) ?? []));
}
const childOf = (c) => {
  if (c === scope) return 0;
  for (let cur = c; cur && cur !== 100000; cur = parent.get(cur)) if (parent.get(cur) === scope) return cur;
  return 0;
};
const codes = desc.sort((a, b) => a - b);
const kids = codes.map(childOf);
const arr = (xs) => `'{${xs.join(',')}}'`;
const args = `'${user}', '{viewer,operator,maintainer,platform_admin}', false, '2026-11-06T08:00:00Z', '2026-11-07T08:00:00Z', 2100, 120, ${scope === 100000}, ${arr(codes)}, ${arr(kids)}`;
const jit = process.env.BENCH_JIT === 'on' ? 'on' : 'off'; // production sets jit=off per summary transaction
const prep = `SET plan_cache_mode = force_custom_plan;\nSET jit = ${jit};\nPREPARE s(text,text[],bool,timestamptz,timestamptz,int,int,bool,int[],int[]) AS ${sql};\n`;
const psql = process.env.PSQL || 'psql';
const exec = (input) => {
  const r = spawnSync(psql, ['-X', '-At', '-v', 'ON_ERROR_STOP=1', '-d', db], { input, encoding: 'utf8', maxBuffer: 1 << 28 });
  if (r.status !== 0) throw Error(r.stderr);
  return r.stdout;
};
const explainOut = exec(prep + `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) EXECUTE s(${args});`);
const plan = JSON.parse(explainOut.slice(explainOut.lastIndexOf('\n[') + 1))[0];
const alarmScans = [];
let nodes = 0;
(function walk(n) {
  nodes++;
  if (n['Relation Name'] === 'alarms') alarmScans.push(`${n['Node Type']}${n['Index Name'] ? '(' + n['Index Name'] + ')' : ''} rows=${n['Actual Rows'] * n['Actual Loops']}`);
  (n.Plans ?? []).forEach(walk);
})(plan.Plan);
const timed = exec(prep + '\\timing on\n\\o /dev/null\n' + Array.from({ length: 7 }, () => `EXECUTE s(${args});`).join('\n'));
const ms = [...timed.matchAll(/^Time: ([\d.]+) ms/gm)].map((m) => Number(m[1])).sort((a, b) => a - b);
const summary = JSON.parse(exec(prep + `EXECUTE s(${args});`).trim().split('\n').at(-1));
console.log(JSON.stringify({
  label, user, region, jit, firstExplainMs: plan['Execution Time'], planNodes: nodes,
  sharedHit: plan.Plan['Shared Hit Blocks'], sharedRead: plan.Plan['Shared Read Blocks'], tempWritten: plan.Plan['Temp Written Blocks'],
  alarmScans, warm: { median: ms[3], min: ms[0], max: ms[6] },
  result: { registered: summary.registered, unlocated: summary.unlocated, regions: summary.regions.length, recordsTotal: summary.recordsTotal },
}));
