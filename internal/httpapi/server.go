package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/easyhr/easyhr-be/internal/attendance"
	"github.com/easyhr/easyhr-be/internal/auth"
	"github.com/easyhr/easyhr-be/internal/documents"
	"github.com/easyhr/easyhr-be/internal/identity"
	"github.com/easyhr/easyhr-be/internal/onboarding"
	"github.com/easyhr/easyhr-be/internal/payroll"
	"github.com/easyhr/easyhr-be/internal/people"
	"github.com/easyhr/easyhr-be/internal/tenancy"
	"github.com/easyhr/easyhr-be/internal/timeoff"
)

type Server struct {
	echo          *echo.Echo
	pool          *pgxpool.Pool
	api           *echo.Group
	authenticated *echo.Group
	identity      *identity.Resolver
	provisioner   *identity.Provisioner
	people        *people.Directory
	timeOff       *timeoff.Service
	payroll       *payroll.Service
	attendance    *attendance.Service
	onboarding    *onboarding.Service
	documents     *documents.Service
	setup         *tenancy.SetupService
	departments   *tenancy.DepartmentService
	divisions     *tenancy.DivisionService
}

func New(pool *pgxpool.Pool, allowedOrigins []string, authentication auth.Middleware, clerkSecretKey string) *Server {
	return newServer(pool, allowedOrigins, authentication, clerkSecretKey, nil)
}

func NewWithDocumentStorage(pool *pgxpool.Pool, allowedOrigins []string, authentication auth.Middleware, clerkSecretKey string, documentStorage documents.Storage) *Server {
	return newServer(pool, allowedOrigins, authentication, clerkSecretKey, documentStorage)
}

func newServer(pool *pgxpool.Pool, allowedOrigins []string, authentication auth.Middleware, clerkSecretKey string, documentStorage documents.Storage) *Server {
	e := echo.New()
	e.HideBanner = true
	e.HTTPErrorHandler = problemHandler
	e.Use(middleware.RequestID())
	e.Use(middleware.Recover())
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins:     allowedOrigins,
		AllowMethods:     []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodOptions},
		AllowHeaders:     []string{echo.HeaderAuthorization, echo.HeaderContentType, echo.HeaderXRequestID, "Idempotency-Key", "X-EasyHR-Workspace"},
		ExposeHeaders:    []string{echo.HeaderXRequestID},
		AllowCredentials: true,
		MaxAge:           86400,
	}))

	s := &Server{echo: e, pool: pool, identity: identity.NewResolver(pool), provisioner: identity.NewProvisioner(pool, clerkSecretKey), people: people.NewDirectory(pool), timeOff: timeoff.NewService(pool), attendance: attendance.NewService(pool), onboarding: onboarding.NewService(pool), documents: documents.NewService(pool, documentStorage), setup: tenancy.NewSetupService(pool), payroll: payroll.NewService(pool), departments: tenancy.NewDepartmentService(pool), divisions: tenancy.NewDivisionService(pool)}
	if authentication != nil {
		s.authenticated = e.Group("/api/v1")
		s.authenticated.Use(adaptHTTPMiddleware(authentication))
		s.api = e.Group("/api/v1")
		s.api.Use(adaptHTTPMiddleware(authentication))
		s.api.Use(adaptStandardMiddleware(s.identity.Middleware))
	}
	s.registerRoutes()
	return s
}

func adaptStandardMiddleware(wrap func(http.Handler) http.Handler) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			var handlerError error
			wrap(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				c.SetRequest(request)
				handlerError = next(c)
			})).ServeHTTP(c.Response(), c.Request())
			return handlerError
		}
	}
}

func adaptHTTPMiddleware(wrap auth.Middleware) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			var handlerError error
			if requestID := c.Response().Header().Get(echo.HeaderXRequestID); requestID != "" {
				c.Request().Header.Set(echo.HeaderXRequestID, requestID)
			}
			wrap(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				c.SetRequest(request)
				handlerError = next(c)
			})).ServeHTTP(c.Response(), c.Request())
			return handlerError
		}
	}
}

func (s *Server) Handler() http.Handler { return s.echo }

