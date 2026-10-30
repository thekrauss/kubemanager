package domain

import (
	"errors"
	"net"
	"regexp"
	"strings"
)

var (
	ErrInvalidName      = errors.New("name must be 3-63 chars: lowercase letters, digits and hyphens, not starting or ending with a hyphen")
	ErrReservedName     = errors.New("this subdomain is reserved")
	ErrInvalidHostname  = errors.New("invalid hostname")
	ErrPlatformHostname = errors.New("hostnames under the platform domain cannot be added as custom domains")
)

var labelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])$`)

// reservedSubdomains : sous-domaines de la plateforme qu'un site ne peut pas prendre.
var reservedSubdomains = map[string]bool{
	"www": true, "api": true, "app": true, "admin": true, "edge": true,
	"auth": true, "login": true, "console": true, "dashboard": true,
	"mail": true, "smtp": true, "status": true, "docs": true, "static": true,
	"cdn": true, "assets": true, "realtime": true, "billing": true,
}

// ValidateName : nom de site et sous-domaine = un label DNS (3–63 caractères).
func ValidateName(name string) error {
	if !labelRe.MatchString(name) || strings.Contains(name, "--") {
		return ErrInvalidName
	}
	return nil
}

// ValidateSubdomain : ValidateName + liste des sous-domaines réservés.
func ValidateSubdomain(sub string) error {
	if err := ValidateName(sub); err != nil {
		return err
	}
	if reservedSubdomains[sub] {
		return ErrReservedName
	}
	return nil
}

// NormalizeHostname valide un domaine personnalisé et le met en forme
// canonique (minuscules, sans point final). Refuse les IP, les noms sans
// point et tout nom sous le domaine de la plateforme.
func NormalizeHostname(raw, baseDomain string) (string, error) {
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if host == "" || len(host) > maxHostnameLen || net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		return "", ErrInvalidHostname
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > maxLabelLen || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", ErrInvalidHostname
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return "", ErrInvalidHostname
			}
		}
	}
	base := strings.TrimSuffix(strings.ToLower(baseDomain), ".")
	if base != "" && (host == base || strings.HasSuffix(host, "."+base)) {
		return "", ErrPlatformHostname
	}
	return host, nil
}
