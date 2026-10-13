package identity

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Principal struct {
	UserID         string
	MembershipID   string
	OrganizationID string
	ExternalID     string
	Permissions    map[string]struct{}
}

type contextKey struct{}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, contextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(contextKey{}).(Principal)
	return principal, ok
}

func (principal Principal) HasPermission(permission string) bool {
	_, ok := principal.Permissions[permission]
	return ok
}

type Resolver struct {
	pool *pgxpool.Pool
}

func NewResolver(pool *pgxpool.Pool) *Resolver {
	return &Resolver{pool: pool}
}

// Middleware resolves a verified Clerk identity to exactly one active EasyHR
// membership. The workspace is determined from:
// 1. Explicit X-EasyHR-Workspace request header
// 2. Origin or Referer host headers (e.g. acme.easyhr.pro or acme.localhost:3000)
// 3. Host header
// 4. Default user membership lookup if not specified on apex/generic domain
// In all cases, the server strictly validates that the authenticated identity
// has an ACTIVE membership in that workspace in PostgreSQL before granting access.
func (resolver *Resolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		claims, ok := clerk.SessionClaimsFromContext(request.Context())
		if !ok || claims == nil || claims.Subject == "" {
			writeProblem(writer, request, http.StatusUnauthorized, "auth.missing_token", "Unauthorized", "A verified session is required")
			return
		}

		workspace := extractWorkspaceFromRequest(request)
		if workspace == "" {
			err := resolver.pool.QueryRow(request.Context(), `
				SELECT w.subdomain
				FROM users u
				JOIN memberships m ON m.user_id = u.id AND m.status = 'active'
				JOIN workspaces w ON w.organization_id = m.organization_id
				WHERE u.external_identity_id = $1
				LIMIT 1`, claims.Subject).Scan(&workspace)
			if err != nil {
				_ = resolver.pool.QueryRow(request.Context(), `
					SELECT w.subdomain
					FROM users u
					JOIN employees e ON (e.user_id = u.id OR LOWER(e.work_email) = LOWER(u.email))
					JOIN workspaces w ON w.organization_id = e.organization_id
					WHERE u.external_identity_id = $1
					LIMIT 1`, claims.Subject).Scan(&workspace)
			}
			if workspace == "" {
				writeProblem(writer, request, http.StatusForbidden, "membership.not_active", "Forbidden", "No active organization workspace exists for this account")
				return
			}
		}

		principal, err := resolver.resolve(request.Context(), claims.Subject, workspace)
		if err != nil {
			if err == errMembershipNotFound {
				writeProblem(writer, request, http.StatusForbidden, "membership.not_active", "Forbidden", "No active membership exists for this workspace")
				return
			}
			writeProblem(writer, request, http.StatusInternalServerError, "identity.resolve_failed", "Internal server error", "Could not resolve the authenticated user")
			return
		}

		next.ServeHTTP(writer, request.WithContext(WithPrincipal(request.Context(), principal)))
	})
}

var reservedSubdomainsList = map[string]bool{
	"api": true, "app": true, "admin": true, "auth": true, "billing": true,
	"dashboard": true, "docs": true, "mail": true, "support": true, "www": true,
	"staging": true, "dev": true, "test": true, "demo": true, "clerk": true,
}

func extractWorkspaceFromRequest(request *http.Request) string {
	if ws := strings.TrimSpace(request.Header.Get("X-EasyHR-Workspace")); ws != "" {
		clean := strings.ToLower(ws)
		if !reservedSubdomainsList[clean] {
			return clean
		}
	}
	if origin := request.Header.Get("Origin"); origin != "" {
		if ws := extractSubdomainFromURL(origin); ws != "" {
			return ws
		}
	}
	if referer := request.Header.Get("Referer"); referer != "" {
		if ws := extractSubdomainFromURL(referer); ws != "" {
			return ws
		}
	}
	if ws := extractSubdomainFromHost(request.Host); ws != "" {
		return ws
	}
	return ""
}

func extractSubdomainFromURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return extractSubdomainFromHost(parsed.Host)
}

func extractSubdomainFromHost(host string) string {
	if idx := strings.Index(host, ":"); idx != -1 {
		host = host[:idx]
	}
	host = strings.ToLower(strings.TrimSpace(host))

	// Production: <subdomain>.easyhr.pro
	if strings.HasSuffix(host, ".easyhr.pro") {
		sub := strings.TrimSuffix(host, ".easyhr.pro")
		if idx := strings.LastIndex(sub, "."); idx != -1 {
			sub = sub[idx+1:]
		}
		if sub != "" && !reservedSubdomainsList[sub] {
			return sub
		}
	}

	// Local development: <subdomain>.localhost
	if strings.HasSuffix(host, ".localhost") {
		sub := strings.TrimSuffix(host, ".localhost")
		if idx := strings.LastIndex(sub, "."); idx != -1 {
			sub = sub[idx+1:]
		}
		if sub != "" && !reservedSubdomainsList[sub] {
			return sub
		}
	}

	return ""
}

var errMembershipNotFound = fmt.Errorf("active membership not found")
