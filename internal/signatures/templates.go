package signatures

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type Template struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Category      string `json:"category"`
	Description   string `json:"description"`
	Body          string `json:"body"`
	HeaderText    string `json:"headerText"`
	FooterText    string `json:"footerText"`
	ExpiresInDays int    `json:"expiresInDays"`
	IsActive      bool   `json:"isActive"`
}

type SaveTemplateInput struct {
	Name          string `json:"name"`
	Category      string `json:"category"`
	Description   string `json:"description"`
	Body          string `json:"body"`
	HeaderText    string `json:"headerText"`
	FooterText    string `json:"footerText"`
	ExpiresInDays int    `json:"expiresInDays"`
}

type CreateGeneratedRequestInput struct {
	TemplateID  string            `json:"templateId"`
	Message     string            `json:"message"`
	Recipients  []CreateRecipient `json:"recipients"`
	MergeFields map[string]string `json:"mergeFields"`
}

type ExternalSignature struct {
	RequestID      string `json:"requestId"`
	DocumentID     string `json:"documentId,omitempty"`
	DocumentURL    string `json:"documentUrl,omitempty"`
	Title          string `json:"title"`
	Body           string `json:"body"`
	Header         string `json:"header"`
	Footer         string `json:"footer"`
	LogoURL        string `json:"logoUrl"`
	BrandColor     string `json:"brandColor"`
	WebsiteURL     string `json:"websiteUrl"`
	ContactPhone   string `json:"contactPhone"`
	ContactEmail   string `json:"contactEmail"`
	Message        string `json:"message"`
	RecipientName  string `json:"recipientName"`
	RecipientEmail string `json:"recipientEmail"`
	CompanyName    string `json:"companyName"`
	Status         string `json:"status"`
}

type TemplateBranding struct {
	CompanyName        string   `json:"companyName"`
	LegalName          string   `json:"legalName"`
	RegistrationNumber string   `json:"registrationNumber"`
	WebsiteURL         string   `json:"websiteUrl"`
	ContactEmail       string   `json:"contactEmail"`
	ContactPhone       string   `json:"contactPhone"`
	Address            string   `json:"address"`
	Locations          []string `json:"locations"`
	LogoURL            string   `json:"logoUrl"`
	BrandColor         string   `json:"brandColor"`
	DefaultHeader      string   `json:"defaultHeader"`
	DefaultFooter      string   `json:"defaultFooter"`
}

var mergeToken = regexp.MustCompile(`\{\{([a-zA-Z0-9_]+)\}\}`)
var allowedMergeTokens = map[string]bool{"employee_name": true, "company_name": true, "job_title": true, "effective_date": true, "today": true}