func (s *Server) registerRoutes() {
	s.echo.GET("/healthz", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})
	s.echo.GET("/readyz", s.readiness)
	if s.api != nil {
		s.api.GET("/me", s.getMe)
		s.api.GET("/employees", s.listEmployees)
		s.api.GET("/employees/:employeeId", s.getEmployee)
		s.api.PATCH("/employees/:employeeId", s.updateEmployee)
		s.api.GET("/departments", s.listDepartments)
		s.api.POST("/departments", s.createDepartment)
		s.api.GET("/divisions", s.listDivisions)
		s.api.POST("/divisions", s.createDivision)
		s.api.POST("/employees", s.createEmployee)
		s.api.POST("/leave/requests", s.submitLeaveRequest)
		s.api.GET("/leave/requests", s.listLeaveRequests)
		s.api.GET("/leave/balances", s.listLeaveBalances)
		s.api.POST("/leave/types", s.createLeaveType)
		s.api.POST("/leave/policies", s.createLeavePolicy)
		s.api.POST("/leave/policies/:policyId/assignments", s.assignLeavePolicy)
		s.api.POST("/attendance/punches", s.createAttendancePunch)
		s.api.GET("/attendance/punches", s.listAttendancePunches)
		s.api.GET("/attendance/live", s.getLiveAttendance)
		s.api.POST("/documents/upload-intents", s.createDocumentUploadIntent)
		s.api.GET("/documents", s.listDocuments)
		s.api.POST("/documents/:documentId/complete", s.completeDocumentUpload)
		s.api.GET("/documents/:documentId/download-url", s.getDocumentDownloadUrl)
		s.api.POST("/attendance/punches/:punchId/corrections", s.createAttendanceCorrection)
		s.api.POST("/onboarding/templates", s.createOnboardingTemplate)
		s.api.GET("/onboarding/templates", s.listOnboardingTemplates)
		s.api.POST("/onboarding/templates/:templateId/publish", s.publishOnboardingTemplate)
		s.api.POST("/onboarding/instances", s.initiateOnboarding)
		s.api.PATCH("/onboarding/tasks/:taskId", s.updateOnboardingTask)
		s.api.POST("/leave/requests/:requestId/approve", s.approveLeaveRequest)
		s.api.POST("/leave/requests/:requestId/approve-hr", s.approveLeaveRequestHR)
		s.api.POST("/leave/requests/:requestId/decline", s.declineLeaveRequest)
		s.api.POST("/leave/requests/:requestId/cancel", s.cancelLeaveRequest)
		s.authenticated.POST("/setup/organizations", s.createOrganization)
		s.authenticated.POST("/auth/provision", s.provisionIdentity)
	}
}

func (s *Server) listDivisions(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("organization:read") && !principal.HasPermission("employees:read") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to view divisions")
	}
	result, err := s.divisions.List(c.Request().Context(), principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "divisions.lookup_failed", "Internal server error", "Could not retrieve divisions")
	}
	return c.JSON(http.StatusOK, map[string]any{"data": result})
}

func (s *Server) createDivision(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to manage divisions")
	}
	var input tenancy.CreateDivisionInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	result, err := s.divisions.Create(c.Request().Context(), principal.OrganizationID, principal.UserID, input)
	if errors.Is(err, tenancy.ErrDivisionConflict) {
		return problem(c, http.StatusConflict, "divisions.conflict", "Division conflict", "A division with this name or code already exists")
	}
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_division", "Invalid division", err.Error())
	}
	return c.JSON(http.StatusCreated, result)
}

func (s *Server) listDepartments(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("organization:read") && !principal.HasPermission("employees:read") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to view departments")
	}
	result, err := s.departments.List(c.Request().Context(), principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "departments.lookup_failed", "Internal server error", "Could not retrieve departments")
	}
	return c.JSON(http.StatusOK, map[string]any{"data": result})
}

func (s *Server) createDepartment(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to manage departments")
	}
	var input tenancy.CreateDepartmentInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	result, err := s.departments.Create(c.Request().Context(), principal.OrganizationID, principal.UserID, input)
	if errors.Is(err, tenancy.ErrDepartmentNotFound) {
		return problem(c, http.StatusNotFound, "departments.reference_not_found", "Not found", "The division or parent department was not found")
	}
	if errors.Is(err, tenancy.ErrDepartmentConflict) {
		return problem(c, http.StatusConflict, "departments.conflict", "Department conflict", "A department with this name or code already exists")
	}
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_department", "Invalid department", err.Error())
	}
	return c.JSON(http.StatusCreated, result)
}

