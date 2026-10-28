package storage

import (
	"fmt"
	"testing"
	"time"
)

func TestManagementLoginPreservesIdentity(t *testing.T) {
	s, ctx := openTestStore(t)
	first, err := s.ProvisionLogin(ctx, "test-admin", LoginProvision{Subject: "alice", Name: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.ProvisionLogin(ctx, "test-admin", LoginProvision{Subject: "alice", Name: "Alice Updated"})
	if err != nil {
		t.Fatal(err)
	}
	if first.UserID != second.UserID {
		t.Fatal("repeat setup changed identity")
	}
	if _, err = s.ProvisionLogin(ctx, "test-admin", LoginProvision{Subject: "bad\nsubject"}); err == nil {
		t.Fatal("invalid login accepted")
	}
	if _, err = s.ProvisionLogin(ctx, "test-admin", LoginProvision{Subject: "alice", Role: "unexpected"}); err == nil {
		t.Fatal("invalid role accepted")
	}
}

func TestManagementPendingRevocationRejectsActivationAtomically(t *testing.T) {
	s, ctx := openTestStore(t)
	user, err := s.ProvisionLogin(ctx, "test-admin", LoginProvision{Subject: "pending-user", Name: "Original"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE partner_users SET active=false,key_revocation_pending=true WHERE user_id=$1`, user.UserID); err != nil {
		t.Fatal(err)
	}
	active := true
	if _, err := s.ProvisionLogin(ctx, "test-admin", LoginProvision{Subject: "pending-user", Name: "Changed", Active: &active}); err == nil {
		t.Fatal("pending revocation reported successful activation")
	}
	var actualActive bool
	var name string
	if err := s.db.QueryRowContext(ctx, `SELECT active,tags->>'first_name' FROM partner_users WHERE user_id=$1`, user.UserID).Scan(&actualActive, &name); err != nil {
		t.Fatal(err)
	}
	if actualActive || name != "Original" {
		t.Fatal("failed activation partially changed the profile")
	}
}

func TestManagedPricesPreserveRecordedCharges(t *testing.T) {
	s, ctx := openTestStore(t)
	one, zero, ten := 1.0, 0.0, 10.0
	p := ManagedPrice{Model: "management-fixture", Provider: "mock", Input: &one, Output: &zero, CacheWrite: &zero, CacheRead: &zero}
	if err := s.SetManagedPrice(ctx, "test", p); err != nil {
		t.Fatal(err)
	}
	event := UsageEvent{EventID: "frozen-before", Timestamp: time.Now(), Username: "alice", Model: p.Model, PromptTokens: 1000000, TotalTokens: 1000000}
	if err := s.InsertEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	p.Input = &ten
	if err := s.SetManagedPrice(ctx, "test", p); err != nil {
		t.Fatal(err)
	}
	var charge float64
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT %s FROM usage_events e LEFT JOIN model_pricing p ON p.model=e.model WHERE e.event_id=$1`, costUSDExpr), event.EventID).Scan(&charge); err != nil {
		t.Fatal(err)
	}
	if charge != 1 {
		t.Fatalf("old charge changed to %v", charge)
	}
	event.EventID = "frozen-after"
	if err := s.InsertEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT cost_usd FROM usage_events WHERE event_id=$1`, event.EventID).Scan(&charge); err != nil {
		t.Fatal(err)
	}
	if charge != 10 {
		t.Fatalf("new charge=%v", charge)
	}
}
