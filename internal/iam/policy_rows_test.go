package iam

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var errRowsCut = errors.New("connexion coupée")

type cutDriver struct{}

func (cutDriver) Open(string) (driver.Conn, error) { return cutConn{}, nil }

type cutConn struct{}

func (cutConn) Prepare(string) (driver.Stmt, error) { return cutStmt{}, nil }
func (cutConn) Close() error                        { return nil }
func (cutConn) Begin() (driver.Tx, error)           { return nil, errors.New("pas de transaction") }

type cutStmt struct{}

func (cutStmt) Close() error                               { return nil }
func (cutStmt) NumInput() int                              { return -1 }
func (cutStmt) Exec([]driver.Value) (driver.Result, error) { return driver.RowsAffected(0), nil }
func (cutStmt) Query([]driver.Value) (driver.Rows, error)  { return cutRows{}, nil }

type cutRows struct{}

func (cutRows) Columns() []string         { return []string{"id", "email", "name"} }
func (cutRows) Close() error              { return nil }
func (cutRows) Next([]driver.Value) error { return errRowsCut }

// Lecture interrompue : une erreur, pas une liste de membres tronquée.
func TestDescribeMembers_RowsError(t *testing.T) {
	name := "cut-" + uuid.NewString()
	sql.Register(name, cutDriver{})
	conn, err := sql.Open(name, "")
	require.NoError(t, err)
	defer conn.Close()
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: conn}), &gorm.Config{DisableAutomaticPing: true, SkipDefaultTransaction: true})
	require.NoError(t, err)

	s := &PolicyService{DB: db}
	p := &Policy{Bindings: []PolicyBinding{{Role: "roles/viewer", Members: []Principal{UserPrincipal(uuid.New())}}}}
	_, err = s.describeMembers(context.Background(), p)
	assert.ErrorIs(t, err, errRowsCut)
}