func (s *Server) updateEmployee(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("employees:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to update employee records")
	}
	var input people.UpdateEmployeeInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	result, err := s.people.Update(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Param("employeeId"), input)
	if errors.Is(err, people.ErrEmployeeNotFound) {
		return problem(c, http.StatusNotFound, "employees.not_found", "Not found", "The employee was not found")
	}
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_employee_update", "Invalid employee update", err.Error())
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) getEmployee(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	employeeID := c.Param("employeeId")
	isSelf, err := s.people.IsEmployeeUser(c.Request().Context(), principal.OrganizationID, employeeID, principal.UserID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "employee.access_lookup_failed", "Internal server error", "Could not verify employee access")
	}
	if !isSelf && !principal.HasPermission("employees:read") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You may only view your own employee record")
	}
	result, err := s.people.Get(c.Request().Context(), principal.OrganizationID, employeeID)
	if errors.Is(err, people.ErrEmployeeNotFound) {
		return problem(c, http.StatusNotFound, "employees.not_found", "Not found", "The employee was not found")
	}
	if err != nil {
		return problem(c, http.StatusInternalServerError, "employees.lookup_failed", "Internal server error", "Could not retrieve the employee")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listDocuments(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	employeeID := strings.TrimSpace(c.QueryParam("employeeId"))
	if employeeID == "" {
		return problem(c, http.StatusUnprocessableEntity, "validation.employee_id_required", "Employee required", "employeeId is required")
	}
	isSelf, err := s.people.IsEmployeeUser(c.Request().Context(), principal.OrganizationID, employeeID, principal.UserID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "documents.access_lookup_failed", "Internal server error", "Could not verify employee access")
	}
	if !isSelf && !principal.HasPermission("documents:read") && !principal.HasPermission("documents:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You may only view your own documents")
	}
	limit := 25
	if raw := c.QueryParam("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil {
			return problem(c, http.StatusUnprocessableEntity, "validation.invalid_limit", "Invalid limit", "limit must be an integer")
		}
	}
	result, err := s.documents.List(c.Request().Context(), principal.OrganizationID, employeeID, limit)
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_document_filter", "Invalid document filter", err.Error())
	}
	return c.JSON(http.StatusOK, map[string]any{"data": result})
}

func (s *Server) createDocumentUploadIntent(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("documents:upload") && !principal.HasPermission("documents:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to upload employee documents")
	}
	var input documents.CreateUploadIntentInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	result, err := s.documents.CreateUploadIntent(c.Request().Context(), principal.OrganizationID, principal.UserID, input)
	if errors.Is(err, documents.ErrDocumentNotFound) {
		return problem(c, http.StatusNotFound, "documents.employee_not_found", "Not found", "The employee was not found")
	}
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_document_upload", "Invalid document upload", err.Error())
	}
	return c.JSON(http.StatusCreated, result)
}