func (service *Service) ListTemplates(ctx context.Context, org string) ([]Template, error) {
	rows, err := service.pool.Query(ctx, `SELECT id::text,name,category,description,body,header_text,footer_text,expires_in_days,is_active FROM signature_templates WHERE organization_id=$1 ORDER BY updated_at DESC`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Template{}
	for rows.Next() {
		var t Template
		if err := rows.Scan(&t.ID, &t.Name, &t.Category, &t.Description, &t.Body, &t.HeaderText, &t.FooterText, &t.ExpiresInDays, &t.IsActive); err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	return result, rows.Err()
}

func (service *Service) GetTemplateBranding(ctx context.Context, org string) (TemplateBranding, error) {
	var b TemplateBranding
	err := service.pool.QueryRow(ctx, `SELECT o.name,COALESCE(lp.legal_name,o.name),COALESCE(lp.registration_number,''),COALESCE(lp.website_url,''),COALESCE(lp.contact_email,''),COALESCE(lp.contact_phone,''),concat_ws(', ',NULLIF(lp.address_line_1,''),NULLIF(lp.address_line_2,''),NULLIF(lp.city,''),NULLIF(lp.administrative_area,''),NULLIF(lp.postal_code,'')),COALESCE(w.logo_url,''),COALESCE(w.brand_color,'#4F46E5') FROM organizations o LEFT JOIN organization_legal_profiles lp ON lp.organization_id=o.id LEFT JOIN workspaces w ON w.organization_id=o.id WHERE o.id=$1`, org).Scan(&b.CompanyName, &b.LegalName, &b.RegistrationNumber, &b.WebsiteURL, &b.ContactEmail, &b.ContactPhone, &b.Address, &b.LogoURL, &b.BrandColor)
	if err != nil {
		return b, err
	}
	rows, err := service.pool.Query(ctx, `SELECT name,concat_ws(', ',NULLIF(city,''),country_code) FROM locations WHERE organization_id=$1 AND status='active' ORDER BY name`, org)
	if err != nil {
		return b, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, place string
		if err := rows.Scan(&name, &place); err != nil {
			return b, err
		}
		if place != "" {
			b.Locations = append(b.Locations, name+" — "+place)
		} else {
			b.Locations = append(b.Locations, name)
		}
	}
	if err := rows.Err(); err != nil {
		return b, err
	}
	b.DefaultHeader = b.LegalName
	if b.RegistrationNumber != "" {
		b.DefaultHeader += "\nCompany registration: " + b.RegistrationNumber
	}
	if b.Address != "" {
		b.DefaultHeader += "\n" + b.Address
	}
	var footer []string
	if b.WebsiteURL != "" {
		footer = append(footer, b.WebsiteURL)
	}
	if b.ContactEmail != "" {
		footer = append(footer, b.ContactEmail)
	}
	if b.ContactPhone != "" {
		footer = append(footer, b.ContactPhone)
	}
	if len(b.Locations) > 0 {
		footer = append(footer, "Offices: "+strings.Join(b.Locations, " · "))
	}
	b.DefaultFooter = strings.Join(footer, "\n")
	return b, nil
}

func (service *Service) SaveTemplate(ctx context.Context, org, actor, id string, input SaveTemplateInput) (Template, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Category = strings.TrimSpace(input.Category)
	input.Description = strings.TrimSpace(input.Description)
	input.Body = strings.TrimSpace(input.Body)
	input.HeaderText = strings.TrimSpace(input.HeaderText)
	input.FooterText = strings.TrimSpace(input.FooterText)
	if input.Name == "" || len(input.Name) > 200 || input.Body == "" || len(input.Body) > 100_000 || len(input.HeaderText)+len(input.FooterText) > 20_000 {
		return Template{}, errors.New("template name and body are required; body must be at most 100 KB and header/footer at most 20 KB combined")
	}
	if input.Category == "" {
		input.Category = "General"
	}
	if input.ExpiresInDays == 0 {
		input.ExpiresInDays = 14
	}
	if input.ExpiresInDays < 1 || input.ExpiresInDays > 365 {
		return Template{}, errors.New("expiry must be between 1 and 365 days")
	}
	for _, m := range mergeToken.FindAllStringSubmatch(input.Body+"\n"+input.HeaderText+"\n"+input.FooterText, -1) {
		if !allowedMergeTokens[m[1]] {
			return Template{}, fmt.Errorf("unsupported merge field {{%s}}", m[1])
		}
	}
	var t Template
	var err error
	if id != "" {
		err = service.pool.QueryRow(ctx, `UPDATE signature_templates SET name=$3,category=$4,description=$5,body=$6,header_text=$7,footer_text=$8,expires_in_days=$9,is_active=true,updated_at=now() WHERE id=$1 AND organization_id=$2 RETURNING id::text,name,category,description,body,header_text,footer_text,expires_in_days,is_active`, id, org, input.Name, input.Category, input.Description, input.Body, input.HeaderText, input.FooterText, input.ExpiresInDays).Scan(&t.ID, &t.Name, &t.Category, &t.Description, &t.Body, &t.HeaderText, &t.FooterText, &t.ExpiresInDays, &t.IsActive)
	} else {
		err = service.pool.QueryRow(ctx, `INSERT INTO signature_templates (organization_id,name,category,description,body,header_text,footer_text,expires_in_days,created_by_user_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (organization_id,name) DO UPDATE SET category=EXCLUDED.category,description=EXCLUDED.description,body=EXCLUDED.body,header_text=EXCLUDED.header_text,footer_text=EXCLUDED.footer_text,expires_in_days=EXCLUDED.expires_in_days,is_active=true,updated_at=now() RETURNING id::text,name,category,description,body,header_text,footer_text,expires_in_days,is_active`, org, input.Name, input.Category, input.Description, input.Body, input.HeaderText, input.FooterText, input.ExpiresInDays, actor).Scan(&t.ID, &t.Name, &t.Category, &t.Description, &t.Body, &t.HeaderText, &t.FooterText, &t.ExpiresInDays, &t.IsActive)
	}
	if err == nil {
		_, _ = service.pool.Exec(ctx, `INSERT INTO audit_events (organization_id,actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES ($1,$2,'signature.template_saved','signature_template',$3,'unknown','{}'::jsonb)`, org, actor, t.ID)
	}
	return t, err
}

func (service *Service) CreateGeneratedRequest(ctx context.Context, org, actor string, input CreateGeneratedRequestInput) (SignatureRequest, []string, error) {
	if err := validateRecipients(input.Recipients); err != nil {
		return SignatureRequest{}, nil, err
	}
	var t Template
	err := service.pool.QueryRow(ctx, `SELECT id::text,name,body,header_text,footer_text,expires_in_days FROM signature_templates WHERE id=$1 AND organization_id=$2 AND is_active`, input.TemplateID, org).Scan(&t.ID, &t.Name, &t.Body, &t.HeaderText, &t.FooterText, &t.ExpiresInDays)
	if err != nil {
		return SignatureRequest{}, nil, ErrNotFound
	}
	fields := map[string]string{"today": time.Now().Format("2006-01-02")}
	var company string
	_ = service.pool.QueryRow(ctx, `SELECT name FROM organizations WHERE id=$1`, org).Scan(&company)
	fields["company_name"] = company
	for k, v := range input.MergeFields {
		if !allowedMergeTokens[k] || len(v) > 500 {
			return SignatureRequest{}, nil, errors.New("invalid template merge field")
		}
		fields[k] = v
	}
	body := mergeToken.ReplaceAllStringFunc(t.Body, func(token string) string { key := mergeToken.FindStringSubmatch(token)[1]; return fields[key] })
	header := mergeToken.ReplaceAllStringFunc(t.HeaderText, func(token string) string { key := mergeToken.FindStringSubmatch(token)[1]; return fields[key] })
	footer := mergeToken.ReplaceAllStringFunc(t.FooterText, func(token string) string { key := mergeToken.FindStringSubmatch(token)[1]; return fields[key] })
	for _, token := range mergeToken.FindAllString(body+"\n"+header+"\n"+footer, -1) {
		return SignatureRequest{}, nil, fmt.Errorf("missing value for merge field %s", token)
	}
	hash := sha256.Sum256([]byte(header + "\n" + body + "\n" + footer))
	title := t.Name
	branding, err := service.GetTemplateBranding(ctx, org)
	if err != nil {
		return SignatureRequest{}, nil, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return SignatureRequest{}, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var reqID string
	err = tx.QueryRow(ctx, `INSERT INTO signature_requests (organization_id,document_id,template_id,provider,status,custom_message,title,body_rendered,header_rendered,footer_rendered,body_sha256,brand_color,logo_url,website_url,contact_phone,contact_email,expires_at,created_by_user_id) VALUES ($1,NULL,$2,'native','pending',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,now()+($14::int * INTERVAL '1 day'),$15) RETURNING id::text`, org, t.ID, input.Message, title, body, header, footer, hex.EncodeToString(hash[:]), branding.BrandColor, branding.LogoURL, branding.WebsiteURL, branding.ContactPhone, branding.ContactEmail, t.ExpiresInDays, actor).Scan(&reqID)
	if err != nil {
		return SignatureRequest{}, nil, err
	}
	result := SignatureRequest{ID: reqID, Title: title, Status: "pending"}
	links := make([]string, len(input.Recipients))
	result.Recipients = make([]SignatureRecipient, len(input.Recipients))
	for i, r := range input.Recipients {
		tokenBytes := make([]byte, 32)
		if _, err = rand.Read(tokenBytes); err != nil {
			return SignatureRequest{}, nil, err
		}
		token := hex.EncodeToString(tokenBytes)
		tokenHash := sha256.Sum256([]byte(token))
		var recID string
		err = tx.QueryRow(ctx, `INSERT INTO signature_recipients (signature_request_id,recipient_email,recipient_name,status,signing_order,public_token_hash) VALUES ($1,$2,$3,'pending',$4,$5) RETURNING id::text`, reqID, r.Email, r.Name, i+1, hex.EncodeToString(tokenHash[:])).Scan(&recID)
		if err != nil {
			return SignatureRequest{}, nil, err
		}
		result.Recipients[i] = SignatureRecipient{ID: recID, Email: r.Email, Name: r.Name, Status: "pending"}
		links[i] = "https://easyhr.pro/sign/" + token
	}
	if err = tx.Commit(ctx); err != nil {
		return SignatureRequest{}, nil, err
	}
	return result, links, nil
}

func (service *Service) CreateDocumentRequest(ctx context.Context, org, actor, documentID string, input CreateSignatureRequestInput) (SignatureRequest, []string, error) {
	if err := validateRecipients(input.Recipients); err != nil {
		return SignatureRequest{}, nil, err
	}
	var title string
	if err := service.pool.QueryRow(ctx, `SELECT filename FROM documents WHERE id=$1 AND organization_id=$2 AND status='available'`, documentID, org).Scan(&title); err != nil {
		return SignatureRequest{}, nil, ErrNotFound
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return SignatureRequest{}, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var reqID string
	err = tx.QueryRow(ctx, `INSERT INTO signature_requests (organization_id,document_id,provider,status,custom_message,title,expires_at,created_by_user_id) VALUES ($1,$2,'native','pending',$3,$4,now()+INTERVAL '14 days',$5) RETURNING id::text`, org, documentID, input.Message, title, actor).Scan(&reqID)
	if err != nil {
		return SignatureRequest{}, nil, err
	}
	result := SignatureRequest{ID: reqID, DocumentID: documentID, Status: "pending", Recipients: make([]SignatureRecipient, len(input.Recipients))}
	links := make([]string, len(input.Recipients))
	for i, r := range input.Recipients {
		b := make([]byte, 32)
		if _, err = rand.Read(b); err != nil {
			return SignatureRequest{}, nil, err
		}
		token := hex.EncodeToString(b)
		h := sha256.Sum256([]byte(token))
		var id string
		err = tx.QueryRow(ctx, `INSERT INTO signature_recipients (signature_request_id,recipient_email,recipient_name,status,signing_order,public_token_hash) VALUES ($1,$2,$3,'pending',$4,$5) RETURNING id::text`, reqID, r.Email, r.Name, i+1, hex.EncodeToString(h[:])).Scan(&id)
		if err != nil {
			return SignatureRequest{}, nil, err
		}
		result.Recipients[i] = SignatureRecipient{ID: id, Email: r.Email, Name: r.Name, Status: "pending"}
		links[i] = "https://easyhr.pro/sign/" + token
	}
	if err = tx.Commit(ctx); err != nil {
		return SignatureRequest{}, nil, err
	}
	return result, links, nil
}

func (service *Service) GetExternalSignature(ctx context.Context, token string) (ExternalSignature, error) {
	h := sha256.Sum256([]byte(token))
	var x ExternalSignature
	err := service.pool.QueryRow(ctx, `SELECT sr.id::text,COALESCE(sr.document_id::text,''),COALESCE(sr.title,d.filename,'Document'),COALESCE(sr.body_rendered,''),COALESCE(sr.header_rendered,''),COALESCE(sr.footer_rendered,''),COALESCE(sr.logo_url,''),COALESCE(sr.brand_color,'#4F46E5'),COALESCE(sr.website_url,''),COALESCE(sr.contact_phone,''),COALESCE(sr.contact_email,''),COALESCE(sr.custom_message,''),r.recipient_name,r.recipient_email,o.name,r.status FROM signature_recipients r JOIN signature_requests sr ON sr.id=r.signature_request_id JOIN organizations o ON o.id=sr.organization_id LEFT JOIN documents d ON d.id=sr.document_id WHERE r.public_token_hash=$1 AND sr.status IN ('pending','sent') AND (sr.expires_at IS NULL OR sr.expires_at>now()) AND r.status IN ('pending','sent')`, hex.EncodeToString(h[:])).Scan(&x.RequestID, &x.DocumentID, &x.Title, &x.Body, &x.Header, &x.Footer, &x.LogoURL, &x.BrandColor, &x.WebsiteURL, &x.ContactPhone, &x.ContactEmail, &x.Message, &x.RecipientName, &x.RecipientEmail, &x.CompanyName, &x.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExternalSignature{}, ErrRecipientNotFound
	}
	return x, err
}

func (service *Service) SignExternalRequestWithIdentity(ctx context.Context, token, signatureType, signatureData, ip, ua string) error {
	h := sha256.Sum256([]byte(token))
	var org, requestID, recipientID, email string
	err := service.pool.QueryRow(ctx, `SELECT sr.organization_id::text,sr.id::text,r.id::text,r.recipient_email FROM signature_recipients r JOIN signature_requests sr ON sr.id=r.signature_request_id WHERE r.public_token_hash=$1 AND r.status IN ('pending','sent') AND sr.status IN ('pending','sent') AND (sr.expires_at IS NULL OR sr.expires_at>now())`, hex.EncodeToString(h[:])).Scan(&org, &requestID, &recipientID, &email)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRecipientNotFound
	}
	if err != nil {
		return err
	}
	// The ordinary signature method relies on a registered user email. Public signing
	// performs the same validation and evidence capture directly for external recipients.
	if err = validateSignature(signatureType, signatureData); err != nil {
		return err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var updated string
	err = tx.QueryRow(ctx, `UPDATE signature_recipients SET status='completed',signed_at=now(),signature_type=$2,signature_data=$3,signer_ip=$4,signer_user_agent=$5,consent_at=now(),public_token_hash=NULL WHERE id=$1 AND public_token_hash IS NOT NULL RETURNING id::text`, recipientID, signatureType, signatureData, ip, ua).Scan(&updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRecipientNotFound
	}
	if err != nil {
		return err
	}
	var pending int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM signature_recipients WHERE signature_request_id=$1 AND status!='completed'`, requestID).Scan(&pending); err != nil {
		return err
	}
	if pending == 0 {
		if _, err = tx.Exec(ctx, `UPDATE signature_requests SET status='completed',completed_at=now() WHERE id=$1`, requestID); err != nil {
			return err
		}
	}
	hash := sha256.Sum256([]byte(signatureData))
	if _, err = tx.Exec(ctx, `INSERT INTO signature_events (signature_request_id,recipient_id,event_type,detail,ip_address) VALUES ($1,$2,'recipient_signed',$3,$4)`, requestID, recipientID, "sha256:"+hex.EncodeToString(hash[:]), ip); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events (organization_id,action,resource_type,resource_id,request_id,ip_address,user_agent,metadata) VALUES ($1,'signature.external_recipient_signed','signature_request',$2,'unknown',NULLIF($3,'')::inet,$4,jsonb_build_object('emailHash',$5::text,'signatureHash',$6::text))`, org, requestID, ip, ua, hashText(email), hex.EncodeToString(hash[:])); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func hashText(value string) string {
	h := sha256.Sum256([]byte(strings.ToLower(value)))
	return hex.EncodeToString(h[:])
}

func (service *Service) SignExternalRequest(ctx context.Context, token, signatureType, signatureData, ip, ua string) error {
	h := sha256.Sum256([]byte(token))
	var recipientID, requestID, org string
	err := service.pool.QueryRow(ctx, `SELECT r.id::text,sr.id::text,sr.organization_id::text FROM signature_recipients r JOIN signature_requests sr ON sr.id=r.signature_request_id WHERE r.public_token_hash=$1 AND r.status IN ('pending','sent') AND sr.status IN ('pending','sent') AND (sr.expires_at IS NULL OR sr.expires_at>now())`, hex.EncodeToString(h[:])).Scan(&recipientID, &requestID, &org)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRecipientNotFound
	}
	if err != nil {
		return err
	}
	if err = service.SignRequest(ctx, org, "", mustExternalEmail(ctx, service, recipientID), requestID, signatureType, signatureData, ip, ua); err != nil {
		return err
	}
	_, err = service.pool.Exec(ctx, `UPDATE signature_recipients SET public_token_hash=NULL WHERE id=$1`, recipientID)
	return err
}

func mustExternalEmail(ctx context.Context, s *Service, id string) string {
	var e string
	_ = s.pool.QueryRow(ctx, `SELECT recipient_email FROM signature_recipients WHERE id=$1`, id).Scan(&e)
	return e
}
