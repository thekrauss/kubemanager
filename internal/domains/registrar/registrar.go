// Package registrar : achat de noms de domaine pour le compte des clients
// (revente). Rien ici n'est exposé tant que la facturation n'existe pas : en
// V1, chaque client apporte son domaine et prouve qu'il en est propriétaire
// (voir workloads/service/domains.go).
//
// Un domaine acheté par la plateforme n'aura pas besoin de cette preuve : la
// commande l'établit, et la plateforme écrit elle-même les enregistrements DNS
// grâce à SetRecords.
package registrar

import (
	"context"
	"errors"
)

// Erreurs communes aux fournisseurs.
var (
	ErrNotAvailable = errors.New("registrar: domain not available")
	ErrUnsupported  = errors.New("registrar: extension not sold by this registrar")
)

// Availability : réponse à une recherche de nom.
type Availability struct {
	Domain    string
	Available bool
	// Premium : tarif spécial, à afficher et à confirmer explicitement.
	Premium bool
	// PriceCents, Currency : prix de vente du fournisseur, TTC si connu. La
	// marge de la plateforme s'applique au-dessus, côté facturation.
	PriceCents int64
	Currency   string
}

// Contact : titulaire du domaine. Il doit être le client réel (règles ICANN et
// des registres) et non la plateforme : c'est lui qui possède le domaine.
type Contact struct {
	FirstName, LastName, Organization string
	Email, Phone                      string
	Address, City, PostalCode         string
	Country                           string // ISO 3166-1 alpha-2
}

// Order : demande d'achat.
type Order struct {
	Domain string
	Years  int
	Owner  Contact
}

// OrderResult : commande passée. L'activation du domaine est asynchrone.
type OrderResult struct {
	OrderID string
}

// Record : enregistrement DNS.
type Record struct {
	Type, Name, Value string
	TTL               int
}

// Registrar : fournisseur de noms de domaine. Toute commande engage de
// l'argent réel : l'appelant doit avoir encaissé le client avant Register, et
// un nouvel essai ne doit jamais repasser une commande sans vérifier la première.
type Registrar interface {
	Check(ctx context.Context, domain string) (*Availability, error)
	Register(ctx context.Context, order Order) (*OrderResult, error)
	// SetRecords : crée les enregistrements dans la zone du domaine.
	SetRecords(ctx context.Context, zone string, records []Record) error
}
