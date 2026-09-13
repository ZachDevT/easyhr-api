package timeoff

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

var ErrLeaveTypeConflict = errors.New("leave type conflicts with an existing company leave type")
var leaveTypeCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,62}$`)

type CreateLeaveTypeInput struct {
	Code             string `json:"code"`
	Name             string `json:"name"`
	Unit             string `json:"unit"`
	IsPaid           bool   `json:"isPaid"`
	RequiresApproval bool   `json:"requiresApproval"`
}
type LeaveType struct {
	ID               string `json:"id"`
	Code             string `json:"code"`
	Name             string `json:"name"`
	Unit             string `json:"unit"`
	IsPaid           bool   `json:"isPaid"`
	RequiresApproval bool   `json:"requiresApproval"`
	Status           string `json:"status"`
}

func (input CreateLeaveTypeInput) Validate() error {
	if !leaveTypeCodePattern.MatchString(input.Code) {
		return fmt.Errorf("code must use lowercase letters, numbers, and underscores")
	}
	if n := len(strings.TrimSpace(input.Name)); n < 1 || n > 120 {
		return fmt.Errorf("name must contain between 1 and 120 characters")
	}
	if input.Unit != "minutes" && input.Unit != "days" {
		return fmt.Errorf("unit must be minutes or days")
	}
	return nil
}
func (service *Service) CreateLeaveType(ctx context.Context, organizationID, actorUserID string, input CreateLeaveTypeInput) (LeaveType, error) {
	if err := input.Validate(); err != nil {
		return LeaveType{}, err
	}
	result := LeaveType{Code: input.Code, Name: strings.TrimSpace(input.Name), Unit: input.Unit, IsPaid: input.IsPaid, RequiresApproval: input.RequiresApproval, Status: "active"}
	err := service.pool.QueryRow(ctx, `INSERT INTO leave_types (organization_id,code,name,unit,is_paid,requires_approval) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id::text`, organizationID, result.Code, result.Name, result.Unit, result.IsPaid, result.RequiresApproval).Scan(&result.ID)
	if err != nil {
		var pge *pgconn.PgError
		if errors.As(err, &pge) && pge.Code == "23505" {
			return LeaveType{}, ErrLeaveTypeConflict
		}
		return LeaveType{}, err
	}
	if _, err := service.pool.Exec(ctx, `INSERT INTO audit_events (organization_id,actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES ($1,$2,'leave_type.created','leave_type',$3,'unknown',jsonb_build_object('paid',$4,'unit',$5))`, organizationID, actorUserID, result.ID, result.IsPaid, result.Unit); err != nil {
		return LeaveType{}, err
	}
	return result, nil
}
