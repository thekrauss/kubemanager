package router

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/loopfz/gadgeto/tonic"
	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/iam"
	idempotency "github.com/thekrauss/kubemanager/internal/infrastructure/idempotence"
	authSvc "github.com/thekrauss/kubemanager/internal/modules/auth/service"
	identityKC "github.com/thekrauss/kubemanager/internal/modules/identity/keycloak"
	identitySvc "github.com/thekrauss/kubemanager/internal/modules/identity/service"
)

// ErrorBody : réponse d'erreur. "error" duplique "message" pour les clients
// existants (kweb lit message || error).
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Error   string `json:"error"`
}

type sentinelMapping struct {
	err    error
	status int
	code   string
	// detailed : le message complet (contexte ajouté par %w) est sûr à
	// exposer et aide l'utilisateur ("unknown member: user:x@y").
	detailed bool
}

// sentinelErrors : erreurs sentinelles des modules → statut HTTP. Une erreur
// non typée et absente de cette table est une erreur interne (500, message
// générique) : jamais de détail technique renvoyé au client.
var sentinelErrors = []sentinelMapping{
	{gorm.ErrRecordNotFound, http.StatusNotFound, betoerrors.CodeNotFound, false},

	{identityKC.ErrInvalidCredentials, http.StatusUnauthorized, betoerrors.CodeUnauthorized, false},
	{identityKC.ErrUserAlreadyExists, http.StatusConflict, betoerrors.CodeConflict, false},
	{identityKC.ErrUserNotFound, http.StatusNotFound, betoerrors.CodeNotFound, false},
	{identitySvc.ErrRegisterInputInvalid, http.StatusBadRequest, betoerrors.CodeInvalidInput, false},
	{identitySvc.ErrBootstrapInputInvalid, http.StatusBadRequest, betoerrors.CodeInvalidInput, false},
	{identitySvc.ErrBootstrapProjectRequired, http.StatusBadRequest, betoerrors.CodeInvalidInput, false},
	{identitySvc.ErrUnknownProvider, http.StatusBadRequest, betoerrors.CodeInvalidInput, false},
	{identitySvc.ErrInvalidState, http.StatusBadRequest, betoerrors.CodeInvalidInput, false},
	{identitySvc.ErrEmailNotVerified, http.StatusForbidden, betoerrors.CodeForbidden, false},
	{identitySvc.ErrSubjectInvalid, http.StatusUnauthorized, betoerrors.CodeUnauthorized, false},
	{identitySvc.ErrIdentityServiceUnavailable, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", false},

	{authSvc.ErrUserAPIKeysRetired, http.StatusGone, "GONE", false},
	{idempotency.ErrPayloadMismatch, http.StatusUnprocessableEntity, "IDEMPOTENCY_MISMATCH", false},
	{idempotency.ErrInProgress, http.StatusConflict, betoerrors.CodeConflict, false},

	// IAM : messages rédigés pour l'utilisateur de l'API.
	{iam.ErrResourceNotFound, http.StatusNotFound, betoerrors.CodeNotFound, false},
	{iam.ErrEtagMismatch, http.StatusConflict, "ABORTED", false},
	{iam.ErrEtagRequired, http.StatusBadRequest, betoerrors.CodeInvalidInput, false},
	{iam.ErrLastOwner, http.StatusConflict, "FAILED_PRECONDITION", false},
	{iam.ErrEscalation, http.StatusForbidden, "PERMISSION_DENIED", true},
	{iam.ErrUnknownMember, http.StatusBadRequest, betoerrors.CodeInvalidInput, true},
	{iam.ErrInvalidPrincipal, http.StatusBadRequest, betoerrors.CodeInvalidInput, true},
	{iam.ErrInvalidBinding, http.StatusBadRequest, betoerrors.CodeInvalidInput, true},
	{iam.ErrUnsupportedCondition, http.StatusBadRequest, betoerrors.CodeInvalidInput, true},
	{iam.ErrInvalidResourceName, http.StatusBadRequest, betoerrors.CodeInvalidInput, false},
	{iam.ErrPolicyNotSupported, http.StatusBadRequest, betoerrors.CodeInvalidInput, false},
	{iam.ErrServiceAccountNotFound, http.StatusNotFound, betoerrors.CodeNotFound, false},
	{iam.ErrAPIKeyNotFound, http.StatusNotFound, betoerrors.CodeNotFound, false},
	{iam.ErrInvalidAccountID, http.StatusBadRequest, betoerrors.CodeInvalidInput, false},
	{iam.ErrAccountIDTaken, http.StatusConflict, betoerrors.CodeConflict, false},
	{iam.ErrInvalidKeyRestrictions, http.StatusBadRequest, betoerrors.CodeInvalidInput, true},
	{iam.ErrInvalidKeyExpiry, http.StatusBadRequest, betoerrors.CodeInvalidInput, false},
	{iam.ErrActAsRequired, http.StatusForbidden, "PERMISSION_DENIED", false},
}

// classifyError : statut, code et message sûr à exposer.
func classifyError(err error) (int, ErrorBody) {
	body := func(code, msg string) ErrorBody { return ErrorBody{Code: code, Message: msg, Error: msg} }

	var bindErr tonic.BindError
	var bindErrPtr *tonic.BindError
	if errors.As(err, &bindErr) || errors.As(err, &bindErrPtr) {
		return http.StatusBadRequest, body(betoerrors.CodeInvalidInput, err.Error())
	}
	var typed *betoerrors.Error
	if errors.As(err, &typed) {
		status, _ := betoerrors.ToHTTPError(typed)
		return status, body(typed.Code, typed.Message)
	}
	for _, m := range sentinelErrors {
		if errors.Is(err, m.err) {
			msg := m.err.Error()
			if m.detailed {
				msg = err.Error()
			}
			if m.err == gorm.ErrRecordNotFound {
				msg = "not found"
			}
			return m.status, body(m.code, msg)
		}
	}
	return http.StatusInternalServerError, body(betoerrors.CodeInternal, "internal server error")
}

// newErrorHook : hook tonic global. Les 5xx sont journalisés avec la cause
// réelle, qui n'est jamais renvoyée au client.
func newErrorHook(logger *zap.SugaredLogger) tonic.ErrorHook {
	return func(c *gin.Context, err error) (int, interface{}) {
		status, body := classifyError(err)
		if status >= http.StatusInternalServerError && logger != nil {
			logger.Errorw("request failed",
				"method", c.Request.Method, "route", c.FullPath(), "status", status, "error", err)
		}
		return status, body
	}
}
