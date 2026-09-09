#!/usr/bin/env python3
"""Guard shared Docker hosts from smoke-project and published-port collisions."""
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parent.parent


class SmokeIsolationTests(unittest.TestCase):
    def test_integration_database_is_selected_by_exact_run_identity(self):
        workflow = (ROOT / ".github/workflows/ci.yml").read_text()
        self.assertNotIn("55432", workflow)
        self.assertIn("--label tv.subcult.augr.ci-service=integration-postgres", workflow)
        self.assertIn("--filter 'label=tv.subcult.augr.ci-service=integration-postgres'", workflow)
        self.assertIn('[[ "$database_container" =~ ^[0-9a-f]{12,64}$ ]]', workflow)

    def test_project_is_scoped_to_run_and_attempt(self):
        workflow = (ROOT / ".github/workflows/ci.yml").read_text()
        smoke = workflow.split("  smoke-tests:", 1)[1].split("  build:", 1)[0]
        self.assertIn(
            "COMPOSE_PROJECT_NAME: augr-smoke-${{ github.run_id }}-${{ github.run_attempt }}",
            smoke,
        )
        self.assertIn("docker compose port app 8080", smoke)
        self.assertIn("docker compose port postgres 5432", smoke)
        self.assertNotIn("http://127.0.0.1:8081", smoke)
        self.assertNotIn("@127.0.0.1:5434/", smoke)

    def test_smoke_does_not_reuse_development_ports_or_bind_mount(self):
        override = (ROOT / "docker-compose.smoke.yml").read_text()
        self.assertIn("postgres:\n    ports: !override\n      - '127.0.0.1::5432'", override)
        self.assertIn("redis:\n    ports: !reset []", override)
        self.assertIn("volumes: !reset []", override)
        self.assertIn("ports: !override\n      - '127.0.0.1::8080'", override)


if __name__ == "__main__":
    unittest.main()
