// SEC-EX-001: npm audit gate with one approved, time-limited exception
// (picomatch GHSA-fc5q-39xc-vjpf / CVE-2026-112366, dev chain only).
//
// Fail-closed. The gate passes only when:
//   * the full `npm audit --json` report parses, is consistent and contains no
//     high/critical finding other than the approved advisory and the packages
//     whose only reason to be reported is a dependency on it;
//   * `npm audit --omit=dev --json` reports no high/critical finding;
//   * every reported node exists in package-lock.json as a dev dependency, and
//     the picomatch nodes are exactly the approved ones (version + integrity);
//   * the approval file is byte-for-byte the reviewed record (sha256 pinned
//     here) and today is not after its expiry, nor more than 7 days after an
//     official picomatch fix was published on the registry.
// Anything else (other GHSA, a second picomatch advisory, a production
// finding, unknown nodes, a changed tree, malformed JSON, npm/registry errors,
// an expired or edited approval) fails. No other policy is relaxed: the audit
// level stays `high`, nothing else is ignored.
import { createHash } from 'node:crypto';

export const APPROVAL_SHA256 = '08501e4a3749d7ca045da90b7416cba8dbe495120481cc1c1f5fb0ec6e9a925e';
const SEVERE = new Set(['high', 'critical']);
const DAY = 86400000;

const fail = (reasons) => ({ ok: false, reasons });
const sha256 = (text) => createHash('sha256').update(text).digest('hex');

function parse(text, label, reasons) {
  try {
    const value = JSON.parse(text);
    if (!value || typeof value !== 'object' || Array.isArray(value)) throw Error('not an object');
    return value;
  } catch (error) {
    reasons.push(`${label}: invalid JSON (${error.message})`);
    return null;
  }
}

function semver(v) {
  const m = /^(\d+)\.(\d+)\.(\d+)$/.exec(v);
  return m ? m.slice(1).map(Number) : null;
}
const greater = (a, b) => a[0] - b[0] || a[1] - b[1] || a[2] - b[2];

function checkReport(report, label, reasons) {
  if (report.error) reasons.push(`${label}: npm reported an error (${report.error.code || report.error.summary || 'unknown'})`);
  if (report.auditReportVersion !== 2) reasons.push(`${label}: unsupported auditReportVersion ${report.auditReportVersion}`);
  const vulns = report.vulnerabilities, meta = report.metadata?.vulnerabilities;
  if (!vulns || typeof vulns !== 'object' || Array.isArray(vulns) || !meta || typeof meta !== 'object') {
    reasons.push(`${label}: missing vulnerabilities or metadata`);
    return null;
  }
  const counts = { info: 0, low: 0, moderate: 0, high: 0, critical: 0 };
  for (const [name, v] of Object.entries(vulns)) {
    if (!v || v.name !== name || !(v.severity in counts) || !Array.isArray(v.via) || !Array.isArray(v.nodes)) {
      reasons.push(`${label}: malformed entry ${name}`);
      return null;
    }
    counts[v.severity]++;
  }
  for (const k of Object.keys(counts)) {
    if (meta[k] !== counts[k]) reasons.push(`${label}: metadata.${k}=${meta[k]} does not match ${counts[k]} entries`);
  }
  if (meta.total !== Object.values(counts).reduce((a, b) => a + b, 0)) reasons.push(`${label}: metadata.total mismatch`);
  return vulns;
}

/**
 * @param {object} input
 * @param {string} input.full       raw stdout of `npm audit --json`
 * @param {string} input.prod       raw stdout of `npm audit --omit=dev --json`
 * @param {string} input.lock       raw package-lock.json
 * @param {string} input.approval   raw approval file
 * @param {string} input.registry   raw `npm view picomatch time --json`
 * @param {Date}   input.now
 */
