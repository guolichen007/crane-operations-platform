// npm audit gate with ONE approved, time-limited, development-only exception (CRANE-SEC-001).
//
//   node tools/npm-audit-gate.mjs [--report-dir DIR]          (CI: after npm ci)
//
// Steps (any failure exits non-zero; nothing is skipped silently):
//   1. `npm audit --json` (full tree) — raw report saved to DIR/npm-audit-full.json;
//   2. `npm audit --omit=dev --audit-level=high` — must exit 0 (no production HIGH/CRITICAL); raw
//      output saved to DIR/npm-audit-production.txt;
//   3. the full report is evaluated (evaluate()):
//      - any CRITICAL fails; the report must be a valid npm v2 audit report;
//      - every HIGH entry must be explained recursively: its direct advisories may only be the
//        approved GHSA on the approved package/range, and every HIGH/CRITICAL package it depends on
//        (npm "metavulnerability" via strings) must itself be explained — down to the approved
//        advisory; an entry whose HIGH severity cannot be traced to the approved advisory fails;
//      - every explained entry must be in the recorded propagation list (an unknown chain fails)
//        and every affected lock node must be dev=true in package-lock.json; the installed
//        vulnerable package must be the approved version;
//      - an expired or unapproved exception fails;
//   4. prints NPM_AUDIT=PASS (no HIGH) or NPM_AUDIT=PASS_WITH_APPROVED_EXCEPTION … — never "fixed".
import { spawnSync } from 'node:child_process';
import { readFileSync, writeFileSync, mkdirSync, existsSync } from 'node:fs';
import { join, resolve } from 'node:path';

export const EXCEPTION_FILE = 'config/security/npm-audit-exceptions.json';

/** Pure evaluation (unit-tested). Returns { problems, explained, verdict }. */
export function evaluate(report, lock, exception, now = new Date()) {
  const problems = [];
  if (!report || typeof report !== 'object' || report.auditReportVersion !== 2 || typeof report.vulnerabilities !== 'object' || report.vulnerabilities === null || !report.metadata?.vulnerabilities)
    return { problems: ['npm audit report is not a valid v2 JSON report (scan failed?)'], explained: [], verdict: 'FAIL' };
  if (report.error) problems.push('npm audit reported an error: ' + JSON.stringify(report.error).slice(0, 200));
  const vulns = report.vulnerabilities;
  const meta = report.metadata.vulnerabilities;
  if ((meta.critical ?? 0) > 0) problems.push(`${meta.critical} CRITICAL finding(s)`);
  for (const [name, v] of Object.entries(vulns)) if (v.severity === 'critical') problems.push(`CRITICAL ${name}`);

  const ex = exception;
  const exceptionUsable = ex && ex.status === 'APPROVED' && ex.advisory && ex.package && Array.isArray(ex.propagation);
  const expired = exceptionUsable && !(now < new Date(ex.expires));
  if (exceptionUsable && Number.isNaN(new Date(ex.expires).getTime())) problems.push('exception expiry is not a valid date');
  const isApprovedAdvisory = (a) => exceptionUsable && typeof a === 'object' && a.name === ex.package && String(a.url ?? '').endsWith('/' + ex.advisory) && a.range === ex.vulnerableRange && a.severity === ex.severity;

  // Recursive explanation of HIGH entries (memoized; cycles are not explained).
  const memo = new Map();
  const explain = (name, stack = new Set()) => {
    if (memo.has(name)) return memo.get(name);
    if (stack.has(name)) return { ok: false, reachesApproved: false, why: `cycle at ${name}` };
    const v = vulns[name];
    if (!v) return { ok: false, reachesApproved: false, why: `via ${name} not in report` };
    stack.add(name);
    let ok = true, reaches = false, why = '';
    for (const via of v.via ?? []) {
      if (typeof via === 'string') {
        const dep = vulns[via];
        if (!dep) { ok = false; why = `unknown via ${via}`; continue; }
        if (dep.severity === 'high' || dep.severity === 'critical') {
          const r = explain(via, stack);
          if (!r.ok) { ok = false; why = r.why; }
          reaches ||= r.reachesApproved;
        }
      } else if (via && typeof via === 'object') {
        if (via.severity === 'high' || via.severity === 'critical') {
          if (isApprovedAdvisory(via)) reaches = true;
          else { ok = false; why = `unapproved advisory ${via.url ?? via.source} (${via.severity}) on ${via.name}`; }
        }
      } else { ok = false; why = `malformed via in ${name}`; }
    }
    stack.delete(name);
    if (ok && !reaches) { ok = false; why = `HIGH severity of ${name} is not traced to the approved advisory`; }
    const r = { ok, reachesApproved: reaches, why };
    memo.set(name, r);
    return r;
  };

  const explained = [];
  for (const [name, v] of Object.entries(vulns)) {
    if (v.severity !== 'high') continue;
    if (!exceptionUsable) { problems.push(`HIGH ${name} (no approved exception)`); continue; }
    const r = explain(name);
    if (!r.ok) { problems.push(`HIGH ${name}: ${r.why}`); continue; }
    if (!ex.propagation.includes(name)) { problems.push(`HIGH ${name}: dependency chain not recorded in ${ex.id} propagation (path changed)`); continue; }
    // every affected node must be a development-only lock entry
    for (const node of v.nodes ?? []) {
      const entry = lock?.packages?.[node];
      if (!entry) problems.push(`HIGH ${name}: node ${node} missing from package-lock.json`);
      else if (entry.dev !== true) problems.push(`HIGH ${name}: ${node} is NOT dev-only (dev=${entry.dev}) — exception does not cover production`);
    }
    if ((v.nodes ?? []).length === 0) problems.push(`HIGH ${name}: no lock node reported`);
    explained.push(name);
  }
  if (explained.length) {
    if (expired) problems.push(`exception ${ex.id} expired at ${ex.expires}`);
    // the vulnerable package itself must be the approved version everywhere in the lock
    const installs = Object.entries(lock?.packages ?? {}).filter(([k]) => k === `node_modules/${ex.package}` || k.endsWith(`/node_modules/${ex.package}`));
    if (!installs.length) problems.push(`${ex.package} not found in package-lock.json`);
    for (const [k, e] of installs) {
      if (e.version !== ex.installedVersion) problems.push(`${k} is ${e.version}, exception covers ${ex.installedVersion} only`);
      if (e.dev !== true) problems.push(`${k} is not dev-only`);
    }
  }
  const verdict = problems.length ? 'FAIL' : explained.length ? 'PASS_WITH_APPROVED_EXCEPTION' : 'PASS';
  return { problems, explained, verdict };
}

