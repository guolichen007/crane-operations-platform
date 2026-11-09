// Pure gate logic for the isolated PostgreSQL test batches (tools/r2-b2-test.mjs).
// Kept separate so the decision "did every required test really PASS on a real
// database" is unit-tested (tests/contract/r2-db-gate.test.mjs) and cannot be
// weakened silently.

/** Exact `go test -v` PASS line for a top-level test (prefix collisions excluded). */
export function passed(output, name) {
  return new RegExp(`^--- PASS: ${escape(name)} \\(`, 'm').test(output);
}
function escape(s) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/**
 * Returns the list of problems for one gated package run; empty = gate passes.
 * - any `--- SKIP:` line is a failure (a DB test that skipped never touched PostgreSQL)
 * - any `--- FAIL:` line or a missing final `PASS` is a failure
 * - every required test must have its own exact `--- PASS: <name> (` line
 * - when `exact` is set, the run must contain no PASS lines other than the required ones
 *   (guards a -test.run filter that silently matches nothing or something else)
 */
export function gateProblems(output, required, { exact = false } = {}) {
  const problems = [];
  if (/^\s*--- SKIP:/m.test(output)) problems.push('skipped test present');
  if (/^\s*--- FAIL:/m.test(output) || !/^PASS$/m.test(output)) problems.push('test binary did not PASS');
  for (const name of required) if (!passed(output, name)) problems.push(`required test not PASSed: ${name}`);
  if (exact) {
    const top = [...output.matchAll(/^--- PASS: (\S+) \(/gm)].map((m) => m[1]);
    const extra = top.filter((n) => !required.includes(n));
    if (extra.length) problems.push(`unexpected tests ran: ${extra.join(', ')}`);
  }
  return problems;
}

/** `-test.run` value selecting exactly the given top-level tests. */
export function runPattern(required) {
  return '^(' + required.map(escape).join('|') + ')$';
}
