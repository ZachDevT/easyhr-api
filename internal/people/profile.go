package people

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var ErrEmployeeNotFound = errors.New("employee not found")

type PersonalProfile struct {
	PersonalEmail              *string `json:"personalEmail"`
	PersonalPhone              *string `json:"personalPhone"`
	AddressLine1               *string `json:"addressLine1"`
	AddressLine2               *string `json:"addressLine2"`
	City                       *string `json:"city"`
	AdministrativeArea         *string `json:"administrativeArea"`
	PostalCode                 *string `json:"postalCode"`
	EmergencyContactName       *string `json:"emergencyContactName"`
	EmergencyContactRelationship *string `json:"emergencyContactRelationship"`
	EmergencyContactPhone      *string `json:"emergencyContactPhone"`
	PaymentMethod              *string `json:"paymentMethod"`
	BankName                   *string `json:"bankName"`
	BankRoutingNumber          *string `json:"bankRoutingNumber"`
	BankAccountNumber          *string `json:"bankAccountNumber"`
	HandbookSignedAt           *string `json:"handbookSignedAt"`
}

type EmployeeProfile struct {
	ID             string  `json:"id"`
	EmployeeNumber string  `json:"employeeNumber"`
	FirstName      string  `json:"firstName"`
	LastName       string  `json:"lastName"`
	WorkEmail      string  `json:"workEmail"`
	PersonalEmail  *string `json:"personalEmail"`
	PersonalPhone  *string `json:"personalPhone"`
	Status         string  `json:"status"`
	DepartmentID   *string `json:"departmentId"`
	Department     string  `json:"department"`
	DivisionID     *string `json:"divisionId"`
	Division       string  `json:"division"`
	LocationID     *string `json:"locationId"`
	Location       string  `json:"location"`
	ManagerID      *string `json:"managerId"`
	Manager        string  `json:"manager"`
	JobTitle       *string `json:"jobTitle"`
	StartDate      *string `json:"startDate"`
	Avatar         string  `json:"avatar"`
	CreatedAt      string  `json:"createdAt"`
	UpdatedAt      string  `json:"updatedAt"`

	// Personal profile & onboarding details
	AddressLine1               *string `json:"addressLine1"`
	AddressLine2               *string `json:"addressLine2"`
	City                       *string `json:"city"`
	AdministrativeArea         *string `json:"administrativeArea"`
	PostalCode                 *string `json:"postalCode"`
	EmergencyContactName       *string `json:"emergencyContactName"`
	EmergencyContactRelationship *string `json:"emergencyContactRelationship"`
	EmergencyContactPhone      *string `json:"emergencyContactPhone"`
	PaymentMethod              *string `json:"paymentMethod"`
	BankName                   *string `json:"bankName"`
	BankRoutingNumber          *string `json:"bankRoutingNumber"`
	BankAccountNumber          *string `json:"bankAccountNumber"`
	HandbookSignedAt           *string `json:"handbookSignedAt"`
}

const employeeProfileSelectSQL = `
	SELECT 
		e.id::text, 
		e.employee_number,
		e.first_name, 
		e.last_name, 
		e.work_email,
		profile.personal_email, 
		profile.personal_phone,
		e.status,
		assignment.department_id::text, 
		COALESCE(d.name, 'General') AS department,
		assignment.division_id::text,
		COALESCE(div.name, '') AS division,
		assignment.location_id::text,
		COALESCE(loc.name, 'Remote') AS location,
		assignment.manager_employee_id::text, 
		COALESCE(NULLIF(TRIM(CONCAT(m.first_name, ' ', m.last_name)), ''), 'No manager assigned') AS manager,
		assignment.job_title,
		e.hired_on::text, 
		COALESCE(u.avatar_url, '') AS avatar,
		e.created_at::text, 
		e.updated_at::text,
		profile.address_line_1,
		profile.address_line_2,
		profile.city,
		profile.administrative_area,
		profile.postal_code,
		profile.emergency_contact_name,
		profile.emergency_contact_relationship,
		profile.emergency_contact_phone,
		profile.payment_method,
		profile.bank_name,
		profile.bank_routing_number,
		profile.bank_account_number,
		profile.handbook_signed_at::text
	FROM employees e
	LEFT JOIN employee_personal_profiles profile ON profile.employee_id = e.id
	LEFT JOIN LATERAL (
		SELECT department_id, division_id, location_id, manager_employee_id, job_title
		FROM employment_assignments
		WHERE employee_id = e.id AND effective_to IS NULL
		ORDER BY effective_from DESC
		LIMIT 1
	) assignment ON true
	LEFT JOIN departments d ON d.id = assignment.department_id
	LEFT JOIN divisions div ON div.id = assignment.division_id
	LEFT JOIN locations loc ON loc.id = assignment.location_id
	LEFT JOIN employees m ON m.id = assignment.manager_employee_id
	LEFT JOIN users u ON u.id = e.user_id`

