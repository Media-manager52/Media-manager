from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[2]


class BackendSplitContractTests(unittest.TestCase):
    """Every PostgreSQL test must land on a machine that Verify starts."""

    def test_matrix_starts_every_machine_the_split_counts(self):
        workflow = (ROOT / ".github/workflows/verify.yml").read_text()
        job = workflow[workflow.index("\n  backend-db:\n"):workflow.index("\n  backend:\n")]
        machines = re.search(r"machine: \[([0-9, ]+)\]", job).group(1)
        declared = [int(value) for value in machines.split(",")]
        count = int(re.search(r"BACKEND_TEST_PART: postgres:\$\{\{ matrix\.machine \}\}/([0-9]+)", job).group(1))
        self.assertEqual(declared, list(range(count)))

    def test_backend_result_requires_every_backend_job(self):
        workflow = (ROOT / ".github/workflows/verify.yml").read_text()
        job = workflow[workflow.index("\n  backend:\n"):workflow.index("\n  containers:\n")]
        self.assertIn("needs: [reuse, backend-checks, backend-db]", job)
        self.assertIn('test "$CHECKS" = success && test "$DATABASE" = success', job)


if __name__ == "__main__":
    unittest.main()
