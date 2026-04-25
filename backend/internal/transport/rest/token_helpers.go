package rest

import "github.com/DBulamu/mnema/backend/internal/domain"

// domainToken centralises the conversion from the wire string to the
// domain Token type. Using a helper rather than inline type conversions
// keeps the conversion grep-able and easy to evolve (e.g. to add input
// length checks if desired).
func domainToken(s string) domain.Token { return domain.Token(s) }
