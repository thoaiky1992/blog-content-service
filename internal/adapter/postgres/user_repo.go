package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sky/blog-service/internal/app"
	"github.com/sky/blog-service/internal/domain"
)

// UserRepo implements app.UserRepository for mock authentication.
type UserRepo struct {
	pool *pgxpool.Pool
}

func NewUserRepo(pool *pgxpool.Pool) *UserRepo { return &UserRepo{pool: pool} }

var _ app.UserRepository = (*UserRepo)(nil)

// GetByID resolves the actor. The role always comes from the database.
func (r *UserRepo) GetByID(ctx context.Context, id uuid.UUID) (domain.Actor, error) {
	var role string
	err := r.pool.QueryRow(ctx, `SELECT role FROM users WHERE id = $1`, id).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Actor{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Actor{}, mapErr("get user", err)
	}
	return domain.Actor{ID: id, Role: domain.Role(role)}, nil
}
