package auth

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/clerk/clerk-sdk-go/v2"
	clerkhttp "github.com/clerk/clerk-sdk-go/v2/http"
	"github.com/clerk/clerk-sdk-go/v2/jwks"
)

type ClerkConfig struct {
	SecretKey         string
	Issuer            string
	AuthorizedParties []string
	RequiredAudiences []string
}

type Middleware func(http.Handler) http.Handler

func NewClerkMiddleware(config ClerkConfig) (Middleware, error) {
	if config.SecretKey == "" || config.Issuer == "" || len(config.AuthorizedParties) == 0 {
		return nil, fmt.Errorf("Clerk secret key, issuer, and authorized parties are required")
	}

	secretKey := config.SecretKey
	jwksClient := jwks.NewClient(&clerk.ClientConfig{
		BackendConfig: clerk.BackendConfig{Key: &secretKey},
	})

	return func(next http.Handler) http.Handler {
		return clerkhttp.WithHeaderAuthorization(
			clerkhttp.JWKSClient(jwksClient),
			clerkhttp.ProxyURL(config.Issuer),
			clerkhttp.AuthorizedParty(func(party string) bool {
				if party == "" {
					return false
				}
				if party == "https://easyhr.pro" || strings.HasSuffix(party, ".easyhr.pro") {
					return true
				}
				if strings.HasPrefix(party, "http://localhost:") || strings.HasPrefix(party, "https://localhost:") {
					return true
				}
				if strings.HasSuffix(party, ".vercel.app") {
					return true
				}
				for _, ap := range config.AuthorizedParties {
					if party == ap {
						return true
					}
				}
				return false
			}),
			clerkhttp.Leeway(5*time.Second),
			clerkhttp.AuthorizationFailureHandler(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writeUnauthorized(writer, request, "auth.invalid_token", "The session token could not be verified")
			})),
		)(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			claims, ok := clerk.SessionClaimsFromContext(request.Context())
			if !ok || claims == nil || claims.Subject == "" {
				writeUnauthorized(writer, request, "auth.missing_token", "A valid bearer token is required")
				return
			}
			if !hasRequiredAudience(claims.Audience, config.RequiredAudiences) {
				writeUnauthorized(writer, request, "auth.invalid_audience", "The session token is not intended for this API")
				return
			}
			next.ServeHTTP(writer, request)
		}))
	}, nil
}

func hasRequiredAudience(tokenAudiences, requiredAudiences []string) bool {
	if len(requiredAudiences) == 0 {
		return true
	}
	for _, audience := range requiredAudiences {
		if slices.Contains(tokenAudiences, audience) {
			return true
		}
	}
	return false
}
