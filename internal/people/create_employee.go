package people

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrCreateConflict    = errors.New("employee creation conflicts with an existing record")
	ErrIdempotencyReused = errors.New("idempotency key was reused with a different request")
)

type CreateEmployeeInput struct {
	EmployeeNumber  string `json:"employeeNumber"`
	FirstName       string `json:"firstName"`
	LastName        string `json:"lastName"`
	PreferredName   string `json:"preferredName,omitempty"`
	WorkEmail       string `json:"workEmail"`
	PersonalEmail   string `json:"personalEmail,omitempty"`
	PersonalPhone   string `json:"personalPhone,omitempty"`
	HiredOn         string `json:"hiredOn"`
	EmploymentType  string `json:"employmentType"`
	WorkerType      string `json:"workerType"`
	WorkArrangement string `json:"workArrangement"`
	FTEBasisPoints  int    `json:"fteBasisPoints"`
	PayType         string `json:"payType"`
	JobTitle        string `json:"jobTitle"`
	DepartmentID    string `json:"departmentId,omitempty"`
	DivisionID      string `json:"divisionId,omitempty"`
	LocationID      string `json:"locationId,omitempty"`
	ManagerID       string `json:"managerId,omitempty"`
	PositionID      string `json:"positionId,omitempty"`
	SendInvitation  bool   `json:"sendInvitation,omitempty"`
}

func (input *CreateEmployeeInput) SetDefaults() {
	if input.EmploymentType == "" {
		input.EmploymentType = "full_time"
	}
	if input.WorkerType == "" {
		input.WorkerType = "employee"
	}
	if input.WorkArrangement == "" {
		input.WorkArrangement = "on_site"
	}
	if input.PayType == "" {
		input.PayType = "salary"
	}
	if input.FTEBasisPoints == 0 {
		input.FTEBasisPoints = 10000
	}
	if input.HiredOn == "" {
		input.HiredOn = time.Now().Format("2006-01-02")
	}
}

type CreateEmployeeResult struct {
	ID        string `json:"id"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	WorkEmail string `json:"workEmail"`
	Status    string `json:"status"`
	HiredOn   string `json:"hiredOn"`
}

func (input CreateEmployeeInput) Validate() error {
	if strings.TrimSpace(input.DepartmentID) == "" || strings.TrimSpace(input.PositionID) == "" {
		return fmt.Errorf("departmentId and positionId are required; choose a department role before creating the employee")
	}
	for field, rule := range map[string]struct {
		value string
		max   int
	}{"employeeNumber": {input.EmployeeNumber, 80}, "firstName": {input.FirstName, 120}, "lastName": {input.LastName, 120}, "jobTitle": {input.JobTitle, 160}} {
		if n := len(strings.TrimSpace(rule.value)); n == 0 || n > rule.max {
			return fmt.Errorf("%s must contain between 1 and %d characters", field, rule.max)
		}
	}
	if _, err := mail.ParseAddress(input.WorkEmail); err != nil {
		return fmt.Errorf("workEmail must be valid")
	}
	if input.PersonalEmail != "" {
		if _, err := mail.ParseAddress(input.PersonalEmail); err != nil {
			return fmt.Errorf("personalEmail must be valid")
		}
	}
	if _, err := time.Parse(time.DateOnly, input.HiredOn); err != nil {
		return fmt.Errorf("hiredOn must be an ISO date")
	}
	if !oneOf(input.EmploymentType, "full_time", "part_time", "fixed_term", "contractor", "intern") {
		return fmt.Errorf("employmentType is invalid")
	}
	if !oneOf(input.WorkerType, "employee", "contractor") {
		return fmt.Errorf("workerType is invalid")
	}
	if !oneOf(input.WorkArrangement, "on_site", "hybrid", "remote") {
		return fmt.Errorf("workArrangement is invalid")
	}
	if !oneOf(input.PayType, "salary", "hourly") {
		return fmt.Errorf("payType is invalid")
	}
	if input.FTEBasisPoints < 1 || input.FTEBasisPoints > 10000 {
		return fmt.Errorf("fteBasisPoints must be between 1 and 10000")
	}
	return nil
}

func (directory *Directory) Create(ctx context.Context, organizationID, actorUserID, requestID, idempotencyKey string, input CreateEmployeeInput) (CreateEmployeeResult, error) {
	if err := input.Validate(); err != nil {
		return CreateEmployeeResult{}, err
	}
	if len(idempotencyKey) < 16 || len(idempotencyKey) > 255 {
		return CreateEmployeeResult{}, fmt.Errorf("Idempotency-Key must contain between 16 and 255 characters")
	}
	hash, err := createEmployeeHash(input)
	if err != nil {
		return CreateEmployeeResult{}, err
	}
	tx, err := directory.pool.Begin(ctx)
	if err != nil {
		return CreateEmployeeResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if replay, found, err := findCreateReplay(ctx, tx, organizationID, actorUserID, idempotencyKey, hash); err != nil {
		return CreateEmployeeResult{}, err
	} else if found {
		return replay, nil
	}
	if err := validateReferences(ctx, tx, organizationID, input); err != nil {
		return CreateEmployeeResult{}, err
	}
	result := CreateEmployeeResult{FirstName: strings.TrimSpace(input.FirstName), LastName: strings.TrimSpace(input.LastName), WorkEmail: strings.ToLower(strings.TrimSpace(input.WorkEmail)), Status: "onboarding", HiredOn: input.HiredOn}
	err = tx.QueryRow(ctx, `INSERT INTO employees (organization_id,employee_number,first_name,last_name,preferred_name,work_email,status,hired_on) VALUES ($1,$2,$3,$4,$5,$6,'onboarding',$7) RETURNING id::text`, organizationID, strings.TrimSpace(input.EmployeeNumber), result.FirstName, result.LastName, nullString(input.PreferredName), result.WorkEmail, input.HiredOn).Scan(&result.ID)
	if err != nil {
		return CreateEmployeeResult{}, normalizeCreateError(err)
	}
	if input.PersonalEmail != "" || input.PersonalPhone != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO employee_personal_profiles (employee_id,personal_email,personal_phone) VALUES ($1,$2,$3)`, result.ID, nullString(input.PersonalEmail), nullString(input.PersonalPhone)); err != nil {
			return CreateEmployeeResult{}, err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO employment_terms (employee_id,effective_from,employment_type,worker_type,work_arrangement,fte_basis_points,pay_type,created_by_user_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, result.ID, input.HiredOn, input.EmploymentType, input.WorkerType, input.WorkArrangement, input.FTEBasisPoints, input.PayType, actorUserID); err != nil {
		return CreateEmployeeResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO employment_assignments (employee_id,department_id,division_id,location_id,manager_employee_id,job_title,effective_from,created_by_user_id,position_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,'')::uuid)`, result.ID, nullString(input.DepartmentID), nullString(input.DivisionID), nullString(input.LocationID), nullString(input.ManagerID), strings.TrimSpace(input.JobTitle), input.HiredOn, actorUserID, input.PositionID); err != nil {
		return CreateEmployeeResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO employee_status_events (employee_id,new_status,effective_on,reason,created_by_user_id) VALUES ($1,'onboarding',$2,'Initial employee record created',$3)`, result.ID, input.HiredOn, actorUserID); err != nil {
		return CreateEmployeeResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (organization_id,actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES ($1,$2,'employee.created','employee',$3,$4,jsonb_build_object('employeeNumber',$5::text))`, organizationID, actorUserID, result.ID, requestID, input.EmployeeNumber); err != nil {
		return CreateEmployeeResult{}, err
	}
	body, err := json.Marshal(result)
	if err != nil {
		return CreateEmployeeResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO idempotency_keys (organization_id,actor_user_id,route,key,request_hash,response_status,response_body,expires_at) VALUES ($1,$2,'/api/v1/employees',$3,$4,201,$5::jsonb,now()+interval '24 hours')`, organizationID, actorUserID, idempotencyKey, hash, body); err != nil {
		return CreateEmployeeResult{}, normalizeCreateError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return CreateEmployeeResult{}, err
	}
	return result, nil
}

