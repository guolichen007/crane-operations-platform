// G3-4-A1: turn local-private/baobei/g34a1/uat-evidence/results.jsonl (one
// executable UAT run) into the PASS/FAIL/BLOCKED table of doc 34 §3.5.
// Usage: node tests/baobei/uat/uat-report.mjs [runId]  (default: latest run)
import { readFileSync } from 'node:fs';
const steps = ['S-01','S-02','S-03','S-04','S-05','S-06','S-07','S-08','S-09','S-10','S-11','M-01','M-02','M-03','M-04','S-12','S-13','M-05','S-14','S-14b','S-15','M-06','A-01','D-01','D-02','D-03','D-04','A-02','A-03','A-04','A-05','A-06','A-07','A-08'];
const rows = readFileSync('local-private/baobei/g34a1/uat-evidence/results.jsonl', 'utf8').trim().split('\n').map((l) => JSON.parse(l));
const run = process.argv[2] || rows.at(-1).run;
const mine = new Map(rows.filter((r) => r.run === run).map((r) => [r.id, r]));
console.log(`运行 ${run} · SHA ${rows.find((r) => r.run === run)?.sha}\n`);
console.log('| 步骤 | 身份 | 内容 | 结果 | 接口与服务端状态 | 截图 |\n| --- | --- | --- | --- | --- | --- |');
for (const id of steps) {
  const r = mine.get(id);
  if (!r) { console.log(`| ${id} | | | BLOCKED | 前序步骤失败，未执行 | |`); continue; }
  const facts = Object.entries(r.facts).map(([k, v]) => `${k}=${typeof v === 'object' ? JSON.stringify(v) : v}`).join('；').replace(/\|/g, '\\|');
  console.log(`| ${id} | ${r.who} | ${r.title} | ${r.result} | ${r.result === 'FAIL' ? r.error.replace(/\|/g, '\\|') : facts} | ${r.shot} |`);
}
