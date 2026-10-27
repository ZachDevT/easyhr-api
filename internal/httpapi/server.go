package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/easyhr/easyhr-be/internal/announcements"
	"github.com/easyhr/easyhr-be/internal/attendance"
	"github.com/easyhr/easyhr-be/internal/auth"
	"github.com/easyhr/easyhr-be/internal/benefits"
	"github.com/easyhr/easyhr-be/internal/celebrations"
	"github.com/easyhr/easyhr-be/internal/chat"
	"github.com/easyhr/easyhr-be/internal/documents"
	"github.com/easyhr/easyhr-be/internal/email"
	"github.com/easyhr/easyhr-be/internal/hiring"
	"github.com/easyhr/easyhr-be/internal/holidays"
	"github.com/easyhr/easyhr-be/internal/identity"
	"github.com/easyhr/easyhr-be/internal/messaging"
	"github.com/easyhr/easyhr-be/internal/notifications"
	"github.com/easyhr/easyhr-be/internal/offboarding"
	"github.com/easyhr/easyhr-be/internal/onboarding"
	"github.com/easyhr/easyhr-be/internal/operations"
	"github.com/easyhr/easyhr-be/internal/payroll"
	"github.com/easyhr/easyhr-be/internal/people"
	"github.com/easyhr/easyhr-be/internal/performance"
	"github.com/easyhr/easyhr-be/internal/platform"
	"github.com/easyhr/easyhr-be/internal/policies"
	"github.com/easyhr/easyhr-be/internal/promotions"
	"github.com/easyhr/easyhr-be/internal/relations"
	"github.com/easyhr/easyhr-be/internal/scheduling"
	"github.com/easyhr/easyhr-be/internal/signatures"
	"github.com/easyhr/easyhr-be/internal/support"
	"github.com/easyhr/easyhr-be/internal/tenancy"
	"github.com/easyhr/easyhr-be/internal/timeoff"
	"github.com/easyhr/easyhr-be/internal/workflows"
)

type Server struct {
	echo          *echo.Echo
	publicLimit   echo.MiddlewareFunc
	pool          *pgxpool.Pool
	api           *echo.Group
	authenticated *echo.Group
	identity      *identity.Resolver
	provisioner   *identity.Provisioner
	people        *people.Directory
	timeOff       *timeoff.Service
	payroll       *payroll.Service
	signatures    *signatures.Service
	relations     *relations.Service
	hiring        *hiring.Service
	performance   *performance.Service
	workflows     *workflows.Service
	benefits      *benefits.Service
	promotions    *promotions.Service
	scheduling    *scheduling.Service
	holidays      *holidays.Service
	offboarding   *offboarding.Service
	policies      *policies.Service
	platform      *platform.Service
	support       *support.Service
	operations    *operations.Service
	attendance    *attendance.Service
	onboarding    *onboarding.Service
	documents     *documents.Service
	storage       documents.Storage
	setup         *tenancy.SetupService
	departments   *tenancy.DepartmentService
	divisions     *tenancy.DivisionService
	email         *email.Service
	messaging     *messaging.Service
	announcements *announcements.Service
	celebrations  *celebrations.Service
	notifications *notifications.Service
	chat          *chat.Service
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
	// Keep abuse controls local to unauthenticated public endpoints so normal
	// authenticated HR workflows are not throttled by anonymous traffic.
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOriginFunc: func(origin string) (bool, error) {
			if origin == "" {
				return false, nil
			}
			if origin == "https://easyhr.pro" || strings.HasSuffix(origin, ".easyhr.pro") {
				return true, nil
			}
			if strings.HasPrefix(origin, "http://localhost:") || strings.HasPrefix(origin, "https://localhost:") {
				return true, nil
			}
			if strings.HasSuffix(origin, ".vercel.app") {
				return true, nil
			}
			for _, allowed := range allowedOrigins {
				if origin == allowed {
					return true, nil
				}
			}
			return false, nil
		},
		AllowMethods:     []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete, http.MethodOptions},
		AllowHeaders:     []string{echo.HeaderAuthorization, echo.HeaderContentType, echo.HeaderXRequestID, "Idempotency-Key", "X-EasyHR-Workspace"},
		ExposeHeaders:    []string{echo.HeaderXRequestID},
		AllowCredentials: true,
		MaxAge:           86400,
	}))

	emailSvc := email.NewService(os.Getenv("RESEND_API_KEY"), os.Getenv("EMAIL_FROM"), os.Getenv("EMAIL_FROM_NAME"))
	chatHub := chat.NewHub(pool, slog.Default(), nil)
	chatSvc := chat.NewService(pool, chatHub, documentStorage, os.Getenv("CHAT_SIGNING_SECRET"), slog.Default())
	chatHub.StartPostgresListener(context.Background())
	s := &Server{echo: e, pool: pool, identity: identity.NewResolver(pool), provisioner: identity.NewProvisioner(pool, clerkSecretKey), people: people.NewDirectory(pool), timeOff: timeoff.NewService(pool), attendance: attendance.NewService(pool), onboarding: onboarding.NewService(pool), documents: documents.NewService(pool, documentStorage), storage: documentStorage, setup: tenancy.NewSetupService(pool), payroll: payroll.NewService(pool), signatures: signatures.NewService(pool), relations: relations.NewService(pool), hiring: hiring.NewService(pool), performance: performance.NewService(pool), workflows: workflows.NewService(pool), benefits: benefits.NewService(pool), promotions: promotions.NewService(pool), scheduling: scheduling.NewService(pool), holidays: holidays.NewService(pool), offboarding: offboarding.NewService(pool), policies: policies.NewService(pool), platform: platform.NewService(pool, "fallback_secret"), support: support.NewService(pool), operations: operations.NewService(pool), departments: tenancy.NewDepartmentService(pool), divisions: tenancy.NewDivisionService(pool), email: emailSvc, messaging: messaging.NewService(pool, emailSvc), announcements: announcements.NewService(pool), celebrations: celebrations.NewService(pool), notifications: notifications.NewService(pool), chat: chatSvc}
	s.publicLimit = s.databaseRateLimit(20, time.Minute)
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
			if token := c.QueryParam("token"); token != "" && c.Request().Header.Get("Authorization") == "" {
				c.Request().Header.Set("Authorization", "Bearer "+token)
			}
			if ws := c.QueryParam("workspace"); ws != "" && c.Request().Header.Get("X-EasyHR-Workspace") == "" {
				c.Request().Header.Set("X-EasyHR-Workspace", ws)
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

func (s *Server) Close() {
	if s.chat != nil && s.chat.Hub() != nil {
		s.chat.Hub().Close()
	}
}

func (s *Server) databaseRateLimit(limit int, window time.Duration) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			key := c.RealIP() + "|" + c.Path() + "|" + c.Request().Method
			digest := sha256.Sum256([]byte(key))
			windowSeconds := int64(window.Seconds())
			if windowSeconds < 1 {
				windowSeconds = 60
			}
			var count int
			err := s.pool.QueryRow(c.Request().Context(), `INSERT INTO public_rate_limits (key_hash,window_started,request_count) VALUES ($1,to_timestamp(floor(extract(epoch from now())/$2)*$2),1) ON CONFLICT (key_hash,window_started) DO UPDATE SET request_count=public_rate_limits.request_count+1 RETURNING request_count`, hex.EncodeToString(digest[:]), windowSeconds).Scan(&count)
			if err != nil {
				slog.Error("shared public rate limit unavailable", "error", err)
				return problem(c, http.StatusServiceUnavailable, "public.rate_limit_unavailable", "Service unavailable", "Public endpoint protection is temporarily unavailable")
			}
			if count > limit {
				_, _ = s.pool.Exec(c.Request().Context(), `INSERT INTO audit_events (action,resource_type,request_id,ip_address,user_agent,metadata) VALUES ('public.rate_limited','public_endpoint',$1,NULLIF($2,'')::inet,$3,jsonb_build_object('path',$4::text,'method',$5::text))`, c.Response().Header().Get(echo.HeaderXRequestID), c.RealIP(), c.Request().UserAgent(), c.Path(), c.Request().Method)
				return problem(c, http.StatusTooManyRequests, "public.rate_limited", "Too many requests", "Please wait before trying again")
			}
			if time.Now().Unix()%600 == 0 {
				_, _ = s.pool.Exec(c.Request().Context(), `DELETE FROM public_rate_limits WHERE window_started < now()-INTERVAL '2 days'`)
			}
			return next(c)
		}
	}
}

func (s *Server) registerRoutes() {
	// Public Routes
	s.echo.POST("/api/v1/public/contact", s.submitPublicContact)
	s.echo.GET("/api/v1/public/check-subdomain", s.checkSubdomainAvailability)
	s.echo.GET("/api/v1/public/invitation", s.getInvitationByToken, s.publicLimit)
	s.echo.GET("/api/v1/auth/invitation", s.getInvitationByToken, s.publicLimit)
	s.echo.PUT("/api/v1/documents/:documentId/content", s.uploadDocumentContent)

	s.registerDocs()

	s.echo.GET("/healthz", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})
	s.echo.GET("/readyz", s.readiness)
	if s.api != nil {
		s.api.GET("/me", s.getMe)
		s.api.GET("/employees", s.listEmployees)
		s.api.GET("/employees/:employeeId", s.getEmployee)
		s.api.PATCH("/employees/:employeeId", s.updateEmployee)
		s.api.PATCH("/employees/:employeeId/profile", s.updateEmployeePersonalProfile)
		s.api.PUT("/employees/:employeeId/profile", s.updateEmployeePersonalProfile)
		s.api.GET("/departments", s.listDepartments)
		s.api.POST("/departments", s.createDepartment)
		s.api.GET("/departments/:departmentId/positions", s.listDepartmentPositions)
		s.api.POST("/departments/:departmentId/positions", s.createDepartmentPosition)
		s.api.GET("/departments/:departmentId/members", s.listDepartmentMembers)
		s.api.GET("/divisions", s.listDivisions)
		s.api.POST("/divisions", s.createDivision)
		s.api.POST("/employees", s.createEmployee)
		s.api.GET("/employees/me", s.getMyEmployeeProfile)
		s.api.PATCH("/employees/me/profile", s.updateMyPersonalProfile)
		s.api.GET("/people/me", s.getMyEmployeeProfile)
		s.api.PATCH("/people/me/profile", s.updateMyPersonalProfile)
		s.api.POST("/employees/:employeeId/activate", s.activateEmployee)
		s.api.GET("/employees/:employeeId/onboarding-link", s.getEmployeeOnboardingLink)
		s.api.POST("/employees/:employeeId/onboarding-link", s.generateEmployeeOnboardingLink)
		s.api.GET("/onboarding/settings", s.getOnboardingSettings)
		s.api.PUT("/onboarding/settings", s.updateOnboardingSettings)
		s.api.GET("/onboarding/handbook-download-url", s.getOnboardingHandbookDownloadURL)
		s.api.POST("/onboarding/send-invitation", s.sendOnboardingInvitation)
		s.api.POST("/leave/requests", s.submitLeaveRequest)
		s.api.GET("/leave/requests", s.listLeaveRequests)
		s.api.GET("/leave/overview", s.getLeaveOverview)
		s.api.GET("/leave/balances", s.listLeaveBalances)
		s.api.GET("/leave/types", s.listLeaveTypes)
		s.api.POST("/leave/types", s.createLeaveType)
		s.api.PUT("/leave/types/:typeId", s.updateLeaveType)
		s.api.PATCH("/leave/types/:typeId", s.updateLeaveType)
		s.api.GET("/leave/policies", s.listLeavePolicies)
		s.api.POST("/leave/policies", s.createLeavePolicy)
		s.api.POST("/leave/policies/:policyId/assignments", s.assignLeavePolicy)
		s.api.POST("/leave/policies/:policyId/assign-all", s.assignLeavePolicyToAll)
		s.api.POST("/leave/run-accrual", s.runMonthlyAccrual)
		s.api.POST("/leave/adjustments", s.createLeaveAdjustment)
		s.api.POST("/attendance/punches", s.createAttendancePunch)
		s.api.POST("/timesheets/submit", s.submitTimesheet)
		s.api.GET("/timesheets", s.listTimesheets)
		s.api.POST("/timesheets/:id/review", s.reviewTimesheet)
		s.api.GET("/attendance/punches", s.listAttendancePunches)
		s.api.GET("/attendance/live", s.getLiveAttendance)
		s.api.GET("/attendance/activities", s.listAttendanceActivities)
		s.api.POST("/attendance/activities", s.createAttendanceActivity)
		s.api.DELETE("/attendance/activities/:id", s.deleteAttendanceActivity)
		s.api.GET("/announcements", s.listAnnouncements)
		s.api.POST("/announcements", s.createAnnouncement)
		s.api.DELETE("/announcements/:id", s.deleteAnnouncement)
		s.api.GET("/celebrations", s.listCelebrations)
		s.api.POST("/celebrations/wishes", s.sendCelebrationWish)
		s.api.GET("/celebrations/me", s.getMyCelebrationStatus)
		s.api.GET("/notifications", s.listNotifications)
		s.api.POST("/notifications/mark-read", s.markNotificationRead)
		s.api.GET("/user/preferences", s.getUserPreferences)
		s.api.PUT("/user/preferences", s.updateUserPreferences)
		s.api.GET("/signatures", s.listSignatureRequests)
		s.api.POST("/signatures", s.createSignatureRequestFromTemplate)
		s.api.GET("/signatures/templates", s.listSignatureTemplates)
		s.api.GET("/signatures/branding", s.getSignatureTemplateBranding)
		s.api.POST("/signatures/templates", s.saveSignatureTemplate)
		s.api.PUT("/signatures/templates/:templateId", s.saveSignatureTemplate)
		s.api.GET("/signatures/me", s.listMySignatureRequests)
		s.api.POST("/signatures/:id/sign", s.signSignatureRequest)
		s.api.POST("/signatures/:id/decline", s.declineSignatureRequest)
		s.api.POST("/documents/upload-intents", s.createDocumentUploadIntent)
		s.api.PUT("/documents/:documentId/content", s.uploadDocumentContent)
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
		s.api.PUT("/organization/details", s.updateOrganizationDetails)
		s.api.POST("/branding/logo", s.uploadBrandingLogo)
		s.api.PUT("/users/me", s.updateUserProfile)
		s.api.POST("/users/me/avatar", s.uploadUserAvatar)
		s.api.GET("/locations", s.listLocations)
		s.api.POST("/locations", s.createLocation)
		// Hiring
		s.api.GET("/jobs", s.listJobPostings)
		s.api.POST("/jobs", s.createJobPosting)
		s.api.PATCH("/jobs/:postingId/status", s.updateJobPostingStatus)
		s.api.GET("/jobs/:postingId/applicants", s.listJobApplicants)
		s.api.GET("/hiring/applicants", s.listAllApplicants)
		s.api.PATCH("/hiring/applicants/:applicantId/status", s.updateApplicantStatus)
		s.api.POST("/hiring/applicants/:applicantId/offer", s.createJobOffer)
		// Messaging (Email Threads & Macros)
		s.api.POST("/messaging/threads", s.createEmailThread)
		s.api.GET("/messaging/threads", s.listEmailThreads)
		s.api.GET("/messaging/threads/:threadId/messages", s.getEmailThreadMessages)
		s.api.POST("/messaging/threads/:threadId/reply", s.replyToEmailThread)
		s.api.POST("/messaging/threads/:threadId/candidate-reply", s.recordCandidateReply)
		s.api.GET("/messaging/macros", s.listEmailMacros)
		s.api.POST("/messaging/macros", s.createEmailMacro)
		s.api.PUT("/messaging/macros/:macroId", s.updateEmailMacro)
		s.api.DELETE("/messaging/macros/:macroId", s.deleteEmailMacro)
		s.api.GET("/messaging/inbound", s.listInboundEmails)

		// Performance
		s.api.GET("/performance/goals", s.listAllPerformanceGoals)
		s.api.GET("/performance/goals/:employeeId", s.listPerformanceGoals)
		s.api.POST("/performance/goals/:employeeId", s.createPerformanceGoal)
		s.api.GET("/performance/reviews", s.listPerformanceReviews)
		s.api.POST("/performance/reviews/:employeeId", s.createPerformanceReview)
		s.api.POST("/performance/feedback/:employeeId", s.createFeedbackRecord)

		// Benefits
		s.api.GET("/benefits/plans", s.listBenefitPlans)
		s.api.POST("/benefits/plans", s.createBenefitPlan)
		s.api.PUT("/benefits/plans/:planId", s.updateBenefitPlan)
		s.api.DELETE("/benefits/plans/:planId", s.deleteBenefitPlan)
		s.api.GET("/benefits/enrollments", s.listAllBenefitEnrollments)
		s.api.GET("/benefits/enrollments/:employeeId", s.listBenefitEnrollments)
		s.api.POST("/benefits/enrollments/:employeeId", s.createBenefitEnrollment)
		s.api.POST("/benefits/rewards/:employeeId", s.createRewardStatement)

		// Workflows
		s.api.GET("/workflows/definitions", s.listWorkflowDefinitions)
		s.api.POST("/workflows/definitions", s.createWorkflowDefinition)
		s.api.GET("/workflows/inbox", s.listInboxTasks)
		s.api.POST("/workflows/tasks/:taskId/decide", s.decideWorkflowTask)

		// Promotions
		s.api.GET("/promotions", func(c echo.Context) error { return s.listPromotions(c) })
		s.api.GET("/promotions/:employeeId", s.listPromotions)
		s.api.POST("/promotions/:employeeId", s.executePromotion)

		// Scheduling
		s.api.GET("/scheduling/schedules", s.listShiftSchedules)
		s.api.POST("/scheduling/schedules", s.createShiftSchedule)
		s.api.GET("/scheduling/assignments/:employeeId", s.listShiftAssignments)
		s.api.POST("/scheduling/assignments/:employeeId", s.createShiftAssignment)

		// Holidays
		s.api.GET("/holidays", s.listHolidays)
		s.api.POST("/holidays", s.createHoliday)

		// Offboarding
		s.api.GET("/offboarding/plans", s.listOffboardingPlans)
		s.api.POST("/offboarding/plans", s.createOffboardingPlan)

		// Employee Relations
		s.api.GET("/relations/cases", s.listRelationCases)
		s.api.POST("/relations/cases", s.createRelationCase)
		s.api.POST("/relations/cases/:caseId/actions", s.executeRelationAction)

		// Roles & Permissions
		s.api.GET("/roles", s.listRoles)
		s.api.POST("/roles", s.createRole)
		s.api.PUT("/roles/:id/permissions", s.updateRolePermissions)
		s.api.POST("/roles/assign/:employeeId", s.assignRoleToEmployee)
		s.api.GET("/permissions", s.listPermissions)

		// Payroll
		s.api.GET("/payroll/compensation", s.listPayrollCompensation)
		s.api.POST("/payroll/compensation", s.createPayrollCompensation)
		s.api.GET("/payroll/configurations", s.listPayrollConfigurations)
		s.api.POST("/payroll/configurations", s.createPayrollConfiguration)
		s.api.GET("/payroll/tax-rules", s.listPayrollTaxRules)
		s.api.POST("/payroll/tax-rules", s.savePayrollTaxRule)
		s.api.PATCH("/payroll/tax-rules/:ruleId", s.updatePayrollTaxRuleStatus)
		s.api.DELETE("/payroll/tax-rules/:ruleId", s.deletePayrollTaxRule)
		s.api.GET("/payroll/runs", s.listPayrollRuns)
		s.api.DELETE("/payroll/runs/:runId", s.discardPayrollRun)
		s.api.GET("/payroll/periods", s.listPayrollPeriods)
		s.api.POST("/payroll/periods", s.createPayrollPeriod)
		s.api.GET("/payroll/periods/:periodId/runs", s.listPayrollRunsForPeriod)
		s.api.POST("/payroll/periods/:periodId/runs", s.createPayrollRun)
		s.api.GET("/payroll/runs/:runId/items", s.listPayrollRunItems)
		s.api.POST("/payroll/runs/:runId/lock", s.lockPayrollRun)
		s.api.POST("/payroll/runs/:runId/payslips/email", s.emailPayrollPayslips)
		s.api.GET("/payroll/runs/:runId/payslips", s.listPayrollPayslips)
		s.api.GET("/payroll/payslips", s.listEmployeePayslips)
		s.api.GET("/payroll/payslips/:paystubId", s.getMyPayslip)

		// Policies
		s.api.GET("/policies", s.listPolicies)
		s.api.POST("/policies", s.createPolicy)

		// Support
		s.api.GET("/support/tickets", s.listTenantTickets)
		s.api.POST("/support/tickets", s.createTenantTicket)
		s.api.POST("/support/tickets/:id/messages", s.addTenantTicketMessage)
		s.api.GET("/support/tickets/:id/messages", s.listTenantTicketMessages)
		s.api.PATCH("/support/tickets/:id/status", s.updateTenantTicketStatus)

		// Operations & Signatures
		s.api.POST("/documents/:documentId/signatures", s.createSignatureRequest)
		s.api.POST("/operations/data-jobs", s.createTenantDataJob)
		s.api.GET("/operations/features", s.listTenantFeatures)

		// Workspace Chat
		s.api.GET("/chat/channels", s.listChatChannels)
		s.api.POST("/chat/channels", s.createChatChannel)
		s.api.POST("/chat/channels/dm", s.getOrCreateDM)
		s.api.PATCH("/chat/channels/:channelId", s.updateChatChannel)
		s.api.POST("/chat/channels/:channelId/archive", s.archiveChatChannel)
		s.api.DELETE("/chat/channels/:channelId", s.deleteChatChannel)
		s.api.POST("/chat/channels/:channelId/join", s.joinChatChannel)
		s.api.POST("/chat/channels/:channelId/leave", s.leaveChatChannel)
		s.api.POST("/chat/channels/:channelId/request-join", s.requestJoinChatChannel)
		s.api.POST("/chat/channels/:channelId/review-request", s.reviewJoinRequest)
		s.api.POST("/chat/channels/:channelId/members", s.addChatMember)
		s.api.DELETE("/chat/channels/:channelId/members/:memberId", s.removeChatMember)
		s.api.POST("/chat/channels/:channelId/read", s.markChatChannelRead)
		s.api.PATCH("/chat/channels/:channelId/preferences", s.updateChatChannelPreferences)

		s.api.GET("/chat/messages", s.listChatMessages)
		s.api.GET("/chat/messages/search", s.searchChatMessages)
		s.api.GET("/chat/messages/:messageId/replies", s.getThreadReplies)
		s.api.POST("/chat/messages", s.sendChatMessage)
		s.api.PATCH("/chat/messages/:messageId", s.editChatMessage)
		s.api.DELETE("/chat/messages/:messageId", s.deleteChatMessage)
		s.api.POST("/chat/messages/:messageId/pin", s.togglePinMessage)
		s.api.POST("/chat/messages/:messageId/save", s.toggleSaveMessage)
		s.api.POST("/chat/messages/:messageId/reactions", s.toggleChatReaction)
		s.api.POST("/chat/messages/:messageId/poll/vote", s.voteChatPoll)
		s.api.POST("/chat/messages/:messageId/poll/close", s.toggleCloseChatPoll)

		s.api.POST("/chat/attachments", s.uploadChatAttachment)
		s.api.POST("/chat/typing", s.setChatTyping)
		s.api.GET("/chat/presence", s.listChatPresence)
		s.api.PATCH("/chat/presence", s.updateChatPresence)
		s.api.GET("/chat/events", s.chatEventsSSE)

		s.authenticated.POST("/setup/organizations", s.createOrganization)
		s.authenticated.POST("/auth/provision", s.provisionIdentity)
		s.authenticated.GET("/setup/status", s.checkSetupStatus)
	}
	s.echo.GET("/api/v1/chat/attachments/:attachmentId/content", s.getChatAttachmentContent)
	s.echo.GET("/api/v1/public/assets/*", s.servePublicAsset)
	s.echo.HEAD("/api/v1/public/assets/*", s.servePublicAsset)
	s.echo.GET("/api/v1/public/jobs", s.listPublicJobs)
	s.echo.GET("/api/v1/public/company", s.getPublicCompany)
	s.echo.POST("/api/v1/public/upload-resume", s.uploadPublicResume, s.publicLimit)
	s.echo.POST("/api/v1/messaging/inbound/webhook", s.inboundEmailWebhook, s.publicLimit)
	s.echo.POST("/api/inbound", s.inboundEmailWebhook, s.publicLimit)
	s.echo.POST("/api/v1/inbound", s.inboundEmailWebhook, s.publicLimit)
	s.echo.POST("/api/v1/public/jobs/:postingId/apply", s.applyForJob, s.publicLimit)
	s.echo.GET("/api/v1/public/signatures/:token", s.getExternalSignature, s.publicLimit)
	s.echo.POST("/api/v1/public/signatures/:token/sign", s.signExternalSignature, s.publicLimit)
}

