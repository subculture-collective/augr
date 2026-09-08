#!/usr/bin/env python3
"""Negative cases for the integration runner's execution/skip gate."""
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("integration", Path(__file__).with_name("test-integration.py"))
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)


def event(key, action="pass", output=""):
    package, test = key.rsplit("/", 1)
    return {"Package": gate.MODULE + package, "Test": test, "Action": action, "Output": output}


class IntegrationGateTests(unittest.TestCase):
    def setUp(self):
        self.events = [event(key) for key in gate.REQUIRED]

    def test_requires_exercised_contracts_even_when_go_succeeds(self):
        for events in [[], self.events[1:], [dict(e, Action="run") for e in self.events]]:
            with self.subTest(events=len(events)):
                self.assertTrue(gate.validate(events, {})[0])

    def test_unexpected_skip_and_changed_reason_fail(self):
        key = "internal/repository/postgres/TestRetained"
        events = self.events + [event(key, "output", "requires database"), event(key, "skip")]
        self.assertTrue(gate.validate(events, {})[0])
        self.assertTrue(gate.validate(events, {key: "different prerequisite"})[0])
        self.assertFalse(gate.validate(events, {key: "requires database"})[0])

    def test_exception_cannot_hide_missing_required_behavior(self):
        key = next(iter(gate.REQUIRED))
        events = [e for e in self.events if e["Test"] != key.rsplit("/", 1)[1]]
        events += [event(key, "output", "skipped"), event(key, "skip")]
        self.assertTrue(gate.validate(events, {key: "skipped"})[0])

    def test_removed_exception_and_subtest_skip_fail(self):
        self.assertTrue(gate.validate(self.events, {"missing/TestGone": "retained"})[0])
        self.assertTrue(gate.validate(self.events + [event("internal/example/TestContract/case", "skip")], {})[0])

    def test_package_failure_is_not_hidden_by_passed_contracts(self):
        events = self.events + [{"Package": "broken", "Action": "fail"}]
        self.assertTrue(gate.validate(events, {})[0])
        self.assertFalse(gate.validate(self.events, {})[0])


if __name__ == "__main__":
    unittest.main()
