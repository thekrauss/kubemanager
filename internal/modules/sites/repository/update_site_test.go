package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/modules/sites/domain"
)

type recDriver struct{ last atomic.Value }

func (d *recDriver) Open(string) (driver.Conn, error) { return recConn{d}, nil }

type recConn struct{ d *recDriver }

func (c recConn) Prepare(q string) (driver.Stmt, error) { return recStmt{c.d, q}, nil }
func (recConn) Close() error                            { return nil }
func (recConn) Begin() (driver.Tx, error)               { return recTx{}, nil }

type recTx struct{}

func (recTx) Commit() error   { return nil }
func (recTx) Rollback() error { return nil }

type recStmt struct {
	d *recDriver
	q string
}

func (recStmt) Close() error  { return nil }
func (recStmt) NumInput() int { return -1 }
func (s recStmt) Exec([]driver.Value) (driver.Result, error) {
	s.d.last.Store(s.q)
	return driver.RowsAffected(1), nil
}
func (recStmt) Query([]driver.Value) (driver.Rows, error) { return nil, driver.ErrSkip }

// Modifier un réglage n'écrase ni le statut ni le déploiement actif écrits entre
// la lecture et l'écriture (avant : Save de toute la ligne).
func TestUpdateSite_OnlyWritesSettings(t *testing.T) {
	d := &recDriver{}
	name := "rec-" + uuid.NewString()
	sql.Register(name, d)
	conn, err := sql.Open(name, "")
	require.NoError(t, err)
	defer conn.Close()
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: conn}), &gorm.Config{DisableAutomaticPing: true, SkipDefaultTransaction: true})
	require.NoError(t, err)

	active := uuid.New()
	repo := NewSiteRepository(db)
	require.NoError(t, repo.UpdateSite(context.Background(), &domain.Site{ID: uuid.New(), SPAFallback: false, Status: domain.SiteStatusLive, ActiveDeploymentID: &active}))

	q := d.last.Load().(string)
	assert.Contains(t, q, "spa_fallback")
	assert.NotContains(t, q, `"status"`)
	assert.NotContains(t, q, "active_deployment_id")
	assert.NotContains(t, q, "subdomain")
}
