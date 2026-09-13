package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/easyhr/easyhr-be/internal/platform"
	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
)

type platformContextKey struct{}

type PlatformPrincipal struct {
	OperatorID string
}

func PlatformPrincipalFromContext(ctx context.Context) (PlatformPrincipal, bool) {
	p, ok := ctx.Value(platformContextKey{}).(PlatformPrincipal)
	return p, ok
}

func PlatformAuthMiddleware(jwtSecret string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			authHeader := c.Request().Header.Get("Authorization")
			if authHeader == "" {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Missing authorization header"})
			}

			parts := strings.Split(authHeader, " ")
			if len(parts) != 2 || parts[0] != "Bearer" {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Invalid authorization format"})
			}

			tokenString := parts[1]
			claims := &platform.PlatformClaims{}

			token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
				return []byte(jwtSecret), nil
			})

			if err != nil || !token.Valid || !claims.IsPlatform {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Invalid platform token"})
			}

			req := c.Request()
			ctx := context.WithValue(req.Context(), platformContextKey{}, PlatformPrincipal{
				OperatorID: claims.OperatorID,
			})
			c.SetRequest(req.WithContext(ctx))

			return next(c)
		}
	}
}
