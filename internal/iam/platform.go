package iam

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// PlatformRoleAssignment : rôles staff déclarés en configuration (iam.md §10).
// La configuration est la source de vérité : un email retiré perd son rôle.
type PlatformRoleAssignment struct {
	Role   string   // roles/platform.admin | roles/platform.support
	Emails []string // comptes betorun du staff
}

// PlatformReconcileResult : ce que la réconciliation a changé.
type PlatformReconcileResult struct {
	Added   []string
	Removed []string
	// UnknownEmails : déclarés mais sans compte (ils n'ont jamais ouvert de
	// session) ; ils recevront leur rôle à la prochaine réconciliation.
	UnknownEmails []string
}

// ReconcilePlatformRoles aligne les bindings sur "platform" avec la
// configuration. Idempotent : peut tourner sur plusieurs réplicas.
func ReconcilePlatformRoles(ctx context.Context, db *gorm.DB, desired []PlatformRoleAssignment, actor string) (*PlatformReconcileResult, error) {
	res := &PlatformReconcileResult{}
	for _, a := range desired {
		role, ok := LookupRole(a.Role)
		if !ok || !role.PlatformOnly {
			return nil, ErrInvalidBinding
		}
		want := map[Principal]bool{}
		for _, raw := range a.Emails {
			email := strings.ToLower(strings.TrimSpace(raw))
			if email == "" {
				continue
			}
			var id uuid.UUID
			err := db.WithContext(ctx).Raw(`SELECT id FROM users WHERE lower(email) = ? AND deleted_at IS NULL`, email).Row().Scan(&id)
			if errors.Is(err, sql.ErrNoRows) {
				res.UnknownEmails = append(res.UnknownEmails, email)
				continue
			}
			if err != nil {
				return nil, err
			}
			want[UserPrincipal(id)] = true
		}

		var current []string
		if err := db.WithContext(ctx).Model(&bindingRow{}).
			Where("resource_name = ? AND role = ?", string(Platform), a.Role).
			Pluck("principal", &current).Error; err != nil {
			return nil, err
		}
		have := map[Principal]bool{}
		for _, p := range current {
			have[Principal(p)] = true
		}

		for p := range want {
			if have[p] {
				continue
			}
			if err := AddBinding(ctx, db, Binding{Resource: Platform, Role: a.Role, Principal: p}, actor); err != nil {
				return nil, err
			}
			res.Added = append(res.Added, a.Role+" "+string(p))
		}
		for p := range have {
			if want[p] {
				continue
			}
			if err := db.WithContext(ctx).
				Where("resource_name = ? AND role = ? AND principal = ?", string(Platform), a.Role, string(p)).
				Delete(&bindingRow{}).Error; err != nil {
				return nil, err
			}
			if err := touchPolicy(ctx, db, Platform, actor); err != nil {
				return nil, err
			}
			notifyChanged(p)
			res.Removed = append(res.Removed, a.Role+" "+string(p))
		}
	}
	sort.Strings(res.Added)
	sort.Strings(res.Removed)
	sort.Strings(res.UnknownEmails)
	return res, nil
}
