package timeoff

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

type ListRequestsInput struct {
	EmployeeID        string
	Status            string
	Cursor            string
	Limit             int
	CanApproveManager bool
	CanApproveHR      bool
}

type LeaveRequestPage struct {
	Data []LeaveRequest `json:"data"`
	Page CursorPage     `json:"page"`
}

type CursorPage struct {
	NextCursor *string `json:"nextCursor"`
	Limit      int     `json:"limit"`
	HasMore    bool    `json:"hasMore"`
}

type leaveRequestCursor struct {
	SubmittedAt time.Time `json:"submittedAt"`
	ID          string    `json:"id"`
}

func (service *Service) ListRequests(ctx context.Context, organizationID, actorUserID string, input ListRequestsInput) (LeaveRequestPage, error) {
	if input.Limit < 1 || input.Limit > 100 {
		return LeaveRequestPage{}, fmt.Errorf("limit must be between 1 and 100")
	}
	if input.Status != "" && !isLeaveRequestStatus(input.Status) {
		return LeaveRequestPage{}, fmt.Errorf("status is invalid")
	}
	var cursor *leaveRequestCursor
	if input.Cursor != "" {
		parsed, err := decodeLeaveRequestCursor(input.Cursor)
		if err != nil {
			return LeaveRequestPage{}, fmt.Errorf("cursor is invalid")
		}
		cursor = &parsed
	}

	var cursorTime *time.Time
	var cursorID string
	if cursor != nil {
		cursorTime, cursorID = &cursor.SubmittedAt, cursor.ID
	}
	rows, err := service.pool.Query(ctx, `
		SELECT request.id::text, request.employee_id::text, request.leave_type_id::text,
		       request.start_date::text, request.end_date::text, request.requested_minutes,
		       request.status, request.submitted_at, request.reason
		FROM leave_requests request
		JOIN employees employee ON employee.id = request.employee_id
		WHERE request.organization_id = $1
		  AND ($2 = '' OR request.employee_id = $2)
		  AND ($3 = '' OR request.status = $3)
		  AND (
			 employee.user_id = $4
			 OR $5::boolean
			 OR ($6::boolean AND EXISTS (
				 SELECT 1 FROM employment_assignments subject
				 JOIN employees manager ON manager.id = subject.manager_employee_id
				 WHERE subject.employee_id = request.employee_id
				   AND subject.effective_to IS NULL
				   AND manager.organization_id = request.organization_id
				   AND manager.user_id = $4
			 ))
		  )
		  AND ($7::timestamptz IS NULL OR (request.submitted_at, request.id) < ($7::timestamptz, $8::uuid))
		ORDER BY request.submitted_at DESC, request.id DESC
		LIMIT $9`, organizationID, input.EmployeeID, input.Status, actorUserID, input.CanApproveHR, input.CanApproveManager, cursorTime, nullableUUID(cursorID), input.Limit+1)
	if err != nil {
		return LeaveRequestPage{}, err
	}
	defer rows.Close()
	items := make([]LeaveRequest, 0, input.Limit+1)
	var timestamps []time.Time
	for rows.Next() {
		var item LeaveRequest
		var submittedAt time.Time
		if err = rows.Scan(&item.ID, &item.EmployeeID, &item.LeaveTypeID, &item.StartDate, &item.EndDate, &item.RequestedMinutes, &item.Status, &submittedAt, &item.Reason); err != nil {
			return LeaveRequestPage{}, err
		}
		item.SubmittedAt = submittedAt.UTC().Format(time.RFC3339)
		items, timestamps = append(items, item), append(timestamps, submittedAt)
	}
	if err = rows.Err(); err != nil {
		return LeaveRequestPage{}, err
	}
	page := LeaveRequestPage{Data: items, Page: CursorPage{Limit: input.Limit}}
	if len(items) > input.Limit {
		page.Page.HasMore = true
		page.Data = items[:input.Limit]
		last := page.Data[len(page.Data)-1]
		encoded, err := encodeLeaveRequestCursor(leaveRequestCursor{SubmittedAt: timestamps[input.Limit-1], ID: last.ID})
		if err != nil {
			return LeaveRequestPage{}, err
		}
		page.Page.NextCursor = &encoded
	}
	return page, nil
}

func isLeaveRequestStatus(value string) bool {
	for _, status := range []string{"pending_manager", "pending_hr", "approved", "declined", "cancelled"} {
		if value == status {
			return true
		}
	}
	return false
}

func encodeLeaveRequestCursor(value leaveRequestCursor) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeLeaveRequestCursor(value string) (leaveRequestCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return leaveRequestCursor{}, err
	}
	var parsed leaveRequestCursor
	if err = json.Unmarshal(raw, &parsed); err != nil || parsed.ID == "" || parsed.SubmittedAt.IsZero() {
		return leaveRequestCursor{}, fmt.Errorf("invalid cursor")
	}
	return parsed, nil
}

func nullableUUID(value string) any {
	if value == "" {
		return nil
	}
	return value
}