func (s *Server) completeDocumentUpload(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("documents:upload") && !principal.HasPermission("documents:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to complete employee document uploads")
	}
	var body struct {
		ChecksumSHA256 string `json:"checksumSha256"`
	}
	if err := c.Bind(&body); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	result, err := s.documents.CompleteUpload(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Param("documentId"), body.ChecksumSHA256)
	if errors.Is(err, documents.ErrDocumentNotFound) {
		return problem(c, http.StatusNotFound, "documents.not_found", "Not found", "The document was not found")
	}
	if errors.Is(err, documents.ErrDocumentNotCompletable) {
		return problem(c, http.StatusConflict, "documents.invalid_upload_state", "Document upload state conflict", "The document is not awaiting a valid upload completion")
	}
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_document_completion", "Invalid document completion", err.Error())
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listAttendancePunches(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	employeeID := strings.TrimSpace(c.QueryParam("employeeId"))
	if employeeID == "" {
		return problem(c, http.StatusUnprocessableEntity, "validation.employee_id_required", "Employee required", "employeeId is required")
	}
	isSelf, err := s.people.IsEmployeeUser(c.Request().Context(), principal.OrganizationID, employeeID, principal.UserID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "attendance.access_lookup_failed", "Internal server error", "Could not verify employee access")
	}
	if !isSelf && !principal.HasPermission("attendance:read_team") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You may only view your own attendance punches")
	}
	limit := 25
	if raw := c.QueryParam("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil {
			return problem(c, http.StatusUnprocessableEntity, "validation.invalid_limit", "Invalid limit", "limit must be an integer")
		}
	}
	result, err := s.attendance.ListPunches(c.Request().Context(), principal.OrganizationID, attendance.ListPunchesInput{EmployeeID: employeeID, From: c.QueryParam("from"), To: c.QueryParam("to"), Cursor: c.QueryParam("cursor"), Limit: limit})
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_attendance_filter", "Invalid attendance filter", err.Error())
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) cancelLeaveRequest(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	result, err := s.timeOff.Cancel(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Param("requestId"))
	if err != nil {
		switch {
		case errors.Is(err, timeoff.ErrDecisionNotFound):
			return problem(c, http.StatusNotFound, "leave.request_not_found", "Not found", "The leave request was not found")
		case errors.Is(err, timeoff.ErrCancellationForbidden):
			return problem(c, http.StatusForbidden, "authorization.self_service_only", "Forbidden", "You may cancel only your own leave request")
		case errors.Is(err, timeoff.ErrRequestConflict):
			return problem(c, http.StatusConflict, "leave.invalid_state", "Leave request state conflict", "Only pending leave requests can be cancelled")
		default:
			return problem(c, http.StatusUnprocessableEntity, "validation.invalid_cancellation", "Invalid cancellation", err.Error())
		}
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) declineLeaveRequest(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("leave:approve_manager") && !principal.HasPermission("leave:approve_hr") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to decline leave requests")
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := c.Bind(&body); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	result, err := s.timeOff.Decline(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Param("requestId"), body.Reason, principal.HasPermission("leave:approve_manager"), principal.HasPermission("leave:approve_hr"))
	if err != nil {
		switch {
		case errors.Is(err, timeoff.ErrDecisionNotFound):
			return problem(c, http.StatusNotFound, "leave.request_not_found", "Not found", "The leave request was not found")
		case errors.Is(err, timeoff.ErrDecisionForbidden):
			return problem(c, http.StatusForbidden, "authorization.not_direct_manager", "Forbidden", "You are not permitted to decide this leave request")
		case errors.Is(err, timeoff.ErrRequestConflict):
			return problem(c, http.StatusConflict, "leave.invalid_state", "Leave request state conflict", "The leave request cannot be declined from its current state")
		default:
			return problem(c, http.StatusUnprocessableEntity, "validation.invalid_decline", "Invalid decline", err.Error())
		}
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listLeaveRequests(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	limit := 25
	if raw := c.QueryParam("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return problem(c, http.StatusUnprocessableEntity, "validation.invalid_limit", "Invalid limit", "limit must be an integer")
		}
		limit = parsed
	}
	result, err := s.timeOff.ListRequests(c.Request().Context(), principal.OrganizationID, principal.UserID, timeoff.ListRequestsInput{
		EmployeeID:        strings.TrimSpace(c.QueryParam("employeeId")),
		Status:            c.QueryParam("status"),
		Cursor:            c.QueryParam("cursor"),
		Limit:             limit,
		CanApproveManager: principal.HasPermission("leave:approve_manager"),
		CanApproveHR:      principal.HasPermission("leave:approve_hr"),
	})
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_leave_request_filter", "Invalid leave request filter", err.Error())
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listLeaveBalances(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	employeeID := strings.TrimSpace(c.QueryParam("employeeId"))
	if employeeID == "" {
		return problem(c, http.StatusUnprocessableEntity, "validation.employee_id_required", "Employee required", "employeeId is required")
	}
	isOwnEmployee, err := s.people.IsEmployeeUser(c.Request().Context(), principal.OrganizationID, employeeID, principal.UserID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "leave.balance_lookup_failed", "Internal server error", "Could not verify employee access")
	}
	if !isOwnEmployee && !principal.HasPermission("leave:request_any") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You may only view your own leave balances")
	}
	result, err := s.timeOff.ListBalances(c.Request().Context(), principal.OrganizationID, employeeID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "leave.balance_lookup_failed", "Internal server error", "Could not retrieve leave balances")
	}
	return c.JSON(http.StatusOK, map[string]any{"data": result})
}

func (s *Server) listOnboardingTemplates(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	canManage := principal.HasPermission("onboarding:manage_templates")
	if !canManage && !principal.HasPermission("onboarding:initiate") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to view onboarding templates")
	}
	status := c.QueryParam("status")
	if status == "" && !canManage {
		status = "published"
	}
	result, err := s.onboarding.ListTemplates(c.Request().Context(), principal.OrganizationID, status)
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_onboarding_template_filter", "Invalid template filter", err.Error())
	}
	return c.JSON(http.StatusOK, map[string]any{"data": result})
}

