package operations

import (
	"context"
)

type ComplianceRequest struct {
	ID              string  `json:"id"`
	OrganizationID  string  `json:"organizationId"`
	RequestType     string  `json:"requestType"`
	SubjectEmail    string  `json:"subjectEmail"`
	Status          string  `json:"status"`
	ResolutionNotes *string `json:"resolutionNotes"`
}

func (s *Service) ListPlatformComplianceRequests(ctx context.Context) ([]ComplianceRequest, error) {
	rows, err := s.pool.Query(ctx, "SELECT id::text, organization_id::text, request_type, subject_email, status, resolution_notes FROM tenant_compliance_requests ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []ComplianceRequest
	for rows.Next() {
		var c ComplianceRequest
		if err := rows.Scan(&c.ID, &c.OrganizationID, &c.RequestType, &c.SubjectEmail, &c.Status, &c.ResolutionNotes); err != nil {
			return nil, err
		}
		items = append(items, c)
	}
	return items, nil
}
