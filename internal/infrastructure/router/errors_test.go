package router

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"
	"gorm.io/gorm"

	authSvc "github.com/thekrauss/kubemanager/internal/modules/auth/service"
	identityKC "github.com/thekrauss/kubemanager/internal/modules/identity/keycloak"
)

func TestClassifyError(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
		msg    string
	}{
		{"typed not found", betoerrors.New(betoerrors.CodeNotFound, "site introuvable"), http.StatusNotFound, betoerrors.CodeNotFound, "site introuvable"},
		{"typed conflict wrapped", fmt.Errorf("ctx: %w", betoerrors.New(betoerrors.CodeConflict, "déjà pris")), http.StatusConflict, betoerrors.CodeConflict, "déjà pris"},
		{"typed with cause hides cause", &betoerrors.Error{Code: betoerrors.CodeNotFound, Message: "workload not found", Cause: errors.New("sql: secret detail")}, http.StatusNotFound, betoerrors.CodeNotFound, "workload not found"},
		{"gorm not found", fmt.Errorf("get: %w", gorm.ErrRecordNotFound), http.StatusNotFound, betoerrors.CodeNotFound, "not found"},
		{"sentinel credentials", identityKC.ErrInvalidCredentials, http.StatusUnauthorized, betoerrors.CodeUnauthorized, "invalid credentials"},
		{"unknown → 500 generic", errors.New("dial tcp 10.0.0.5:5432: connection refused"), http.StatusInternalServerError, betoerrors.CodeInternal, "internal server error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := classifyError(tc.err)
			assert.Equal(t, tc.status, status)
			assert.Equal(t, tc.code, body.Code)
			assert.Equal(t, tc.msg, body.Message)
			assert.Equal(t, body.Message, body.Error, "error duplique message pour kweb")
		})
	}
}

func TestClassifyError_UserAPIKeysRetired(t *testing.T) {
	status, body := classifyError(authSvc.ErrUserAPIKeysRetired)
	assert.Equal(t, http.StatusGone, status)
	assert.Contains(t, body.Message, "serviceAccounts", "le message indique la nouvelle API")
}
