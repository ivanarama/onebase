import importlib.util
from pathlib import Path
import sys
import tempfile
import unittest

import test_coverage_test

spec = importlib.util.spec_from_file_location("race_coverage", Path(__file__).with_name("test-race-coverage.py"))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


class RaceCoverageCLI(test_coverage_test.CoverageCLI):
    # Run the same real Go/Node, dynamic discovery, exact profile-sum and
    # negative CLI acceptance through the positional compatibility entry point.
    runner = Path(__file__).with_name("test-race-coverage.py")

    def cli_command(self, runner):
        if runner == self.runner:
            return [sys.executable, str(runner), "coverage.out"]
        return super().cli_command(runner)


class RaceCoverageTest(unittest.TestCase):
    def test_inventory_includes_examples_and_fuzz_and_rejects_wrong_execution(self):
        names = runner.discover_names("TestA\nExample\nFuzzSeed\nok module 0.1s\n")
        runner.verify_inventory(names, list(reversed(names)))
        for actual in [names[:-1], names + [names[0]], names + ["TestUnexpected"]]:
            with self.assertRaises(ValueError):
                runner.verify_inventory(names, actual)
        for output in ["ok module 0.1s\n", "TestA\nTestA\n", "TestInvalid[\n"]:
            with self.assertRaises(ValueError):
                runner.discover_names(output)

    def test_merge_sums_atomic_counters_and_preserves_uncovered_blocks(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            profiles = [root / "other.out", root / "ui1.out", root / "ui2.out"]
            profiles[0].write_text("mode: atomic\nmodule/other.go:1.1,2.2 2 0\n", encoding="utf-8")
            profiles[1].write_text("mode: atomic\nmodule/ui.go:1.1,2.2 1 3\nmodule/ui.go:3.1,4.2 2 0\n", encoding="utf-8")
            profiles[2].write_text("mode: atomic\nmodule/ui.go:1.1,2.2 1 4\nmodule/ui.go:3.1,4.2 2 5\n", encoding="utf-8")
            out = root / "coverage.out"
            runner.merge_profiles(profiles, out)
            self.assertEqual(out.read_text(encoding="utf-8"), "mode: atomic\nmodule/other.go:1.1,2.2 2 0\nmodule/ui.go:1.1,2.2 1 7\nmodule/ui.go:3.1,4.2 2 5\n")
            for invalid in ["mode: set\n", "mode: atomic\n", "mode: atomic\nmodule/other.go:1.1,2.2 3 1\n", "mode: atomic\nmodule/other.go:1.1,2.2 2 -1\n"]:
                profiles[1].write_text(invalid, encoding="utf-8")
                selected = profiles[:2] if "other.go" in invalid else [profiles[1]]
                before = out.read_bytes()
                with self.assertRaises(ValueError):
                    runner.merge_profiles(selected, out)
                self.assertEqual(out.read_bytes(), before)


if __name__ == "__main__":
    unittest.main()
