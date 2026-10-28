package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

// LoginProvision is an operator-owned profile contract for non-email identities.
// Partner/SSO profile validation remains unchanged.
type LoginProvision struct {
	Subject string `json:"subject"`
	Name    string `json:"name"`
	Role    string `json:"role,omitempty"`
	Active  *bool  `json:"active,omitempty"`
}

var loginSubjectPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]{0,127}$`)

// ProvisionLogin preserves an existing login's UUID and all usage records.
// It does not create keys or change budgets. Those are separate API operations.
func (s *Store) ProvisionLogin(ctx context.Context, actor string, p LoginProvision) (PartnerUser, error) {
	if !loginSubjectPattern.MatchString(p.Subject) || len(p.Name) > 256 {
		return PartnerUser{}, fmt.Errorf("invalid subject or name")
	}
	if p.Role != "" && p.Role != PartnerRoleUser && p.Role != PartnerRoleAdmin && p.Role != PartnerRoleSuperAdmin {
		return PartnerUser{}, fmt.Errorf("invalid role")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PartnerUser{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('metering-login'),hashtext($1))`, p.Subject); err != nil {
		return PartnerUser{}, err
	}
	var id string
	err = tx.QueryRowContext(ctx, `SELECT user_id FROM partner_user_logins WHERE username=$1`, p.Subject).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		var b [16]byte
		if _, err = rand.Read(b[:]); err != nil {
			return PartnerUser{}, err
		}
		b[6] = (b[6] & 15) | 64
		b[8] = (b[8] & 63) | 128
		id = fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
		name := p.Name
		if name == "" {
			name = p.Subject
		}
		tags, _ := json.Marshal(map[string]string{"first_name": name})
		if _, err = tx.ExecContext(ctx, `INSERT INTO partner_users(user_id,tags,created_by,updated_by) VALUES($1,$2,$3,$3)`, id, string(tags), actor); err != nil {
			return PartnerUser{}, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO partner_user_logins(username,user_id,is_current) VALUES($1,$2,true)`, p.Subject, id); err != nil {
			return PartnerUser{}, err
		}
	} else if err != nil {
		return PartnerUser{}, err
	}
	if p.Name != "" {
		if _, err = tx.ExecContext(ctx, `UPDATE partner_users SET tags=tags||jsonb_build_object('first_name',$2::text),updated_by=$3,updated_at=now() WHERE user_id=$1`, id, p.Name, actor); err != nil {
			return PartnerUser{}, err
		}
	}
	if p.Role != "" {
		if _, err = tx.ExecContext(ctx, `UPDATE partner_users SET role=$2,updated_by=$3,updated_at=now() WHERE user_id=$1`, id, p.Role, actor); err != nil {
			return PartnerUser{}, err
		}
	}
	if p.Active != nil {
		result, updateErr := tx.ExecContext(ctx, `UPDATE partner_users SET active=$2,updated_by=$3,updated_at=now() WHERE user_id=$1 AND NOT key_revocation_pending`, id, *p.Active, actor)
		if updateErr != nil {
			return PartnerUser{}, updateErr
		}
		changed, updateErr := result.RowsAffected()
		if updateErr != nil {
			return PartnerUser{}, updateErr
		}
		if changed != 1 {
			return PartnerUser{}, fmt.Errorf("profile activation cannot change while key revocation is pending")
		}
	}
	detail, _ := json.Marshal(p)
	if _, err = tx.ExecContext(ctx, `INSERT INTO org_audit(actor,action,target,detail) VALUES($1,'login.provision',$2,$3)`, actor, p.Subject, string(detail)); err != nil {
		return PartnerUser{}, err
	}
	if err = tx.Commit(); err != nil {
		return PartnerUser{}, err
	}
	s.invalidateQuotaCache()
	return s.GetPartnerUser(ctx, id)
}
