"""Fail-closed tests for verify_acceptance.py (V1.1 R7-D)."""
import copy
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import verify_acceptance as va  # noqa: E402

TEMPLATE = json.loads((HERE / 'acceptance-template.json').read_text(encoding='utf-8'))
SHA = 'a' * 40


def complete():
    d = copy.deepcopy(TEMPLATE)
    d['final_sha'] = SHA
    d['ci'] = {'run_id': '44027107200', 'attempt': 1, 'head_sha': SHA, 'jobs': {j: 'success' for j in va.EXPECTED_JOBS}}
    d['gates'] = {g: 'PASS' for g in va.EXPECTED_GATES}
    for c in d['cases']:
        c.update(status='PASS', evidence_files=['evidence/' + c['id'] + '.md'], reviewer='独立复核', reviewed_at='2026-11-17')
    return d


class VerifyAcceptance(unittest.TestCase):
    def test_template_matches_the_tool(self):
        self.assertEqual(set(TEMPLATE['gates']), va.EXPECTED_GATES)
        self.assertEqual(len(va.EXPECTED_GATES), 18)
        self.assertEqual([c['id'] for c in TEMPLATE['cases']], va.EXPECTED_CASES)
        self.assertEqual(set(TEMPLATE['ci']['jobs']), va.EXPECTED_JOBS)

    def test_complete_manifest_is_eligible(self):
        self.assertEqual(va.check(complete()), [])

    def test_untouched_template_is_rejected(self):
        self.assertTrue(va.check(copy.deepcopy(TEMPLATE)))

    def rejected(self, mutate, expect):
        d = complete()
        mutate(d)
        failures = va.check(d)
        self.assertTrue(any(expect in x for x in failures), failures)

    def test_empty_gates(self):
        self.rejected(lambda d: d.update(gates={}), 'missing gates')

    def test_missing_one_gate(self):
        self.rejected(lambda d: d['gates'].pop('PATH_B'), 'missing gates: PATH_B')

    def test_unknown_gate(self):
        self.rejected(lambda d: d['gates'].update(EXTRA='PASS'), 'unknown gates')

    def test_not_run_gate(self):
        self.rejected(lambda d: d['gates'].update(REAL_KEYCLOAK='NOT_RUN'), 'REAL_KEYCLOAK is NOT_RUN')

    def test_gates_not_object(self):
        self.rejected(lambda d: d.update(gates=['PASS']), 'gates must be an object')

    def test_duplicate_case(self):
        def dup(d):
            d['cases'][1] = copy.deepcopy(d['cases'][0])
        self.rejected(dup, 'duplicates: AC-01')

    def test_missing_case(self):
        self.rejected(lambda d: d['cases'].pop(), 'exactly AC-01..AC-27')

    def test_not_run_case(self):
        self.rejected(lambda d: d['cases'][5].update(status='NOT_RUN'), 'AC-06 is NOT_RUN')

    def test_case_without_evidence(self):
        self.rejected(lambda d: d['cases'][0].update(evidence_files=['  ']), 'AC-01 lacks evidence_files')

    def test_case_without_reviewer(self):
        self.rejected(lambda d: d['cases'][0].update(reviewer=''), 'reviewer')

    def test_wrong_sha(self):
        self.rejected(lambda d: d.update(final_sha='30ab9d9'), 'final_sha must be')

    def test_uppercase_sha(self):
        self.rejected(lambda d: d.update(final_sha=SHA.upper()), 'final_sha must be')

    def test_head_sha_differs(self):
        self.rejected(lambda d: d['ci'].update(head_sha='b' * 40), 'ci.head_sha != final_sha')

    def test_final_equals_baseline(self):
        self.rejected(lambda d: d.update(source_baseline_sha=SHA), 'equals the source baseline')

    def test_missing_run(self):
        self.rejected(lambda d: d['ci'].update(run_id=''), 'ci.run_id')

    def test_attempt_two(self):
        self.rejected(lambda d: d['ci'].update(attempt=2), 'attempt must be exactly 1')

    def test_attempt_bool(self):
        self.rejected(lambda d: d['ci'].update(attempt=True), 'attempt must be exactly 1')

    def test_attempt_string(self):
        self.rejected(lambda d: d['ci'].update(attempt='1'), 'attempt must be exactly 1')

    def test_seven_jobs(self):
        self.rejected(lambda d: d['ci']['jobs'].pop('baobei-e2e'), 'exactly the 8')

    def test_failed_job(self):
        self.rejected(lambda d: d['ci']['jobs'].update({'baobei-db-test': 'failure'}), 'baobei-db-test')

    def test_unknown_top_key(self):
        self.rejected(lambda d: d.update(override=True), 'unknown top-level keys')

    def test_cli_exit_codes_and_duplicate_keys(self):
        with tempfile.TemporaryDirectory() as tmp:
            good = os.path.join(tmp, 'good.json')
            Path(good).write_text(json.dumps(complete(), ensure_ascii=False), encoding='utf-8')
            r = subprocess.run([sys.executable, str(HERE / 'verify_acceptance.py'), good], capture_output=True, text=True)
            self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
            bad = os.path.join(tmp, 'bad.json')
            Path(bad).write_text(json.dumps({**complete(), 'gates': {}}), encoding='utf-8')
            r = subprocess.run([sys.executable, str(HERE / 'verify_acceptance.py'), bad], capture_output=True, text=True)
            self.assertEqual(r.returncode, 2)
            self.assertIn('NO-GO', r.stdout)
            dup = os.path.join(tmp, 'dup.json')
            text = json.dumps(complete())
            Path(dup).write_text(text[:-1] + ', "final_sha": "' + SHA + '"}', encoding='utf-8')
            r = subprocess.run([sys.executable, str(HERE / 'verify_acceptance.py'), dup], capture_output=True, text=True)
            self.assertEqual(r.returncode, 2)
            self.assertIn('duplicate JSON key', r.stderr)


if __name__ == '__main__':
    unittest.main()