func (s *Server) updateOnboardingTask(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	var input onboarding.UpdateTaskInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	result, err := s.onboarding.UpdateTask(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Param("taskId"), input, principal.HasPermission("onboarding:complete_tasks"))
	if errors.Is(err, onboarding.ErrTaskNotFound) {
		return problem(c, http.StatusNotFound, "onboarding.task_not_found", "Not found", "The onboarding task was not found")
	}
	if errors.Is(err, onboarding.ErrTaskForbidden) {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You are not assigned to this onboarding task")
	}
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_onboarding_task", "Invalid onboarding task", err.Error())
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) publishOnboardingTemplate(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("onboarding:manage_templates") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to manage onboarding templates")
	}
	result, err := s.onboarding.PublishTemplate(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Param("templateId"))
	if errors.Is(err, onboarding.ErrTemplateNotFound) {
		return problem(c, http.StatusNotFound, "onboarding.template_not_found", "Not found", "The onboarding template was not found")
	}
	if errors.Is(err, onboarding.ErrTemplateNotPublishable) {
		return problem(c, http.StatusConflict, "onboarding.template_not_publishable", "Template cannot be published", "Only a draft template with at least one task can be published")
	}
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "onboarding.template_publish_failed", "Template could not be published", err.Error())
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) initiateOnboarding(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("onboarding:initiate") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to initiate onboarding")
	}
	var input onboarding.InitiateInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	result, err := s.onboarding.Initiate(c.Request().Context(), principal.OrganizationID, principal.UserID, input)
	if errors.Is(err, onboarding.ErrInitiationNotFound) {
		return problem(c, http.StatusNotFound, "onboarding.employee_or_template_not_found", "Not found", "The employee or published onboarding template was not found")
	}
	if errors.Is(err, onboarding.ErrInitiationConflict) {
		return problem(c, http.StatusConflict, "onboarding.instance_conflict", "Onboarding conflict", "An onboarding instance already exists for this employee, template and start date")
	}
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_onboarding_instance", "Invalid onboarding instance", err.Error())
	}
	return c.JSON(http.StatusCreated, result)
}

func (s *Server) createOnboardingTemplate(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("onboarding:manage_templates") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to manage onboarding templates")
	}
	var input onboarding.CreateTemplateInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	result, err := s.onboarding.CreateTemplate(c.Request().Context(), principal.OrganizationID, principal.UserID, input)
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_onboarding_template", "Invalid onboarding template", err.Error())
	}
	return c.JSON(http.StatusCreated, result)
}

func (s *Server) createAttendanceCorrection(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("attendance:correct") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to correct attendance punches")
	}
	var input attendance.CreateCorrectionInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	result, err := s.attendance.CreateCorrection(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Param("punchId"), input)
	if errors.Is(err, attendance.ErrPunchNotFound) {
		return problem(c, http.StatusNotFound, "attendance.punch_not_found", "Not found", "The attendance punch was not found")
	}
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_correction", "Invalid attendance correction", err.Error())
	}
	return c.JSON(http.StatusCreated, result)
}

func (s *Server) createAttendancePunch(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("attendance:punch_self") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to record attendance punches")
	}
	var input attendance.CreatePunchInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	result, err := s.attendance.CreateSelfPunch(c.Request().Context(), principal.OrganizationID, principal.UserID, input)
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_punch", "Invalid attendance punch", err.Error())
	}
	return c.JSON(http.StatusCreated, result)
}

