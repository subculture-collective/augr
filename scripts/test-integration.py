#!/usr/bin/env python3
"""Run the complete Go suite against an explicitly disposable, migrated database."""
import argparse
import json
import os
import re
from pathlib import Path
import subprocess
import sys
import tempfile
from urllib.parse import urlsplit, unquote

ROOT = Path(__file__).resolve().parent.parent
MODULE = "github.com/PatrickFanella/get-rich-quick/"
PACKAGES = ["./cmd/...", "./internal/...", "./migrations/...", "./monitoring/..."]
# These contracts must execute, not merely exist or report a skip.
REQUIRED = {
    "internal/automation/TestOperationalDailyScoringGuards",
    "internal/automation/TestDeepScanSIPFallbackReceipts",
    "internal/data/alpaca/TestStockDailyPreservesSplitAdjustedSIPSource",
    "internal/data/alpaca/TestStockDailyCompletedSessionDelay",
    "internal/data/alpaca/TestStockDailyCacheSeparatesAndRevalidatesSource",
    "internal/repository/postgres/TestStockDailyCacheSourceIsolation",
    "internal/automation/TestDeepScanFallsBackFromStaleCompletedDailySeries",
    "internal/automation/TestDeepScanFreshnessCacheAndClassification",
    "internal/repository/postgres/TestOptionMarketPayloadPreservesExactAndLegacySourceEvidence",
    "internal/repository/postgres/TestOptionTradePayloadPreservesExactAndLegacySourceEvidence",
    "internal/repository/postgres/TestOptionSnapshotPayloadPreservesExactAndLegacySourceEvidence",
    "internal/datasetimport/TestProviderSourceCapturesExactSnapshotEvidence",
    "internal/datasetimport/TestProviderSourceRejectsMismatchedExactSnapshots",
    "internal/datasetimport/TestProviderSourceRejectsLegacyFloatSnapshots",
    "internal/data/alpaca/TestExactOptionsSnapshotProviderPages",
    "internal/data/alpaca/TestExactOptionsSnapshotProviderRejectsInvalidRequests",
    "internal/data/alpaca/TestDecodeExactSnapshotPreservesPresenceAndPrecision",
    "internal/data/alpaca/TestDecodeExactSnapshotOptionalTrade",
    "internal/data/alpaca/TestDecodeExactSnapshotRejectsMissingAndMalformedFields",
    "internal/dataset/TestSnapshotSourceObjectBinding",
    "internal/dataset/TestExactSnapshotQuoteSourceBinding",
    "internal/dataset/TestExactSnapshotAggregateSourceBinding",
    "internal/domain/TestParseStrictOCC",
    "internal/datasetimport/TestExactOptionTradeImportProviderPagination",
    "internal/datasetimport/TestExactOptionTradeImportRejectsMismatchedEvidence",
    "internal/datasetimport/TestOptionTradeImportRejectsLegacyFloatSource",
    "internal/data/alpaca/TestExactOptionsTradeProviderPages",
    "internal/data/alpaca/TestDecodeExactOptionTradePreservesSource",
    "internal/dataset/TestExactOptionTradeSourceBinding",
    "internal/datasetimport/TestExactOptionBarImportRoundtrip",
    "internal/datasetimport/TestExactOptionBarImportProviderPagination",
    "internal/datasetimport/TestExactOptionBarImportRejectsMismatchedEvidence",
    "internal/datasetimport/TestOptionBarImportRejectsLegacyFloatSource",
    "internal/data/alpaca/TestExactOptionsProviderPages",
    "internal/repository/postgres/TestBoundMarketDatasetPreservesExactSourceEvidence",
    "internal/datasetimport/TestExactStockImportEvidence",
    "internal/dataset/TestMarketPayloadSourceEvidenceRoundtrip",
    "internal/data/polygon/TestExactProviderPageEvidence",
    "internal/data/polygon/TestExactProviderPagination",
    "internal/discovery/TestScreenUsesFrozenManifestEvaluationInterval",
    "internal/discovery/TestSweepHistoryUsesFrozenManifestIntervalAcrossWallClocks",
    "internal/data/TestResearchIntervalReconstructsExactScopeAndFailsClosed",
    "internal/data/TestResearchIntervalNeverFallsBackForBoundReaders",
    "internal/repository/postgres/TestConfiguredScopeEnablesStockAndKeepsOptionsFailClosed",
    "internal/data/alpaca/TestStockQuotePennyContractPriceDomain",
    "cmd/augr-reference-import/TestReferenceImportPostgresReplay",
    "internal/repository/postgres/TestPipelineETFCaptureQualifiedStatus",
    "internal/repository/postgres/TestLoadSignalPreparationRetainedGraph",
    "internal/repository/postgres/TestPipelineStockCaptureQualifiedStatus",
    "internal/repository/postgres/TestPipelineStockCaptureRejectsMissingStatus",
    "internal/repository/postgres/TestAlpacaStockQuotePersistenceAndReplay",
    "internal/agent/TestRunnerCompletionPreparationChronology",
    "cmd/tradingagent/TestConfigurePreparedStockCapture",
    "internal/repository/postgres/TestInternalAccountCapitalSnapshotPersistsReplaysAndRejectsForgery",
    "internal/repository/postgres/TestInternalAccountCapitalSnapshotDoesNotCallBroker",
    "internal/repository/postgres/TestInternalAccountCapitalUsesAttestedProjectionWithoutExperiment",
    "internal/repository/postgres/TestCashOnlyProjectionBootstrapRefreshAndReplay",
    "migrations/TestPortfolioRiskMigrationRejectsForgeryMutationAndRollback",
    "internal/repository/postgres/TestAccountRepoCreatesAccountWithOpeningCapital",
    "internal/repository/postgres/TestAccountRepoRejectsCapitalFlowMetadataConflictBeyondFloatPrecision",
    "internal/repository/postgres/TestLedgerRepoRejectsIdempotencyPayloadConflict",
    "internal/repository/postgres/TestEconomicEventRepoFailedApplyRetainsRawWithoutPartialLedger",
    "internal/repository/postgres/TestEconomicEventRepoConcurrentIdenticalApplyConverges",
    "internal/repository/postgres/TestMarketPayloadRepositoryPersistsBindsAndRejectsMutation",
    "internal/repository/postgres/TestMarketPayloadBindingRejectsDivergentObservation",
    "internal/repository/postgres/TestMarketPayloadMigrationEmptyRollbackAndReapply",
    "internal/signal/TestSignalRecorderCanonicalPostgresGuard",
}


