package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/easyhr/easyhr-be/internal/identity"
	"github.com/easyhr/easyhr-be/internal/payroll"
)

func (s *Server) listPayrollRuns(c echo.Context) error {
	p, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !p.HasPermission("payroll:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to view payroll runs")
	}
	items, err := s.payroll.ListRuns(c.Request().Context(), p.OrganizationID)
	if err != nil {
		slog.Error("list payroll runs failed", "error", err)
		return problem(c, http.StatusInternalServerError, "payroll.runs_failed", "Internal server error", "Could not load payroll runs")
	}
	return c.JSON(http.StatusOK, map[string]any{"data": items})
}

func (s *Server) discardPayrollRun(c echo.Context) error {
	p, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !p.HasPermission("payroll:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to manage payroll")
	}
	if err := s.payroll.DiscardRun(c.Request().Context(), p.OrganizationID, c.Param("runId")); err != nil {
		if errors.Is(err, payroll.ErrInvalidState) {
			return problem(c, http.StatusConflict, "payroll.invalid_state", "Run can't be discarded", "Only runs that haven't been approved can be discarded")
		}
		return problem(c, http.StatusInternalServerError, "payroll.discard_failed", "Internal server error", "Could not discard payroll run")
	}
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) updatePayrollTaxRuleStatus(c echo.Context) error {
	p, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !p.HasPermission("payroll:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to manage payroll")
	}
	var in struct {
		Active *bool `json:"active"`
	}
	if err := c.Bind(&in); err != nil || in.Active == nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", "Send {\"active\": true|false}")
	}
	if err := s.payroll.SetTaxRuleActive(c.Request().Context(), p.OrganizationID, c.Param("ruleId"), *in.Active); err != nil {
		if errors.Is(err, payroll.ErrNotFound) {
			return problem(c, http.StatusNotFound, "payroll.tax_rule_not_found", "Not found", "Deduction not found")
		}
		return problem(c, http.StatusInternalServerError, "payroll.tax_rule_failed", "Internal server error", "Could not update deduction")
	}
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) deletePayrollTaxRule(c echo.Context) error {
	p, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !p.HasPermission("payroll:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to manage payroll")
	}
	if err := s.payroll.DeleteTaxRule(c.Request().Context(), p.OrganizationID, c.Param("ruleId")); err != nil {
		if errors.Is(err, payroll.ErrNotFound) {
			return problem(c, http.StatusNotFound, "payroll.tax_rule_not_found", "Not found", "Deduction not found")
		}
		return problem(c, http.StatusInternalServerError, "payroll.tax_rule_failed", "Internal server error", "Could not delete deduction")
	}
	return c.NoContent(http.StatusNoContent)
}
