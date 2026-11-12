"""G5-RC0: test fixtures must not print live credentials to stdout / CI logs."""
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[3]
API = ROOT / "services/project-intake-api"
# Values that are usable credentials or personal data while a fixture runs.
FORBIDDEN = re.compile(r'fmt\.Print[a-z]*\([^)]*(TemporaryPassword|BAOBEI_KC_TEMP=|BAOBEI_KC_PHONE=|Credential\.Login|token|Token|secret|Secret)')


class FixtureOutputTests(unittest.TestCase):
    def test_go_fixtures_do_not_print_credentials(self):
        offenders = []
        for path in sorted(API.glob("*_test.go")):
            for no, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
                if FORBIDDEN.search(line):
                    offenders.append(f"{path.name}:{no}: {line.strip()}")
        self.assertEqual(offenders, [], "fixtures print live credentials:\n" + "\n".join(offenders))


if __name__ == "__main__":
    unittest.main()