/** Modules of the production browser bundle must not include development tooling. */
export function bundleProblems(moduleIds, forbidden) {
  const out = [];
  for (const id of moduleIds) for (const pkg of forbidden) if (id.includes(`/node_modules/${pkg}/`)) out.push(`${pkg} in browser bundle (${id})`);
  return out;
}

function npm(args) {
  const r = spawnSync(process.platform === 'win32' ? 'npm.cmd' : 'npm', args, { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 });
  if (r.error) throw Error(`npm ${args.join(' ')} could not run: ${r.error.message}`);
  return r;
}

if (import.meta.main) {
  const flag = (n) => { const i = process.argv.indexOf(n); return i > 0 ? process.argv[i + 1] : undefined; };
  const dir = resolve(flag('--report-dir') ?? 'test-results-security');
  mkdirSync(dir, { recursive: true });
  try {
    const full = npm(['audit', '--json']);
    writeFileSync(join(dir, 'npm-audit-full.json'), full.stdout);
    let report;
    try { report = JSON.parse(full.stdout); } catch { throw Error('npm audit --json did not return JSON (scan failed)'); }
    const prod = npm(['audit', '--omit=dev', '--audit-level=high']);
    writeFileSync(join(dir, 'npm-audit-production.txt'), (prod.stdout ?? '') + (prod.stderr ?? ''));
    const prodJson = npm(['audit', '--omit=dev', '--json']);
    let prodReport;
    try { prodReport = JSON.parse(prodJson.stdout); } catch { throw Error('npm audit --omit=dev --json did not return JSON'); }
    const pm = prodReport.metadata?.vulnerabilities ?? {};
    console.log(`NPM_PRODUCTION_HIGH=${pm.high ?? '?'} NPM_PRODUCTION_CRITICAL=${pm.critical ?? '?'}`);
    if (prod.status !== 0 || (pm.high ?? 1) !== 0 || (pm.critical ?? 1) !== 0) throw Error('production dependencies have HIGH/CRITICAL findings (npm audit --omit=dev --audit-level=high failed)');
    // No exception file → no exception: every HIGH fails (the state after the upstream fix).
    const exception = existsSync(EXCEPTION_FILE) ? JSON.parse(readFileSync(EXCEPTION_FILE, 'utf8')) : null;
    const lock = JSON.parse(readFileSync('package-lock.json', 'utf8'));
    const result = evaluate(report, lock, exception);
    const m = report.metadata?.vulnerabilities ?? {};
    console.log(`NPM_FULL high=${m.high} critical=${m.critical} moderate=${m.moderate} explained=${result.explained.join(',') || 'none'}`);
    if (result.verdict === 'FAIL') {
      for (const p of result.problems) console.error('NPM_AUDIT_PROBLEM ' + p);
      throw Error('NPM_AUDIT=FAIL');
    }
    if (result.verdict === 'PASS_WITH_APPROVED_EXCEPTION')
      console.log(`NPM_AUDIT=PASS_WITH_APPROVED_EXCEPTION exception=${exception.id} advisory=${exception.advisory} package=${exception.package}@${exception.installedVersion} scope=dev-only expires=${exception.expires} (NOT fixed; approved by ${exception.approvedBy})`);
    else console.log('NPM_AUDIT=PASS');
  } catch (e) {
    console.error(e.message);
    process.exitCode = 1;
  }
}
