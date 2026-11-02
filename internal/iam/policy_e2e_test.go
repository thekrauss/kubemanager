package iam_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thekrauss/kubemanager/internal/iam"
)

func TestE2E_Policies(t *testing.T) {
	db := e2eDB(t)
	ctx := context.Background()
	engine := iam.NewEngine(&iam.GormStore{DB: db}, iam.DefaultCacheTTL)
	svc := &iam.PolicyService{DB: db, Engine: engine}

	awa := iam.UserPrincipal(uuid.MustParse("11111111-1111-1111-1111-111111111111"))
	moussa := iam.UserPrincipal(uuid.MustParse("22222222-2222-2222-2222-222222222222"))
	fatou := iam.UserPrincipal(uuid.MustParse("33333333-3333-3333-3333-333333333333"))
	project := iam.ProjectName(uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"))

	// ── Lecture : bindings groupés + infos d'affichage ──
	view, err := svc.Get(ctx, project)
	require.NoError(t, err)
	assert.NotEmpty(t, view.Policy.Etag)
	// Utilisateurs seulement : la migration 000007 ajoute aussi des comptes
	// de service legacy (clés km_… des données de test).
	roles := map[string][]iam.Principal{}
	for _, b := range view.Policy.Bindings {
		for _, m := range b.Members {
			if strings.HasPrefix(string(m), "user:") {
				roles[b.Role] = append(roles[b.Role], m)
			}
		}
	}
	assert.Equal(t, []iam.Principal{awa}, roles["roles/owner"])
	assert.Equal(t, []iam.Principal{moussa}, roles["roles/developer"])
	emails := map[iam.Principal]string{}
	for _, m := range view.Members {
		emails[m.Principal] = m.Email
	}
	assert.Equal(t, "awa@ex.sn", emails[awa], "la console affiche l'email, pas l'UUID")

	// ── Écriture par email, avec etag ──
	next := view.Policy
	next.Bindings = append(next.Bindings, iam.PolicyBinding{Role: "roles/viewer", Members: []iam.Principal{"user:FATOU@ex.sn"}})
	updated, err := svc.Set(ctx, awa, project, next)
	require.NoError(t, err)
	assert.NotEqual(t, view.Policy.Etag, updated.Policy.Etag)
	d, err := engine.Check(ctx, fatou, iam.ProjectsGet, project)
	require.NoError(t, err)
	assert.True(t, d.Allowed, "email résolu (insensible à la casse) et droit effectif tout de suite")

	// ── Etag périmé : le second admin ne peut pas écraser le premier ──
	_, err = svc.Set(ctx, awa, project, next) // next porte l'ancien etag
	assert.ErrorIs(t, err, iam.ErrEtagMismatch)
	_, err = svc.Set(ctx, awa, project, iam.Policy{Bindings: next.Bindings})
	assert.ErrorIs(t, err, iam.ErrEtagRequired)

	// ── Anti-verrouillage ──
	current, _ := svc.Get(ctx, project)
	noOwner := iam.Policy{Etag: current.Policy.Etag, Bindings: []iam.PolicyBinding{
		{Role: "roles/viewer", Members: []iam.Principal{awa}}}}
	_, err = svc.Set(ctx, awa, project, noOwner)
	assert.ErrorIs(t, err, iam.ErrLastOwner)

	// ── Membre inconnu ──
	unknown := current.Policy
	unknown.Bindings = append(append([]iam.PolicyBinding{}, unknown.Bindings...),
		iam.PolicyBinding{Role: "roles/viewer", Members: []iam.Principal{"user:nobody@ex.sn"}})
	_, err = svc.Set(ctx, awa, project, unknown)
	assert.ErrorIs(t, err, iam.ErrUnknownMember)
	assert.Contains(t, err.Error(), "nobody@ex.sn", "le message dit quel membre pose problème")

	// ── On ne donne pas ce qu'on n'a pas ──
	withAdmin := current.Policy
	withAdmin.Bindings = append(append([]iam.PolicyBinding{}, withAdmin.Bindings...),
		iam.PolicyBinding{Role: "roles/iam.securityAdmin", Members: []iam.Principal{fatou}})
	adminView, err := svc.Set(ctx, awa, project, withAdmin)
	require.NoError(t, err, "l'owner peut nommer un admin sécurité")

	escalate := adminView.Policy
	escalate.Bindings = append(append([]iam.PolicyBinding{}, escalate.Bindings...),
		iam.PolicyBinding{Role: "roles/owner", Members: []iam.Principal{fatou}})
	_, err = svc.Set(ctx, fatou, project, escalate)
	assert.ErrorIs(t, err, iam.ErrEscalation, "un admin sécurité ne peut pas se nommer owner")

	fine := adminView.Policy
	fine.Bindings = append(append([]iam.PolicyBinding{}, fine.Bindings...),
		iam.PolicyBinding{Role: "roles/logging.viewer", Members: []iam.Principal{moussa}})
	_, err = svc.Set(ctx, fatou, project, fine)
	assert.NoError(t, err, "il peut accorder un rôle dont il détient les permissions")

	// ── Cas limites ──
	orphan, err := svc.Get(ctx, iam.ProjectName(uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")))
	require.NoError(t, err)
	assert.Equal(t, "ACAB", orphan.Policy.Etag, "ressource sans policy : etag conventionnel")
	_, err = svc.Get(ctx, iam.Platform)
	assert.ErrorIs(t, err, iam.ErrPolicyNotSupported)
	_, err = svc.Get(ctx, iam.ProjectName(uuid.New()))
	assert.ErrorIs(t, err, iam.ErrResourceNotFound)
}
