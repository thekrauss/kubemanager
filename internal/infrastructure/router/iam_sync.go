package router

import (
	"context"
	"strings"
	"time"

	"github.com/thekrauss/kubemanager/internal/audit"

	"github.com/google/uuid"

	"github.com/thekrauss/kubemanager/internal/iam"
)

// iamInvalidateChannel : changements de bindings diffusés entre process
// (réplicas de l'API, worker Temporal qui crée les projets…).
const iamInvalidateChannel = "iam:invalidate"

// startIAMSync : un changement de bindings invalide le cache IAM local et
// celui des autres process. Un retrait de droit prend ainsi effet partout
// sans attendre l'expiration du cache (iam.DefaultCacheTTL reste le filet).
func (a *App) startIAMSync(ctx context.Context) {
	origin := uuid.NewString()

	iam.OnBindingsChanged(func(p iam.Principal) {
		a.IAM.Invalidate(p)
		if err := a.Redis.Publish(ctx, iamInvalidateChannel, origin+"|"+string(p)).Err(); err != nil {
			a.Logger.Warnw("iam invalidation publish failed", "principal", p, "error", err)
		}
	})

	sub := a.Redis.Subscribe(ctx, iamInvalidateChannel)
	go func() {
		defer sub.Close()
		for msg := range sub.Channel() {
			from, principal, ok := strings.Cut(msg.Payload, "|")
			if !ok || from == origin {
				continue
			}
			a.IAM.Invalidate(iam.Principal(principal))
		}
	}()
}

// platformReconcileEvery : délai maximal pour qu'un changement de la liste du
// staff (configuration) prenne effet.
const platformReconcileEvery = 10 * time.Minute

// startPlatformRolesReconciler : aligne les rôles staff sur la configuration,
// au démarrage puis périodiquement (un membre du staff qui ouvre sa première
// session reçoit son rôle au passage suivant).
func (a *App) startPlatformRolesReconciler(ctx context.Context) {
	desired := []iam.PlatformRoleAssignment{
		{Role: "roles/platform.admin", Emails: a.Config.IAM.PlatformAdmins},
		{Role: "roles/platform.support", Emails: a.Config.IAM.PlatformSupport},
	}
	run := func() {
		rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		res, err := iam.ReconcilePlatformRoles(rctx, a.DB, desired, "system:config")
		if err != nil {
			a.Logger.Errorw("platform roles reconciliation failed", "error", err)
			return
		}
		// Chaque changement de rôle staff laisse une trace dans le journal d'audit.
		w := &audit.GormWriter{DB: a.DB}
		for kind, list := range map[string][]string{"binding_added": res.Added, "binding_removed": res.Removed} {
			for _, b := range list {
				a.Logger.Infow("platform role reconciled", kind, b)
				if err := w.Write(rctx, &audit.Entry{Principal: "system:config", Action: "iam.platformRoles.reconcile",
					Resource: string(iam.Platform), Decision: audit.DecisionAllow,
					Metadata: map[string]any{kind: b}}); err != nil {
					a.Logger.Errorw("audit write failed", "error", err)
				}
			}
		}
		if len(res.UnknownEmails) > 0 {
			a.Logger.Warnw("platform staff without account yet (first login pending)", "emails", res.UnknownEmails)
		}
	}
	run()
	go func() {
		t := time.NewTicker(platformReconcileEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				run()
			}
		}
	}()
}