func (s *Server) createLeavePolicy(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("leave:manage_policies") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to manage leave policies")
	}
	var input timeoff.CreatePolicyInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	result, err := s.timeOff.CreatePolicy(c.Request().Context(), principal.OrganizationID, principal.UserID, input)
	if errors.Is(err, timeoff.ErrRequestNotFound) {
		return problem(c, http.StatusNotFound, "leave.type_not_found", "Not found", "The active leave type was not found")
	}
	if errors.Is(err, timeoff.ErrLeavePolicyConflict) {
		return problem(c, http.StatusConflict, "leave.policy_conflict", "Leave policy conflict", "A policy with this name and date already exists")
	}
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_leave_policy", "Invalid leave policy", err.Error())
	}
	return c.JSON(http.StatusCreated, result)
}
func (s *Server) assignLeavePolicy(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("leave:manage_policies") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to assign leave policies")
	}
	var input timeoff.AssignPolicyInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	if err := s.timeOff.AssignPolicy(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Param("policyId"), input); errors.Is(err, timeoff.ErrRequestNotFound) {
		return problem(c, http.StatusNotFound, "leave.policy_or_employee_not_found", "Not found", "The active policy or employee was not found")
	} else if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_policy_assignment", "Invalid policy assignment", err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) createLeaveType(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("leave:manage_policies") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to manage leave types")
	}
	var input timeoff.CreateLeaveTypeInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	result, err := s.timeOff.CreateLeaveType(c.Request().Context(), principal.OrganizationID, principal.UserID, input)
	if errors.Is(err, timeoff.ErrLeaveTypeConflict) {
		return problem(c, http.StatusConflict, "leave.type_conflict", "Leave type conflict", "A leave type with this code or name already exists")
	}
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_leave_type", "Invalid leave type", err.Error())
	}
	return c.JSON(http.StatusCreated, result)
}

func (s *Server) approveLeaveRequestHR(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("leave:approve_hr") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to complete HR leave approvals")
	}
	var body struct {
		Comment string `json:"comment"`
	}
	if err := c.Bind(&body); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	result, err := s.timeOff.ApproveHR(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Param("requestId"), body.Comment)
	if err != nil {
		switch {
		case errors.Is(err, timeoff.ErrDecisionNotFound):
			return problem(c, http.StatusNotFound, "leave.request_not_found", "Not found", "The leave request was not found")
		case errors.Is(err, timeoff.ErrRequestConflict):
			return problem(c, http.StatusConflict, "leave.invalid_state", "Leave request state conflict", "The leave request cannot be completed from its current state")
		default:
			return problem(c, http.StatusUnprocessableEntity, "validation.invalid_decision", "Invalid decision", err.Error())
		}
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) approveLeaveRequest(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("leave:approve_manager") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to approve manager leave requests")
	}
	var body struct {
		Comment string `json:"comment"`
	}
	if err := c.Bind(&body); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	result, err := s.timeOff.ApproveManager(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Param("requestId"), body.Comment)
	if err != nil {
		switch {
		case errors.Is(err, timeoff.ErrDecisionNotFound):
			return problem(c, http.StatusNotFound, "leave.request_not_found", "Not found", "The leave request was not found")
		case errors.Is(err, timeoff.ErrRequestConflict):
			return problem(c, http.StatusConflict, "leave.invalid_state", "Leave request state conflict", "The leave request is no longer waiting for manager approval")
		case strings.Contains(err.Error(), "direct manager"):
			return problem(c, http.StatusForbidden, "authorization.not_direct_manager", "Forbidden", "Only the employee's direct manager may approve this request")
		default:
			return problem(c, http.StatusUnprocessableEntity, "validation.invalid_decision", "Invalid decision", err.Error())
		}
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) submitLeaveRequest(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	var input timeoff.SubmitRequestInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	if !principal.HasPermission("leave:request_any") {
		if !principal.HasPermission("leave:request_self") {
			return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to submit leave requests")
		}
		isSelf, err := s.people.IsEmployeeUser(c.Request().Context(), principal.OrganizationID, input.EmployeeID, principal.UserID)
		if err != nil {
			return err
		}
		if !isSelf {
			return problem(c, http.StatusForbidden, "authorization.self_service_only", "Forbidden", "You may submit leave only for your own employee record")
		}
	}
	result, err := s.timeOff.Submit(c.Request().Context(), principal.OrganizationID, principal.UserID, input)
	if err != nil {
		switch {
		case errors.Is(err, timeoff.ErrRequestNotFound):
			return problem(c, http.StatusNotFound, "leave.policy_not_found", "Not found", "The employee or eligible leave policy was not found")
		case strings.Contains(err.Error(), "insufficient"):
			return problem(c, http.StatusConflict, "leave.insufficient_balance", "Leave balance is insufficient", err.Error())
		default:
			return problem(c, http.StatusUnprocessableEntity, "validation.invalid_leave_request", "Invalid request", err.Error())
		}
	}
	return c.JSON(http.StatusCreated, result)
}