func validateReferences(ctx context.Context, tx pgx.Tx, org string, input CreateEmployeeInput) error {
	for _, ref := range []struct{ table, id, field string }{{"departments", input.DepartmentID, "departmentId"}, {"divisions", input.DivisionID, "divisionId"}, {"locations", input.LocationID, "locationId"}, {"employees", input.ManagerID, "managerId"}} {
		if ref.id == "" {
			continue
		}
		var ok bool
		query := fmt.Sprintf("SELECT EXISTS (SELECT 1 FROM %s WHERE id=$1 AND organization_id=$2)", ref.table)
		if err := tx.QueryRow(ctx, query, ref.id, org).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%s does not belong to this organization", ref.field)
		}
	}
	if input.PositionID != "" {
		var valid bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM department_positions WHERE id=$1 AND organization_id=$2 AND department_id=NULLIF($3,'')::uuid AND status='active')`, input.PositionID, org, input.DepartmentID).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return fmt.Errorf("positionId must be an active position in the selected department")
		}
	}
	return nil
}
func findCreateReplay(ctx context.Context, tx pgx.Tx, org, actor, key, hash string) (CreateEmployeeResult, bool, error) {
	var stored string
	var body []byte
	err := tx.QueryRow(ctx, `SELECT request_hash,response_body FROM idempotency_keys WHERE organization_id=$1 AND actor_user_id=$2 AND route='/api/v1/employees' AND key=$3 AND expires_at>now()`, org, actor, key).Scan(&stored, &body)
	if errors.Is(err, pgx.ErrNoRows) {
		return CreateEmployeeResult{}, false, nil
	}
	if err != nil {
		return CreateEmployeeResult{}, false, err
	}
	if stored != hash {
		return CreateEmployeeResult{}, false, ErrIdempotencyReused
	}
	var result CreateEmployeeResult
	if err := json.Unmarshal(body, &result); err != nil {
		return CreateEmployeeResult{}, false, err
	}
	return result, true, nil
}
func createEmployeeHash(input CreateEmployeeInput) (string, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func normalizeCreateError(err error) error {
	var pge *pgconn.PgError
	if errors.As(err, &pge) && pge.Code == "23505" {
		return ErrCreateConflict
	}
	return err
}
func nullString(value string) any {
	if value = strings.TrimSpace(value); value == "" {
		return nil
	}
	return value
}
func oneOf(value string, accepted ...string) bool {
	for _, item := range accepted {
		if value == item {
			return true
		}
	}
	return false
}