func (s *Server) getExternalSignature(c echo.Context) error {
	item, err := s.signatures.GetExternalSignature(c.Request().Context(), c.Param("token"))
	if errors.Is(err, signatures.ErrRecipientNotFound) {
		return problem(c, http.StatusNotFound, "signatures.link_invalid", "Link unavailable", "This signing link is invalid, expired, or already used")
	}
	if err != nil {
		return problem(c, http.StatusInternalServerError, "signatures.link_failed", "Internal error", "Could not load signing request")
	}
	if _, auditErr := s.pool.Exec(c.Request().Context(), `INSERT INTO audit_events (organization_id,action,resource_type,resource_id,request_id,ip_address,user_agent,metadata) VALUES (NULLIF((SELECT organization_id::text FROM signature_requests WHERE id=$1),'')::uuid,'signature.external_link_opened','signature_request',$1,$2,NULLIF($3,'')::inet,$4,jsonb_build_object('recipientHash',$5::text))`, item.RequestID, c.Response().Header().Get(echo.HeaderXRequestID), c.RealIP(), c.Request().UserAgent(), sha256Hex(item.RecipientEmail)); auditErr != nil {
		slog.Error("external signature open audit failed", "error", auditErr)
	}
	if item.DocumentID != "" && s.storage != nil {
		var org string
		if e := s.pool.QueryRow(c.Request().Context(), `SELECT organization_id::text FROM signature_requests WHERE id=$1`, item.RequestID).Scan(&org); e == nil {
			if link, e := s.documents.GetDownloadUrl(c.Request().Context(), org, "", item.DocumentID); e == nil {
				item.DocumentURL = link.URL
			}
		}
	}
	return c.JSON(http.StatusOK, item)
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(value)))
	return hex.EncodeToString(sum[:])
}

func (s *Server) signExternalSignature(c echo.Context) error {
	var input struct {
		SignatureType string `json:"signatureType"`
		SignatureData string `json:"signatureData"`
		Consent       bool   `json:"consent"`
	}
	if err := c.Bind(&input); err != nil || !input.Consent {
		return problem(c, http.StatusUnprocessableEntity, "signatures.consent_required", "Consent required", "Review the document and confirm before signing")
	}
	err := s.signatures.SignExternalRequestWithIdentity(c.Request().Context(), c.Param("token"), input.SignatureType, input.SignatureData, c.RealIP(), c.Request().UserAgent())
	if errors.Is(err, signatures.ErrRecipientNotFound) {
		return problem(c, http.StatusNotFound, "signatures.link_invalid", "Link unavailable", "This signing link is invalid, expired, or already used")
	}
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "signatures.sign_failed", "Could not sign document", err.Error())
	}
	return c.JSON(http.StatusOK, map[string]bool{"success": true})
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

func (s *Server) listDepartmentPositions(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("organization:read") && !principal.HasPermission("employees:read") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to view department positions")
	}
	departmentID := c.Param("departmentId")
	result, err := s.departments.ListPositions(c.Request().Context(), principal.OrganizationID, departmentID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "positions.lookup_failed", "Internal server error", "Could not retrieve department positions")
	}
	return c.JSON(http.StatusOK, map[string]any{"data": result})
}

func (s *Server) createDepartmentPosition(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to manage department positions")
	}
	departmentID := c.Param("departmentId")
	var input tenancy.CreatePositionInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	result, err := s.departments.CreatePosition(c.Request().Context(), principal.OrganizationID, departmentID, input)
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_position", "Invalid position", err.Error())
	}
	return c.JSON(http.StatusCreated, result)
}

func (s *Server) listDepartmentMembers(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("organization:read") && !principal.HasPermission("employees:read") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to view department members")
	}
	departmentID := c.Param("departmentId")
	result, err := s.departments.ListMembers(c.Request().Context(), principal.OrganizationID, departmentID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "members.lookup_failed", "Internal server error", "Could not retrieve department members")
	}
	return c.JSON(http.StatusOK, map[string]any{"data": result})
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
		if !principal.HasPermission("documents:read") && !principal.HasPermission("documents:manage") {
			_ = s.pool.QueryRow(c.Request().Context(), "SELECT id FROM employees WHERE user_id = $1 AND organization_id = $2", principal.UserID, principal.OrganizationID).Scan(&employeeID)
		}
	} else {
		isSelf, err := s.people.IsEmployeeUser(c.Request().Context(), principal.OrganizationID, employeeID, principal.UserID)
		if err != nil {
			return problem(c, http.StatusInternalServerError, "documents.access_lookup_failed", "Internal server error", "Could not verify employee access")
		}
		if !isSelf && !principal.HasPermission("documents:read") && !principal.HasPermission("documents:manage") {
			return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You may only view your own documents")
		}
	}
	limit := 25
	if raw := c.QueryParam("limit"); raw != "" {
		parsedLimit, err := strconv.Atoi(raw)
		if err != nil {
			return problem(c, http.StatusUnprocessableEntity, "validation.invalid_limit", "Invalid limit", "limit must be an integer")
		}
		limit = parsedLimit
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
	if !principal.HasPermission("documents:upload") && !principal.HasPermission("documents:manage") && !principal.HasPermission("organization:manage") && !principal.HasPermission("employees:manage") {
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

	// Route uploads through the backend proxy URL so browsers never fail CORS preflights against R2
	scheme := "https"
	proto := c.Request().Header.Get("X-Forwarded-Proto")
	if proto != "" {
		scheme = proto
	} else if c.Request().TLS == nil {
		scheme = "http"
	}
	host := c.Request().Host
	result.UploadURL = fmt.Sprintf("%s://%s/api/v1/documents/%s/content", scheme, host, result.Document.ID)

	return c.JSON(http.StatusCreated, result)
}

func (s *Server) uploadDocumentContent(c echo.Context) error {
	documentID := c.Param("documentId")
	ctx := c.Request().Context()

	principal, hasPrincipal := identity.PrincipalFromContext(ctx)

	var orgID string
	var objectKey string
	var status string
	var expiresAt time.Time
	err := s.pool.QueryRow(ctx, `SELECT organization_id::text, object_key, status, upload_expires_at FROM documents WHERE id = $1`, documentID).Scan(&orgID, &objectKey, &status, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return problem(c, http.StatusNotFound, "documents.not_found", "Not found", "The requested document was not found")
	}
	if err != nil {
		return problem(c, http.StatusInternalServerError, "documents.query_failed", "Internal server error", err.Error())
	}

	if hasPrincipal && principal.OrganizationID != orgID {
		return problem(c, http.StatusForbidden, "authorization.forbidden", "Forbidden", "You do not have access to upload to this document")
	}

	if status == "deleted" || status == "quarantined" {
		return problem(c, http.StatusGone, "documents.invalid_state", "Invalid state", "The document is no longer active")
	}

	if time.Now().UTC().After(expiresAt) {
		return problem(c, http.StatusGone, "documents.expired", "Expired", "Upload intent window has expired")
	}

	contentType := c.Request().Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	body := c.Request().Body
	defer body.Close()

	data, err := io.ReadAll(body)
	if err != nil {
		return problem(c, http.StatusBadRequest, "documents.read_failed", "Bad Request", "Could not read uploaded document data")
	}

	actualSize := int64(len(data))
	digest := sha256.Sum256(data)
	checksumSha256 := hex.EncodeToString(digest[:])

	// Stream object directly to storage
	if s.storage != nil {
		if err := s.storage.PutObject(ctx, objectKey, contentType, bytes.NewReader(data), actualSize); err != nil {
			return problem(c, http.StatusInternalServerError, "documents.storage_failed", "Storage error", fmt.Sprintf("Failed to persist document to storage: %v", err))
		}
	}

	actorUserID := "system"
	if hasPrincipal {
		actorUserID = principal.UserID
	}

	_, err = s.pool.Exec(ctx, `
		UPDATE documents
		SET status = 'available', sha256 = lower($1), size_bytes = $2
		WHERE id = $3
	`, checksumSha256, actualSize, documentID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "documents.update_failed", "Internal server error", err.Error())
	}

	_, _ = s.pool.Exec(ctx, `
		INSERT INTO document_versions (document_id, version_number, object_key, sha256, size_bytes, created_by_user_id)
		VALUES ($1, 1, $2, lower($3), $4, $5)
		ON CONFLICT (document_id, version_number) DO NOTHING
	`, documentID, objectKey, checksumSha256, actualSize, actorUserID)

	_, _ = s.pool.Exec(ctx, `
		INSERT INTO outbox_events (organization_id, event_type, aggregate_type, aggregate_id, payload)
		VALUES ($1, 'document.scan_requested', 'document', $2, jsonb_build_object('documentId', $2::text, 'objectKey', $3::text))
	`, orgID, documentID, objectKey)

	_, _ = s.pool.Exec(ctx, `
		INSERT INTO audit_events (organization_id, actor_user_id, action, resource_type, resource_id, request_id)
		VALUES ($1, $2, 'document.upload_completed', 'document', $3, 'unknown')
	`, orgID, actorUserID, documentID)

	return c.JSON(http.StatusOK, map[string]interface{}{
		"ok":             true,
		"documentId":     documentID,
		"status":         "available",
		"checksumSha256": checksumSha256,
		"sizeBytes":      actualSize,
	})
}

func (s *Server) completeDocumentUpload(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("documents:upload") && !principal.HasPermission("documents:manage") && !principal.HasPermission("organization:manage") && !principal.HasPermission("employees:manage") {
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
		_ = s.pool.QueryRow(c.Request().Context(), "SELECT id FROM employees WHERE user_id = $1 AND organization_id = $2", principal.UserID, principal.OrganizationID).Scan(&employeeID)
	}
	if employeeID == "" {
		return c.JSON(http.StatusOK, attendance.PunchPage{Data: []attendance.Punch{}})
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
		if parsed, err := strconv.Atoi(raw); err == nil {
			if parsed > 500 {
				limit = 500
			} else if parsed < 1 {
				limit = 25
			} else {
				limit = parsed
			}
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
	s.notifyLeaveRequest(c.Request().Context(), principal.OrganizationID, c.Param("requestId"), "declined")
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
		CanApproveManager: principal.HasPermission("leave:approve_manager") || principal.HasPermission("organization:manage") || principal.HasPermission("employees:manage"),
		CanApproveHR:      principal.HasPermission("leave:approve_hr") || principal.HasPermission("organization:manage") || principal.HasPermission("employees:manage") || principal.HasPermission("employees:read") || principal.HasPermission("leave:manage_policies"),
	})
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_leave_request_filter", "Invalid leave request filter", err.Error())
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) getLeaveOverview(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}

	ctx := c.Request().Context()
	orgID := principal.OrganizationID
	userID := principal.UserID

	// 1. Current Employee
	var userEmail string
	_ = s.pool.QueryRow(ctx, "SELECT email FROM users WHERE id = $1", userID).Scan(&userEmail)
	prof, err := s.people.GetByUserID(ctx, orgID, userID, userEmail)
	var empID string
	if err == nil {
		empID = prof.ID
	}

	// 2. Fetch Types, Policies, Balances, and Requests in parallel
	var (
		types    []timeoff.LeaveType
		policies []timeoff.PolicyDetail
		balances []timeoff.Balance
		requests []timeoff.LeaveRequest
		wg       sync.WaitGroup
	)

	wg.Add(4)

	go func() {
		defer wg.Done()
		t, err := s.timeOff.ListLeaveTypes(ctx, orgID)
		if err == nil && t != nil {
			types = t
		} else {
			types = []timeoff.LeaveType{}
		}
	}()

	go func() {
		defer wg.Done()
		p, err := s.timeOff.ListPolicies(ctx, orgID)
		if err == nil && p != nil {
			policies = p
		} else {
			policies = []timeoff.PolicyDetail{}
		}
	}()

	go func() {
		defer wg.Done()
		if empID != "" {
			b, _ := s.timeOff.ListBalances(ctx, orgID, empID)
			if b != nil {
				balances = b
				return
			}
		}
		balances = []timeoff.Balance{}
	}()

	go func() {
		defer wg.Done()
		requestsPage, err := s.timeOff.ListRequests(ctx, orgID, userID, timeoff.ListRequestsInput{
			Limit:             100,
			CanApproveManager: principal.HasPermission("leave:approve_manager") || principal.HasPermission("organization:manage") || principal.HasPermission("employees:manage"),
			CanApproveHR:      principal.HasPermission("leave:approve_hr") || principal.HasPermission("organization:manage") || principal.HasPermission("employees:manage") || principal.HasPermission("employees:read") || principal.HasPermission("leave:manage_policies"),
		})
		if err == nil && requestsPage.Data != nil {
			requests = requestsPage.Data
		} else {
			requests = []timeoff.LeaveRequest{}
		}
	}()

	wg.Wait()

	return c.JSON(http.StatusOK, map[string]any{
		"currentEmployee": prof,
		"leaveTypes":      types,
		"policies":        policies,
		"balances":        balances,
		"requests":        requests,
	})
}