func (s *Server) createEmployee(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("employees:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to create employee records")
	}
	var input people.CreateEmployeeInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	result, err := s.people.Create(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Response().Header().Get(echo.HeaderXRequestID), c.Request().Header.Get("Idempotency-Key"), input)
	if err != nil {
		switch {
		case errors.Is(err, people.ErrCreateConflict):
			return problem(c, http.StatusConflict, "employee.conflict", "Employee conflict", "Employee number or work email already exists")
		case errors.Is(err, people.ErrIdempotencyReused):
			return problem(c, http.StatusConflict, "idempotency.key_reused", "Idempotency key conflict", "The Idempotency-Key was previously used for a different request")
		default:
			return problem(c, http.StatusUnprocessableEntity, "validation.invalid_employee", "Invalid employee", "Employee data or referenced organization record is invalid")
		}
	}
	return c.JSON(http.StatusCreated, result)
}

func (s *Server) listEmployees(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("employees:read") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to view employee records")
	}
	limit := 25
	if rawLimit := c.QueryParam("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil {
			return problem(c, http.StatusUnprocessableEntity, "validation.invalid_limit", "Invalid request", "limit must be an integer")
		}
		limit = parsed
	}
	items, err := s.people.List(c.Request().Context(), principal.OrganizationID, c.QueryParam("search"), c.QueryParam("status"), limit)
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_filter", "Invalid request", err.Error())
	}
	return c.JSON(http.StatusOK, map[string]any{
		"data": items,
		"page": map[string]any{"nextCursor": nil, "limit": limit, "hasMore": false},
	})
}

func (s *Server) provisionIdentity(c echo.Context) error {
	claims, ok := clerk.SessionClaimsFromContext(c.Request().Context())
	if !ok || claims == nil || claims.Subject == "" {
		return problem(c, http.StatusUnauthorized, "auth.missing_token", "Unauthorized", "A verified session is required")
	}
	if err := s.provisioner.Provision(c.Request().Context(), claims.Subject); err != nil {
		switch {
		case errors.Is(err, identity.ErrIdentityProfileIncomplete):
			return problem(c, http.StatusConflict, "identity.profile_incomplete", "Account profile incomplete", "A verified primary email, first name, and last name are required")
		case errors.Is(err, identity.ErrIdentityEmailConflict):
			return problem(c, http.StatusConflict, "identity.email_conflict", "Account conflict", "This email is already linked to another EasyHR account")
		default:
			return err
		}
	}
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) createOrganization(c echo.Context) error {
	claims, ok := clerk.SessionClaimsFromContext(c.Request().Context())
	if !ok || claims == nil || claims.Subject == "" {
		return problem(c, http.StatusUnauthorized, "auth.missing_token", "Unauthorized", "A verified session is required")
	}
	var input tenancy.OrganizationSetupInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	ctx := tenancy.WithRequestID(c.Request().Context(), c.Response().Header().Get(echo.HeaderXRequestID))
	result, err := s.setup.CreateOrganization(ctx, claims.Subject, c.Request().Header.Get("Idempotency-Key"), input)
	if err != nil {
		switch {
		case errors.Is(err, tenancy.ErrSetupUserNotProvisioned):
			return problem(c, http.StatusConflict, "identity.not_provisioned", "Account not ready", "The authenticated account has not been provisioned by the identity sync")
		case errors.Is(err, tenancy.ErrSetupConflict):
			return problem(c, http.StatusConflict, "organization.conflict", "Organization setup conflict", "The requested workspace or organization data already exists")
		case errors.Is(err, tenancy.ErrIdempotencyKeyReused):
			return problem(c, http.StatusConflict, "idempotency.key_reused", "Idempotency key conflict", "The Idempotency-Key was previously used for a different request")
		case strings.Contains(err.Error(), "must ") || strings.Contains(err.Error(), "not currently supported") || strings.Contains(err.Error(), "unsupported platform"):
			return problem(c, http.StatusUnprocessableEntity, "validation.invalid_field", "Invalid request", err.Error())
		default:
			return err
		}
	}
	return c.JSON(http.StatusCreated, result)
}

func (s *Server) getMe(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	permissions := make([]string, 0, len(principal.Permissions))
	for permission := range principal.Permissions {
		permissions = append(permissions, permission)
	}
	sort.Strings(permissions)
	return c.JSON(http.StatusOK, map[string]any{
		"userId":         principal.UserID,
		"membershipId":   principal.MembershipID,
		"organizationId": principal.OrganizationID,
		"permissions":    permissions,
	})
}

