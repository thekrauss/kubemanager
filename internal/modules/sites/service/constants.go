package service

import (
	"time"

	"github.com/thekrauss/kubemanager/internal/core/pgerr"
)

const pgUniqueViolation = pgerr.UniqueViolation

const (
	// deploymentsListLimit : nombre de déploiements renvoyés par la liste.
	deploymentsListLimit = 50
	// domainLookupTimeout : délai de la résolution DNS d'un domaine à vérifier.
	domainLookupTimeout = 5 * time.Second
)

// Hôtes de développement local servis en http (*.localhost).
const (
	localhostHost   = "localhost"
	localhostSuffix = ".localhost"
	schemeHTTP      = "http://"
	schemeHTTPS     = "https://"
)
