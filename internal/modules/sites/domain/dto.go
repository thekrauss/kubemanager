package domain

// Toutes les routes sont imbriquées sous /projects/:id/sites (":id" = projet,
// imposé par les routes /projects/:id existantes) pour que
// RequireProjectPermission vérifie l'appartenance au projet.

type ProjectPath struct {
	ProjectID string `path:"id" binding:"required" description:"ID du projet"`
}

type SitePath struct {
	ProjectID string `path:"id" binding:"required" description:"ID du projet"`
	SiteID    string `path:"siteID" binding:"required" description:"ID du site"`
}

type CreateSiteRequest struct {
	ProjectID string `path:"id" binding:"required"`
	Name      string `json:"name" binding:"required" description:"Nom du site (minuscules, chiffres, tirets)"`
	// Subdomain : défaut = Name. Servi sur <subdomain>.<base_domain>.
	Subdomain   string `json:"subdomain" description:"Sous-domaine (défaut : le nom)"`
	SPAFallback bool   `json:"spa_fallback" description:"Renvoyer index.html pour les chemins inconnus (application monopage)"`
}

// UploadPath : handler brut (wrapRawHandler lit les mêmes étiquettes path: que tonic).
type UploadPath struct {
	ProjectID string `path:"id" binding:"required"`
	SiteID    string `path:"siteID" binding:"required"`
}

type UpdateSiteRequest struct {
	ProjectID   string `path:"id" binding:"required"`
	SiteID      string `path:"siteID" binding:"required"`
	SPAFallback *bool  `json:"spa_fallback,omitempty"`
}

type DeploymentPath struct {
	ProjectID    string `path:"id" binding:"required"`
	SiteID       string `path:"siteID" binding:"required"`
	DeploymentID string `path:"deploymentID" binding:"required"`
}

type CreateDomainRequest struct {
	ProjectID string `path:"id" binding:"required"`
	SiteID    string `path:"siteID" binding:"required"`
	Hostname  string `json:"hostname" binding:"required" description:"Domaine personnalisé (ex: www.example.com)"`
}

type DomainPath struct {
	ProjectID string `path:"id" binding:"required"`
	SiteID    string `path:"siteID" binding:"required"`
	DomainID  string `path:"domainID" binding:"required"`
}

// SiteResponse : site + URL publique.
type SiteResponse struct {
	Site
	URL string `json:"url"`
}

// DomainResponse : domaine + instructions DNS à afficher à l'utilisateur.
type DomainResponse struct {
	SiteDomain
	CNAMETarget string `json:"cname_target"`
	// Records : preuve de propriété (TXT), puis routage (CNAME).
	Records []DNSRecord `json:"records"`
	// Pointing : le domaine pointe vers la plateforme (connu après une vérification).
	Pointing bool `json:"pointing"`
}

// DNSRecord : enregistrement à créer chez le fournisseur DNS.
type DNSRecord struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ChallengePrefix : nom de l'enregistrement TXT de preuve de propriété.
const ChallengePrefix = "_betorun-challenge."