func (s *Server) readiness(c echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
	defer cancel()
	if err := s.pool.Ping(ctx); err != nil {
		return problem(c, http.StatusServiceUnavailable, "service.not_ready", "Service unavailable", "Database is not ready")
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ready"})
}

type problemResponse struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Code      string `json:"code"`
	Detail    string `json:"detail,omitempty"`
	RequestID string `json:"requestId"`
}

func problem(c echo.Context, status int, code, title, detail string) error {
	return c.JSON(status, problemResponse{
		Type:      "https://docs.easyhr.example/errors/" + strings.ReplaceAll(code, ".", "-"),
		Title:     title,
		Status:    status,
		Code:      code,
		Detail:    detail,
		RequestID: c.Response().Header().Get(echo.HeaderXRequestID),
	})
}

func problemHandler(err error, c echo.Context) {
	if c.Response().Committed {
		return
	}
	var httpErr *echo.HTTPError
	if errors.As(err, &httpErr) {
		message, _ := httpErr.Message.(string)
		if message == "" {
			message = http.StatusText(httpErr.Code)
		}
		_ = problem(c, httpErr.Code, "http.request_failed", http.StatusText(httpErr.Code), message)
		return
	}
	_ = problem(c, http.StatusInternalServerError, "internal.unexpected", "Internal server error", "An unexpected error occurred")
}

func (s *Server) getLiveAttendance(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !principal.HasPermission("attendance:read_team") && !principal.HasPermission("attendance:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to view live attendance")
	}
	var input attendance.LiveAttendanceInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", err.Error())
	}
	result, err := s.attendance.GetLiveAttendance(c.Request().Context(), principal.OrganizationID, input)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "attendance.live_failed", "Internal server error", "Could not load live attendance")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) getDocumentDownloadUrl(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	// Simplified permission check: in a real app we'd verify the user can see this specific document based on employee link
	if !principal.HasPermission("documents:read") && !principal.HasPermission("documents:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to view documents")
	}
	result, err := s.documents.GetDownloadUrl(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Param("documentId"))
	if err != nil {
		return problem(c, http.StatusNotFound, "documents.download_failed", "Not found", "Document could not be downloaded")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listPayrollConfigurations(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok { return c.NoContent(http.StatusUnauthorized) }
	if !principal.HasPermission("payroll:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to view payroll configurations")
	}
	result, err := s.payroll.ListConfigurations(c.Request().Context(), principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "payroll.list_failed", "Internal server error", "Could not load payroll configurations")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listPayrollPeriods(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok { return c.NoContent(http.StatusUnauthorized) }
	if !principal.HasPermission("payroll:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to view payroll periods")
	}
	result, err := s.payroll.ListPeriods(c.Request().Context(), principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "payroll.periods_failed", "Internal server error", "Could not load payroll periods")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) createPayrollPeriod(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok { return c.NoContent(http.StatusUnauthorized) }
	if !principal.HasPermission("payroll:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to manage payroll")
	}
	var input payroll.CreatePeriodInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", err.Error())
	}
	result, err := s.payroll.CreatePeriod(c.Request().Context(), principal.OrganizationID, principal.UserID, input)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "payroll.period_create_failed", "Internal server error", "Could not create payroll period")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) createPayrollRun(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok { return c.NoContent(http.StatusUnauthorized) }
	if !principal.HasPermission("payroll:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to manage payroll")
	}
	result, err := s.payroll.InitiateRun(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Param("periodId"))
	if err != nil {
		if errors.Is(err, payroll.ErrNotFound) {
			return problem(c, http.StatusNotFound, "payroll.period_not_found", "Not found", "Payroll period not found")
		}
		return problem(c, http.StatusInternalServerError, "payroll.run_failed", "Internal server error", "Could not initiate payroll run")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) lockPayrollRun(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok { return c.NoContent(http.StatusUnauthorized) }
	if !principal.HasPermission("payroll:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to manage payroll")
	}
	err := s.payroll.LockRun(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Param("runId"))
	if err != nil {
		if errors.Is(err, payroll.ErrInvalidState) {
			return problem(c, http.StatusConflict, "payroll.invalid_state", "Conflict", "Run cannot be locked in its current state")
		}
		return problem(c, http.StatusInternalServerError, "payroll.lock_failed", "Internal server error", "Could not lock payroll run")
	}
	return c.NoContent(http.StatusNoContent)
}
