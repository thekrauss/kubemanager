package sts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/MicahParks/keyfunc/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/thekrauss/kubemanager/internal/audit"
	"github.com/thekrauss/kubemanager/internal/iam"
)

// Identité des workloads (iam.md §7.2) : un pod présente le jeton Kubernetes
// projeté de son compte « system:serviceaccount:<namespace>:<workload> »
// (audience = émetteur du STS). Il est vérifié avec les clés que le watcher
// du cluster a rapportées ; le workload correspondant désigne le compte de
// service betorun. Aucune clé à gérer, aucune relation de confiance à créer.

const (
	// MethodWorkload : jeton obtenu par un pod d'un workload betorun.
	MethodWorkload    = "workload"
	workloadRefPrefix = "workload:"
	k8sSubjectPrefix  = "system:serviceaccount:"
)

// clusterKeys : clés d'un cluster, reconstruites quand le watcher en publie de nouvelles.
type clusterKeys struct {
	updatedAt time.Time
	jwks      *keyfunc.JWKS
}

type workloadKeys struct {
	mu    sync.Mutex
	cache map[string]clusterKeys
}

func (w *workloadKeys) keyfunc(clusterID string, updatedAt time.Time, raw []byte) (jwt.Keyfunc, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if c, ok := w.cache[clusterID]; ok && c.updatedAt.Equal(updatedAt) {
		return c.jwks.Keyfunc, nil
	}
	jwks, err := keyfunc.NewJSON(json.RawMessage(raw))
	if err != nil {
		return nil, err
	}
	if w.cache == nil {
		w.cache = map[string]clusterKeys{}
	}
	w.cache[clusterID] = clusterKeys{updatedAt: updatedAt, jwks: jwks}
	return jwks.Keyfunc, nil
}

// exchangeWorkload : handled=false si le jeton ne vient pas du cluster des
// workloads (l'échange continue alors avec les relations de confiance).
func (s *Service) exchangeWorkload(ctx context.Context, subjectToken, rawIssuer string) (tok *Token, p iam.Principal, handled bool, err error) {
	if s.WorkloadClusterID == "" {
		return nil, "", false, nil
	}
	var (
		storedIssuer string
		jwksRaw      []byte
		updatedAt    time.Time
	)
	err = s.DB.WithContext(ctx).Raw(`SELECT issuer, jwks, updated_at FROM cluster_oidc_keys WHERE cluster_id = ?`,
		s.WorkloadClusterID).Row().Scan(&storedIssuer, &jwksRaw, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && storedIssuer != rawIssuer) {
		return nil, "", false, nil
	}
	if err != nil {
		return nil, "", true, err
	}
	kf, err := s.workloadKeys.keyfunc(s.WorkloadClusterID, updatedAt, jwksRaw)
	if err != nil {
		return nil, "", true, err
	}
	var c jwt.RegisteredClaims
	_, err = jwt.ParseWithClaims(subjectToken, &c, kf, jwt.WithValidMethods([]string{"RS256", "ES256"}),
		jwt.WithIssuer(rawIssuer), jwt.WithAudience(s.Issuer), jwt.WithExpirationRequired(),
		jwt.WithLeeway(leeway), jwt.WithTimeFunc(s.now))
	if err != nil {
		if errors.Is(err, jwt.ErrTokenInvalidAudience) {
			return nil, "", true, invalidGrant(fmt.Sprintf("subject_token audience must be %q", s.Issuer))
		}
		return nil, "", true, invalidGrant("subject_token: " + describe(err))
	}
	ns, name, ok := strings.Cut(strings.TrimPrefix(c.Subject, k8sSubjectPrefix), ":")
	if !strings.HasPrefix(c.Subject, k8sSubjectPrefix) || !ok || ns == "" || name == "" {
		return nil, "", true, invalidGrant("subject_token is not a Kubernetes service account token")
	}

	var workloadID, saID uuid.UUID
	err = s.DB.WithContext(ctx).Raw(`SELECT w.id, w.service_account_id FROM workloads w
		JOIN service_accounts sa ON sa.id = w.service_account_id
		WHERE w.namespace = ? AND w.name = ? AND sa.deleted_at IS NULL AND NOT sa.disabled
		ORDER BY w.created_at DESC LIMIT 1`, ns, name).
		Row().Scan(&workloadID, &saID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", true, invalidGrant(fmt.Sprintf("no betorun workload runs as a service account for %q", c.Subject))
	}
	if err != nil {
		return nil, "", true, err
	}
	principal := iam.ServiceAccountPrincipal(saID)
	token, exp, err := s.Mint(principal, MethodWorkload, workloadRefPrefix+workloadID.String(), FederatedTokenTTL)
	if err != nil {
		return nil, principal, true, err
	}
	audit.Annotate(ctx, "workload", workloadID.String())
	audit.Annotate(ctx, "external_subject", c.Subject)
	audit.Annotate(ctx, "token_expires_at", exp)
	return &Token{AccessToken: token, TokenType: "Bearer", ExpiresIn: int(exp.Sub(s.now()).Seconds()),
		IssuedTokenType: TokenTypeAccess}, principal, true, nil
}
