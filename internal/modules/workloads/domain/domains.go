package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// MaxDomainsPerWorkload : domaines d'un conteneur.
const MaxDomainsPerWorkload = 10

// ChallengePrefix : nom de l'enregistrement TXT de preuve de propriété.
const ChallengePrefix = "_betorun-challenge."

// Types d'enregistrement DNS demandés pour pointer vers la plateforme.
const (
	RecordCNAME = "CNAME"
	RecordA     = "A"
	RecordTXT   = "TXT"
)

// WorkloadDomain : domaine personnalisé d'un conteneur.
type WorkloadDomain struct {
	ID         uuid.UUID  `gorm:"type:uuid;primary_key" json:"id"`
	WorkloadID uuid.UUID  `gorm:"type:uuid;not null" json:"workload_id"`
	Hostname   string     `gorm:"not null" json:"hostname"`
	Token      string     `gorm:"not null" json:"-"`
	Verified   bool       `gorm:"not null" json:"verified"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	CheckedAt  *time.Time `json:"checked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

func (WorkloadDomain) TableName() string { return "workload_domains" }

// DNSRecord : enregistrement à créer chez le fournisseur DNS.
type DNSRecord struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

// DomainResponse : domaine et instructions.
type DomainResponse struct {
	WorkloadDomain
	// Records : preuve de propriété (TXT), puis routage (CNAME ou A).
	Records []DNSRecord `json:"records"`
	// Pointing : le domaine pointe déjà vers la plateforme (nécessaire au
	// certificat HTTPS). Connu après une vérification.
	Pointing bool `json:"pointing"`
}

// AddDomainRequest : nouveau domaine.
type AddDomainRequest struct {
	Hostname string `json:"hostname" binding:"required" description:"Domaine (ex: www.exemple.sn)"`
}

var (
	ErrDomainNotFound  = errors.New("domain not found")
	ErrDomainExists    = errors.New("this domain is already attached to this container")
	ErrDomainClaimed   = errors.New("this domain is already in use on betorun")
	ErrDomainLimit     = errors.New("too many domains for this container (10 maximum)")
	ErrDomainsDisabled = errors.New("custom domains are not available: the platform has no public address")
)
