package prometheus_test

import (
	"os"
	"strings"
	"testing"
)

func TestDailyReviewDegradedAlertUsesDedicatedMetricAndJobScope(t *testing.T) {
	rules, err := os.ReadFile("alerts.yml")
	if err != nil {
		t.Fatalf("read alerts.yml: %v", err)
	}

	text := string(rules)
	want := `expr: increase(tradingagent_automation_job_degraded_total{job_name="daily_review"}[15m]) > 0`
	if !strings.Contains(text, "- alert: AugrDailyReviewDegraded") || !strings.Contains(text, want) {
		t.Fatalf("daily review degraded alert missing dedicated expression %q", want)
	}
	if strings.Contains(text, `tradingagent_automation_job_degraded_total[`) {
		t.Fatal("degraded alert must not cover unrelated jobs")
	}
}
