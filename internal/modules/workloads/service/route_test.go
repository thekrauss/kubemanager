package service

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/thekrauss/kubemanager/internal/core/configs"
)

func TestPublicRoute(t *testing.T) {
	saved := configs.AppConfig
	defer func() { configs.AppConfig = saved }()
	project := uuid.MustParse("37f62891-bdce-40b1-be27-833c1fea931c")

	// Sans IP du point d'entrée : pas d'URL (avant : hôte invalide « web-37f628..sslip.io »).
	configs.AppConfig.Vps.AdressIp = ""
	assert.Equal(t, route{}, publicRoute("web", project, false))

	configs.AppConfig.Vps.AdressIp = " 5.196.223.196 "
	configs.AppConfig.Kubernetes.IngressClass = "traefik"
	configs.AppConfig.Kubernetes.IngressNamespace = "kube-system"
	r := publicRoute("web", project, false)
	assert.Equal(t, "web-37f628.5.196.223.196.sslip.io", r.Host)
	assert.Equal(t, "http://web-37f628.5.196.223.196.sslip.io", r.URL, "URL enregistrée avec le workload")
	assert.Equal(t, "traefik", r.IngressClass)
	assert.Equal(t, "kube-system", r.IngressNamespace)
	assert.Equal(t, "https://web-37f628.5.196.223.196.sslip.io", publicRoute("web", project, true).URL)
}
