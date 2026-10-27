package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/easyhr/easyhr-be/internal/email"
	"github.com/easyhr/easyhr-be/internal/identity"
	"github.com/easyhr/easyhr-be/internal/signatures"
	"github.com/labstack/echo/v4"
)

type createTemplateRequestPayload struct {
	DocumentTitle string                       `json:"documentTitle"`
	DocumentName  string                       `json:"documentName"`
	Message       string                       `json:"message"`
	CustomMessage string                       `json:"customMessage"`
	TemplateID    string                       `json:"templateId"`
	Recipients    []signatures.CreateRecipient `json:"recipients"`
	MergeFields   map[string]string            `json:"mergeFields"`
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
	if s.storage != nil {
		for i := range results {
			if results[i].DocumentID == "" {
				continue
			}
			if link, linkErr := s.documents.GetDownloadUrl(c.Request().Context(), principal.OrganizationID, principal.UserID, results[i].DocumentID); linkErr == nil {
				results[i].DocumentURL = link.URL
			}
		}
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
	if s.storage != nil {
		for i := range results {
			if results[i].DocumentID == "" {
				continue
			}
			if link, linkErr := s.documents.GetDownloadUrl(c.Request().Context(), principal.OrganizationID, principal.UserID, results[i].DocumentID); linkErr == nil {
				results[i].DocumentURL = link.URL
			}
		}
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

	msg := input.Message
	if msg == "" {
		msg = input.CustomMessage
	}

	if input.TemplateID == "" {
		return problem(c, http.StatusUnprocessableEntity, "signatures.template_required", "Template required", "Choose an uploaded document or an organization template")
	}
	result, links, err := s.signatures.CreateGeneratedRequest(c.Request().Context(), principal.OrganizationID, principal.UserID, signatures.CreateGeneratedRequestInput{TemplateID: input.TemplateID, Message: msg, Recipients: input.Recipients, MergeFields: input.MergeFields})
	if err != nil {
		if errors.Is(err, signatures.ErrNotFound) {
			return problem(c, http.StatusNotFound, "signatures.template_not_found", "Not found", "Template is unavailable")
		}
		return problem(c, http.StatusUnprocessableEntity, "signatures.create_failed", "Could not create signature request", err.Error())
	}
	s.notifySignatureRecipientsWithLinks(c.Request().Context(), principal.OrganizationID, input.Recipients, links, result.Title, msg)

	return c.JSON(http.StatusCreated, result)
}

func (s *Server) listSignatureTemplates(c echo.Context) error {
	p, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !p.HasPermission("signatures:manage") && !p.HasPermission("signatures:read") && !p.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You cannot view signature templates")
	}
	items, err := s.signatures.ListTemplates(c.Request().Context(), p.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "signatures.templates_failed", "Internal error", "Could not load templates")
	}
	return c.JSON(http.StatusOK, map[string]any{"data": items})
}

func (s *Server) getSignatureTemplateBranding(c echo.Context) error {
	p, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !p.HasPermission("signatures:manage") && !p.HasPermission("signatures:read") && !p.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You cannot use company template branding")
	}
	branding, err := s.signatures.GetTemplateBranding(c.Request().Context(), p.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "signatures.branding_failed", "Internal error", "Could not load company branding")
	}
	return c.JSON(http.StatusOK, branding)
}

func (s *Server) saveSignatureTemplate(c echo.Context) error {
	p, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !p.HasPermission("signatures:manage") && !p.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You cannot manage signature templates")
	}
	var input signatures.SaveTemplateInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", err.Error())
	}
	item, err := s.signatures.SaveTemplate(c.Request().Context(), p.OrganizationID, p.UserID, c.Param("templateId"), input)
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "signatures.template_invalid", "Invalid template", err.Error())
	}
	return c.JSON(http.StatusOK, item)
}

func (s *Server) notifySignatureRecipients(ctx context.Context, organizationID string, recipients []signatures.CreateRecipient, title, message string) {
	links := make([]string, len(recipients))
	for i := range links {
		links[i] = "https://easyhr.pro/my-signatures"
	}
	s.notifySignatureRecipientsWithLinks(ctx, organizationID, recipients, links, title, message)
}

func (s *Server) notifySignatureRecipientsWithLinks(ctx context.Context, organizationID string, recipients []signatures.CreateRecipient, links []string, title, message string) {
	var company, logo, color string
	if err := s.pool.QueryRow(ctx, `SELECT o.name, COALESCE(w.logo_url, ''), COALESCE(w.brand_color, '#4F46E5') FROM organizations o LEFT JOIN workspaces w ON w.organization_id=o.id WHERE o.id=$1`, organizationID).Scan(&company, &logo, &color); err != nil {
		slog.Error("signature email branding lookup failed", "error", err)
		return
	}
	if message == "" {
		message = "Please review and sign this document."
	}
	for i, recipient := range recipients {
		link := "https://easyhr.pro/my-signatures"
		if i < len(links) && links[i] != "" {
			link = links[i]
		}
		if err := s.email.SendWorkflowNotice(ctx, recipient.Email, "Signature requested: "+title, "A document needs your signature", message, "Review and sign", link, email.BrandingInfo{CompanyName: company, LogoURL: logo, BrandColor: color}); err != nil {
			slog.Error("signature request email failed", "requestId", "created", "error", err)
		}
	}
}

func (s *Server) signSignatureRequest(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	var userEmail string
	_ = s.pool.QueryRow(c.Request().Context(), `SELECT email FROM users WHERE id = $1`, principal.UserID).Scan(&userEmail)

	var input struct {
		SignatureType string `json:"signatureType"`
		SignatureData string `json:"signatureData"`
		Consent       bool   `json:"consent"`
	}
	if err := c.Bind(&input); err != nil || !input.Consent {
		return problem(c, http.StatusUnprocessableEntity, "signatures.consent_required", "Consent required", "Provide your typed signature and consent to sign")
	}
	id := c.Param("id")
	if err := s.signatures.SignRequest(c.Request().Context(), principal.OrganizationID, principal.UserID, userEmail, id, input.SignatureType, input.SignatureData, c.RealIP(), c.Request().UserAgent()); err != nil {
		if errors.Is(err, signatures.ErrRecipientNotFound) {
			return problem(c, http.StatusNotFound, "signatures.recipient_not_found", "Not found", "This request is no longer available to your account")
		}
		return problem(c, http.StatusUnprocessableEntity, "signatures.sign_failed", "Could not sign document", err.Error())
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
		return problem(c, http.StatusUnprocessableEntity, "signatures.reason_required", "Reason required", "Provide a reason before declining this request")
	}

	id := c.Param("id")
	if err := s.signatures.DeclineRequest(c.Request().Context(), principal.OrganizationID, principal.UserID, userEmail, id, body.Reason, c.RealIP()); err != nil {
		if errors.Is(err, signatures.ErrRecipientNotFound) {
			return problem(c, http.StatusNotFound, "signatures.recipient_not_found", "Not found", "This request is no longer available to your account")
		}
		return problem(c, http.StatusInternalServerError, "signatures.decline_failed", "Internal error", err.Error())
	}

	return c.JSON(http.StatusOK, map[string]bool{"success": true})
}
