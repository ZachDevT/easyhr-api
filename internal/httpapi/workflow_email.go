package httpapi

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/easyhr/easyhr-be/internal/email"
)

func (s *Server) notifyLeaveRequest(ctx context.Context, organizationID, requestID, status string) {
	var to, employee, leaveType, startDate, endDate, company, logo, color string
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(NULLIF(p.personal_email, ''), e.work_email),
		       e.first_name || ' ' || e.last_name, t.name,
		       r.start_date::text, r.end_date::text, o.name,
		       COALESCE(w.logo_url, ''), COALESCE(w.brand_color, '#4F46E5')
		FROM leave_requests r
		JOIN employees e ON e.id = r.employee_id
		JOIN leave_types t ON t.id = r.leave_type_id
		JOIN organizations o ON o.id = r.organization_id
		LEFT JOIN employee_personal_profiles p ON p.employee_id = e.id
		LEFT JOIN workspaces w ON w.organization_id = o.id
		WHERE r.id = $1::uuid AND r.organization_id = $2::uuid`, requestID, organizationID).
		Scan(&to, &employee, &leaveType, &startDate, &endDate, &company, &logo, &color)
	if err != nil {
		slog.Error("leave notification context lookup failed", "requestId", requestID, "error", err)
		return
	}
	if to == "" || s.email == nil {
		return
	}
	subject := fmt.Sprintf("Leave request %s", status)
	body := fmt.Sprintf("%s’s %s leave request for %s to %s was %s.", employee, leaveType, startDate, endDate, status)
	err = s.email.SendWorkflowNotice(ctx, to, subject, "Leave request update", body, "View leave requests", "https://easyhr.pro/time-off", email.BrandingInfo{CompanyName: company, LogoURL: logo, BrandColor: color})
	if err != nil {
		slog.Error("leave notification email failed", "requestId", requestID, "error", err)
	}
}