def validate(events, exceptions):
    passed, skipped, discovered = set(), set(), set()
    reasons = {}
    failures = []
    for event in events:
        test = event.get("Test", "")
        if not test:
            if event["Action"] == "fail":
                failures.append(event["Package"])
            continue
        key = event["Package"].removeprefix(MODULE) + "/" + test
        if event["Action"] == "output":
            reasons[key] = reasons.get(key, "") + event.get("Output", "")
        if "/" not in test:
            discovered.add(key)
        if event["Action"] == "pass":
            passed.add(key)
        elif event["Action"] == "skip":
            skipped.add(key)
        elif event["Action"] == "fail":
            failures.append(key)
    for key in sorted(skipped):
        if key not in exceptions or exceptions[key] not in reasons.get(key, ""):
            failures.append("unexpected skip: " + key)
    failures.extend("required contract did not pass: " + key for key in sorted(REQUIRED - passed))
    failures.extend("stale skip exception: " + key for key in sorted(exceptions.keys() - discovered))
    return failures, len(passed), len(skipped)


def failure_diagnostics(events):
    """Expose structured failure metadata, never raw test output or DSNs."""
    running = set()
    failed = set()
    timed_out = set()
    for event in events:
        package = event.get("Package", "")
        test = event.get("Test", "")
        key = package.removeprefix(MODULE) + "/" + test
        action = event.get("Action")
        if test and action == "run":
            running.add(key)
        elif test and action in {"pass", "skip", "fail"}:
            running.discard(key)
        if action == "fail":
            failed.add(package.removeprefix(MODULE))
        if action == "output" and "panic: test timed out after " in event.get("Output", ""):
            timed_out.add(package.removeprefix(MODULE))
    return {
        "failed_packages": sorted(failed),
        "timeout_packages": sorted(timed_out),
        "unfinished_tests": sorted(running),
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output-dir", type=Path)
    parser.add_argument("--coverage", type=Path)
    args = parser.parse_args()
    dsn = os.environ.get("TEST_DATABASE_URL", "")
    parsed = urlsplit(dsn)
    if parsed.scheme not in {"postgres", "postgresql"} or not unquote(parsed.path).endswith("_test"):
        parser.error("TEST_DATABASE_URL must explicitly name a disposable PostgreSQL database ending in _test; apply migrations first")
    output = args.output_dir or Path(tempfile.mkdtemp(prefix="augr-integration-"))
    output.mkdir(parents=True, exist_ok=True)
    env = os.environ.copy()
    # Never inherit retained qualification databases or externally enabled tiers.
    for key in list(env):
        if "DB_URL" in key or "DATABASE_URL" in key or key in {"RUN_SMOKE_TEST", "OPENCODE_INTEGRATION_URL", "OPENCODE_SERVER_PASSWORD", "AUGR_RETAIN_PROJECTION_SCHEMA"}:
            env.pop(key)
    env.update(TEST_DATABASE_URL=dsn, DATABASE_URL=dsn, DB_URL=dsn)
    exceptions = json.loads((ROOT / "scripts/integration-exceptions.json").read_text())
    discovery = subprocess.run(["go", "test", "-race", "-p=1", "-list=^Test", "-json", *PACKAGES], cwd=ROOT, env=env, capture_output=True, text=True)
    (output / "discovery.jsonl").write_text(discovery.stdout + discovery.stderr)
    names = set()
    for line in discovery.stdout.splitlines():
        event = json.loads(line)
        name = event.get("Output", "").strip()
        if re.fullmatch(r"Test\w+", name):
            names.add(event["Package"].removeprefix(MODULE) + "/" + name)
    missing = (REQUIRED | exceptions.keys()) - names
    if discovery.returncode or missing:
        print("Integration discovery failed; missing registered contracts:", sorted(missing))
        print("Discovery evidence:", output / "discovery.jsonl")
        return 1
    command = ["go", "test", "-race", "-count=1", "-p=1", "-timeout=30m", "-json"]
    if args.coverage:
        command.append("-coverprofile=" + str(args.coverage.resolve()))
    command.extend(PACKAGES)
    events = []
    with (output / "tests.jsonl").open("w", buffering=1) as log:
        process = subprocess.Popen(command, cwd=ROOT, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        for line in process.stdout:
            log.write(line)
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                print(line, end="", flush=True)
                continue
            events.append(event)
            if event["Action"] in {"pass", "fail"} and not event.get("Test"):
                print(event["Action"], event["Package"], flush=True)
        code = process.wait()
    failures, passed, skipped = validate(events, exceptions)
    summary = {"exit_code": code, "passed_including_subtests": passed, "explicit_skips": skipped, "failures": failures}
    if code or failures:
        summary["diagnostics"] = failure_diagnostics(events)
    (output / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
    print(json.dumps(summary, indent=2))
    print("Integration evidence:", output)
    return 1 if code or failures else 0


if __name__ == "__main__":
    sys.exit(main())
