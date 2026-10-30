package activities

import "time"

const (
	// k8sProcessingTTL : fenêtre d'idempotence des activities Kubernetes.
	k8sProcessingTTL = 2 * time.Minute
	// projectQuotaName : ResourceQuota de l'espace de noms d'un projet.
	projectQuotaName = "project-quota"
)