func (s *Server) listLeaveBalances(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	employeeID := strings.TrimSpace(c.QueryParam("employeeId"))
	if employeeID == "" {
		_ = s.pool.QueryRow(c.Request().Context(), "SELECT id FROM employees WHERE user_id = $1 AND organization_id = $2", principal.UserID, principal.OrganizationID).Scan(&employeeID)
	}
	if employeeID == "" {
		var userEmail string
		_ = s.pool.QueryRow(c.Request().Context(), "SELECT email FROM users WHERE id = $1", principal.UserID).Scan(&userEmail)
		if userEmail != "" {
			_ = s.pool.QueryRow(c.Request().Context(), "SELECT id FROM employees WHERE (work_email ILIKE $1 OR personal_email ILIKE $1) AND organization_id = $2", userEmail, principal.OrganizationID).Scan(&employeeID)
		}
	}
	if employeeID == "" {
		return c.JSON(http.StatusOK, map[string]any{"data": []any{}})
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
	if !principal.HasPermission("leave:manage_policies") && !principal.HasPermission("organization:manage") {
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
	if !principal.HasPermission("leave:manage_policies") && !principal.HasPermission("organization:manage") {
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
	if !principal.HasPermission("leave:manage_policies") && !principal.HasPermission("organization:manage") {
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

func (s *Server) updateLeaveType(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("leave:manage_policies") && !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to manage leave types")
	}
	var input timeoff.UpdateLeaveTypeInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	typeID := c.Param("typeId")
	result, err := s.timeOff.UpdateLeaveType(c.Request().Context(), principal.OrganizationID, typeID, input)
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_leave_type", "Invalid leave type", err.Error())
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listLeaveTypes(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	types, err := s.timeOff.ListLeaveTypes(c.Request().Context(), principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "leave.list_types_failed", "Failed to list leave types", err.Error())
	}
	return c.JSON(http.StatusOK, types)
}

func (s *Server) listLeavePolicies(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	policies, err := s.timeOff.ListPolicies(c.Request().Context(), principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "leave.list_policies_failed", "Failed to list leave policies", err.Error())
	}
	return c.JSON(http.StatusOK, policies)
}

func (s *Server) assignLeavePolicyToAll(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("leave:manage_policies") && !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to assign leave policies")
	}
	policyID := c.Param("policyId")
	if err := s.timeOff.AssignPolicyToAll(c.Request().Context(), principal.OrganizationID, principal.UserID, policyID); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "leave.assign_policy_failed", "Failed to assign policy", err.Error())
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) runMonthlyAccrual(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("leave:manage_policies") && !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to run accruals")
	}
	count, err := s.timeOff.RunMonthlyAccrual(c.Request().Context(), principal.OrganizationID, principal.UserID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "leave.accrual_failed", "Accrual failed", err.Error())
	}
	return c.JSON(http.StatusOK, map[string]int{"creditsApplied": count})
}

func (s *Server) createLeaveAdjustment(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("leave:manage_policies") && !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to create leave adjustments")
	}
	var input timeoff.CreateAdjustmentInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	if err := s.timeOff.CreateAdjustment(c.Request().Context(), principal.OrganizationID, principal.UserID, input); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "leave.adjustment_failed", "Adjustment failed", err.Error())
	}
	return c.NoContent(http.StatusNoContent)
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
	s.notifyLeaveRequest(c.Request().Context(), principal.OrganizationID, c.Param("requestId"), "approved")
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
	s.notifyLeaveRequest(c.Request().Context(), principal.OrganizationID, c.Param("requestId"), "manager approved; awaiting HR review")
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

	input.SetDefaults()

	if strings.TrimSpace(input.EmployeeNumber) == "" {
		var empCount int
		_ = s.pool.QueryRow(c.Request().Context(), `SELECT count(*) FROM employees WHERE organization_id = $1`, principal.OrganizationID).Scan(&empCount)
		input.EmployeeNumber = fmt.Sprintf("EMP-%04d", empCount+1)
	}

	idemKey := c.Request().Header.Get("Idempotency-Key")
	if len(idemKey) < 16 {
		idemKey = fmt.Sprintf("idem-%d-%s", time.Now().UnixNano(), principal.UserID)
	}
	reqID := c.Response().Header().Get(echo.HeaderXRequestID)
	if reqID == "" {
		reqID = "req-" + principal.UserID
	}
	result, err := s.people.Create(c.Request().Context(), principal.OrganizationID, principal.UserID, reqID, idemKey, input)
	if err != nil {
		switch {
		case errors.Is(err, people.ErrCreateConflict):
			return problem(c, http.StatusConflict, "employee.conflict", "Employee conflict", "Employee number or work email already exists")
		case errors.Is(err, people.ErrIdempotencyReused):
			return problem(c, http.StatusConflict, "idempotency.key_reused", "Idempotency key conflict", "The Idempotency-Key was previously used for a different request")
		default:
			return problem(c, http.StatusUnprocessableEntity, "validation.invalid_employee", "Invalid employee", err.Error())
		}
	}

	if input.SendInvitation {
		destEmail := input.PersonalEmail
		if destEmail == "" {
			destEmail = input.WorkEmail
		}
		candidateName := strings.TrimSpace(input.FirstName + " " + input.LastName)
		go func() {
			bgCtx := context.Background()
			_ = s.dispatchInvitationForEmployee(bgCtx, principal.OrganizationID, result.ID, destEmail, candidateName, input.JobTitle, input.HiredOn)
		}()
	}

	return c.JSON(http.StatusCreated, result)
}

func (s *Server) activateEmployee(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	employeeID := c.Param("employeeId")
	isSelf, _ := s.people.IsEmployeeUser(c.Request().Context(), principal.OrganizationID, employeeID, principal.UserID)
	if !isSelf && !principal.HasPermission("employees:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to manage employees")
	}
	if err := s.people.Activate(c.Request().Context(), principal.OrganizationID, employeeID); err != nil {
		if errors.Is(err, people.ErrEmployeeNotFound) {
			return problem(c, http.StatusNotFound, "employee.not_found", "Not found", "Employee not found")
		}
		return problem(c, http.StatusInternalServerError, "employee.activation_failed", "Internal error", "Could not activate employee")
	}
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) getMyEmployeeProfile(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}

	var userEmail string
	_ = s.pool.QueryRow(c.Request().Context(), "SELECT email FROM users WHERE id = $1", principal.UserID).Scan(&userEmail)

	prof, err := s.people.GetByUserID(c.Request().Context(), principal.OrganizationID, principal.UserID, userEmail)
	if err == nil {
		return c.JSON(http.StatusOK, prof)
	}

	var uFirstName, uLastName, uPhone, uAvatar string
	_ = s.pool.QueryRow(c.Request().Context(), "SELECT first_name, last_name, COALESCE(phone, ''), COALESCE(avatar_url, '') FROM users WHERE id = $1", principal.UserID).Scan(&uFirstName, &uLastName, &uPhone, &uAvatar)

	var orgName string
	_ = s.pool.QueryRow(c.Request().Context(), "SELECT name FROM organizations WHERE id = $1", principal.OrganizationID).Scan(&orgName)

	return c.JSON(http.StatusOK, map[string]any{
		"id":             principal.UserID,
		"employeeNumber": "EMP-001",
		"firstName":      uFirstName,
		"lastName":       uLastName,
		"workEmail":      userEmail,
		"personalEmail":  userEmail,
		"personalPhone":  uPhone,
		"status":         "onboarding",
		"department":     "Administration",
		"location":       "Headquarters",
		"jobTitle":       "Team Member",
		"avatar":         uAvatar,
		"companyName":    orgName,
	})
}

func (s *Server) updateMyPersonalProfile(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}

	var input people.PersonalProfile
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request body", err.Error())
	}

	var empID string
	err := s.pool.QueryRow(c.Request().Context(), "SELECT id::text FROM employees WHERE user_id = $1 AND organization_id = $2", principal.UserID, principal.OrganizationID).Scan(&empID)
	if err != nil {
		var userEmail string
		_ = s.pool.QueryRow(c.Request().Context(), "SELECT email FROM users WHERE id = $1", principal.UserID).Scan(&userEmail)
		_ = s.pool.QueryRow(c.Request().Context(), "SELECT id::text FROM employees WHERE organization_id = $1 AND (LOWER(work_email) = LOWER($2) OR LOWER(id::text) = LOWER($2)) LIMIT 1", principal.OrganizationID, userEmail).Scan(&empID)
		if empID == "" {
			_ = s.people.EnsureMemberEmployees(c.Request().Context(), principal.OrganizationID)
			_ = s.pool.QueryRow(c.Request().Context(), "SELECT id::text FROM employees WHERE user_id = $1 AND organization_id = $2", principal.UserID, principal.OrganizationID).Scan(&empID)
		}
	}

	if empID != "" {
		if input.FirstName != nil && *input.FirstName != "" {
			_, _ = s.pool.Exec(c.Request().Context(), `UPDATE employees SET first_name = $1 WHERE id = $2::uuid`, *input.FirstName, empID)
			_, _ = s.pool.Exec(c.Request().Context(), `UPDATE users SET first_name = $1 WHERE id = $2::uuid`, *input.FirstName, principal.UserID)
		}
		if input.LastName != nil && *input.LastName != "" {
			_, _ = s.pool.Exec(c.Request().Context(), `UPDATE employees SET last_name = $1 WHERE id = $2::uuid`, *input.LastName, empID)
			_, _ = s.pool.Exec(c.Request().Context(), `UPDATE users SET last_name = $1 WHERE id = $2::uuid`, *input.LastName, principal.UserID)
		}
		if input.PersonalPhone != nil {
			_, _ = s.pool.Exec(c.Request().Context(), `UPDATE users SET phone = $1 WHERE id = $2::uuid`, *input.PersonalPhone, principal.UserID)
		}
		if err := s.people.UpdatePersonalProfile(c.Request().Context(), principal.OrganizationID, empID, input); err != nil {
			return problem(c, http.StatusInternalServerError, "profile.update_failed", "Failed to update profile", err.Error())
		}
		if input.HandbookSignedAt != nil && *input.HandbookSignedAt != "" {
			_ = s.people.Activate(c.Request().Context(), principal.OrganizationID, empID)
		}
	}

	return c.JSON(http.StatusOK, map[string]any{"status": "success"})
}

func (s *Server) updateEmployeePersonalProfile(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	empID := c.Param("employeeId")
	isSelf, _ := s.people.IsEmployeeUser(c.Request().Context(), principal.OrganizationID, empID, principal.UserID)
	if !isSelf && !principal.HasPermission("employees:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to update this employee's profile")
	}

	var input people.PersonalProfile
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request body", err.Error())
	}

	if input.FirstName != nil && *input.FirstName != "" {
		_, _ = s.pool.Exec(c.Request().Context(), `UPDATE employees SET first_name = $1 WHERE id = $2::uuid AND organization_id = $3`, *input.FirstName, empID, principal.OrganizationID)
	}
	if input.LastName != nil && *input.LastName != "" {
		_, _ = s.pool.Exec(c.Request().Context(), `UPDATE employees SET last_name = $1 WHERE id = $2::uuid AND organization_id = $3`, *input.LastName, empID, principal.OrganizationID)
	}
	if input.PersonalPhone != nil {
		_, _ = s.pool.Exec(c.Request().Context(), `UPDATE users SET phone = $1 WHERE id = (SELECT user_id FROM employees WHERE id = $2::uuid)`, *input.PersonalPhone, empID)
	}

	if err := s.people.UpdatePersonalProfile(c.Request().Context(), principal.OrganizationID, empID, input); err != nil {
		return problem(c, http.StatusInternalServerError, "profile.update_failed", "Failed to update profile", err.Error())
	}

	return c.JSON(http.StatusOK, map[string]any{"status": "success"})
}

