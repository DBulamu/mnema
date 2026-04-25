// Package rest contains the HTTP transport — huma handlers, marshal /
// unmarshal, mapping of domain errors to HTTP status codes.
//
// Handlers in this package are thin: parse input, call a usecase,
// marshal the result. Business logic lives in usecase packages.
package rest

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

// BearerSecurityName is the OpenAPI key used in Operation.Security to
// declare that an endpoint requires a JWT. Keep in sync with the scheme
// registered in NewAPI.
const BearerSecurityName = "BearerAuth"

// NewAPI builds a huma API on top of a *http.ServeMux and registers the
// shared OpenAPI metadata (info, security schemes). Returns the API and
// the mux so the caller can mount the mux on a server.
func NewAPI(title, version, description string) (huma.API, *http.ServeMux) {
	mux := http.NewServeMux()
	cfg := huma.DefaultConfig(title, version)
	cfg.Info.Description = description

	if cfg.Components == nil {
		cfg.Components = &huma.Components{}
	}
	if cfg.Components.SecuritySchemes == nil {
		cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{}
	}
	cfg.Components.SecuritySchemes[BearerSecurityName] = &huma.SecurityScheme{
		Type:         "http",
		Scheme:       "bearer",
		BearerFormat: "JWT",
	}

	return humago.New(mux, cfg), mux
}
