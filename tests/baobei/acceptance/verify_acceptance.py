#!/usr/bin/env python3
"""Baobei V1.1 acceptance manifest verifier (fail-closed).

Offline, standard library only, Python 3.8+. It checks the *shape and
completeness* of an acceptance evidence manifest built from
acceptance-template.json. It never contacts GitHub or any server and does not
prove that the evidence is true; an independent reviewer still has to.

Fail-closed rules (V1.1 R7-D; the delivered draft accepted an empty or
partial `gates` object because it only looped over the gates present):

* `gates` must contain exactly the 18 approved gate names, each `PASS`.
* `cases` must contain exactly AC-01..AC-27 once each, each `PASS` with at
  least one evidence file, a reviewer and a review date.
* `final_sha` and `ci.head_sha` are the same full 40-hex SHA; when a
  baseline is given, it must differ from it (the release is not the base).
* `ci.run_id` is a positive integer, `ci.attempt` is exactly the integer 1,
  `ci.jobs` is exactly the eight Baobei CI jobs, each `success`.
* Unknown top-level keys, non-object sections, booleans in place of numbers
  and duplicate JSON keys are rejected.

Exit status: 0 = GO ELIGIBLE (fields only), 2 = NO-GO or invalid input.
"""
import json
import re
import sys
from pathlib import Path

EXPECTED_JOBS = frozenset({
    'baobei-contract', 'baobei-web-quality', 'baobei-build', 'baobei-security',
    'baobei-api-test', 'baobei-db-test', 'baobei-compose', 'baobei-e2e',
})
EXPECTED_GATES = frozenset({
    'R0_CONTRACT', 'R1_MANAGER_OWNERSHIP', 'R2_STATS_ISOLATION', 'R3_IA_PIPELINE_PROFILE',
    'R4_FOLLOWUPS', 'R5_CUSTOMERS', 'R6_TECHNICAL', 'R7_INDEPENDENT_UAT', 'REAL_PG18_4',
    'REAL_KEYCLOAK', 'OWNER_VISUAL_UAT', 'ZERO_PERMISSION_LEAKS', 'MIGRATION_DATA_FINGERPRINT',
    'UNPATCHED_UP', 'PATH_A', 'PATH_B', 'SHARED_HOST_SECURITY', 'PRODUCTION_MUTATION_THIS_RELEASE',
})
EXPECTED_CASES = ['AC-%02d' % n for n in range(1, 28)]
TOP_KEYS = frozenset({'project', 'approved_scope', 'source_baseline_sha', 'final_sha', 'ci', 'gates', 'cases', 'provenance'})
SHA = re.compile(r'^[0-9a-f]{40}$')
DATE = re.compile(r'^\d{4}-\d{2}-\d{2}([T ][0-9:.+\-Z]+)?$')


def _no_duplicates(pairs):
    out = {}
    for k, v in pairs:
        if k in out:
            raise ValueError('duplicate JSON key: %s' % k)
        out[k] = v
    return out


def check(d):
    """Returns the list of failures for a parsed manifest (empty = eligible)."""
    f = []
    if not isinstance(d, dict):
        return ['manifest must be a JSON object']
    extra = set(d) - TOP_KEYS
    if extra:
        f.append('unknown top-level keys: ' + ', '.join(sorted(extra)))
    sha = d.get('final_sha')
    if not isinstance(sha, str) or not SHA.match(sha):
        f.append('final_sha must be a full 40-digit lowercase SHA')
    base = d.get('source_baseline_sha')
    if isinstance(base, str) and base and base == sha:
        f.append('final_sha equals the source baseline SHA')
    ci = d.get('ci')
    if not isinstance(ci, dict):
        f.append('ci must be an object')
        ci = {}
    if ci.get('head_sha') != sha:
        f.append('ci.head_sha != final_sha')
    run = ci.get('run_id')
    if isinstance(run, bool) or not (isinstance(run, int) and run > 0 or isinstance(run, str) and run.isdigit() and int(run) > 0):
        f.append('ci.run_id missing or not a positive integer')
    attempt = ci.get('attempt')
    if isinstance(attempt, bool) or attempt != 1 or not isinstance(attempt, int):
        f.append('ci.attempt must be exactly 1')
    jobs = ci.get('jobs')
    if not isinstance(jobs, dict) or set(jobs) != EXPECTED_JOBS:
        f.append('ci.jobs must be exactly the 8 Baobei CI jobs')
        jobs = jobs if isinstance(jobs, dict) else {}
    for name in sorted(EXPECTED_JOBS):
        if jobs.get(name) != 'success':
            f.append('CI job not success: ' + name)
    gates = d.get('gates')
    if not isinstance(gates, dict):
        f.append('gates must be an object')
        gates = {}
    missing, unknown = EXPECTED_GATES - set(gates), set(gates) - EXPECTED_GATES
    if missing:
        f.append('missing gates: ' + ', '.join(sorted(missing)))
    if unknown:
        f.append('unknown gates: ' + ', '.join(sorted(unknown)))
    for name in sorted(EXPECTED_GATES & set(gates)):
        if gates[name] != 'PASS':
            f.append('gate %s is %s' % (name, gates[name]))
    cases = d.get('cases')
    if not isinstance(cases, list):
        f.append('cases must be a list')
        cases = []
    ids = [c.get('id') if isinstance(c, dict) else None for c in cases]
    if sorted(i for i in ids if isinstance(i, str)) != EXPECTED_CASES or len(ids) != 27:
        dup = sorted({i for i in ids if ids.count(i) > 1 and i is not None})
        f.append('cases must be exactly AC-01..AC-27 once each' + (' (duplicates: %s)' % ', '.join(dup) if dup else ''))
    for c in cases:
        if not isinstance(c, dict):
            f.append('case entry is not an object')
            continue
        cid = c.get('id', '?')
        if c.get('status') != 'PASS':
            f.append('%s is %s' % (cid, c.get('status')))
        files = c.get('evidence_files')
        if not isinstance(files, list) or not files or not all(isinstance(x, str) and x.strip() for x in files):
            f.append(cid + ' lacks evidence_files')
        if not isinstance(c.get('reviewer'), str) or not c['reviewer'].strip():
            f.append(cid + ' lacks an independent reviewer')
        if not isinstance(c.get('reviewed_at'), str) or not DATE.match(c['reviewed_at']):
            f.append(cid + ' lacks reviewed_at')
    return f


def run(path):
    d = json.loads(Path(path).read_text(encoding='utf-8'), object_pairs_hook=_no_duplicates)
    failures = check(d)
    print('Baobei V1.1 acceptance gate:', 'GO ELIGIBLE FOR OWNER RELEASE APPROVAL' if not failures else 'NO-GO')
    for x in failures:
        print(' -', x)
    print('NOTE: validates evidence manifest fields ONLY. Does NOT prove evidence truth, SHA/CI authenticity, or authorize production release.')
    return 0 if not failures else 2


if __name__ == '__main__':
    if len(sys.argv) != 2:
        print('Usage: python3 verify_acceptance.py acceptance.json', file=sys.stderr)
        sys.exit(2)
    try:
        sys.exit(run(sys.argv[1]))
    except (ValueError, KeyError, TypeError, OSError) as exc:
        print('INVALID:', exc, file=sys.stderr)
        sys.exit(2)
