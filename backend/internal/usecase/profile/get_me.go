// Package profile contains profile-related usecases.
package profile

import (
	"context"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// userByIDFinder is the only thing GetMe needs. The package-local name
// keeps this interface scoped — a separate usecase that also reads
// users would declare its own port with the methods *it* uses.
type userByIDFinder interface {
	GetByID(ctx context.Context, id string) (domain.User, error)
}

// GetMe loads the authenticated user's profile by ID.
type GetMe struct {
	Users userByIDFinder
}

func (uc *GetMe) Run(ctx context.Context, userID string) (domain.User, error) {
	if userID == "" {
		return domain.User{}, domain.ErrInvalidArgument
	}
	return uc.Users.GetByID(ctx, userID)
}
