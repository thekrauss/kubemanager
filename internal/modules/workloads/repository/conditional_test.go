package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/modules/workloads/domain"
)

// recordingDriver : base minimale, qui retient la dernière requête et annonce le
// nombre de lignes touchées voulu.
type recordingDriver struct {
	lastQuery atomic.Value
	affected  atomic.Int64
}

func (d *recordingDriver) Open(string) (driver.Conn, error) { return &recordingConn{d}, nil }

type recordingConn struct{ d *recordingDriver }

func (c *recordingConn) Prepare(q string) (driver.Stmt, error) { return &recordingStmt{c.d, q}, nil }
func (c *recordingConn) Close() error                          { return nil }
func (c *recordingConn) Begin() (driver.Tx, error)             { return recordingTx{}, nil }

type recordingTx struct{}

func (recordingTx) Commit() error   { return nil }
func (recordingTx) Rollback() error { return nil }

type recordingStmt struct {
	d *recordingDriver
	q string
}

func (s *recordingStmt) Close() error  { return nil }
func (s *recordingStmt) NumInput() int { return -1 }
func (s *recordingStmt) Exec([]driver.Value) (driver.Result, error) {
	s.d.lastQuery.Store(s.q)
	return driver.RowsAffected(s.d.affected.Load()), nil
}
func (s *recordingStmt) Query([]driver.Value) (driver.Rows, error) {
	return nil, driver.ErrSkip
}

func recordingRepo(t *testing.T) (WorkloadRepository, *recordingDriver) {
	t.Helper()
	d := &recordingDriver{}
	name := "recording-" + uuid.NewString()
	sql.Register(name, d)
	conn, err := sql.Open(name, "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: conn}), &gorm.Config{DisableAutomaticPing: true, SkipDefaultTransaction: true})
	require.NoError(t, err)
	return NewWorkloadRepository(db), d
}

// Une écriture sur un conteneur en suppression ne touche aucune ligne : refus
// explicite, jamais de réussite silencieuse.
func TestUpdates_RefusedWhileDeleting(t *testing.T) {
	repo, db := recordingRepo(t)
	ctx, id := context.Background(), uuid.New()
	writes := map[string]func() error{
		"UpdateStatus":  func() error { return repo.UpdateStatus(ctx, id, "RUNNING", "DEPLOYED") },
		"UpdateRuntime": func() error { return repo.UpdateRuntime(ctx, id, 1, "100m", "64Mi", 8080, false) },
		"UpdateSpec":    func() error { return repo.UpdateSpec(ctx, id, "nginx:2", "1Gi", "", []byte("{}")) },
	}
	for name, write := range writes {
		db.affected.Store(0)
		assert.ErrorIs(t, write(), domain.ErrWorkloadDeleting, name)
		q := db.lastQuery.Load().(string)
		assert.Contains(t, q, "status NOT IN", name)

		db.affected.Store(1)
		assert.NoError(t, write(), name)
	}
}

// Les statuts de la suppression restent inconditionnels : une reprise après
// DELETE_FAILED ou un échec du workflow de suppression doit pouvoir s'écrire.
func TestUpdateStatus_DeleteStatusesAreUnconditional(t *testing.T) {
	repo, db := recordingRepo(t)
	ctx, id := context.Background(), uuid.New()
	for _, status := range []string{"DELETING", "DELETE_FAILED", "DELETED"} {
		require.NoError(t, repo.UpdateStatus(ctx, id, status, "X"), status)
		assert.False(t, strings.Contains(db.lastQuery.Load().(string), "NOT IN"), status)
	}
}

// Une nouvelle version n'écrase pas le statut déjà écrit par le workflow.
func TestUpdateSpec_LeavesStatusAlone(t *testing.T) {
	repo, db := recordingRepo(t)
	db.affected.Store(1)
	require.NoError(t, repo.UpdateSpec(context.Background(), uuid.New(), "nginx:2", "1Gi", "", []byte("{}")))
	q := db.lastQuery.Load().(string)
	assert.NotContains(t, q, `"status"=`)
	assert.NotContains(t, q, "current_phase")
}