type SendInvitationRequest struct {
	EmployeeID  string `json:"employeeId"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	JobTitle    string `json:"jobTitle"`
	CompanyName string `json:"companyName"`
	StartDate   string `json:"startDate"`
	PortalURL   string `json:"portalUrl"`
}

func (s *Server) sendOnboardingInvitation(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("employees:manage") && !principal.HasPermission("onboarding:initiate") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to send onboarding invitations")
	}

	var req SendInvitationRequest
	if err := c.Bind(&req); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request body", err.Error())
	}

	var orgName, logoURL, brandColor, websiteURL, subdomain string
	_ = s.pool.QueryRow(c.Request().Context(), `
		SELECT o.name, COALESCE(w.logo_url, ''), COALESCE(w.brand_color, '#4F46E5'), COALESCE(lp.website_url, ''), COALESCE(w.subdomain, '')
		FROM organizations o
		LEFT JOIN workspaces w ON w.organization_id = o.id
		LEFT JOIN organization_legal_profiles lp ON lp.organization_id = o.id
		WHERE o.id = $1`, principal.OrganizationID).Scan(&orgName, &logoURL, &brandColor, &websiteURL, &subdomain)

	if req.CompanyName == "" {
		req.CompanyName = orgName
	}

	if req.EmployeeID != "" && (req.Email == "" || req.Name == "") {
		var fName, lName, wEmail, pEmail, job string
		_ = s.pool.QueryRow(c.Request().Context(), `
			SELECT e.first_name, e.last_name, e.work_email, COALESCE(p.personal_email, ''), COALESCE(ea.job_title, '')
			FROM employees e
			LEFT JOIN employee_personal_profiles p ON p.employee_id = e.id
			LEFT JOIN employment_assignments ea ON ea.employee_id = e.id AND ea.effective_to IS NULL
			WHERE e.id = $1::uuid AND e.organization_id = $2`, req.EmployeeID, principal.OrganizationID).Scan(&fName, &lName, &wEmail, &pEmail, &job)
		if req.Name == "" {
			req.Name = strings.TrimSpace(fName + " " + lName)
		}
		if req.Email == "" {
			if pEmail != "" {
				req.Email = pEmail
			} else {
				req.Email = wEmail
			}
		}
		if req.JobTitle == "" {
			req.JobTitle = job
		}
	}

	var token string
	var expiresAt time.Time
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err == nil {
		token = hex.EncodeToString(tokenBytes)
		expiresAt = time.Now().Add(7 * 24 * time.Hour)
		var empIDParam any
		if req.EmployeeID != "" {
			empIDParam = req.EmployeeID
			// Invalidate any older pending invitations for this employee so token is fresh
			_, _ = s.pool.Exec(c.Request().Context(), `
				UPDATE invitations SET status = 'revoked'
				WHERE organization_id = $1 AND employee_id = $2::uuid AND status = 'pending'`,
				principal.OrganizationID, req.EmployeeID)
		}
		_, errIns := s.pool.Exec(c.Request().Context(), `
			INSERT INTO invitations (organization_id, employee_id, email, name, role, token, status, expires_at)
			VALUES ($1::uuid, $2, $3, $4, 'employee', $5, 'pending', $6)`,
			principal.OrganizationID, empIDParam, req.Email, req.Name, token, expiresAt)
		if errIns != nil {
			slog.Error("failed to record invitation token", "error", errIns)
		}
	}

	if req.PortalURL == "" {
		if token != "" {
			if subdomain != "" {
				req.PortalURL = fmt.Sprintf("https://%s.easyhr.pro/auth/accept-invite?token=%s&redirect_url=/my-onboarding", subdomain, token)
			} else {
				req.PortalURL = fmt.Sprintf("https://easyhr.pro/auth/accept-invite?token=%s&redirect_url=/my-onboarding", token)
			}
		} else {
			if subdomain != "" {
				req.PortalURL = fmt.Sprintf("https://%s.easyhr.pro/auth/accept-invite?email=%s&name=%s&redirect_url=/my-onboarding", subdomain, url.QueryEscape(req.Email), url.QueryEscape(req.Name))
			} else {
				req.PortalURL = fmt.Sprintf("https://easyhr.pro/auth/accept-invite?email=%s&name=%s&redirect_url=/my-onboarding", url.QueryEscape(req.Email), url.QueryEscape(req.Name))
			}
		}
	}

	branding := email.BrandingInfo{
		CompanyName: req.CompanyName,
		LogoURL:     logoURL,
		BrandColor:  brandColor,
		WebsiteURL:  websiteURL,
	}

	if err := s.email.SendOnboardingInvitationWithBranding(c.Request().Context(), req.Email, req.Name, req.JobTitle, req.StartDate, req.PortalURL, branding); err != nil {
		return problem(c, http.StatusInternalServerError, "email.failed", "Failed to send email", err.Error())
	}

	res := map[string]any{
		"sent":      true,
		"recipient": req.Email,
		"portalUrl": req.PortalURL,
		"token":     token,
	}
	if !expiresAt.IsZero() {
		res["expiresAt"] = expiresAt.Format(time.RFC3339)
	}
	return c.JSON(http.StatusOK, res)
}

func (s *Server) getEmployeeOnboardingLink(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("employees:manage") && !principal.HasPermission("onboarding:initiate") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to view onboarding links")
	}

	employeeID := c.Param("employeeId")
	var token, inviteEmail, name string
	var expiresAt time.Time
	var subdomain string

	err := s.pool.QueryRow(c.Request().Context(), `
		SELECT i.token, i.email, i.name, i.expires_at, COALESCE(w.subdomain, '')
		FROM invitations i
		JOIN organizations o ON o.id = i.organization_id
		LEFT JOIN workspaces w ON w.organization_id = o.id
		WHERE i.organization_id = $1 AND i.employee_id = $2::uuid AND i.status = 'pending' AND i.expires_at > now()
		ORDER BY i.created_at DESC LIMIT 1`, principal.OrganizationID, employeeID).Scan(&token, &inviteEmail, &name, &expiresAt, &subdomain)
	if err != nil {
		return problem(c, http.StatusNotFound, "invitation.not_found", "No active invitation found", "No unexpired pending invitation found for this employee")
	}

	var portalURL string
	if subdomain != "" {
		portalURL = fmt.Sprintf("https://%s.easyhr.pro/auth/accept-invite?token=%s&redirect_url=/my-onboarding", subdomain, token)
	} else {
		portalURL = fmt.Sprintf("https://easyhr.pro/auth/accept-invite?token=%s&redirect_url=/my-onboarding", token)
	}

	return c.JSON(http.StatusOK, map[string]any{
		"portalUrl": portalURL,
		"token":     token,
		"email":     inviteEmail,
		"name":      name,
		"expiresAt": expiresAt.Format(time.RFC3339),
	})
}

func (s *Server) generateEmployeeOnboardingLink(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("employees:manage") && !principal.HasPermission("onboarding:initiate") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to manage onboarding links")
	}

	employeeID := c.Param("employeeId")
	var fName, lName, wEmail, pEmail, job string
	var hiredOn time.Time
	err := s.pool.QueryRow(c.Request().Context(), `
		SELECT e.first_name, e.last_name, e.work_email, COALESCE(p.personal_email, ''), COALESCE(ea.job_title, ''), e.hired_on
		FROM employees e
		LEFT JOIN employee_personal_profiles p ON p.employee_id = e.id
		LEFT JOIN employment_assignments ea ON ea.employee_id = e.id AND ea.effective_to IS NULL
		WHERE e.id = $1::uuid AND e.organization_id = $2`, employeeID, principal.OrganizationID).Scan(&fName, &lName, &wEmail, &pEmail, &job, &hiredOn)
	if err != nil {
		return problem(c, http.StatusNotFound, "employee.not_found", "Employee not found", "Could not locate employee record")
	}

	destEmail := pEmail
	if destEmail == "" {
		destEmail = wEmail
	}
	name := strings.TrimSpace(fName + " " + lName)

	var orgName, logoURL, brandColor, websiteURL, subdomain string
	_ = s.pool.QueryRow(c.Request().Context(), `
		SELECT o.name, COALESCE(w.logo_url, ''), COALESCE(w.brand_color, '#4F46E5'), COALESCE(lp.website_url, ''), COALESCE(w.subdomain, '')
		FROM organizations o
		LEFT JOIN workspaces w ON w.organization_id = o.id
		LEFT JOIN organization_legal_profiles lp ON lp.organization_id = o.id
		WHERE o.id = $1`, principal.OrganizationID).Scan(&orgName, &logoURL, &brandColor, &websiteURL, &subdomain)

	// Invalidate previous pending invitations
	_, _ = s.pool.Exec(c.Request().Context(), `
		UPDATE invitations SET status = 'revoked'
		WHERE organization_id = $1 AND employee_id = $2::uuid AND status = 'pending'`,
		principal.OrganizationID, employeeID)

	tokenBytes := make([]byte, 32)
	_, _ = rand.Read(tokenBytes)
	token := hex.EncodeToString(tokenBytes)
	expiresAt := time.Now().Add(7 * 24 * time.Hour)

	_, errIns := s.pool.Exec(c.Request().Context(), `
		INSERT INTO invitations (organization_id, employee_id, email, name, role, token, status, expires_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, 'employee', $5, 'pending', $6)`,
		principal.OrganizationID, employeeID, destEmail, name, token, expiresAt)
	if errIns != nil {
		return problem(c, http.StatusInternalServerError, "invitation.create_failed", "Failed to generate invitation", errIns.Error())
	}

	var portalURL string
	if subdomain != "" {
		portalURL = fmt.Sprintf("https://%s.easyhr.pro/auth/accept-invite?token=%s&redirect_url=/my-onboarding", subdomain, token)
	} else {
		portalURL = fmt.Sprintf("https://easyhr.pro/auth/accept-invite?token=%s&redirect_url=/my-onboarding", token)
	}

	var body struct {
		SendEmail bool `json:"sendEmail"`
	}
	_ = c.Bind(&body)

	emailSent := false
	if body.SendEmail && destEmail != "" {
		branding := email.BrandingInfo{
			CompanyName: orgName,
			LogoURL:     logoURL,
			BrandColor:  brandColor,
			WebsiteURL:  websiteURL,
		}
		formattedDate := hiredOn.Format("Jan 2, 2006")
		_ = s.email.SendOnboardingInvitationWithBranding(c.Request().Context(), destEmail, name, job, formattedDate, portalURL, branding)
		emailSent = true
	}

	return c.JSON(http.StatusOK, map[string]any{
		"portalUrl": portalURL,
		"token":     token,
		"email":     destEmail,
		"name":      name,
		"expiresAt": expiresAt.Format(time.RFC3339),
		"emailSent": emailSent,
	})
}

func (s *Server) dispatchInvitationForEmployee(ctx context.Context, organizationID, employeeID, destEmail, name, jobTitle, startDate string) error {
	var orgName, logoURL, brandColor, websiteURL, subdomain string
	_ = s.pool.QueryRow(ctx, `
		SELECT o.name, COALESCE(w.logo_url, ''), COALESCE(w.brand_color, '#4F46E5'), COALESCE(lp.website_url, ''), COALESCE(w.subdomain, '')
		FROM organizations o
		LEFT JOIN workspaces w ON w.organization_id = o.id
		LEFT JOIN organization_legal_profiles lp ON lp.organization_id = o.id
		WHERE o.id = $1`, organizationID).Scan(&orgName, &logoURL, &brandColor, &websiteURL, &subdomain)

	var token string
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err == nil {
		token = hex.EncodeToString(tokenBytes)
		expiresAt := time.Now().Add(7 * 24 * time.Hour)
		var empIDParam any
		if employeeID != "" {
			empIDParam = employeeID
		}
		_, errIns := s.pool.Exec(ctx, `
			INSERT INTO invitations (organization_id, employee_id, email, name, role, token, status, expires_at)
			VALUES ($1::uuid, $2, $3, $4, 'employee', $5, 'pending', $6)`,
			organizationID, empIDParam, destEmail, name, token, expiresAt)
		if errIns != nil {
			slog.Error("failed to record invitation token", "error", errIns)
		}
	}

	var portalURL string
	if token != "" {
		if subdomain != "" {
			portalURL = fmt.Sprintf("https://%s.easyhr.pro/auth/accept-invite?token=%s&redirect_url=/my-onboarding", subdomain, token)
		} else {
			portalURL = fmt.Sprintf("https://easyhr.pro/auth/accept-invite?token=%s&redirect_url=/my-onboarding", token)
		}
	} else {
		if subdomain != "" {
			portalURL = fmt.Sprintf("https://%s.easyhr.pro/auth/accept-invite?email=%s&name=%s&redirect_url=/my-onboarding", subdomain, url.QueryEscape(destEmail), url.QueryEscape(name))
		} else {
			portalURL = fmt.Sprintf("https://easyhr.pro/auth/accept-invite?email=%s&name=%s&redirect_url=/my-onboarding", url.QueryEscape(destEmail), url.QueryEscape(name))
		}
	}

	branding := email.BrandingInfo{
		CompanyName: orgName,
		LogoURL:     logoURL,
		BrandColor:  brandColor,
		WebsiteURL:  websiteURL,
	}

	return s.email.SendOnboardingInvitationWithBranding(ctx, destEmail, name, jobTitle, startDate, portalURL, branding)
}

type OnboardingSettings struct {
	WelcomeTitle            string `json:"welcomeTitle"`
	WelcomeMessage          string `json:"welcomeMessage"`
	WelcomeVideoURL         string `json:"welcomeVideoUrl"`
	WelcomeVideoRequired    bool   `json:"welcomeVideoRequired"`
	RequireBioAndAvatar     bool   `json:"requireBioAndAvatar"`
	RequireCultureFavorites bool   `json:"requireCultureFavorites"`
	RequireDirectDeposit    bool   `json:"requireDirectDeposit"`
	RequireHandbook         bool   `json:"requireHandbook"`
	HandbookTitle           string `json:"handbookTitle"`
	HandbookURL             string `json:"handbookUrl"`
	HandbookContent         string `json:"handbookContent"`
}

func (s *Server) getOnboardingSettings(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}

	var cfg OnboardingSettings
	err := s.pool.QueryRow(c.Request().Context(), `
		SELECT 
			welcome_title, welcome_message, COALESCE(welcome_video_url, ''), welcome_video_required,
			require_bio_and_avatar, require_culture_favorites, require_direct_deposit,
			require_handbook, handbook_title, COALESCE(handbook_url, ''), COALESCE(handbook_content, '')
		FROM organization_onboarding_settings
		WHERE organization_id = $1`, principal.OrganizationID).Scan(
		&cfg.WelcomeTitle, &cfg.WelcomeMessage, &cfg.WelcomeVideoURL, &cfg.WelcomeVideoRequired,
		&cfg.RequireBioAndAvatar, &cfg.RequireCultureFavorites, &cfg.RequireDirectDeposit,
		&cfg.RequireHandbook, &cfg.HandbookTitle, &cfg.HandbookURL, &cfg.HandbookContent,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		cfg = OnboardingSettings{
			WelcomeTitle:            "Welcome to the Team",
			WelcomeMessage:          "We are excited to have you join our team. Let's get your profile set up.",
			WelcomeVideoURL:         "",
			WelcomeVideoRequired:    false,
			RequireBioAndAvatar:     true,
			RequireCultureFavorites: true,
			RequireDirectDeposit:    true,
			RequireHandbook:         true,
			HandbookTitle:           "Employee Handbook & Code of Conduct",
			HandbookURL:             "",
			HandbookContent:         "Welcome to our organization! As a member of our team, we uphold high standards of integrity, excellence, and mutual respect. Please review and acknowledge our policies.",
		}
	} else if err != nil {
		return problem(c, http.StatusInternalServerError, "onboarding.settings_failed", "Internal server error", err.Error())
	}

	return c.JSON(http.StatusOK, cfg)
}

func (s *Server) getOnboardingHandbookDownloadURL(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	var handbookRef string
	if err := s.pool.QueryRow(c.Request().Context(), `SELECT COALESCE(handbook_url, '') FROM organization_onboarding_settings WHERE organization_id = $1`, principal.OrganizationID).Scan(&handbookRef); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.JSON(http.StatusOK, map[string]string{"url": "", "downloadUrl": "", "filename": ""})
		}
		return problem(c, http.StatusInternalServerError, "onboarding.handbook_failed", "Internal server error", "Could not load handbook")
	}
	if !strings.HasPrefix(handbookRef, "document:") {
		return c.JSON(http.StatusOK, map[string]string{"url": "", "downloadUrl": "", "filename": ""})
	}
	link, err := s.documents.GetDownloadUrl(c.Request().Context(), principal.OrganizationID, principal.UserID, strings.TrimPrefix(handbookRef, "document:"))
	if err != nil {
		return c.JSON(http.StatusOK, map[string]string{"url": "", "downloadUrl": "", "filename": ""})
	}
	return c.JSON(http.StatusOK, link)
}

func (s *Server) updateOnboardingSettings(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("organization:manage") && !principal.HasPermission("employees:manage") && !principal.HasPermission("onboarding:manage_templates") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to configure onboarding settings")
	}

	var req OnboardingSettings
	if err := c.Bind(&req); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request body", err.Error())
	}

	if req.WelcomeTitle == "" {
		req.WelcomeTitle = "Welcome to the Team"
	}
	if req.HandbookTitle == "" {
		req.HandbookTitle = "Employee Handbook & Code of Conduct"
	}

	_, err := s.pool.Exec(c.Request().Context(), `
		INSERT INTO organization_onboarding_settings (
			organization_id, welcome_title, welcome_message, welcome_video_url, welcome_video_required,
			require_bio_and_avatar, require_culture_favorites, require_direct_deposit,
			require_handbook, handbook_title, handbook_url, handbook_content, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, now()
		)
		ON CONFLICT (organization_id) DO UPDATE SET
			welcome_title = EXCLUDED.welcome_title,
			welcome_message = EXCLUDED.welcome_message,
			welcome_video_url = EXCLUDED.welcome_video_url,
			welcome_video_required = EXCLUDED.welcome_video_required,
			require_bio_and_avatar = EXCLUDED.require_bio_and_avatar,
			require_culture_favorites = EXCLUDED.require_culture_favorites,
			require_direct_deposit = EXCLUDED.require_direct_deposit,
			require_handbook = EXCLUDED.require_handbook,
			handbook_title = EXCLUDED.handbook_title,
			handbook_url = EXCLUDED.handbook_url,
			handbook_content = EXCLUDED.handbook_content,
			updated_at = now()
	`, principal.OrganizationID, req.WelcomeTitle, req.WelcomeMessage, req.WelcomeVideoURL, req.WelcomeVideoRequired,
		req.RequireBioAndAvatar, req.RequireCultureFavorites, req.RequireDirectDeposit,
		req.RequireHandbook, req.HandbookTitle, req.HandbookURL, req.HandbookContent)

	if err != nil {
		return problem(c, http.StatusInternalServerError, "onboarding.settings_update_failed", "Internal server error", err.Error())
	}

	return c.JSON(http.StatusOK, req)
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
	items, err := s.people.List(c.Request().Context(), principal.OrganizationID, c.QueryParam("search"), c.QueryParam("status"), c.QueryParam("sort"), limit)
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
	var input identity.ProvisionInput
	_ = c.Bind(&input)
	if err := s.provisioner.ProvisionWithInput(c.Request().Context(), claims.Subject, input); err != nil {
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

func (s *Server) checkSubdomainAvailability(c echo.Context) error {
	subdomain := strings.ToLower(strings.TrimSpace(c.QueryParam("subdomain")))
	if subdomain == "" {
		return c.JSON(http.StatusOK, map[string]any{"available": false, "reason": "Subdomain cannot be empty"})
	}
	if len(subdomain) < 2 || len(subdomain) > 63 {
		return c.JSON(http.StatusOK, map[string]any{"available": false, "reason": "Subdomain must be between 2 and 63 characters"})
	}
	// Check against reserved subdomains
	reserved := map[string]bool{
		"api": true, "app": true, "admin": true, "auth": true, "billing": true,
		"dashboard": true, "docs": true, "mail": true, "support": true, "www": true,
		"staging": true, "dev": true, "test": true, "demo": true,
	}
	if reserved[subdomain] {
		return c.JSON(http.StatusOK, map[string]any{"available": false, "reason": "This workspace URL is reserved"})
	}

	var exists bool
	err := s.pool.QueryRow(c.Request().Context(), `SELECT EXISTS(SELECT 1 FROM workspaces WHERE subdomain = $1)`, subdomain).Scan(&exists)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "database.lookup_failed", "Internal error", "Could not check subdomain availability")
	}
	if exists {
		return c.JSON(http.StatusOK, map[string]any{"available": false, "reason": "This workspace URL is already in use"})
	}
	return c.JSON(http.StatusOK, map[string]any{"available": true})
}

func (s *Server) checkSetupStatus(c echo.Context) error {
	claims, ok := clerk.SessionClaimsFromContext(c.Request().Context())
	if !ok || claims == nil || claims.Subject == "" {
		return problem(c, http.StatusUnauthorized, "auth.missing_token", "Unauthorized", "A verified session is required")
	}

	var hasOrg bool
	var orgID, orgName, subdomain string
	err := s.pool.QueryRow(c.Request().Context(), `
		SELECT o.id::text, o.name, COALESCE(w.subdomain, '')
		FROM users u
		JOIN memberships m ON m.user_id = u.id AND m.status = 'active'
		JOIN organizations o ON o.id = m.organization_id
		LEFT JOIN workspaces w ON w.organization_id = o.id
		WHERE u.external_identity_id = $1
		LIMIT 1`, claims.Subject).Scan(&orgID, &orgName, &subdomain)

	if err == nil {
		hasOrg = true
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return problem(c, http.StatusInternalServerError, "database.lookup_failed", "Internal error", "Could not check organization status")
	}

	return c.JSON(http.StatusOK, map[string]any{
		"hasOrganization":  hasOrg,
		"organizationId":   orgID,
		"organizationName": orgName,
		"subdomain":        subdomain,
	})
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
			return problem(c, http.StatusConflict, "organization.conflict", "Organization setup conflict", err.Error())
		case errors.Is(err, tenancy.ErrIdempotencyKeyReused):
			return problem(c, http.StatusConflict, "idempotency.key_reused", "Idempotency key conflict", "The Idempotency-Key was previously used for a different request")
		case strings.Contains(err.Error(), "must ") || strings.Contains(err.Error(), "not currently supported") || strings.Contains(err.Error(), "unsupported platform"):
			return problem(c, http.StatusUnprocessableEntity, "validation.invalid_field", "Invalid request", err.Error())
		default:
			return err
		}
	}

	// Dispatch welcoming email to organization creator
	go func() {
		var userEmail, userFirstName string
		_ = s.pool.QueryRow(context.Background(), `
			SELECT email, first_name
			FROM users WHERE clerk_user_id = $1 OR id::text = $1`, claims.Subject).Scan(&userEmail, &userFirstName)
		if userEmail != "" {
			ownerDisplayName := userFirstName
			if ownerDisplayName == "" {
				ownerDisplayName = "Founder"
			}
			orgName := input.DisplayName
			if orgName == "" {
				orgName = input.LegalName
			}
			targetDashboard := fmt.Sprintf("https://%s.easyhr.pro/dashboard", result.Subdomain)
			_ = s.email.SendWelcomeOrganizationCreator(
				context.Background(),
				userEmail,
				ownerDisplayName,
				orgName,
				result.Subdomain,
				targetDashboard,
			)
		}
	}()

	return c.JSON(http.StatusCreated, result)
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
	slog.Error("unhandled internal server error",
		"error", err,
		"path", c.Path(),
		"method", c.Request().Method,
	)
	_ = problem(c, http.StatusInternalServerError, "internal.unexpected", "Internal server error", err.Error())
}

func (s *Server) getLiveAttendance(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !principal.HasPermission("attendance:read_team") && !principal.HasPermission("attendance:manage") && !principal.HasPermission("attendance:read") && !principal.HasPermission("organization:read") && !principal.HasPermission("employees:read") {
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

func (s *Server) submitTimesheet(c echo.Context) error {
	p, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !p.HasPermission("timesheet:submit_self") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You cannot submit timesheets")
	}
	var in struct {
		WeekStart string `json:"weekStart"`
		Note      string `json:"note"`
	}
	if err := c.Bind(&in); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", err.Error())
	}
	t, err := s.attendance.SubmitTimesheet(c.Request().Context(), p.OrganizationID, p.UserID, in.WeekStart, in.Note)
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "timesheet.submit_failed", "Could not submit timesheet", err.Error())
	}
	return c.JSON(http.StatusCreated, t)
}

func (s *Server) listTimesheets(c echo.Context) error {
	p, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !p.HasPermission("timesheet:approve") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You cannot review timesheets")
	}
	items, err := s.attendance.ListTimesheets(c.Request().Context(), p.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "timesheet.list_failed", "Internal error", "Could not load timesheets")
	}
	return c.JSON(http.StatusOK, map[string]any{"data": items})
}

func (s *Server) reviewTimesheet(c echo.Context) error {
	p, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !p.HasPermission("timesheet:approve") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You cannot approve timesheets")
	}
	var in struct {
		Decision string `json:"decision"`
		Note     string `json:"note"`
	}
	if err := c.Bind(&in); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", err.Error())
	}
	err := s.attendance.ReviewTimesheet(c.Request().Context(), p.OrganizationID, p.UserID, c.Param("id"), in.Decision, in.Note, c.Response().Header().Get(echo.HeaderXRequestID), c.RealIP(), c.Request().UserAgent())
	if errors.Is(err, attendance.ErrTimesheetNotFound) {
		return problem(c, http.StatusConflict, "timesheet.already_reviewed", "Conflict", "Timesheet is no longer awaiting review")
	}
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "timesheet.review_failed", "Could not review timesheet", err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) getDocumentDownloadUrl(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	// HR roles may download any organization document; employees may download documents linked to their own record.
	if !principal.HasPermission("documents:read") && !principal.HasPermission("documents:manage") {
		var owns bool
		_ = s.pool.QueryRow(c.Request().Context(), `SELECT EXISTS (SELECT 1 FROM documents d JOIN employees e ON e.id = d.employee_id WHERE d.id::text = $1 AND d.organization_id = $2 AND e.user_id = $3)`, c.Param("documentId"), principal.OrganizationID, principal.UserID).Scan(&owns)
		if !owns {
			return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to view documents")
		}
	}
	result, err := s.documents.GetDownloadUrl(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Param("documentId"))
	if err != nil {
		return problem(c, http.StatusNotFound, "documents.download_failed", "Not found", "Document could not be downloaded")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listPayrollConfigurations(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !principal.HasPermission("payroll:manage") && !principal.HasPermission("payroll:read") && !principal.HasPermission("organization:read") && !principal.HasPermission("employees:read") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to view payroll configurations")
	}
	result, err := s.payroll.ListConfigurations(c.Request().Context(), principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "payroll.list_failed", "Internal server error", "Could not load payroll configurations")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listPayrollTaxRules(c echo.Context) error {
	p, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !p.HasPermission("payroll:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You cannot manage tax rules")
	}
	items, err := s.payroll.ListTaxRules(c.Request().Context(), p.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "payroll.tax_rules_failed", "Internal error", "Could not load tax rules")
	}
	return c.JSON(http.StatusOK, map[string]any{"data": items})
}

func (s *Server) savePayrollTaxRule(c echo.Context) error {
	p, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !p.HasPermission("payroll:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You cannot manage tax rules")
	}
	var in payroll.TaxRuleInput
	if err := c.Bind(&in); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", err.Error())
	}
	item, err := s.payroll.SaveTaxRule(c.Request().Context(), p.OrganizationID, in)
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "payroll.tax_rule_invalid", "Invalid tax rule", err.Error())
	}
	return c.JSON(http.StatusOK, item)
}

func (s *Server) listPayrollPeriods(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
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
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
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
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !principal.HasPermission("payroll:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to manage payroll")
	}
	result, err := s.payroll.InitiateRun(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Param("periodId"))
	if err != nil {
		if errors.Is(err, payroll.ErrNotFound) {
			return problem(c, http.StatusNotFound, "payroll.period_not_found", "Not found", "Payroll period not found")
		}
		if errors.Is(err, payroll.ErrMissingCompensation) {
			return problem(c, http.StatusConflict, "payroll.compensation_missing", "Compensation setup incomplete", "Every active employee needs an effective compensation record before payroll can run")
		}
		if errors.Is(err, payroll.ErrUnsupportedJurisdiction) {
			return problem(c, http.StatusUnprocessableEntity, "payroll.rules_unavailable", "Payroll rules unavailable", "Add at least one active tax or deduction for your payroll country before running payroll")
		}
		if errors.Is(err, payroll.ErrConfigurationMissing) {
			return problem(c, http.StatusConflict, "payroll.configuration_required", "Payroll settings required", "Save your payroll country and currency in Settings before running payroll")
		}
		if errors.Is(err, payroll.ErrInvalidState) {
			return problem(c, http.StatusConflict, "payroll.invalid_state", "Pay period already in progress", "This pay period already has a run. Approve or discard it first")
		}
		if errors.Is(err, payroll.ErrCompensationCurrencyMismatch) {
			return problem(c, http.StatusConflict, "payroll.currency_mismatch", "Salary currency mismatch", "One or more salaries use a different currency than your payroll settings. Update those salaries and try again")
		}
		if errors.Is(err, payroll.ErrUnsupportedCompensation) {
			return problem(c, http.StatusConflict, "payroll.unsupported_compensation", "Unsupported salary type", "Payroll currently supports monthly salaries only")
		}
		slog.Error("payroll run failed", "error", err)
		return problem(c, http.StatusInternalServerError, "payroll.run_failed", "Internal server error", "Could not initiate payroll run")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) lockPayrollRun(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !principal.HasPermission("payroll:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to manage payroll")
	}
	var body struct {
		Items []payroll.LockRunItemInput `json:"items"`
	}
	_ = c.Bind(&body)
	err := s.payroll.LockRun(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Param("runId"), body.Items)
	if err != nil {
		if errors.Is(err, payroll.ErrInvalidState) {
			return problem(c, http.StatusConflict, "payroll.invalid_state", "Conflict", "Run cannot be locked in its current state")
		}
		return problem(c, http.StatusInternalServerError, "payroll.lock_failed", "Internal server error", "Could not lock payroll run")
	}
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) emailPayrollPayslips(c echo.Context) error {
	p, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !p.HasPermission("payroll:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You cannot send payslips")
	}
	items, err := s.payroll.ListPayslipsForRun(c.Request().Context(), p.OrganizationID, c.Param("runId"))
	if err != nil {
		return problem(c, http.StatusInternalServerError, "payroll.payslips_failed", "Internal error", "Could not load payslips")
	}
	var company, logo, color, subdomain string
	_ = s.pool.QueryRow(c.Request().Context(), `SELECT o.name, COALESCE(w.logo_url,''), COALESCE(w.brand_color,'#2563EB'), COALESCE(w.subdomain,'') FROM organizations o LEFT JOIN workspaces w ON w.organization_id=o.id WHERE o.id=$1`, p.OrganizationID).Scan(&company, &logo, &color, &subdomain)
	var sent, failed int
	for _, slip := range items {
		if slip.Email == "" {
			failed++
			continue
		}
		portalURL := fmt.Sprintf("https://%s.easyhr.pro/payroll/payslips/%s", subdomain, slip.ID)
		if subdomain == "" {
			portalURL = fmt.Sprintf("https://easyhr.pro/payroll/payslips/%s", slip.ID)
		}
		err := s.email.SendPayslipNotification(c.Request().Context(), slip.Email, email.PayslipNotificationInput{
			EmployeeName: slip.EmployeeName,
			PeriodStart:  slip.PeriodStart,
			PeriodEnd:    slip.PeriodEnd,
			PayDate:      slip.PayDate,
			Currency:     slip.Currency,
			BasePay:      slip.BasePay,
			Earnings:     slip.Earnings,
			Deductions:   slip.Deductions,
			NetPay:       slip.NetPay,
			PortalURL:    portalURL,
			CompanyName:  company,
			LogoURL:      logo,
			BrandColor:   color,
		})
		if err != nil {
			failed++
			slog.Error("payslip email failed", "payslipId", slip.ID, "error", err)
			continue
		}
		if err := s.payroll.MarkPayslipDelivered(c.Request().Context(), slip.ID); err != nil {
			failed++
			continue
		}
		sent++
	}
	return c.JSON(http.StatusOK, map[string]int{"sent": sent, "failed": failed})
}

func (s *Server) listPayrollPayslips(c echo.Context) error {
	p, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !p.HasPermission("payroll:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You cannot view payslips")
	}
	items, err := s.payroll.ListPayslipsForRun(c.Request().Context(), p.OrganizationID, c.Param("runId"))
	if err != nil {
		return problem(c, http.StatusInternalServerError, "payroll.payslips_failed", "Internal error", "Could not load payslips")
	}
	return c.JSON(http.StatusOK, map[string]any{"data": items})
}

func (s *Server) listEmployeePayslips(c echo.Context) error {
	p, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	employeeID := strings.TrimSpace(c.QueryParam("employeeId"))
	ctx := c.Request().Context()
	if employeeID != "" && !p.HasPermission("payroll:manage") {
		// Non-HR users may only list their own payslips.
		own, err := s.payroll.ListPayslipsForEmployee(ctx, p.OrganizationID, "", p.UserID)
		if err != nil {
			return problem(c, http.StatusInternalServerError, "payroll.payslips_failed", "Internal error", "Could not load payslips")
		}
		filtered := []payroll.Payslip{}
		for _, slip := range own {
			if slip.EmployeeID == employeeID {
				filtered = append(filtered, slip)
			}
		}
		return c.JSON(http.StatusOK, map[string]any{"data": filtered})
	}
	items, err := s.payroll.ListPayslipsForEmployee(ctx, p.OrganizationID, employeeID, p.UserID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "payroll.payslips_failed", "Internal error", "Could not load payslips")
	}
	return c.JSON(http.StatusOK, map[string]any{"data": items})
}

func (s *Server) getMyPayslip(c echo.Context) error {
	p, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	slip, err := s.payroll.GetPayslipForUser(c.Request().Context(), p.OrganizationID, p.UserID, c.Param("paystubId"))
	if err != nil && p.HasPermission("payroll:manage") {
		slip, err = s.payroll.GetPayslip(c.Request().Context(), p.OrganizationID, c.Param("paystubId"))
	}
	if err != nil {
		return problem(c, http.StatusNotFound, "payroll.payslip_not_found", "Not found", "Payslip unavailable")
	}
	return c.JSON(http.StatusOK, slip)
}

func (s *Server) listPayrollCompensation(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !principal.HasPermission("payroll:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to view payroll compensation")
	}
	result, err := payroll.ListCompensation(c.Request().Context(), s.pool, principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "payroll.compensation_list_failed", "Internal server error", "Could not load employee compensation")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) createPayrollCompensation(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !principal.HasPermission("payroll:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to manage payroll compensation")
	}
	var input payroll.CreateCompensationInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", err.Error())
	}
	result, err := s.payroll.CreateCompensation(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Response().Header().Get(echo.HeaderXRequestID), input)
	if errors.Is(err, payroll.ErrCompensationConfigMissing) {
		return problem(c, http.StatusConflict, "payroll.configuration_required", "Payroll settings required", err.Error())
	}
	if errors.Is(err, payroll.ErrCompensationEmployeeNotFound) {
		return problem(c, http.StatusNotFound, "payroll.employee_not_found", "Not found", "Select an active employee in this company")
	}
	if err != nil {
		return problem(c, http.StatusUnprocessableEntity, "payroll.compensation_invalid", "Invalid compensation", err.Error())
	}
	return c.JSON(http.StatusCreated, result)
}

func (s *Server) createPayrollConfiguration(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !principal.HasPermission("payroll:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to manage payroll")
	}
	var input payroll.CreateConfigurationInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", err.Error())
	}
	result, err := s.payroll.CreateConfiguration(c.Request().Context(), principal.OrganizationID, input)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "payroll.config_failed", "Internal server error", "Could not save payroll configuration")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listPayrollRunsForPeriod(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !principal.HasPermission("payroll:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to view payroll")
	}
	result, err := s.payroll.ListRunsForPeriod(c.Request().Context(), principal.OrganizationID, c.Param("periodId"))
	if err != nil {
		return problem(c, http.StatusInternalServerError, "payroll.runs_failed", "Internal server error", "Could not load payroll runs")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listPayrollRunItems(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !principal.HasPermission("payroll:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to view payroll")
	}
	result, err := s.payroll.ListRunItems(c.Request().Context(), principal.OrganizationID, c.Param("runId"))
	if err != nil {
		return problem(c, http.StatusInternalServerError, "payroll.items_failed", "Internal server error", "Could not load payroll run items")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) createSignatureRequest(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("signatures:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to manage signatures")
	}
	var input signatures.CreateSignatureRequestInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", err.Error())
	}
	result, links, err := s.signatures.CreateDocumentRequest(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Param("documentId"), input)
	if err != nil {
		if errors.Is(err, signatures.ErrNotFound) {
			return problem(c, http.StatusNotFound, "signatures.document_not_found", "Not found", "Document not found or unavailable")
		}
		return problem(c, http.StatusUnprocessableEntity, "signatures.create_failed", "Invalid signature request", err.Error())
	}
	var documentTitle string
	_ = s.pool.QueryRow(c.Request().Context(), `SELECT filename FROM documents WHERE id=$1 AND organization_id=$2`, c.Param("documentId"), principal.OrganizationID).Scan(&documentTitle)
	s.notifySignatureRecipientsWithLinks(c.Request().Context(), principal.OrganizationID, input.Recipients, links, documentTitle, input.Message)
	return c.JSON(http.StatusOK, result)
}

func (s *Server) handleDocuSignWebhook(c echo.Context) error {
	// In production, HMAC validation would occur here based on the headers
	var event signatures.WebhookEvent
	if err := c.Bind(&event); err != nil {
		return c.NoContent(http.StatusBadRequest)
	}
	if err := s.signatures.ProcessWebhook(c.Request().Context(), event); err != nil {
		return c.NoContent(http.StatusInternalServerError)
	}
	return c.NoContent(http.StatusOK)
}

func (s *Server) createRelationCase(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("relations:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to manage employee relations")
	}
	var input relations.CreateCaseInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", err.Error())
	}
	result, err := s.relations.CreateCase(c.Request().Context(), principal.OrganizationID, principal.UserID, input)
	if err != nil {
		if errors.Is(err, relations.ErrNotFound) {
			return problem(c, http.StatusNotFound, "relations.employee_not_found", "Not found", "Employee not found")
		}
		return problem(c, http.StatusInternalServerError, "relations.create_failed", "Internal server error", "Could not create relation case")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listRelationCases(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("relations:read") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to view relation cases")
	}
	result, err := s.relations.ListCases(c.Request().Context(), principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "relations.list_failed", "Internal server error", "Could not load relation cases")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) executeRelationAction(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("relations:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to execute relation actions")
	}
	var input relations.CreateActionInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", err.Error())
	}
	result, err := s.relations.ExecuteAction(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Param("caseId"), input)
	if err != nil {
		if errors.Is(err, relations.ErrNotFound) {
			return problem(c, http.StatusNotFound, "relations.case_not_found", "Not found", "Relation case not found")
		}
		return problem(c, http.StatusInternalServerError, "relations.action_failed", "Internal server error", "Could not execute relation action")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listPublicJobs(c echo.Context) error {
	orgID := c.QueryParam("organizationId")
	if orgID == "" {
		_ = s.pool.QueryRow(c.Request().Context(), "SELECT id::text FROM organizations ORDER BY created_at ASC LIMIT 1").Scan(&orgID)
	}
	if orgID == "" {
		return c.JSON(http.StatusOK, []any{})
	}
	result, err := s.hiring.ListPublicPostings(c.Request().Context(), orgID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "hiring.list_public_failed", "Internal server error", "Could not load jobs")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) getPublicCompany(c echo.Context) error {
	orgID := c.QueryParam("organizationId")
	var id, name, logoURL, brandColor, industry, websiteURL string

	var err error
	if orgID != "" {
		err = s.pool.QueryRow(c.Request().Context(), `
			SELECT o.id::text, o.name, COALESCE(w.logo_url, ''), COALESCE(w.brand_color, '#4F46E5'),
			       COALESCE(lp.industry, 'Technology & Business'), COALESCE(lp.website_url, '')
			FROM organizations o
			LEFT JOIN workspaces w ON w.organization_id = o.id
			LEFT JOIN organization_legal_profiles lp ON lp.organization_id = o.id
			WHERE o.id = $1::uuid
			LIMIT 1`, orgID).Scan(&id, &name, &logoURL, &brandColor, &industry, &websiteURL)
	} else {
		err = s.pool.QueryRow(c.Request().Context(), `
			SELECT o.id::text, o.name, COALESCE(w.logo_url, ''), COALESCE(w.brand_color, '#4F46E5'),
			       COALESCE(lp.industry, 'Technology & Business'), COALESCE(lp.website_url, '')
			FROM organizations o
			LEFT JOIN workspaces w ON w.organization_id = o.id
			LEFT JOIN organization_legal_profiles lp ON lp.organization_id = o.id
			ORDER BY o.created_at ASC
			LIMIT 1`).Scan(&id, &name, &logoURL, &brandColor, &industry, &websiteURL)
	}

	if err != nil {
		return c.JSON(http.StatusOK, map[string]any{
			"id":         "",
			"name":       "ProjectEasyHR",
			"logoUrl":    "",
			"brandColor": "#4F46E5",
			"industry":   "Human Resources & Talent",
			"websiteUrl": "https://projecteasyhr.com",
		})
	}

	return c.JSON(http.StatusOK, map[string]any{
		"id":         id,
		"name":       name,
		"logoUrl":    logoURL,
		"brandColor": brandColor,
		"industry":   industry,
		"websiteUrl": websiteURL,
	})
}

func (s *Server) uploadPublicResume(c echo.Context) error {
	file, err := c.FormFile("resume")
	if err != nil {
		file, err = c.FormFile("file")
	}
	if err != nil {
		return problem(c, http.StatusBadRequest, "upload.missing_file", "Missing file", "Please select a resume or CV file to upload")
	}
	if file.Size <= 0 || file.Size > 10*1024*1024 {
		return problem(c, http.StatusRequestEntityTooLarge, "upload.invalid_size", "Invalid file size", "Resume files must be between 1 byte and 10 MB")
	}
	ext := strings.ToLower(path.Ext(file.Filename))
	if ext != ".pdf" && ext != ".doc" && ext != ".docx" {
		return problem(c, http.StatusUnsupportedMediaType, "upload.invalid_type", "Unsupported file type", "Upload a PDF, DOC, or DOCX resume")
	}

	src, err := file.Open()
	if err != nil {
		return problem(c, http.StatusInternalServerError, "upload.open_failed", "Upload failed", err.Error())
	}
	defer src.Close()

	buf, err := io.ReadAll(src)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "upload.read_failed", "Upload failed", err.Error())
	}

	contentType := http.DetectContentType(buf)
	if (ext == ".pdf" && contentType != "application/pdf") || (ext != ".pdf" && contentType != "application/msword" && contentType != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" && contentType != "application/zip" && contentType != "application/octet-stream") {
		return problem(c, http.StatusUnsupportedMediaType, "upload.invalid_content", "Invalid file content", "The uploaded content does not match a supported resume format")
	}
	if s.storage == nil {
		return problem(c, http.StatusServiceUnavailable, "upload.storage_unavailable", "Upload unavailable", "Resume storage is not configured")
	}
	key := fmt.Sprintf("resumes/public_%d%s", time.Now().UnixNano(), ext)
	if err := s.storage.PutObject(c.Request().Context(), key, contentType, bytes.NewReader(buf), int64(len(buf))); err != nil {
		slog.Error("public resume storage failed", "requestId", c.Response().Header().Get(echo.HeaderXRequestID), "error", err)
		return problem(c, http.StatusInternalServerError, "upload.storage_failed", "Upload failed", "The resume could not be stored")
	}
	if _, auditErr := s.pool.Exec(c.Request().Context(), `INSERT INTO audit_events (action, resource_type, request_id, ip_address, user_agent, metadata) VALUES ('career.resume_uploaded', 'resume_upload', $1, NULLIF($2, '')::inet, $3, jsonb_build_object('sizeBytes', $4::bigint, 'extension', $5::text))`, c.Response().Header().Get(echo.HeaderXRequestID), c.RealIP(), c.Request().UserAgent(), len(buf), ext); auditErr != nil {
		slog.Error("public resume upload audit failed", "error", auditErr)
	}

	publicURL := s.buildPublicAssetURL(c, key)

	return c.JSON(http.StatusOK, map[string]any{
		"resumeUrl":      publicURL,
		"resumeFilename": file.Filename,
		"key":            key,
	})
}

func (s *Server) applyForJob(c echo.Context) error {
	var input hiring.ApplyInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", err.Error())
	}
	result, err := s.hiring.Apply(c.Request().Context(), c.Param("postingId"), input)
	if err != nil {
		if errors.Is(err, hiring.ErrNotFound) {
			return problem(c, http.StatusNotFound, "hiring.posting_not_found", "Not found", "Job posting not found or not active")
		}
		slog.Error("failed to apply for job", "error", err, "postingId", c.Param("postingId"))
		return problem(c, http.StatusInternalServerError, "hiring.apply_failed", "Internal server error", err.Error())
	}
	if _, auditErr := s.pool.Exec(c.Request().Context(), `INSERT INTO audit_events (organization_id, actor_user_id, action, resource_type, resource_id, request_id, ip_address, user_agent, metadata) SELECT jp.organization_id, NULL, 'career.application_submitted', 'job_applicant', $1::uuid, $2, NULLIF($4, '')::inet, $5, jsonb_build_object('postingId', $3::text) FROM job_postings jp WHERE jp.id = $3::uuid`, result.ID, c.Response().Header().Get(echo.HeaderXRequestID), c.Param("postingId"), c.RealIP(), c.Request().UserAgent()); auditErr != nil {
		slog.Error("career application audit failed", "requestId", c.Response().Header().Get(echo.HeaderXRequestID), "error", auditErr)
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listJobPostings(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("hiring:read") && !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to view job postings")
	}
	result, err := s.hiring.ListPostings(c.Request().Context(), principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "hiring.list_failed", "Internal server error", "Could not load job postings")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) createJobPosting(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("hiring:manage") && !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to manage hiring")
	}
	var input hiring.CreateJobPostingInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", err.Error())
	}
	result, err := s.hiring.CreatePosting(c.Request().Context(), principal.OrganizationID, principal.UserID, input)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "hiring.create_posting_failed", "Internal server error", "Could not create job posting")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) createJobOffer(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("hiring:manage") && !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to manage hiring")
	}
	var input hiring.CreateOfferInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", err.Error())
	}
	result, err := s.hiring.CreateOffer(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Param("applicantId"), input)
	if err != nil {
		if errors.Is(err, hiring.ErrNotFound) {
			return problem(c, http.StatusNotFound, "hiring.applicant_not_found", "Not found", "Applicant not found")
		}
		return problem(c, http.StatusInternalServerError, "hiring.create_offer_failed", "Internal server error", "Could not create job offer")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) updateJobPostingStatus(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !principal.HasPermission("hiring:manage") && !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to manage job postings")
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := c.Bind(&body); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", err.Error())
	}
	if err := s.hiring.UpdatePostingStatus(c.Request().Context(), principal.OrganizationID, c.Param("postingId"), body.Status); err != nil {
		if errors.Is(err, hiring.ErrNotFound) {
			return problem(c, http.StatusNotFound, "hiring.posting_not_found", "Not found", "Job posting not found")
		}
		return problem(c, http.StatusUnprocessableEntity, "hiring.invalid_status", "Invalid status", err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) listJobApplicants(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !principal.HasPermission("hiring:read") && !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to view applicants")
	}
	result, err := s.hiring.ListApplicants(c.Request().Context(), principal.OrganizationID, c.Param("postingId"))
	if err != nil {
		if errors.Is(err, hiring.ErrNotFound) {
			return problem(c, http.StatusNotFound, "hiring.posting_not_found", "Not found", "Job posting not found")
		}
		return problem(c, http.StatusInternalServerError, "hiring.list_applicants_failed", "Internal server error", "Could not load applicants")
	}
	return c.JSON(http.StatusOK, map[string]any{"data": result})
}

func (s *Server) listAllApplicants(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !principal.HasPermission("hiring:read") && !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to view applicants")
	}
	result, err := s.hiring.ListAllApplicants(c.Request().Context(), principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "hiring.list_all_applicants_failed", "Internal server error", "Could not load applicants")
	}
	return c.JSON(http.StatusOK, map[string]any{"data": result})
}

func (s *Server) updateApplicantStatus(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	if !principal.HasPermission("hiring:manage") && !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to manage applicants")
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := c.Bind(&body); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", err.Error())
	}
	result, err := s.hiring.UpdateApplicantStatus(c.Request().Context(), principal.OrganizationID, c.Param("applicantId"), body.Status)
	if err != nil {
		if errors.Is(err, hiring.ErrNotFound) {
			return problem(c, http.StatusNotFound, "hiring.applicant_not_found", "Not found", "Applicant not found")
		}
		return problem(c, http.StatusUnprocessableEntity, "hiring.invalid_status", "Invalid status", err.Error())
	}

	// BambooHR-style automation: When candidate is marked as "hired", automatically:
	// 1. Create/link them as an Employee in the directory (status: 'onboarding')
	// 2. Provision their workspace user/membership with default employee role
	// 3. Initiate an onboarding instance with initial tasks
	// 4. Send a branded candidate welcome & self-onboarding email with their direct workspace link
	if strings.ToLower(body.Status) == "hired" {
		go func(applicant hiring.JobApplicant, orgID, actorID string) {
			empID, convErr := s.convertHiredApplicantToEmployee(context.Background(), orgID, actorID, applicant)
			if convErr != nil {
				slog.Error("failed to convert hired applicant to employee", "error", convErr, "applicantId", applicant.ID)
				return
			}
			slog.Info("hired applicant converted to employee successfully", "employeeId", empID, "applicantId", applicant.ID)

			// Fetch company and workspace details for the welcome email
			var subdomain, brandColor, logoURL, orgName string
			_ = s.pool.QueryRow(context.Background(), `
				SELECT COALESCE(w.subdomain, ''), COALESCE(w.brand_color, '#A238FF'), COALESCE(w.logo_url, ''), o.name
				FROM organizations o
				LEFT JOIN workspaces w ON w.organization_id = o.id
				WHERE o.id = $1`, orgID).Scan(&subdomain, &brandColor, &logoURL, &orgName)

			fullName := strings.TrimSpace(applicant.FirstName + " " + applicant.LastName)

			var token string
			tokenBytes := make([]byte, 32)
			if _, err := rand.Read(tokenBytes); err == nil {
				token = hex.EncodeToString(tokenBytes)
				expiresAt := time.Now().Add(7 * 24 * time.Hour)
				var empIDParam any
				if empID != "" {
					empIDParam = empID
				}
				_, errIns := s.pool.Exec(context.Background(), `
					INSERT INTO invitations (organization_id, employee_id, email, name, role, token, status, expires_at)
					VALUES ($1::uuid, $2, $3, $4, 'employee', $5, 'pending', $6)`,
					orgID, empIDParam, applicant.Email, fullName, token, expiresAt)
				if errIns != nil {
					slog.Error("failed to record invitation token for hired applicant", "error", errIns)
				}
			}

			portalURL := ""
			if token != "" {
				if subdomain != "" {
					portalURL = fmt.Sprintf("https://%s.easyhr.pro/auth/accept-invite?token=%s&redirect_url=/my-onboarding", subdomain, token)
				} else {
					portalURL = fmt.Sprintf("https://easyhr.pro/auth/accept-invite?token=%s&redirect_url=/my-onboarding", token)
				}
			} else {
				portalURL = fmt.Sprintf("https://%s.easyhr.pro/auth/accept-invite?email=%s&name=%s&redirect_url=/my-onboarding", subdomain, url.QueryEscape(applicant.Email), url.QueryEscape(fullName))
				if subdomain == "" {
					portalURL = fmt.Sprintf("https://easyhr.pro/auth/accept-invite?email=%s&name=%s&redirect_url=/my-onboarding", url.QueryEscape(applicant.Email), url.QueryEscape(fullName))
				}
			}

			if applicant.Email != "" {
				_ = s.email.SendOnboardingInvitationWithBranding(
					context.Background(),
					applicant.Email,
					applicant.FirstName+" "+applicant.LastName,
					applicant.JobTitle,
					time.Now().Format("Jan 02, 2006"),
					portalURL,
					email.BrandingInfo{
						CompanyName: orgName,
						BrandColor:  brandColor,
						LogoURL:     logoURL,
					},
				)
			}
		}(result, principal.OrganizationID, principal.UserID)
	}

	return c.JSON(http.StatusOK, result)
}

func (s *Server) convertHiredApplicantToEmployee(ctx context.Context, orgID, actorUserID string, applicant hiring.JobApplicant) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Check if employee with work_email already exists in this organization
	var existingEmployeeID string
	err = tx.QueryRow(ctx, `
		SELECT id::text FROM employees
		WHERE organization_id = $1 AND lower(work_email) = lower($2)`,
		orgID, applicant.Email).Scan(&existingEmployeeID)

	employeeID := existingEmployeeID
	if errors.Is(err, pgx.ErrNoRows) {
		var empCount int
		_ = tx.QueryRow(ctx, `SELECT count(*) FROM employees WHERE organization_id = $1`, orgID).Scan(&empCount)
		empNumber := fmt.Sprintf("EMP-%04d", empCount+1)

		err = tx.QueryRow(ctx, `
			INSERT INTO employees (organization_id, employee_number, first_name, last_name, work_email, status, hired_on)
			VALUES ($1, $2, $3, $4, $5, 'onboarding', CURRENT_DATE)
			RETURNING id::text`,
			orgID, empNumber, applicant.FirstName, applicant.LastName, applicant.Email).Scan(&employeeID)
		if err != nil {
			return "", fmt.Errorf("create employee: %w", err)
		}

		if applicant.Phone != nil && *applicant.Phone != "" {
			_, _ = tx.Exec(ctx, `
				INSERT INTO employee_personal_profiles (employee_id, personal_email, personal_phone)
				VALUES ($1, $2, $3)
				ON CONFLICT (employee_id) DO UPDATE SET personal_phone = $3`,
				employeeID, applicant.Email, *applicant.Phone)
		}

		_, _ = tx.Exec(ctx, `
			INSERT INTO employment_terms (employee_id, effective_from, employment_type, worker_type, work_arrangement, fte_basis_points, pay_type, created_by_user_id)
			VALUES ($1, CURRENT_DATE, 'full_time', 'employee', 'on_site', 10000, 'salary', $2)`,
			employeeID, actorUserID)

		_, _ = tx.Exec(ctx, `
			INSERT INTO employment_assignments (employee_id, job_title, effective_from, created_by_user_id)
			VALUES ($1, $2, CURRENT_DATE, $3)`,
			employeeID, applicant.JobTitle, actorUserID)
	}

	// 2. Ensure user and active membership exist
	var userID string
	err = tx.QueryRow(ctx, `
		SELECT id::text FROM users WHERE lower(email) = lower($1)`, applicant.Email).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		clerkID := fmt.Sprintf("candidate_%s", applicant.ID)
		err = tx.QueryRow(ctx, `
			INSERT INTO users (external_identity_id, email, first_name, last_name)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (lower(email)) DO UPDATE SET updated_at = now()
			RETURNING id::text`,
			clerkID, applicant.Email, applicant.FirstName, applicant.LastName).Scan(&userID)
		if err != nil {
			_ = tx.QueryRow(ctx, `SELECT id::text FROM users WHERE lower(email) = lower($1)`, applicant.Email).Scan(&userID)
		}
	}

	if userID != "" {
		var membershipID string
		err = tx.QueryRow(ctx, `
			INSERT INTO memberships (organization_id, user_id, status)
			VALUES ($1, $2, 'active')
			ON CONFLICT (organization_id, user_id) DO UPDATE SET status = 'active'
			RETURNING id::text`, orgID, userID).Scan(&membershipID)
		if err == nil && membershipID != "" {
			var employeeRoleID string
			_ = tx.QueryRow(ctx, `
				SELECT id::text FROM roles WHERE organization_id = $1 AND key = 'employee'`, orgID).Scan(&employeeRoleID)
			if employeeRoleID != "" {
				_, _ = tx.Exec(ctx, `
					INSERT INTO membership_roles (membership_id, role_id)
					VALUES ($1, $2)
					ON CONFLICT DO NOTHING`, membershipID, employeeRoleID)
			}
		}
	}

	// 3. Ensure onboarding instance & default tasks exist
	var instanceID string
	err = tx.QueryRow(ctx, `
		SELECT id::text FROM onboarding_instances
		WHERE employee_id = $1 AND organization_id = $2
		LIMIT 1`, employeeID, orgID).Scan(&instanceID)
	if errors.Is(err, pgx.ErrNoRows) {
		var templateID string
		var templateVersion int
		_ = tx.QueryRow(ctx, `
			SELECT id::text, version FROM onboarding_templates
			WHERE organization_id = $1 AND status = 'published'
			ORDER BY created_at DESC LIMIT 1`, orgID).Scan(&templateID, &templateVersion)

		if templateID == "" {
			_ = tx.QueryRow(ctx, `
				INSERT INTO onboarding_templates (organization_id, name, version, status, created_by_user_id)
				VALUES ($1, 'General Employee Onboarding', 1, 'published', $2)
				RETURNING id::text`, orgID, actorUserID).Scan(&templateID)
			templateVersion = 1

			defaultTasks := []struct {
				title    string
				category string
				order    int
			}{
				{"Complete personal profile & emergency contact", "hr", 1},
				{"Upload National ID or Passport document", "documents", 2},
				{"Review employee handbook & workplace guidelines", "hr", 3},
				{"Confirm direct deposit bank details", "documents", 4},
				{"First day check-in with department manager", "training", 5},
			}
			for _, t := range defaultTasks {
				_, _ = tx.Exec(ctx, `
					INSERT INTO onboarding_template_tasks (template_id, title, category, assignee_type, sort_order)
					VALUES ($1, $2, $3, 'employee', $4)
					ON CONFLICT DO NOTHING`, templateID, t.title, t.category, t.order)
			}
		}

		if templateID != "" {
			err = tx.QueryRow(ctx, `
				INSERT INTO onboarding_instances (organization_id, employee_id, template_id, template_version, start_date, status, initiated_by_user_id)
				VALUES ($1, $2, $3, $4, CURRENT_DATE, 'on_track', $5)
				ON CONFLICT (employee_id, template_id, start_date) DO UPDATE SET status = 'on_track'
				RETURNING id::text`, orgID, employeeID, templateID, templateVersion, actorUserID).Scan(&instanceID)

			if err == nil && instanceID != "" {
				_, _ = tx.Exec(ctx, `
					INSERT INTO onboarding_tasks (onboarding_instance_id, source_template_task_id, title, description, category, assignee_type, due_date, sort_order)
					SELECT $1, id, title, description, category, assignee_type, CURRENT_DATE + (due_offset_days || ' days')::interval, sort_order
					FROM onboarding_template_tasks
					WHERE template_id = $2
					ON CONFLICT DO NOTHING`, instanceID, templateID)
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return employeeID, nil
}

func (s *Server) listAllPerformanceGoals(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	result, err := s.performance.ListAllGoals(c.Request().Context(), principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "performance.goals_failed", "Internal error", "Could not fetch goals")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listPerformanceGoals(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	result, err := s.performance.ListGoals(c.Request().Context(), principal.OrganizationID, c.Param("employeeId"))
	if err != nil {
		return problem(c, http.StatusInternalServerError, "performance.goals_failed", "Internal error", "Could not fetch goals")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listPerformanceReviews(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	var empID *string
	if q := c.QueryParam("employeeId"); q != "" {
		empID = &q
	}
	result, err := s.performance.ListReviews(c.Request().Context(), principal.OrganizationID, empID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "performance.reviews_failed", "Internal error", "Could not fetch reviews")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) createPerformanceGoal(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	var input performance.CreateGoalInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	result, err := s.performance.CreateGoal(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Param("employeeId"), input)
	if err != nil {
		if errors.Is(err, performance.ErrNotFound) {
			return problem(c, http.StatusNotFound, "performance.not_found", "Not found", "Employee not found")
		}
		return problem(c, http.StatusInternalServerError, "performance.create_failed", "Internal error", "Could not create")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) createPerformanceReview(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	var input performance.CreateReviewInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	result, err := s.performance.CreateReview(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Param("employeeId"), input)
	if err != nil {
		if errors.Is(err, performance.ErrNotFound) {
			return problem(c, http.StatusNotFound, "performance.not_found", "Not found", "Employee not found")
		}
		return problem(c, http.StatusInternalServerError, "performance.create_failed", "Internal error", "Could not create")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) createFeedbackRecord(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	// Assuming general access can create feedback (like peers)
	var input performance.CreateFeedbackInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	result, err := s.performance.CreateFeedback(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Param("employeeId"), input)
	if err != nil {
		if errors.Is(err, performance.ErrNotFound) {
			return problem(c, http.StatusNotFound, "performance.not_found", "Not found", "Employee not found")
		}
		return problem(c, http.StatusInternalServerError, "performance.create_failed", "Internal error", "Could not create")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listWorkflowDefinitions(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	result, err := s.workflows.ListDefinitions(c.Request().Context(), principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "workflows.list_definitions_failed", "Internal error", "Could not fetch workflows")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) createWorkflowDefinition(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("workflows:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "No permission")
	}
	var input workflows.CreateDefinitionInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	result, err := s.workflows.CreateDefinition(c.Request().Context(), principal.OrganizationID, principal.UserID, input)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "workflows.create_failed", "Internal error", "Could not create")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listInboxTasks(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	result, err := s.workflows.ListInbox(c.Request().Context(), principal.UserID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "workflows.list_inbox_failed", "Internal error", "Could not fetch inbox")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) decideWorkflowTask(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	var input workflows.DecideTaskInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	err := s.workflows.DecideTask(c.Request().Context(), principal.UserID, c.Param("taskId"), input)
	if err != nil {
		if errors.Is(err, workflows.ErrNotFound) {
			return problem(c, http.StatusNotFound, "workflows.task_not_found", "Not found", "Task not found")
		}
		return problem(c, http.StatusInternalServerError, "workflows.decide_failed", "Internal error", "Could not process decision")
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "success"})
}

func (s *Server) listBenefitPlans(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	result, err := s.benefits.ListPlans(c.Request().Context(), principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "benefits.list_plans_failed", "Internal error", "Could not fetch plans")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) createBenefitPlan(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("benefits:manage") && !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "No permission")
	}
	var input benefits.CreatePlanInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	result, err := s.benefits.CreatePlan(c.Request().Context(), principal.OrganizationID, principal.UserID, input)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "benefits.create_failed", "Internal error", "Could not create")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) updateBenefitPlan(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("benefits:manage") && !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "No permission")
	}
	var input benefits.CreatePlanInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	result, err := s.benefits.UpdatePlan(c.Request().Context(), principal.OrganizationID, c.Param("planId"), input)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "benefits.update_failed", "Internal error", "Could not update benefit plan")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) deleteBenefitPlan(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("benefits:manage") && !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "No permission")
	}
	err := s.benefits.DeletePlan(c.Request().Context(), principal.OrganizationID, c.Param("planId"))
	if err != nil {
		return problem(c, http.StatusInternalServerError, "benefits.delete_failed", "Internal error", "Could not delete benefit plan")
	}
	return c.NoContent(http.StatusNoContent)
}

func (s *Server) listAllBenefitEnrollments(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("benefits:manage") && !principal.HasPermission("benefits:read") && !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have administrative permission to access all employee enrollments")
	}

	result, err := s.benefits.ListAllEnrollments(c.Request().Context(), principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "benefits.list_enrollments_failed", "Internal error", "Could not fetch enrollments")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listBenefitEnrollments(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	targetEmployeeID := c.Param("employeeId")
	isSelf, _ := s.people.IsEmployeeUser(c.Request().Context(), principal.OrganizationID, targetEmployeeID, principal.UserID)
	if !isSelf && !principal.HasPermission("benefits:manage") && !principal.HasPermission("benefits:read") && !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You can only view your own benefit enrollments")
	}

	result, err := s.benefits.ListEnrollments(c.Request().Context(), principal.OrganizationID, targetEmployeeID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "benefits.list_enrollments_failed", "Internal error", "Could not fetch enrollments")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) createBenefitEnrollment(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("benefits:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "No permission")
	}
	var input benefits.CreateEnrollmentInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	result, err := s.benefits.CreateEnrollment(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Param("employeeId"), input)
	if err != nil {
		if errors.Is(err, benefits.ErrNotFound) {
			return problem(c, http.StatusNotFound, "benefits.not_found", "Not found", "Resource not found")
		}
		return problem(c, http.StatusInternalServerError, "benefits.create_failed", "Internal error", "Could not create")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) createRewardStatement(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("benefits:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "No permission")
	}
	var input benefits.CreateRewardStatementInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	result, err := s.benefits.CreateRewardStatement(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Param("employeeId"), input)
	if err != nil {
		if errors.Is(err, benefits.ErrNotFound) {
			return problem(c, http.StatusNotFound, "benefits.not_found", "Not found", "Employee not found")
		}
		return problem(c, http.StatusInternalServerError, "benefits.create_failed", "Internal error", "Could not create")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listPromotions(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("promotions:read") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "No permission")
	}
	result, err := s.promotions.ListPromotions(c.Request().Context(), principal.OrganizationID, c.Param("employeeId"))
	if err != nil {
		return problem(c, http.StatusInternalServerError, "promotions.list_failed", "Internal error", "Could not fetch promotions")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) executePromotion(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("promotions:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "No permission")
	}
	var input promotions.ExecutePromotionInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	result, err := s.promotions.ExecutePromotion(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Param("employeeId"), input)
	if err != nil {
		if errors.Is(err, promotions.ErrNotFound) {
			return problem(c, http.StatusNotFound, "promotions.not_found", "Not found", "Resource not found")
		}
		return problem(c, http.StatusInternalServerError, "promotions.execute_failed", "Internal error", "Could not execute promotion")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listShiftSchedules(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("scheduling:read") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "No permission")
	}
	result, err := s.scheduling.ListSchedules(c.Request().Context(), principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "scheduling.list_failed", "Internal error", "Could not fetch schedules")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) createShiftSchedule(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("scheduling:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "No permission")
	}
	var input scheduling.CreateScheduleInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	result, err := s.scheduling.CreateSchedule(c.Request().Context(), principal.OrganizationID, principal.UserID, input)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "scheduling.create_failed", "Internal error", "Could not create schedule")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listShiftAssignments(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("scheduling:read") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "No permission")
	}
	result, err := s.scheduling.ListAssignments(c.Request().Context(), principal.OrganizationID, c.Param("employeeId"))
	if err != nil {
		return problem(c, http.StatusInternalServerError, "scheduling.list_failed", "Internal error", "Could not fetch assignments")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) createShiftAssignment(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("scheduling:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "No permission")
	}
	var input scheduling.CreateAssignmentInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	result, err := s.scheduling.CreateAssignment(c.Request().Context(), principal.OrganizationID, principal.UserID, c.Param("employeeId"), input)
	if err != nil {
		if errors.Is(err, scheduling.ErrNotFound) {
			return problem(c, http.StatusNotFound, "scheduling.not_found", "Not found", "Resource not found")
		}
		return problem(c, http.StatusInternalServerError, "scheduling.create_failed", "Internal error", "Could not create assignment")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listHolidays(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	result, err := s.holidays.ListHolidays(c.Request().Context(), principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "holidays.list_failed", "Internal error", "Could not fetch holidays")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) createHoliday(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("holidays:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "No permission")
	}
	var input holidays.CreateHolidayInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	result, err := s.holidays.CreateHoliday(c.Request().Context(), principal.OrganizationID, principal.UserID, input)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "holidays.create_failed", "Internal error", "Could not create holiday")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listOffboardingPlans(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	result, err := s.offboarding.ListPlans(c.Request().Context(), principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "offboarding.list_failed", "Internal error", "Could not fetch offboarding plans")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) createOffboardingPlan(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("offboarding:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "No permission")
	}
	var input offboarding.CreatePlanInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	result, err := s.offboarding.CreatePlan(c.Request().Context(), principal.OrganizationID, principal.UserID, input)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "offboarding.create_failed", "Internal error", "Could not create offboarding plan")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listPolicies(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	result, err := s.policies.ListPolicies(c.Request().Context(), principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "policies.list_failed", "Internal error", "Could not fetch policies")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) createPolicy(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("policies:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "No permission")
	}
	var input policies.CreatePolicyInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	result, err := s.policies.CreatePolicy(c.Request().Context(), principal.OrganizationID, principal.UserID, input)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "policies.create_failed", "Internal error", "Could not create policy")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) platformLogin(c echo.Context) error {
	var input platform.LoginInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", err.Error())
	}
	result, err := s.platform.Login(c.Request().Context(), input)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "auth.invalid_credentials", "Unauthorized", "Invalid credentials")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listPlatformOperators(c echo.Context) error {
	_, ok := PlatformPrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	result, err := s.platform.ListOperators(c.Request().Context())
	if err != nil {
		return problem(c, http.StatusInternalServerError, "platform.list_failed", "Internal error", "Could not fetch operators")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) createPlatformOperator(c echo.Context) error {
	principal, ok := PlatformPrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	hasPerm, err := s.platform.HasPermission(c.Request().Context(), principal.OperatorID, "platform:super_admin")
	if err != nil || !hasPerm {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "Only Super Admins can create operators")
	}

	var input platform.CreateOperatorInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	result, err := s.platform.CreateOperator(c.Request().Context(), input)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "platform.create_failed", "Internal error", "Could not create operator")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) createSupportSession(c echo.Context) error {
	principal, ok := PlatformPrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	hasPerm, err := s.platform.HasPermission(c.Request().Context(), principal.OperatorID, "platform:support")
	if err != nil || !hasPerm {
		// Even without explicit support perm, a super admin can do it
		hasGod, _ := s.platform.HasPermission(c.Request().Context(), principal.OperatorID, "platform:super_admin")
		if !hasGod {
			return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "No support permission")
		}
	}

	var input platform.CreateSupportSessionInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	result, err := s.platform.CreateSupportSession(c.Request().Context(), principal.OperatorID, input)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "platform.create_failed", "Internal error", "Could not create support session")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listPlatformPlans(c echo.Context) error {
	_, ok := PlatformPrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	result, err := s.platform.ListPlans(c.Request().Context())
	if err != nil {
		return problem(c, http.StatusInternalServerError, "billing.list_failed", "Internal error", "Could not fetch plans")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listSettlementMethods(c echo.Context) error {
	_, ok := PlatformPrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	result, err := s.platform.ListSettlementMethods(c.Request().Context())
	if err != nil {
		return problem(c, http.StatusInternalServerError, "billing.list_failed", "Internal error", "Could not fetch settlement methods")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) createSettlementMethod(c echo.Context) error {
	principal, ok := PlatformPrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	hasPerm, err := s.platform.HasPermission(c.Request().Context(), principal.OperatorID, "platform:manage_billing")
	if err != nil || !hasPerm {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "No billing permission")
	}

	var input platform.CreateSettlementMethodInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	result, err := s.platform.CreateSettlementMethod(c.Request().Context(), input)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "billing.create_failed", "Internal error", "Could not create settlement method")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listTenantSubscriptions(c echo.Context) error {
	_, ok := PlatformPrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	result, err := s.platform.ListSubscriptions(c.Request().Context())
	if err != nil {
		return problem(c, http.StatusInternalServerError, "billing.list_failed", "Internal error", "Could not fetch subscriptions")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) createTenantSubscription(c echo.Context) error {
	principal, ok := PlatformPrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	hasPerm, err := s.platform.HasPermission(c.Request().Context(), principal.OperatorID, "platform:manage_billing")
	if err != nil || !hasPerm {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "No billing permission")
	}

	var input platform.CreateSubscriptionInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	result, err := s.platform.CreateSubscription(c.Request().Context(), input)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "billing.create_failed", "Internal error", err.Error())
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listTenantInvoices(c echo.Context) error {
	_, ok := PlatformPrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	result, err := s.platform.ListInvoices(c.Request().Context())
	if err != nil {
		return problem(c, http.StatusInternalServerError, "billing.list_failed", "Internal error", "Could not fetch invoices")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) sendTenantInvoice(c echo.Context) error {
	principal, ok := PlatformPrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	hasPerm, err := s.platform.HasPermission(c.Request().Context(), principal.OperatorID, "platform:manage_billing")
	if err != nil || !hasPerm {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "No billing permission")
	}

	result, err := s.platform.SendInvoice(c.Request().Context(), c.Param("id"))
	if err != nil {
		return problem(c, http.StatusInternalServerError, "billing.update_failed", "Internal error", "Could not send invoice")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) recordInvoicePayment(c echo.Context) error {
	principal, ok := PlatformPrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	hasPerm, err := s.platform.HasPermission(c.Request().Context(), principal.OperatorID, "platform:manage_billing")
	if err != nil || !hasPerm {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "No billing permission")
	}

	var input platform.RecordPaymentInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	result, err := s.platform.RecordPayment(c.Request().Context(), c.Param("id"), input)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "billing.payment_failed", "Internal error", "Could not record payment")
	}
	return c.JSON(http.StatusOK, result)
}

// Public Contact
func (s *Server) submitPublicContact(c echo.Context) error {
	var input support.PublicContactInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	if err := s.support.SubmitPublicContact(c.Request().Context(), input); err != nil {
		return problem(c, http.StatusInternalServerError, "support.contact_failed", "Internal error", "Could not submit contact form")
	}
	return c.NoContent(http.StatusOK)
}

// Tenant Support
func (s *Server) listTenantTickets(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	result, err := s.support.ListTenantTickets(c.Request().Context(), principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "support.list_failed", "Internal error", "Could not fetch tickets")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) createTenantTicket(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	var input support.CreateTenantTicketInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	// Assuming the principal has an employee record bound to them.
	// For MVP, we will assume EmployeeID is UserID (since we decoupled it earlier, we should really lookup the EmployeeID for the UserID).
	// To keep it simple, we pass UserID and assume the tenant.go handles the relationship, or it fails FK.
	// Actually we should look up employee_id. Let's assume EmployeeID is present or we just use UserID for now.
	// Wait, earlier we used employeeID as param. The user who is logged in acts as an employee.
	// Let's query their employee ID.
	var empID string
	err := s.pool.QueryRow(c.Request().Context(), "SELECT id::text FROM employees WHERE user_id = $1 AND organization_id = $2", principal.UserID, principal.OrganizationID).Scan(&empID)
	if err != nil {
		return problem(c, http.StatusForbidden, "support.no_employee", "Forbidden", "Must be an employee to create tickets")
	}

	result, err := s.support.CreateTenantTicket(c.Request().Context(), principal.OrganizationID, empID, input)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "support.create_failed", "Internal error", err.Error())
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) addTenantTicketMessage(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	var empID string
	err := s.pool.QueryRow(c.Request().Context(), "SELECT id::text FROM employees WHERE user_id = $1 AND organization_id = $2", principal.UserID, principal.OrganizationID).Scan(&empID)
	if err != nil {
		return problem(c, http.StatusForbidden, "support.no_employee", "Forbidden", "Must be an employee to reply")
	}

	var input support.CreateTicketMessageInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}

	if err := s.support.AddTenantMessage(c.Request().Context(), principal.OrganizationID, empID, c.Param("id"), input); err != nil {
		return problem(c, http.StatusInternalServerError, "support.reply_failed", "Internal error", "Could not reply")
	}
	return c.NoContent(http.StatusOK)
}

func (s *Server) listTenantTicketMessages(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	result, err := s.support.ListTenantTicketMessages(c.Request().Context(), principal.OrganizationID, c.Param("id"))
	if err != nil {
		return problem(c, http.StatusInternalServerError, "support.messages_failed", "Internal error", "Could not fetch ticket messages")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) updateTenantTicketStatus(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := c.Bind(&body); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", err.Error())
	}
	if err := s.support.UpdateTenantTicketStatus(c.Request().Context(), principal.OrganizationID, c.Param("id"), body.Status); err != nil {
		return problem(c, http.StatusInternalServerError, "support.status_failed", "Internal error", "Could not update ticket status")
	}
	return c.NoContent(http.StatusOK)
}

// Platform Support
func (s *Server) listPlatformTickets(c echo.Context) error {
	_, ok := PlatformPrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	result, err := s.support.ListPlatformTickets(c.Request().Context())
	if err != nil {
		return problem(c, http.StatusInternalServerError, "support.list_failed", "Internal error", "Could not fetch platform tickets")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) addPlatformTicketMessage(c echo.Context) error {
	principal, ok := PlatformPrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	hasPerm, err := s.platform.HasPermission(c.Request().Context(), principal.OperatorID, "platform:support")
	if err != nil || !hasPerm {
		hasGod, _ := s.platform.HasPermission(c.Request().Context(), principal.OperatorID, "platform:super_admin")
		if !hasGod {
			return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "No support permission")
		}
	}

	var input support.CreateTicketMessageInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	if err := s.support.AddPlatformMessage(c.Request().Context(), principal.OperatorID, c.Param("id"), input); err != nil {
		return problem(c, http.StatusInternalServerError, "support.reply_failed", "Internal error", "Could not reply")
	}
	return c.NoContent(http.StatusOK)
}

// Tenant Operations
func (s *Server) createTenantDataJob(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("settings:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "No permission")
	}

	var input operations.CreateDataJobInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	result, err := s.operations.CreateTenantDataJob(c.Request().Context(), principal.OrganizationID, principal.UserID, input)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "operations.create_failed", "Internal error", "Could not create data job")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listTenantFeatures(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	result, err := s.operations.ListTenantFeatures(c.Request().Context(), principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "operations.list_failed", "Internal error", "Could not fetch features")
	}
	return c.JSON(http.StatusOK, result)
}

// Platform Operations
func (s *Server) listPlatformDataJobs(c echo.Context) error {
	_, ok := PlatformPrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	result, err := s.operations.ListPlatformDataJobs(c.Request().Context())
	if err != nil {
		return problem(c, http.StatusInternalServerError, "operations.list_failed", "Internal error", "Could not fetch platform data jobs")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listComplianceRequests(c echo.Context) error {
	_, ok := PlatformPrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	result, err := s.operations.ListPlatformComplianceRequests(c.Request().Context())
	if err != nil {
		return problem(c, http.StatusInternalServerError, "operations.list_failed", "Internal error", "Could not fetch compliance requests")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) listPlatformFeatures(c echo.Context) error {
	_, ok := PlatformPrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	result, err := s.operations.ListPlatformFeatures(c.Request().Context())
	if err != nil {
		return problem(c, http.StatusInternalServerError, "operations.list_failed", "Internal error", "Could not fetch platform features")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) setTenantFeatureOverride(c echo.Context) error {
	principal, ok := PlatformPrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	hasPerm, err := s.platform.HasPermission(c.Request().Context(), principal.OperatorID, "platform:super_admin")
	if err != nil || !hasPerm {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "Only Super Admins can override features")
	}

	var input operations.SetFeatureOverrideInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	if err := s.operations.SetTenantFeatureOverride(c.Request().Context(), principal.OperatorID, c.Param("key"), c.Param("orgId"), input); err != nil {
		return problem(c, http.StatusInternalServerError, "operations.update_failed", "Internal error", "Could not set feature override")
	}
	return c.NoContent(http.StatusOK)
}

// Identity Roles
func (s *Server) listRoles(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	result, err := s.identity.ListRoles(c.Request().Context(), principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "roles.list_failed", "Internal error", "Could not fetch roles")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) createRole(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("settings:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "No permission")
	}

	var input identity.CreateRoleInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	result, err := s.identity.CreateRole(c.Request().Context(), principal.OrganizationID, input)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "roles.create_failed", "Internal error", "Could not create role")
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) updateRolePermissions(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("settings:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "No permission")
	}

	var input identity.UpdateRolePermissionsInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	if err := s.identity.UpdateRolePermissions(c.Request().Context(), principal.OrganizationID, c.Param("id"), input); err != nil {
		return problem(c, http.StatusInternalServerError, "roles.update_failed", "Internal error", "Could not update permissions")
	}
	return c.NoContent(http.StatusOK)
}

func (s *Server) assignRoleToEmployee(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	if !principal.HasPermission("employees:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "No permission")
	}

	var input identity.AssignRole
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid", err.Error())
	}
	if err := s.identity.AssignRoleToEmployee(c.Request().Context(), principal.OrganizationID, c.Param("employeeId"), input.RoleID); err != nil {
		return problem(c, http.StatusInternalServerError, "roles.assign_failed", "Internal error", "Could not assign role")
	}
	return c.NoContent(http.StatusOK)
}

func (s *Server) listPermissions(c echo.Context) error {
	_, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}
	result, err := s.identity.ListAllPermissions(c.Request().Context())
	if err != nil {
		return problem(c, http.StatusInternalServerError, "permissions.list_failed", "Internal error", "Could not fetch permissions")
	}
	return c.JSON(http.StatusOK, result)
}

// Platform Audit
func (s *Server) listPlatformAuditLogs(c echo.Context) error {
	_, ok := PlatformPrincipalFromContext(c.Request().Context())
	if !ok {
		return c.NoContent(http.StatusUnauthorized)
	}

	result, err := s.operations.ListPlatformAuditLogs(c.Request().Context())
	if err != nil {
		return problem(c, http.StatusInternalServerError, "audit.list_failed", "Internal error", "Could not fetch audit logs")
	}
	return c.JSON(http.StatusOK, result)
}

// ─── Messaging Handlers ──────────────────────────────────────────────────────

func (s *Server) createEmailThread(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}
	if !principal.HasPermission("hiring:manage") && !principal.HasPermission("hiring:read") && !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "You do not have permission to send emails to candidates")
	}
	var input struct {
		ApplicantID    string `json:"applicantId"`
		Subject        string `json:"subject"`
		BodyHTML       string `json:"bodyHtml"`
		CandidateEmail string `json:"candidateEmail"`
		CandidateName  string `json:"candidateName"`
		SenderName     string `json:"senderName"`
		SenderEmail    string `json:"senderEmail"`
	}
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	if input.ApplicantID == "" || input.Subject == "" || input.BodyHTML == "" || input.CandidateEmail == "" {
		return problem(c, http.StatusUnprocessableEntity, "validation.missing_fields", "Missing required fields", "applicantId, subject, bodyHtml, and candidateEmail are required")
	}
	if input.SenderName == "" {
		input.SenderName = "Hiring Team"
	}

	thread, err := s.messaging.CreateThread(c.Request().Context(), principal.OrganizationID, input.ApplicantID, principal.UserID, messaging.SendEmailInput{
		Subject:        input.Subject,
		BodyHTML:       input.BodyHTML,
		CandidateEmail: input.CandidateEmail,
		CandidateName:  input.CandidateName,
		SenderName:     input.SenderName,
		SenderEmail:    input.SenderEmail,
	})
	if err != nil {
		slog.Error("create thread failed", "err", err)
		return problem(c, http.StatusInternalServerError, "messaging.create_thread_failed", "Failed to create thread", err.Error())
	}
	return c.JSON(http.StatusCreated, thread)
}

func (s *Server) listEmailThreads(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Context missing")
	}
	if !principal.HasPermission("hiring:manage") && !principal.HasPermission("hiring:read") && !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "Insufficient permission")
	}
	applicantID := strings.TrimSpace(c.QueryParam("applicantId"))
	threads, err := s.messaging.ListThreads(c.Request().Context(), principal.OrganizationID, applicantID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "messaging.list_threads_failed", "Failed to list threads", err.Error())
	}
	return c.JSON(http.StatusOK, map[string]any{"data": threads})
}

func (s *Server) getEmailThreadMessages(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Context missing")
	}
	if !principal.HasPermission("hiring:manage") && !principal.HasPermission("hiring:read") && !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "Insufficient permission")
	}
	threadID := c.Param("threadId")
	msgs, err := s.messaging.GetThreadMessages(c.Request().Context(), principal.OrganizationID, threadID)
	if errors.Is(err, messaging.ErrNotFound) {
		return problem(c, http.StatusNotFound, "messaging.thread_not_found", "Thread not found", "Thread not found")
	}
	if err != nil {
		return problem(c, http.StatusInternalServerError, "messaging.get_messages_failed", "Failed to get messages", err.Error())
	}
	return c.JSON(http.StatusOK, map[string]any{"data": msgs})
}

func (s *Server) replyToEmailThread(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Context missing")
	}
	if !principal.HasPermission("hiring:manage") && !principal.HasPermission("hiring:read") && !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "Insufficient permission")
	}
	var input struct {
		BodyHTML    string `json:"bodyHtml"`
		SenderName  string `json:"senderName"`
		SenderEmail string `json:"senderEmail"`
		SenderType  string `json:"senderType"` // "recruiter" | "candidate"
	}
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	if strings.TrimSpace(input.BodyHTML) == "" {
		return problem(c, http.StatusUnprocessableEntity, "validation.missing_body", "Missing body", "bodyHtml is required")
	}
	threadID := c.Param("threadId")

	var msg messaging.EmailMessage
	var err error
	if strings.ToLower(input.SenderType) == "candidate" {
		msg, err = s.messaging.RecordCandidateReply(c.Request().Context(), principal.OrganizationID, threadID, input.SenderName, input.BodyHTML)
	} else {
		if input.SenderName == "" {
			input.SenderName = "Hiring Team"
		}
		msg, err = s.messaging.ReplyToThread(c.Request().Context(), principal.OrganizationID, threadID, principal.UserID, input.SenderName, input.SenderEmail, input.BodyHTML)
	}

	if errors.Is(err, messaging.ErrNotFound) {
		return problem(c, http.StatusNotFound, "messaging.thread_not_found", "Thread not found", "Thread not found")
	}
	if err != nil {
		slog.Error("reply to thread failed", "err", err)
		return problem(c, http.StatusInternalServerError, "messaging.reply_failed", "Failed to send reply", err.Error())
	}
	return c.JSON(http.StatusCreated, msg)
}

func (s *Server) recordCandidateReply(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Context missing")
	}
	var input struct {
		BodyHTML   string `json:"bodyHtml"`
		SenderName string `json:"senderName"`
	}
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	if strings.TrimSpace(input.BodyHTML) == "" {
		return problem(c, http.StatusUnprocessableEntity, "validation.missing_body", "Missing body", "bodyHtml is required")
	}
	threadID := c.Param("threadId")
	msg, err := s.messaging.RecordCandidateReply(c.Request().Context(), principal.OrganizationID, threadID, input.SenderName, input.BodyHTML)
	if errors.Is(err, messaging.ErrNotFound) {
		return problem(c, http.StatusNotFound, "messaging.thread_not_found", "Thread not found", "Thread not found")
	}
	if err != nil {
		slog.Error("record candidate reply failed", "err", err)
		return problem(c, http.StatusInternalServerError, "messaging.candidate_reply_failed", "Failed to record candidate reply", err.Error())
	}
	return c.JSON(http.StatusCreated, msg)
}

// Inbound email webhook for receiving external replies from Resend Inbound (email.received event) and manual clients
func (s *Server) inboundEmailWebhook(c echo.Context) error {
	rawBytes, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return problem(c, http.StatusBadRequest, "inbound.read_body_failed", "Failed to read body", err.Error())
	}
	if !verifyResendWebhook(os.Getenv("RESEND_WEBHOOK_SECRET"), c.Request().Header.Get("svix-id"), c.Request().Header.Get("svix-timestamp"), c.Request().Header.Get("svix-signature"), rawBytes, time.Now()) {
		if _, auditErr := s.pool.Exec(c.Request().Context(), `INSERT INTO audit_events (action,resource_type,request_id,ip_address,user_agent,metadata) VALUES ('inbound_webhook.signature_rejected','inbound_email',$1,NULLIF($2,'')::inet,$3,jsonb_build_object('provider','resend','payloadBytes',$4::bigint))`, c.Response().Header().Get(echo.HeaderXRequestID), c.RealIP(), c.Request().UserAgent(), len(rawBytes)); auditErr != nil {
			slog.Error("inbound webhook rejection audit failed", "error", auditErr)
		}
		return problem(c, http.StatusUnauthorized, "inbound.signature_invalid", "Webhook signature invalid", "The inbound email webhook signature could not be verified")
	}

	// 1. Try parsing as Resend Webhook event (type: "email.received")
	var resendEvent struct {
		Type      string `json:"type"`
		CreatedAt string `json:"created_at"`
		Data      struct {
			EmailID   string   `json:"email_id"`
			CreatedAt string   `json:"created_at"`
			From      string   `json:"from"`
			To        []string `json:"to"`
			Subject   string   `json:"subject"`
			MessageID string   `json:"message_id"`
		} `json:"data"`
	}

	if err := json.Unmarshal(rawBytes, &resendEvent); err == nil && resendEvent.Type == "email.received" && resendEvent.Data.EmailID != "" {
		if _, auditErr := s.pool.Exec(c.Request().Context(), `INSERT INTO audit_events (action, resource_type, request_id, ip_address, user_agent, metadata) VALUES ('inbound_webhook.received', 'inbound_email', $1, NULLIF($2, '')::inet, $3, jsonb_build_object('provider', 'resend', 'emailId', $4))`, c.Response().Header().Get(echo.HeaderXRequestID), c.RealIP(), c.Request().UserAgent(), resendEvent.Data.EmailID); auditErr != nil {
			slog.Error("inbound email audit event failed", "error", auditErr)
		}
		slog.Info("processing resend inbound email webhook", "email_id", resendEvent.Data.EmailID, "from", resendEvent.Data.From, "subject", resendEvent.Data.Subject)

		// Fetch full email content from Resend Receiving API
		var details *email.ReceivedEmailDetails
		if s.email != nil {
			details, err = s.email.RetrieveReceivedEmail(c.Request().Context(), resendEvent.Data.EmailID)
			if err != nil {
				slog.Error("failed to retrieve received email from resend", "email_id", resendEvent.Data.EmailID, "err", err)
			}
		}

		senderEmail := resendEvent.Data.From
		senderName := resendEvent.Data.From
		subject := resendEvent.Data.Subject
		recipientEmail := "notifications@recruter.easyhr.pro"
		if len(resendEvent.Data.To) > 0 {
			recipientEmail = resendEvent.Data.To[0]
		}
		bodyHTML := ""
		bodyText := ""
		inReplyTo := ""
		messageID := resendEvent.Data.MessageID

		if details != nil {
			if details.From != "" {
				senderEmail = details.From
			}
			if len(details.To) > 0 {
				recipientEmail = details.To[0]
			}
			if details.Subject != "" {
				subject = details.Subject
			}
			bodyHTML = details.HTML
			bodyText = details.Text
			if details.MessageID != "" {
				messageID = details.MessageID
			}
			if details.Headers != nil {
				if fromHeader, ok := details.Headers["from"]; ok && fromHeader != "" {
					senderName = fromHeader
				}
				if irt, ok := details.Headers["in-reply-to"]; ok {
					inReplyTo = irt
				} else if ref, ok := details.Headers["references"]; ok {
					inReplyTo = ref
				}
			}
		}

		msg, procErr := s.messaging.ProcessInboundEmail(c.Request().Context(), messaging.ProcessInboundInput{
			ResendEmailID:  resendEvent.Data.EmailID,
			SenderEmail:    senderEmail,
			SenderName:     senderName,
			RecipientEmail: recipientEmail,
			Subject:        subject,
			BodyHTML:       bodyHTML,
			BodyText:       bodyText,
			MessageID:      messageID,
			InReplyTo:      inReplyTo,
			RawJSON:        rawBytes,
		})
		if procErr != nil {
			slog.Warn("inbound email saved to inbound_emails but unmatched to active thread", "err", procErr)
			return c.JSON(http.StatusOK, map[string]interface{}{
				"success": true,
				"status":  "received_unmatched",
				"emailId": resendEvent.Data.EmailID,
			})
		}

		return c.JSON(http.StatusOK, map[string]interface{}{
			"success": true,
			"status":  "matched_thread",
			"message": msg,
		})
	}

	// 2. Direct / manual test payload fallback
	var input struct {
		ThreadID    string `json:"threadId"`
		SenderEmail string `json:"senderEmail"`
		SenderName  string `json:"senderName"`
		Subject     string `json:"subject"`
		BodyHTML    string `json:"bodyHtml"`
		From        string `json:"from"`
		Text        string `json:"text"`
		HTML        string `json:"html"`
	}
	if err := json.Unmarshal(rawBytes, &input); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}

	body := input.BodyHTML
	if body == "" {
		body = input.HTML
	}
	if body == "" {
		body = input.Text
	}
	if strings.TrimSpace(body) == "" {
		body = "<p>No content</p>"
	}

	senderEmail := input.SenderEmail
	senderName := input.SenderName
	if senderEmail == "" && input.From != "" {
		senderEmail = input.From
		senderName = input.From
	}

	msg, err := s.messaging.InboundWebhookMessage(c.Request().Context(), input.ThreadID, senderEmail, senderName, input.Subject, body)
	if errors.Is(err, messaging.ErrNotFound) {
		return problem(c, http.StatusNotFound, "messaging.thread_not_found", "Thread not found", "No matching thread found for inbound message")
	}
	if err != nil {
		slog.Error("inbound webhook message failed", "err", err)
		return problem(c, http.StatusInternalServerError, "messaging.inbound_failed", "Failed to process inbound message", err.Error())
	}

	return c.JSON(http.StatusOK, map[string]interface{}{"success": true, "message": msg})
}

func (s *Server) listInboundEmails(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Context missing")
	}
	records, err := s.messaging.ListInboundEmails(c.Request().Context(), principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "messaging.list_inbound_failed", "Failed to list inbound emails", err.Error())
	}
	return c.JSON(http.StatusOK, map[string]interface{}{"data": records})
}

func (s *Server) listEmailMacros(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Context missing")
	}
	macros, err := s.messaging.ListMacros(c.Request().Context(), principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "messaging.list_macros_failed", "Failed to list macros", err.Error())
	}
	return c.JSON(http.StatusOK, map[string]any{"data": macros})
}

func (s *Server) createEmailMacro(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Context missing")
	}
	if !principal.HasPermission("hiring:manage") && !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "Insufficient permission")
	}
	var input messaging.CreateMacroInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	if input.Title == "" || input.Shortcut == "" || input.Subject == "" {
		return problem(c, http.StatusUnprocessableEntity, "validation.missing_fields", "Missing fields", "title, shortcut, and subject are required")
	}
	if input.Category == "" {
		input.Category = "hiring"
	}
	macro, err := s.messaging.CreateMacro(c.Request().Context(), principal.OrganizationID, principal.UserID, input)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "messaging.create_macro_failed", "Failed to create macro", err.Error())
	}
	return c.JSON(http.StatusCreated, macro)
}

func (s *Server) updateEmailMacro(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Context missing")
	}
	if !principal.HasPermission("hiring:manage") && !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "Insufficient permission")
	}
	var input messaging.UpdateMacroInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusUnprocessableEntity, "validation.invalid_json", "Invalid request", "Request body must be valid JSON")
	}
	macroID := c.Param("macroId")
	macro, err := s.messaging.UpdateMacro(c.Request().Context(), principal.OrganizationID, macroID, input)
	if errors.Is(err, messaging.ErrNotFound) {
		return problem(c, http.StatusNotFound, "messaging.macro_not_found", "Macro not found", "Macro not found")
	}
	if err != nil {
		return problem(c, http.StatusInternalServerError, "messaging.update_macro_failed", "Failed to update macro", err.Error())
	}
	return c.JSON(http.StatusOK, macro)
}

func (s *Server) deleteEmailMacro(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Context missing")
	}
	if !principal.HasPermission("hiring:manage") && !principal.HasPermission("organization:manage") {
		return problem(c, http.StatusForbidden, "authorization.insufficient_scope", "Forbidden", "Insufficient permission")
	}
	macroID := c.Param("macroId")
	if err := s.messaging.DeleteMacro(c.Request().Context(), principal.OrganizationID, macroID); errors.Is(err, messaging.ErrNotFound) {
		return problem(c, http.StatusNotFound, "messaging.macro_not_found", "Macro not found", "Macro not found")
	} else if err != nil {
		return problem(c, http.StatusInternalServerError, "messaging.delete_macro_failed", "Failed to delete macro", err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

type InvitationResponse struct {
	Valid            bool   `json:"valid"`
	Error            string `json:"error,omitempty"`
	Email            string `json:"email,omitempty"`
	Name             string `json:"name,omitempty"`
	Role             string `json:"role,omitempty"`
	OrganizationName string `json:"organizationName,omitempty"`
	Subdomain        string `json:"subdomain,omitempty"`
	BrandColor       string `json:"brandColor,omitempty"`
	LogoURL          string `json:"logoUrl,omitempty"`
}

func (s *Server) getInvitationByToken(c echo.Context) error {
	token := strings.TrimSpace(c.QueryParam("token"))
	if token == "" {
		return c.JSON(http.StatusOK, InvitationResponse{
			Valid: false,
			Error: "Invitation token is required.",
		})
	}

	var (
		id, orgID, email, name, role, status    string
		empID                                   *string
		expiresAt                               time.Time
		orgName, subdomain, brandColor, logoURL string
	)

	err := s.pool.QueryRow(c.Request().Context(), `
		SELECT i.id::text, i.organization_id::text, i.employee_id::text, i.email, i.name, i.role, i.status, i.expires_at,
		       o.name, COALESCE(w.subdomain, ''), COALESCE(w.brand_color, '#A238FF'), COALESCE(w.logo_url, '')
		FROM invitations i
		JOIN organizations o ON o.id = i.organization_id
		LEFT JOIN workspaces w ON w.organization_id = o.id
		WHERE i.token = $1`, token).Scan(&id, &orgID, &empID, &email, &name, &role, &status, &expiresAt, &orgName, &subdomain, &brandColor, &logoURL)
	if _, auditErr := s.pool.Exec(c.Request().Context(), `INSERT INTO audit_events (organization_id, action, resource_type, resource_id, request_id, ip_address, user_agent, metadata) VALUES (NULLIF($1, '')::uuid, 'public.invitation_lookup', 'invitation', NULLIF($2, '')::uuid, $3, NULLIF($4, '')::inet, $5, jsonb_build_object('found', $6::boolean))`, orgID, id, c.Response().Header().Get(echo.HeaderXRequestID), c.RealIP(), c.Request().UserAgent(), err == nil); auditErr != nil {
		slog.Error("public invitation lookup audit failed", "error", auditErr)
	}

	if err != nil {
		return c.JSON(http.StatusOK, InvitationResponse{
			Valid: false,
			Error: "This invitation link is invalid or could not be found.",
		})
	}

	if status == "accepted" {
		return c.JSON(http.StatusOK, InvitationResponse{
			Valid: false,
			Error: "This invitation has already been accepted. Please sign in to access your portal.",
		})
	}
	if status == "revoked" {
		return c.JSON(http.StatusOK, InvitationResponse{
			Valid: false,
			Error: "This invitation has been revoked. Please contact your company administrator.",
		})
	}
	if time.Now().After(expiresAt) {
		return c.JSON(http.StatusOK, InvitationResponse{
			Valid: false,
			Error: "This invitation link has expired. Please contact your company administrator for a new invitation.",
		})
	}

	// Try to get specific job title if employee_id is linked
	if empID != nil && *empID != "" {
		var jobTitle string
		_ = s.pool.QueryRow(c.Request().Context(), `
			SELECT COALESCE(ea.job_title, '')
			FROM employment_assignments ea
			WHERE ea.employee_id = $1::uuid AND ea.effective_to IS NULL
			LIMIT 1`, *empID).Scan(&jobTitle)
		if jobTitle != "" {
			role = jobTitle
		}
	}

	return c.JSON(http.StatusOK, InvitationResponse{
		Valid:            true,
		Email:            email,
		Name:             name,
		Role:             role,
		OrganizationName: orgName,
		Subdomain:        subdomain,
		BrandColor:       brandColor,
		LogoURL:          logoURL,
	})
}
