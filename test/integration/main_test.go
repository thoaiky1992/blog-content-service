// Package integration runs the PostgreSQL adapter against a real database
// (testcontainers-go, postgres:18, project migrations applied via goose).
// Tests are skipped when Docker is unavailable.
package integration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	pool       *pgxpool.Pool
	skipReason string
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	cleanup, err := setup(ctx)
	if err != nil {
		skipReason = err.Error()
		fmt.Fprintln(os.Stderr, "integration tests will be skipped:", skipReason)
	}
	code := m.Run()
	if cleanup != nil {
		cleanup()
	}
	os.Exit(code)
}

func setup(ctx context.Context) (cleanup func(), err error) {
	ctr, err := tcpostgres.Run(ctx, "postgres:18",
		tcpostgres.WithDatabase("blog"),
		tcpostgres.WithUsername("blog"),
		tcpostgres.WithPassword("blog"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(90*time.Second)),
	)
	if err != nil {
		return nil, fmt.Errorf("start postgres container (is Docker running?): %w", err)
	}
	terminate := func() { _ = testcontainers.TerminateContainer(ctr) }

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		terminate()
		return nil, err
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		terminate()
		return nil, err
	}
	if err := goose.SetDialect("postgres"); err != nil {
		terminate()
		return nil, err
	}
	if err := goose.Up(db, "../../migrations"); err != nil {
		terminate()
		return nil, fmt.Errorf("apply migrations: %w", err)
	}
	_ = db.Close()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		terminate()
		return nil, err
	}
	cfg.MaxConns = 50
	pool, err = pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		terminate()
		return nil, err
	}
	return func() { pool.Close(); terminate() }, nil
}
