package storage

import (
	"testing"
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
