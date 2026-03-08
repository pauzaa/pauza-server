package repository

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Tx is the transaction interface used by AuthService. It embeds DBTX for
// query execution and adds transaction-control methods. Begin creates a
// savepoint (nested transaction) when called on an existing Tx.
type Tx interface {
	DBTX
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Pool is the connection-pool interface used by AuthService. It embeds
// DBTX so the pool can be passed directly to repository methods for
// non-transactional reads, and exposes Begin to start transactions.
type Pool interface {
	DBTX
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Compile-time checks: the real pgx types satisfy these interfaces.
var (
	_ Pool = (*pgxpool.Pool)(nil)
	_ Tx   = (pgx.Tx)(nil)
)