func (directory *Directory) scanEmployeeProfile(row pgx.Row) (EmployeeProfile, error) {
	var result EmployeeProfile
	err := row.Scan(
		&result.ID, 
		&result.EmployeeNumber,
		&result.FirstName, 
		&result.LastName, 
		&result.WorkEmail, 
		&result.PersonalEmail, 
		&result.PersonalPhone,
		&result.Status, 
		&result.DepartmentID, 
		&result.Department,
		&result.DivisionID, 
		&result.Division,
		&result.LocationID, 
		&result.Location,
		&result.ManagerID, 
		&result.Manager,
		&result.JobTitle, 
		&result.StartDate, 
		&result.Avatar,
		&result.CreatedAt, 
		&result.UpdatedAt,
		&result.AddressLine1,
		&result.AddressLine2,
		&result.City,
		&result.AdministrativeArea,
		&result.PostalCode,
		&result.EmergencyContactName,
		&result.EmergencyContactRelationship,
		&result.EmergencyContactPhone,
		&result.PaymentMethod,
		&result.BankName,
		&result.BankRoutingNumber,
		&result.BankAccountNumber,
		&result.HandbookSignedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return EmployeeProfile{}, ErrEmployeeNotFound
	}
	if err != nil {
		return EmployeeProfile{}, fmt.Errorf("get employee: %w", err)
	}
	return result, nil
}

func (directory *Directory) Get(ctx context.Context, organizationID, employeeID string) (EmployeeProfile, error) {
	query := employeeProfileSelectSQL + ` WHERE e.id = $1::uuid AND e.organization_id = $2`
	return directory.scanEmployeeProfile(directory.pool.QueryRow(ctx, query, employeeID, organizationID))
}

func (directory *Directory) GetByUserID(ctx context.Context, organizationID, userID, email string) (EmployeeProfile, error) {
	// First check by direct user_id link
	query := employeeProfileSelectSQL + ` WHERE e.organization_id = $1 AND e.user_id = $2::uuid`
	prof, err := directory.scanEmployeeProfile(directory.pool.QueryRow(ctx, query, organizationID, userID))
	if err == nil {
		return prof, nil
	}

	// Fallback to match by work_email or personal_email if email is non-empty
	if email != "" {
		fallbackQuery := employeeProfileSelectSQL + ` WHERE e.organization_id = $1 AND (LOWER(e.work_email) = LOWER($2) OR LOWER(COALESCE(profile.personal_email, '')) = LOWER($2)) ORDER BY e.created_at DESC LIMIT 1`
		profFallback, errFallback := directory.scanEmployeeProfile(directory.pool.QueryRow(ctx, fallbackQuery, organizationID, email))
		if errFallback == nil {
			// Auto link user_id if not linked yet
			_, _ = directory.pool.Exec(ctx, `UPDATE employees SET user_id = $1 WHERE id = $2::uuid AND user_id IS NULL`, userID, profFallback.ID)
			return profFallback, nil
		}
	}

	return EmployeeProfile{}, ErrEmployeeNotFound
}

func (directory *Directory) UpdatePersonalProfile(ctx context.Context, organizationID, employeeID string, p PersonalProfile) error {
	_, err := directory.pool.Exec(ctx, `
		INSERT INTO employee_personal_profiles (
			employee_id, personal_email, personal_phone,
			address_line_1, address_line_2, city, administrative_area, postal_code,
			emergency_contact_name, emergency_contact_relationship, emergency_contact_phone,
			payment_method, bank_name, bank_routing_number, bank_account_number,
			handbook_signed_at, updated_at
		) VALUES (
			$1::uuid, $2, $3,
			$4, $5, $6, $7, $8,
			$9, $10, $11,
			$12, $13, $14, $15,
			CASE WHEN $16::text IS NOT NULL AND $16::text != '' THEN now() ELSE NULL END, now()
		)
		ON CONFLICT (employee_id) DO UPDATE SET
			personal_email = COALESCE(EXCLUDED.personal_email, employee_personal_profiles.personal_email),
			personal_phone = COALESCE(EXCLUDED.personal_phone, employee_personal_profiles.personal_phone),
			address_line_1 = COALESCE(EXCLUDED.address_line_1, employee_personal_profiles.address_line_1),
			address_line_2 = COALESCE(EXCLUDED.address_line_2, employee_personal_profiles.address_line_2),
			city = COALESCE(EXCLUDED.city, employee_personal_profiles.city),
			administrative_area = COALESCE(EXCLUDED.administrative_area, employee_personal_profiles.administrative_area),
			postal_code = COALESCE(EXCLUDED.postal_code, employee_personal_profiles.postal_code),
			emergency_contact_name = COALESCE(EXCLUDED.emergency_contact_name, employee_personal_profiles.emergency_contact_name),
			emergency_contact_relationship = COALESCE(EXCLUDED.emergency_contact_relationship, employee_personal_profiles.emergency_contact_relationship),
			emergency_contact_phone = COALESCE(EXCLUDED.emergency_contact_phone, employee_personal_profiles.emergency_contact_phone),
			payment_method = COALESCE(EXCLUDED.payment_method, employee_personal_profiles.payment_method),
			bank_name = COALESCE(EXCLUDED.bank_name, employee_personal_profiles.bank_name),
			bank_routing_number = COALESCE(EXCLUDED.bank_routing_number, employee_personal_profiles.bank_routing_number),
			bank_account_number = COALESCE(EXCLUDED.bank_account_number, employee_personal_profiles.bank_account_number),
			handbook_signed_at = CASE WHEN EXCLUDED.handbook_signed_at IS NOT NULL THEN now() ELSE employee_personal_profiles.handbook_signed_at END,
			updated_at = now()
	`, employeeID, p.PersonalEmail, p.PersonalPhone,
		p.AddressLine1, p.AddressLine2, p.City, p.AdministrativeArea, p.PostalCode,
		p.EmergencyContactName, p.EmergencyContactRelationship, p.EmergencyContactPhone,
		p.PaymentMethod, p.BankName, p.BankRoutingNumber, p.BankAccountNumber,
		p.HandbookSignedAt)

	return err
}
