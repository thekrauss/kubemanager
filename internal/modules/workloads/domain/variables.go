package domain

import (
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// Limites d'un conteneur (un Secret Kubernetes ne dépasse pas 1 Mio).
const (
	MaxVariables     = 200
	MaxVariableBytes = 64 << 10
	maxTotalBytes    = 768 << 10
)

// Variable : ligne chiffrée (voir repository.VariableStore).
type Variable struct {
	WorkloadID uuid.UUID `gorm:"type:uuid;primaryKey"`
	Name       string    `gorm:"primaryKey"`
	Sealed     []byte    `gorm:"not null"`
	Secret     bool      `gorm:"not null"`
	UpdatedAt  time.Time
}

func (Variable) TableName() string { return "workload_variables" }

// VariableView : ce que l'API montre. Value est vide pour un secret : une
// valeur secrète ne se relit jamais, elle se remplace.
type VariableView struct {
	Name      string    `json:"name"`
	Secret    bool      `json:"secret"`
	Value     string    `json:"value,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// VariablesResponse : variables d'un conteneur.
type VariablesResponse struct {
	Variables []VariableView `json:"variables"`
	// Changed : une modification a été appliquée et un redéploiement lancé.
	Changed bool `json:"changed"`
}

// PatchVariablesRequest : modifications, appliquées en une seule fois (un seul
// redéploiement).
type PatchVariablesRequest struct {
	Env     map[string]string `json:"env" description:"Variables à créer ou modifier"`
	Secrets map[string]string `json:"secrets" description:"Secrets à créer ou modifier (jamais relisibles ensuite)"`
	Unset   []string          `json:"unset" description:"Noms à supprimer"`
}

// VariableChange : modification à appliquer.
type VariableChange struct {
	Env     map[string]string
	Secrets map[string]string
	Unset   []string
	// ReplaceEnv : les variables non secrètes absentes de Env sont supprimées
	// (env_vars d'une mise à jour). Les secrets restent.
	ReplaceEnv bool
}

var (
	ErrVariableName     = errors.New("invalid variable name: letters, digits and underscores, not starting with a digit")
	ErrVariableConflict = errors.New("the same name cannot be set and removed, or be both a variable and a secret, in one request")
	ErrVariableTooBig   = errors.New("variable value too large (64 KiB maximum)")
	ErrTooManyVariable  = errors.New("too many variables for this container (200 maximum, 768 KiB in total)")
	ErrDeployInProgress = errors.New("a deployment of this container is in progress: wait for it to finish")
	// ErrWorkloadDeleting : une suppression est en cours (ou le conteneur n'existe plus).
	ErrWorkloadDeleting = errors.New("this container is being deleted")
	// ErrWorkloadNotBuilt : première image pas encore construite.
	ErrWorkloadNotBuilt = errors.New("this container is not deployed yet: its first image is still being built")
	ErrWorkloadStopped  = errors.New("this container is stopped: start it before changing its variables")
)

var variableNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// Validate : noms et tailles. Les messages ne citent jamais une valeur.
func (c VariableChange) Validate() error {
	for _, m := range []map[string]string{c.Env, c.Secrets} {
		for name, value := range m {
			if !variableNameRe.MatchString(name) {
				return ErrVariableName
			}
			if len(value) > MaxVariableBytes {
				return ErrVariableTooBig
			}
		}
	}
	for _, name := range c.Unset {
		if !variableNameRe.MatchString(name) {
			return ErrVariableName
		}
		// Sinon, le résultat dépendrait de l'ordre d'application.
		if _, ok := c.Env[name]; ok {
			return ErrVariableConflict
		}
		if _, ok := c.Secrets[name]; ok {
			return ErrVariableConflict
		}
	}
	for name := range c.Env {
		if _, ok := c.Secrets[name]; ok {
			return ErrVariableConflict
		}
	}
	return nil
}

// CheckTotals : limites sur l'ensemble des valeurs d'un conteneur.
func CheckTotals(values map[string]string) error {
	total := 0
	for k, v := range values {
		total += len(k) + len(v)
	}
	if len(values) > MaxVariables || total > maxTotalBytes {
		return ErrTooManyVariable
	}
	return nil
}
