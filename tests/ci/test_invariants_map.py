"""docs/invariants.md may only cite tests that exist."""
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


class InvariantsMapTest(unittest.TestCase):
    def test_every_cited_test_exists(self):
        doc = (ROOT / "docs" / "invariants.md").read_text()
        rows = [line for line in doc.splitlines() if re.match(r"^\| S\d\d \|", line)]
        self.assertGreaterEqual(len(rows), 18)
        go_tests = set()
        for path in list((ROOT / "internal").rglob("*_test.go")) + list((ROOT / "cmd").rglob("*_test.go")):
            go_tests.update(re.findall(r"^func (Test\w+)\(", path.read_text(), re.M))
        for row in rows:
            invariant = row.split("|")[1].strip()
            cited = re.findall(r"`(Test\w+)`", row)
            files = re.findall(r"`(tests/[\w/.-]+)`", row)
            self.assertTrue(cited or files, f"{invariant} cites no test")
            for name in cited:
                self.assertIn(name, go_tests, f"{invariant} cites missing Go test {name}")
            for path in files:
                self.assertTrue((ROOT / path).exists(), f"{invariant} cites missing file {path}")


if __name__ == "__main__":
    unittest.main()
