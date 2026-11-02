package realtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thekrauss/kubemanager/internal/iam"

	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/infrastructure/realtime"
	"github.com/thekrauss/kubemanager/internal/middleware"
)

// TTLs Centrifugo (pattern Centrifugo : tokens courts + refresh côté SDK).
const (
	connectionTokenTTL   = 1 * time.Hour
	subscriptionTokenTTL = 10 * time.Minute
)

// Schémas des URL de Centrifugo (http → ws, https → wss).
const (
	schemeHTTP  = "http://"
	schemeHTTPS = "https://"
	schemeWS    = "ws://"
	schemeWSS   = "wss://"
)

// IRealtimeController : contrat HTTP.
type IRealtimeController interface {
	GetConnectionToken(c *gin.Context) (*ConnectionTokenResponse, error)
	GetSubscriptionToken(c *gin.Context, in *SubscribeRequest) (*SubscriptionTokenResponse, error)
}

// Controller — thin, pas de Service dédié (logique 100% dans le handler).
// Authorizer : point de décision IAM (implémenté par *iam.Engine).
type Authorizer interface {
	Check(ctx context.Context, p iam.Principal, perm iam.Permission, r iam.ResourceName) (iam.Decision, error)
}

type Controller struct {
	signer realtime.TokenSigner
	authz  Authorizer
	wsURL  string // URL WS publique (ex: ws://localhost:8000/connection/websocket)
}

func NewController(cfg configs.CentrifugoConfig, signer realtime.TokenSigner, authz Authorizer) *Controller {
	return &Controller{
		signer: signer,
		authz:  authz,
		wsURL:  buildWSURL(cfg.URL),
	}
}

// Compile-time check.
var _ IRealtimeController = (*Controller)(nil)

//  DTOs

type ConnectionTokenResponse struct {
	Token     string    `json:"token"`
	WSURL     string    `json:"ws_url"`
	Subject   string    `json:"subject"`
	ExpiresAt time.Time `json:"expires_at"`
}

type SubscribeRequest struct {
	Channel string `query:"channel" binding:"required" description:"Canal d'événements (ex: project:xxx:workload:yyy:deploy)"`
}

type SubscriptionTokenResponse struct {
	Token     string    `json:"token"`
	Channel   string    `json:"channel"`
	ExpiresAt time.Time `json:"expires_at"`
}

//  Handlers

// GetConnectionToken : JWT minimal (sub + exp) pour ouvrir la connexion WS.
// N'autorise à AUCUN canal — il faut un subscription token par canal (plus safe).
func (ctrl *Controller) GetConnectionToken(c *gin.Context) (*ConnectionTokenResponse, error) {
	userID, err := requireUserID(c)
	if err != nil {
		return nil, err
	}

	exp := time.Now().Add(connectionTokenTTL)
	token, err := ctrl.signer.GenerateConnectionToken(c.Request.Context(), userID, exp, map[string]any{
		"email": c.GetString(middleware.ContextKeyEmail),
	})
	if err != nil {
		return nil, fmt.Errorf("sign connection token: %w", err)
	}

	return &ConnectionTokenResponse{
		Token:     token,
		WSURL:     ctrl.wsURL,
		Subject:   userID,
		ExpiresAt: exp,
	}, nil
}

// GetSubscriptionToken : JWT scopé à un canal précis. Authz via membership projet.
//
// Extrait le projectID du canal (format `project:<projectID>:...`) et vérifie
// que le user est bien membre du projet. Toute autre forme de canal → 400.
func (ctrl *Controller) GetSubscriptionToken(c *gin.Context, in *SubscribeRequest) (*SubscriptionTokenResponse, error) {
	userID, err := requireUserID(c)
	if err != nil {
		return nil, err
	}
	channel := strings.TrimSpace(in.Channel)
	if channel == "" {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "channel is required")
	}

	projectID, err := parseProjectIDFromChannel(channel)
	if err != nil {
		return nil, err
	}

	// Authz : le user doit être membre du projet.
	if err := ctrl.requireProjectMembership(c.Request.Context(), userID, projectID); err != nil {
		return nil, err
	}

	exp := time.Now().Add(subscriptionTokenTTL)
	token, err := ctrl.signer.GenerateSubscriptionToken(c.Request.Context(), userID, channel, exp)
	if err != nil {
		return nil, fmt.Errorf("sign subscription token: %w", err)
	}
	return &SubscriptionTokenResponse{
		Token:     token,
		Channel:   channel,
		ExpiresAt: exp,
	}, nil
}

//  Helpers ─

func requireUserID(c *gin.Context) (string, error) {
	uid := strings.TrimSpace(c.GetString(middleware.ContextKeyUserID))
	if uid == "" {
		return "", betoerrors.New(betoerrors.CodeUnauthorized, "authenticated user context missing")
	}
	return uid, nil
}

// parseProjectIDFromChannel : attend "project:<projectID>:...".
// Accepte tous les sous-canaux (events, workload:X, workload:X:deploy).
func parseProjectIDFromChannel(channel string) (uuid.UUID, error) {
	parts := strings.SplitN(channel, realtime.ChannelSeparator, 3)
	if len(parts) < 2 || parts[0] != realtime.ChannelProjectNamespace {
		return uuid.Nil, betoerrors.New(betoerrors.CodeInvalidInput, "channel must start with 'project:<projectID>'")
	}
	pid, err := uuid.Parse(strings.TrimSpace(parts[1]))
	if err != nil {
		return uuid.Nil, betoerrors.New(betoerrors.CodeInvalidInput, "invalid projectID in channel")
	}
	return pid, nil
}

// requireProjectMembership : s'abonner aux événements d'un projet exige
// resourcemanager.projects.get dessus. Réponse uniforme "not a member" : ne
// révèle pas si le projet existe.
func (ctrl *Controller) requireProjectMembership(ctx context.Context, userIDStr string, projectID uuid.UUID) error {
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return betoerrors.New(betoerrors.CodeUnauthorized, "invalid user id")
	}
	decision, err := ctrl.authz.Check(ctx, iam.UserPrincipal(userID), iam.ProjectsGet, iam.ProjectName(projectID))
	if errors.Is(err, iam.ErrResourceNotFound) || (err == nil && !decision.Allowed) {
		return betoerrors.New(betoerrors.CodeForbidden, "not a member of this project")
	}
	if err != nil {
		return fmt.Errorf("authorization check: %w", err)
	}
	return nil
}

// buildWSURL : transforme l'URL HTTP configurée en URL WebSocket.
// Ex: http://centrifugo:8000 → ws://centrifugo:8000/connection/websocket
//
//	https://rt.example.com → wss://rt.example.com/connection/websocket
//
// Le frontend qui consomme cet endpoint tourne dans le browser, donc on ne peut
// pas lui donner "ws://centrifugo:8000" (DNS docker interne). L'API devrait
// idéalement être configurée avec l'URL publique — ex: wss://realtime.example.com.
// Pour dev on accepte le http://localhost:8000.
func buildWSURL(httpURL string) string {
	httpURL = strings.TrimRight(strings.TrimSpace(httpURL), "/")
	if httpURL == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(httpURL, schemeHTTPS):
		return schemeWSS + strings.TrimPrefix(httpURL, schemeHTTPS) + realtime.WebSocketPath
	case strings.HasPrefix(httpURL, schemeHTTP):
		return schemeWS + strings.TrimPrefix(httpURL, schemeHTTP) + realtime.WebSocketPath
	default:
		return httpURL + realtime.WebSocketPath
	}
}
