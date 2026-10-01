package storage

import (
	"fmt"
	"testing"
)

// Legacy username-keyed rows (written by the pre-UUID API) must still be
// read and enforced across linked logins for users not in the partner
// directory. The write path for these rows was removed with the UUID API.
func TestUserModelAllowlistLegacyRowsStillEnforced(t *testing.T) {
	s, ctx := openTestStore(t)
	seedQuotaRoster(t, s, ctx)
	quotaExec(t, s, ctx, `INSERT INTO person_identities (username, person_slug) VALUES ('alice_alt', 'alice')`)
	quotaExec(t, s, ctx, `INSERT INTO user_model_allowlists (username, models, updated_by) VALUES ('alice', '{model-a,model-b}', 'legacy')`)

	if decision, err := s.GetMonthlyUsage(ctx, "alice_alt", "model-a", false); err != nil || !decision.ModelAllowed || !decision.HasAccess {
		t.Fatalf("allowed alias model decision = %#v, err %v", decision, err)
	}
	if decision, err := s.GetMonthlyUsage(ctx, "alice_alt", "model-c", false); err != nil || decision.ModelAllowed || decision.HasAccess {
		t.Fatalf("disallowed alias model decision = %#v, err %v", decision, err)
	}
	quotaExec(t, s, ctx, `DELETE FROM user_model_allowlists WHERE username = 'alice'`)
	s.invalidateQuotaCache()
	if decision, err := s.GetMonthlyUsage(ctx, "alice", "model-c", false); err != nil || !decision.ModelAllowed || !decision.HasAccess {
		t.Fatalf("baseline access after clearing policy = %#v, err %v", decision, err)
	}
}

func TestNormalizeUserModelAllowlistRejectsWildcardsAndBoundsList(t *testing.T) {
	if _, err := normalizeUserModelAllowlist([]string{"model-*"}); err == nil {
		t.Fatal("expected wildcard model ID to be rejected")
	}
	tooMany := make([]string, userModelAllowlistMax+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("model-%d", i)
	}
	if _, err := normalizeUserModelAllowlist(tooMany); err == nil {
		t.Fatal("expected oversized allowlist to be rejected")
	}
}