export function evaluate({ full, prod, lock, approval, registry, now }) {
  const reasons = [];
  // 1. The approval record is the reviewed one, and in force.
  if (typeof approval !== 'string' || sha256(approval) !== APPROVAL_SHA256) {
    return fail(['approval: file differs from the approved SEC-EX-001 record (edited or forged)']);
  }
  const ex = parse(approval, 'approval', reasons);
  const fullReport = parse(full, 'npm audit', reasons);
  const prodReport = parse(prod, 'npm audit --omit=dev', reasons);
  const lockFile = parse(lock, 'package-lock.json', reasons);
  const times = parse(registry, 'registry picomatch', reasons);
  if (reasons.length) return fail(reasons);
  if (ex.status !== 'APPROVED' || ex.id !== 'SEC-EX-001') return fail(['approval: not an approved SEC-EX-001 record']);
  const today = Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate());
  let deadline = Date.parse(ex.expires_on + 'T00:00:00Z');
  if (!Number.isFinite(deadline)) return fail(['approval: invalid expires_on']);
  // 2. An official fix on the registry shortens the exception to 7 days.
  const vulnerableMax = semver('4.0.7');
  let fix = null;
  for (const [version, at] of Object.entries(times)) {
    const v = semver(version);
    if (v && greater(v, vulnerableMax) > 0) {
      const published = Date.parse(at);
      if (!Number.isFinite(published)) return fail([`registry: invalid publish time for ${version}`]);
      if (!fix || published < fix.published) fix = { version, published };
    }
  }
  if (!times['4.0.7']) return fail(['registry: picomatch time list incomplete (registry or network error)']);
  if (fix) deadline = Math.min(deadline, fix.published + ex.fix_grace_days * DAY);
  if (today > deadline) {
    return fail([fix ? `approval: official fix picomatch@${fix.version} published; ${ex.fix_grace_days}-day upgrade window passed`
                     : `approval: SEC-EX-001 expired on ${ex.expires_on}`]);
  }
  // 3. Production dependencies: no high/critical at all.
  const prodVulns = checkReport(prodReport, 'npm audit --omit=dev', reasons);
  if (prodVulns) {
    for (const v of Object.values(prodVulns)) if (SEVERE.has(v.severity)) reasons.push(`production dependency ${v.name} has a ${v.severity} finding`);
  }
  // 4. Full report: only the approved advisory and its propagation.
  const vulns = checkReport(fullReport, 'npm audit', reasons);
  if (!vulns) return fail(reasons);
  const packages = lockFile.packages;
  if (!packages || typeof packages !== 'object') return fail(['package-lock.json: no packages map']);
  const approvedNodes = new Map(Object.entries(ex.nodes));
  const lockPicomatch = Object.keys(packages).filter((k) => k.endsWith('node_modules/picomatch'));
  if (lockPicomatch.length !== approvedNodes.size || !lockPicomatch.every((k) => approvedNodes.has(k))) {
    reasons.push(`dependency tree changed: picomatch nodes in lock are ${JSON.stringify(lockPicomatch.sort())}`);
  }
  for (const [node, version] of approvedNodes) {
    const p = packages[node];
    if (!p || p.version !== version || p.dev !== true || p.integrity !== ex.integrity) {
      reasons.push(`dependency tree changed: ${node} is not picomatch@${version} (dev, approved integrity)`);
    }
  }
  const severe = Object.values(vulns).filter((v) => SEVERE.has(v.severity));
  const excepted = [];
  for (const v of severe) {
    if (v.severity === 'critical') reasons.push(`${v.name}: critical finding`);
    for (const node of v.nodes) {
      const p = packages[node];
      if (!p) reasons.push(`${v.name}: unknown node ${node} (not in package-lock.json)`);
      else if (p.dev !== true) reasons.push(`${v.name}: node ${node} is not a dev dependency`);
    }
    const advisories = v.via.filter((x) => typeof x === 'object');
    if (v.name === ex.advisory.package) {
      const a = advisories[0];
      if (advisories.length !== 1 || v.via.length !== 1 || a.url !== ex.advisory.url || a.source !== ex.advisory.npm_source ||
          a.range !== ex.advisory.vulnerable_range || a.severity !== ex.advisory.max_severity || a.name !== ex.advisory.package) {
        reasons.push(`${v.name}: advisories differ from the approved one (${JSON.stringify(advisories.map((x) => x.url || x.source))})`);
      }
      const nodes = [...v.nodes].sort(), expected = [...approvedNodes.keys()].sort();
      if (JSON.stringify(nodes) !== JSON.stringify(expected)) reasons.push(`${v.name}: reported nodes differ from the approved nodes`);
      excepted.push(v.name);
      continue;
    }
    if (advisories.length) {
      reasons.push(`${v.name}: own advisory ${advisories.map((x) => x.url || x.source).join(', ')} is not covered by SEC-EX-001`);
      continue;
    }
    if (!ex.propagated.includes(v.name)) reasons.push(`${v.name}: not an approved propagated package`);
    excepted.push(v.name);
  }
  // Every propagated finding must resolve, through `via`, to the approved
  // advisory only (shared sub-dependencies are fine; a cycle is not).
  const resolves = (name, stack = new Set()) => {
    if (name === ex.advisory.package) return true;
    const v = vulns[name];
    if (!v || stack.has(name) || v.via.some((x) => typeof x === 'object')) return false;
    const next = new Set(stack).add(name);
    return v.via.length > 0 && v.via.every((x) => resolves(x, next));
  };
  for (const v of severe) if (v.name !== ex.advisory.package && !resolves(v.name)) reasons.push(`${v.name}: does not resolve only to ${ex.advisory.ghsa}`);
  if (severe.length && !vulns[ex.advisory.package]) reasons.push('high findings present without the approved advisory');
  if (reasons.length) return fail(reasons);
  return {
    ok: true,
    status: severe.length ? 'PASS_WITH_APPROVED_EXCEPTION' : 'PASS',
    unfixed: severe.length ? {
      exception: ex.id, ghsa: ex.advisory.ghsa, cve: ex.advisory.cve, package: `${ex.advisory.package}@${ex.advisory.vulnerable_range}`,
      nodes: [...approvedNodes.keys()], reported: excepted.sort(), deadline: new Date(deadline).toISOString().slice(0, 10),
      officialFix: fix ? fix.version : null,
    } : null,
    note: severe.length ? null : 'no high/critical findings: remove SEC-EX-001',
  };
}
