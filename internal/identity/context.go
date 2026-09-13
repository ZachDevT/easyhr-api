package identity

import (
	"context"
	"fmt"
	"net/http"
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
// membership. The workspace is sent by the client only as a selector; the
// server looks it up and rejects it unless the identity has an active local
// membership. In production the same selector should be derived from a
// verified workspace host; the explicit header keeps local development and
// the current separate frontend deployments workable.
func (resolver *Resolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		claims, ok := clerk.SessionClaimsFromContext(request.Context())
		if !ok || claims == nil || claims.Subject == "" {
			writeProblem(writer, request, http.StatusUnauthorized, "auth.missing_token", "Unauthorized", "A verified session is required")
			return
		}

		workspace := strings.TrimSpace(request.Header.Get("X-EasyHR-Workspace"))
		if workspace == "" {
			writeProblem(writer, request, http.StatusBadRequest, "workspace.required", "Workspace required", "X-EasyHR-Workspace is required")
			return
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

var errMembershipNotFound = fmt.Errorf("active membership not found")
