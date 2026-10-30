package middleware

import (
	"net/http"
	"strings"

	"github.com/gofrs/uuid"
	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/core/httpheader"
)

// TenantResolver derives the current tenant/project ID from HTTP attributes.
type TenantResolver struct {
	baseDomain  string
	allowCustom bool
}

// NewTenantResolver creates a resolver with the provided global config.
func NewTenantResolver(cfg *configs.GlobalConfig) *TenantResolver {
	if cfg == nil {
		return &TenantResolver{}
	}
	return &TenantResolver{
		baseDomain:  strings.ToLower(strings.TrimSpace(cfg.Multitenancy.BaseDomain)),
		allowCustom: cfg.Multitenancy.AllowCustomDom,
	}
}

// Resolve attempts to read a tenant ID from headers, host, or custom domains.
func (t *TenantResolver) Resolve(r *http.Request) (string, bool) {
	if r == nil {
		return "", false
	}
	if tid := t.fromHeader(r); tid != "" {
		return tid, true
	}
	if tid := t.fromHost(r); tid != "" {
		return tid, true
	}
	return "", false
}

func (t *TenantResolver) fromHeader(r *http.Request) string {
	if r == nil {
		return ""
	}
	for _, h := range []string{httpheader.XTenantID, httpheader.XProjectID} {
		if val := strings.TrimSpace(r.Header.Get(h)); val != "" && isUUID(val) {
			return val
		}
	}
	return ""
}

func (t *TenantResolver) fromHost(r *http.Request) string {
	if r == nil || t.baseDomain == "" {
		return ""
	}
	host := strings.ToLower(strings.TrimSpace(r.Host))
	if host == "" {
		return ""
	}
	if idx := strings.Index(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	host = strings.Trim(host, ".")
	if host == "" {
		return ""
	}
	if t.allowCustom && isUUID(host) {
		return host
	}
	if t.baseDomain == "" {
		return ""
	}
	suffix := "." + t.baseDomain
	if strings.HasSuffix(host, suffix) {
		candidate := strings.TrimSuffix(host, suffix)
		candidate = strings.Trim(candidate, ".")
		if candidate != "" && isUUID(candidate) {
			return candidate
		}
	}
	return ""
}

func isUUID(value string) bool {
	if value == "" {
		return false
	}
	_, err := uuid.FromString(value)
	return err == nil
}
