package service

import (
	"time"

	"github.com/thekrauss/kubemanager/internal/infrastructure/helm/charts"
)

// Chart Helm des workloads enregistré avec chaque workload.
const (
	defaultChartName    = charts.StandardAppName
	defaultChartVersion = "0.1.0"
)

// Exposition publique : <nom>-<projet>.<ip>.<publicRouteDomain>.
const (
	// publicRouteDomain : service DNS joker qui résout <x>.<ip>.sslip.io vers <ip>.
	publicRouteDomain = "sslip.io"
	// publicRouteProjectIDLen : nombre de caractères de l'identifiant du projet
	// repris dans l'hôte public.
	publicRouteProjectIDLen = 6
	schemeHTTP              = "http"
	schemeHTTPS             = "https"
)

// Ports autorisés pour le conteneur.
const (
	maxTargetPort = 65535
	// privilegedPortLimit : ports système (< 1024) refusés, sauf httpPort.
	privilegedPortLimit = 1024
	httpPort            = 80
)

// Opérations Kubernetes / Helm directes.
const (
	// patchTypeMergePatch : constante locale pour éviter un import explicite
	// de k8s.io/apimachinery/pkg/types (qui n'est utilisé qu'ici).
	patchTypeMergePatch = "application/strategic-merge-patch+json"
	helmRollbackTimeout = 2 * time.Minute

	// compensationTimeout : écritures en base détachées de la requête.
	compensationTimeout = 10 * time.Second

	// Arrêt d'un déploiement avant une suppression. L'annulation arrive à
	// l'activité avec son prochain heartbeat (8 s au plus).
	deployStopWait = 30 * time.Second
	deployStopPoll = 500 * time.Millisecond
)

// Valeurs des métriques quand le Metrics Server ne répond pas.
const (
	zeroCPUUsage    = "0m"
	zeroMemoryUsage = "0Mi"
)

// Journaux des conteneurs (équivalent « kubectl logs »).
const (
	defaultLogTailLines = 500
	defaultLogLookback  = 24 * time.Hour
)
