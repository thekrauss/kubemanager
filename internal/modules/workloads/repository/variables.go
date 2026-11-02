package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/thekrauss/kubemanager/internal/modules/workloads/domain"
)

// Sealer : chiffrement authentifié (auth.Sealer).
type Sealer interface {
	Seal(plain []byte, associated string) ([]byte, error)
	Open(sealed []byte, associated string) ([]byte, error)
}

// VariableStore : variables et secrets chiffrés d'un conteneur.
type VariableStore interface {
	// List : vue de l'API, sans la valeur des secrets.
	List(ctx context.Context, workload uuid.UUID) ([]domain.VariableView, error)
	// Values : valeurs en clair, pour écrire le Secret Kubernetes (worker).
	Values(ctx context.Context, workload uuid.UUID) (map[string]string, error)
	// Apply : applique la modification ; la révision n'augmente que si une
	// valeur change (ou à la première écriture). Renvoie la révision et si
	// quelque chose a changé.
	Apply(ctx context.Context, workload uuid.UUID, change domain.VariableChange) (int64, bool, error)
	// Import : variables d'un conteneur antérieur, sans effet s'il a déjà une
	// révision. Toutes marquées secrètes : rien ne dit lesquelles le sont.
	Import(ctx context.Context, workload uuid.UUID, values map[string]string) (int64, error)
}

type gormVariableStore struct {
	db     *gorm.DB
	sealer Sealer
}

// NewVariableStore : stockage en base.
func NewVariableStore(db *gorm.DB, sealer Sealer) VariableStore {
	return &gormVariableStore{db: db, sealer: sealer}
}

// associated : lie un chiffré à son conteneur et à son nom ; recopié sur une
// autre ligne, il ne se déchiffre plus.
func associated(workload uuid.UUID, name string) string {
	return "workload-variable|" + workload.String() + "|" + name
}

func (s *gormVariableStore) rows(db *gorm.DB, workload uuid.UUID) ([]domain.Variable, error) {
	var rows []domain.Variable
	return rows, db.Where("workload_id = ?", workload).Order("name").Find(&rows).Error
}

func (s *gormVariableStore) List(ctx context.Context, workload uuid.UUID) ([]domain.VariableView, error) {
	rows, err := s.rows(s.db.WithContext(ctx), workload)
	if err != nil {
		return nil, err
	}
	out := make([]domain.VariableView, 0, len(rows))
	for _, r := range rows {
		v := domain.VariableView{Name: r.Name, Secret: r.Secret, UpdatedAt: r.UpdatedAt}
		if !r.Secret {
			plain, err := s.sealer.Open(r.Sealed, associated(workload, r.Name))
			if err != nil {
				return nil, err
			}
			v.Value = string(plain)
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *gormVariableStore) Values(ctx context.Context, workload uuid.UUID) (map[string]string, error) {
	rows, err := s.rows(s.db.WithContext(ctx), workload)
	if err != nil {
		return nil, err
	}
	return s.open(workload, rows)
}

func (s *gormVariableStore) open(workload uuid.UUID, rows []domain.Variable) (map[string]string, error) {
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		plain, err := s.sealer.Open(r.Sealed, associated(workload, r.Name))
		if err != nil {
			return nil, err
		}
		out[r.Name] = string(plain)
	}
	return out, nil
}

// lockRevision : sérialise les modifications d'un même conteneur.
func lockRevision(tx *gorm.DB, workload uuid.UUID) (int64, error) {
	var w domain.Workload
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("env_revision").Where("id = ?", workload).First(&w).Error
	return w.EnvRevision, err
}

func (s *gormVariableStore) Apply(ctx context.Context, workload uuid.UUID, change domain.VariableChange) (int64, bool, error) {
	if err := change.Validate(); err != nil {
		return 0, false, err
	}
	var revision int64
	var changed bool
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := lockRevision(tx, workload)
		if err != nil {
			return err
		}
		rows, err := s.rows(tx, workload)
		if err != nil {
			return err
		}
		values, err := s.open(workload, rows)
		if err != nil {
			return err
		}
		secret := make(map[string]bool, len(rows))
		for _, r := range rows {
			secret[r.Name] = r.Secret
		}

		var remove []string
		if change.ReplaceEnv {
			for _, r := range rows {
				if _, kept := change.Env[r.Name]; !r.Secret && !kept {
					remove = append(remove, r.Name)
				}
			}
		}
		for _, name := range change.Unset {
			if _, ok := values[name]; ok {
				remove = append(remove, name)
			}
		}
		// Un secret le reste : une variable du même nom change sa valeur sans
		// la rendre lisible.
		upserts := []domain.Variable{}
		put := func(name, value string, wantSecret bool) error {
			isSecret := wantSecret || secret[name]
			if old, ok := values[name]; ok && old == value && secret[name] == isSecret {
				return nil
			}
			sealed, err := s.sealer.Seal([]byte(value), associated(workload, name))
			if err != nil {
				return err
			}
			upserts = append(upserts, domain.Variable{WorkloadID: workload, Name: name, Sealed: sealed, Secret: isSecret, UpdatedAt: time.Now()})
			values[name] = value
			return nil
		}
		for name, value := range change.Env {
			if err := put(name, value, false); err != nil {
				return err
			}
		}
		for name, value := range change.Secrets {
			if err := put(name, value, true); err != nil {
				return err
			}
		}
		for _, name := range remove {
			delete(values, name)
		}
		if err := domain.CheckTotals(values); err != nil {
			return err
		}
		if len(remove) > 0 {
			if err := tx.Where("workload_id = ? AND name IN ?", workload, remove).Delete(&domain.Variable{}).Error; err != nil {
				return err
			}
		}
		if len(upserts) > 0 {
			if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&upserts).Error; err != nil {
				return err
			}
		}
		changed = len(remove) > 0 || len(upserts) > 0
		revision = current
		if changed || current == 0 {
			revision = current + 1
			return tx.Model(&domain.Workload{}).Where("id = ?", workload).UpdateColumn("env_revision", revision).Error
		}
		return nil
	})
	return revision, changed, err
}

func (s *gormVariableStore) Import(ctx context.Context, workload uuid.UUID, values map[string]string) (int64, error) {
	var revision int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := lockRevision(tx, workload)
		if err != nil {
			return err
		}
		revision = current
		if current != 0 {
			return nil
		}
		rows := make([]domain.Variable, 0, len(values))
		for name, value := range values {
			sealed, err := s.sealer.Seal([]byte(value), associated(workload, name))
			if err != nil {
				return err
			}
			rows = append(rows, domain.Variable{WorkloadID: workload, Name: name, Sealed: sealed, Secret: true, UpdatedAt: time.Now()})
		}
		if len(rows) > 0 {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error; err != nil {
				return err
			}
		}
		revision = 1
		return tx.Model(&domain.Workload{}).Where("id = ?", workload).UpdateColumn("env_revision", revision).Error
	})
	return revision, err
}
