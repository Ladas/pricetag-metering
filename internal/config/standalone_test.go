package config

import "testing"

func TestMonthlyTokenQuotaExplicitZero(t *testing.T) {
	t.Setenv("MONTHLY_TOKEN_QUOTA", "0")
	if got := Load().MonthlyTokenQuota; got != 0 {
		t.Fatalf("explicit zero must disable the token gate, got %v", got)
	}
}
