package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/easyhr/easyhr-be/internal/identity"
	"github.com/easyhr/easyhr-be/internal/signatures"
)

type createTemplateRequestPayload struct {
	DocumentTitle string                       `json:"documentTitle"`
	Message       string                       `json:"message"`
	Recipients    []signatures.CreateRecipient `json:"recipients"`
}

type declineRequestPayload struct {
	Reason string `json:"reason"`
}

func (s *Server) listSignatureRequests(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("signatures:manage") && !principal.HasPermission("signatures:read") && !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to view all signature requests")
	}

	results, err := s.signatures.ListRequests(c.Request().Context(), principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "signatures.list_failed", "Internal error", err.Error())
	}
	return c.JSON(http.StatusOK, map[string]any{"data": results})
}

func (s *Server) listMySignatureRequests(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	// Lookup user email
	var email string
	_ = s.pool.QueryRow(c.Request().Context(), `SELECT email FROM users WHERE id = $1`, principal.UserID).Scan(&email)

	results, err := s.signatures.ListMySignatureRequests(c.Request().Context(), principal.OrganizationID, email)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "signatures.my_list_failed", "Internal error", err.Error())
	}
	return c.JSON(http.StatusOK, map[string]any{"data": results})
}

func (s *Server) createSignatureRequestFromTemplate(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("signatures:manage") && !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to create signature requests")
	}

	var input createTemplateRequestPayload
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", err.Error())
	}

	if input.DocumentTitle == "" {
		input.DocumentTitle = "Employment Document.pdf"
	}

	result, err := s.signatures.CreateRequestFromTemplate(c.Request().Context(), principal.OrganizationID, principal.UserID, input.DocumentTitle, input.Message, input.Recipients)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "signatures.create_failed", "Internal error", err.Error())
	}

	return c.JSON(http.StatusCreated, result)
}

func (s *Server) signSignatureRequest(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	var userEmail string
	_ = s.pool.QueryRow(c.Request().Context(), `SELECT email FROM users WHERE id = $1`, principal.UserID).Scan(&userEmail)

	id := c.Param("id")
	if err := s.signatures.SignRequest(c.Request().Context(), principal.OrganizationID, userEmail, id); err != nil {
		return problem(c, http.StatusInternalServerError, "signatures.sign_failed", "Internal error", err.Error())
	}

	return c.JSON(http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) declineSignatureRequest(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	var userEmail string
	_ = s.pool.QueryRow(c.Request().Context(), `SELECT email FROM users WHERE id = $1`, principal.UserID).Scan(&userEmail)

	var body declineRequestPayload
	_ = c.Bind(&body)
	if body.Reason == "" {
		body.Reason = "Declined by signer"
	}

	id := c.Param("id")
	if err := s.signatures.DeclineRequest(c.Request().Context(), principal.OrganizationID, userEmail, id, body.Reason); err != nil {
		return problem(c, http.StatusInternalServerError, "signatures.decline_failed", "Internal error", err.Error())
	}

	return c.JSON(http.StatusOK, map[string]bool{"success": true})
}
