package rest

import (
	"errors"
	"fmt"

	"github.com/DBulamu/mnema/backend/internal/domain"
	"github.com/danielgtaylor/huma/v2"
)

// errUnauthenticated is returned by handlers that defensively check the
// context after middleware. It collapses to 401 via toHTTP.
var errUnauthenticated = fmt.Errorf("%w: not authenticated", domain.ErrTokenInvalid)

// toHTTP maps a domain error to a huma status error. Any unmapped error
// becomes 500. Keep this function the single place that knows about
// HTTP status codes for domain failures.
func toHTTP(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrInvalidArgument):
		return huma.Error400BadRequest(err.Error())
	case errors.Is(err, domain.ErrLinkInvalid),
		errors.Is(err, domain.ErrSessionInvalid),
		errors.Is(err, domain.ErrTokenInvalid):
		return huma.Error401Unauthorized("unauthorized")
	case errors.Is(err, domain.ErrUserNotFound),
		errors.Is(err, domain.ErrConversationNotFound):
		return huma.Error404NotFound("not found")
	default:
		return huma.Error500InternalServerError("internal error")
	}
}
