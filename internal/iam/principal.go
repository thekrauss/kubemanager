package iam

import (
	"errors"
	"strings"

	"github.com/google/uuid"
)

// Principal : "user:{uuid}", "serviceAccount:{uuid}", "group:{uuid}",
// "allAuthenticatedUsers" ou "allUsers".
type Principal string

const (
	AllAuthenticatedUsers Principal = "allAuthenticatedUsers"
	AllUsers              Principal = "allUsers"
)

const (
	KindUser           = "user"
	KindServiceAccount = "serviceAccount"
	KindGroup          = "group"
)

var ErrInvalidPrincipal = errors.New("invalid principal")

func (p Principal) String() string { return string(p) }

func UserPrincipal(id uuid.UUID) Principal {
	return Principal(KindUser + ":" + id.String())
}

func ServiceAccountPrincipal(id uuid.UUID) Principal {
	return Principal(KindServiceAccount + ":" + id.String())
}

func GroupPrincipal(id uuid.UUID) Principal {
	return Principal(KindGroup + ":" + id.String())
}

// Parse : type et UUID ; les principals spéciaux renvoient uuid.Nil.
func (p Principal) Parse() (kind string, id uuid.UUID, err error) {
	if p == AllAuthenticatedUsers || p == AllUsers {
		return string(p), uuid.Nil, nil
	}
	kind, raw, ok := strings.Cut(string(p), ":")
	if !ok {
		return "", uuid.Nil, ErrInvalidPrincipal
	}
	switch kind {
	case KindUser, KindServiceAccount, KindGroup:
	default:
		return "", uuid.Nil, ErrInvalidPrincipal
	}
	id, err = uuid.Parse(raw)
	if err != nil {
		return "", uuid.Nil, ErrInvalidPrincipal
	}
	return kind, id, nil
}

// Validate : principal bien formé.
func (p Principal) Validate() error {
	_, _, err := p.Parse()
	return err
}
