package email

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type Service struct {
	apiKey     string
	fromEmail  string
	fromName   string
	httpClient *http.Client
}

func NewService(apiKey, fromEmail, fromName string) *Service {
	if fromEmail == "" || strings.HasSuffix(fromEmail, "@projecteasyhr.com") || strings.HasSuffix(fromEmail, "@resend.dev") || fromEmail == "notifications@easyhr.pro" {
		fromEmail = "notifications@recruter.easyhr.pro"
	}
	if fromName == "" {
		fromName = "EasyHR"
	}
	return &Service{
		apiKey:     strings.TrimSpace(apiKey),
		fromEmail:  strings.TrimSpace(fromEmail),
		fromName:   strings.TrimSpace(fromName),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

type SendEmailPayload struct {
	From    string            `json:"from"`
	To      []string          `json:"to"`
	Subject string            `json:"subject"`
	HTML    string            `json:"html"`
	ReplyTo string            `json:"reply_to,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type ReceivedAttachment struct {
	ID                 string `json:"id"`
	Filename           string `json:"filename"`
	ContentType        string `json:"content_type"`
	ContentDisposition string `json:"content_disposition"`
	DownloadURL        string `json:"download_url,omitempty"`
}

type ReceivedEmailDetails struct {
	ID          string               `json:"id"`
	Object      string               `json:"object"`
	To          []string             `json:"to"`
	From        string               `json:"from"`
	CreatedAt   string               `json:"created_at"`
	Subject     string               `json:"subject"`
	HTML        string               `json:"html"`
	Text        string               `json:"text"`
	Headers     map[string]string    `json:"headers"`
	BCC         []string             `json:"bcc"`
	CC          []string             `json:"cc"`
	ReplyTo     []string             `json:"reply_to"`
	MessageID   string               `json:"message_id"`
	Attachments []ReceivedAttachment `json:"attachments"`
	ReceivedFor []string             `json:"received_for"`
}

type BrandingInfo struct {
	CompanyName string
	LogoURL     string
	BrandColor  string
	WebsiteURL  string
}

func (s *Service) sendViaResend(ctx context.Context, toEmail, subject, htmlBody string) error {
	_, err := s.sendViaResendWithHeaders(ctx, toEmail, subject, htmlBody, nil)
	return err
}

func (s *Service) sendViaResendWithHeaders(ctx context.Context, toEmail, subject, htmlBody string, headers map[string]string) (string, error) {
	if strings.TrimSpace(toEmail) == "" {
		return "", fmt.Errorf("recipient email is required")
	}

	if s.apiKey == "" {
		log.Printf("[Email Service: DEV MODE] No RESEND_API_KEY specified. Mock-delivered to %s. Subject: %s", toEmail, subject)
		return fmt.Sprintf("mock-%d", time.Now().UnixNano()), nil
	}

	sendReq := func(recipient, sub string) (int, string, error) {
		payload := SendEmailPayload{
			From:    fmt.Sprintf("%s <%s>", s.fromName, s.fromEmail),
			To:      []string{recipient},
			Subject: sub,
			HTML:    htmlBody,
			ReplyTo: "notifications@recruter.easyhr.pro",
			Headers: headers,
		}

		reqBytes, err := json.Marshal(payload)
		if err != nil {
			return 0, "", err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(reqBytes))
		if err != nil {
			return 0, "", err
		}
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := s.httpClient.Do(req)
		if err != nil {
			return 0, "", err
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(respBody), nil
	}

	statusCode, respStr, err := sendReq(toEmail, subject)
	if err == nil && statusCode >= 200 && statusCode < 300 {
		log.Printf("[Email Success] Email sent successfully via Resend from %s to %s: %s", s.fromEmail, toEmail, respStr)
		var resendResp struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal([]byte(respStr), &resendResp)
		return resendResp.ID, nil
	}

	// Resend sandbox or unverified domain notice
	if statusCode == 403 {
		log.Printf("[Email Notice] Resend status 403 for recipient %s with sender %s: %s. To ensure live delivery to external candidate inboxes, add easyhr.pro at https://resend.com/domains with DNS records (DKIM, SPF).", toEmail, s.fromEmail, respStr)
		if strings.Contains(respStr, "testing emails") {
			fallbackEmail := strings.TrimSpace(os.Getenv("DEV_EMAIL_FALLBACK"))
			if fallbackEmail != "" {
				devSubject := fmt.Sprintf("[Sandbox Test for %s] %s", toEmail, subject)
				statusFallback, respFallback, errFallback := sendReq(fallbackEmail, devSubject)
				if errFallback == nil && statusFallback >= 200 && statusFallback < 300 {
					log.Printf("[Email Success] Delivered to sandbox account owner <%s> via Resend: %s", fallbackEmail, respFallback)
					var resendResp struct {
						ID string `json:"id"`
					}
					_ = json.Unmarshal([]byte(respFallback), &resendResp)
					return resendResp.ID, nil
				}
			}
		}
	}

	log.Printf("[Email Warning] Resend status %d: %s.", statusCode, respStr)
	if err != nil {
		return "", err
	}
	return "", fmt.Errorf("email provider returned status %d", statusCode)
}

// RetrieveReceivedEmail fetches full email body, headers, and metadata from the Resend Receiving API.
func (s *Service) RetrieveReceivedEmail(ctx context.Context, emailID string) (*ReceivedEmailDetails, error) {
	if emailID == "" {
		return nil, fmt.Errorf("email_id is required")
	}
	if s.apiKey == "" {
		return nil, fmt.Errorf("RESEND_API_KEY is not configured")
	}

	url := fmt.Sprintf("https://api.resend.com/emails/receiving/%s", emailID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("resend retrieve received email error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var details ReceivedEmailDetails
	if err := json.Unmarshal(bodyBytes, &details); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &details, nil
}

// 1. Template: Welcome New Organization Creator (Platform/System Email)
func (s *Service) SendWelcomeOrganizationCreator(ctx context.Context, toEmail, ownerName, companyName, subdomain, dashboardURL string) error {
	if dashboardURL == "" {
		dashboardURL = "http://localhost:3000/dashboard"
	}
	if companyName == "" {
		companyName = "Your Organization"
	}

	subject := fmt.Sprintf("Welcome to ProjectEasyHR, %s! Your workspace %s is live", ownerName, companyName)

	htmlBody := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Welcome to ProjectEasyHR</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif; background-color: #f3f4f6; margin: 0; padding: 24px; color: #111827; }
    .card { max-width: 620px; margin: 0 auto; background: #ffffff; border-radius: 16px; overflow: hidden; box-shadow: 0 10px 25px rgba(0,0,0,0.06); }
    .header { background: linear-gradient(135deg, #4338CA 0%%, #6366F1 50%%, #8B5CF6 100%%); padding: 44px 36px; text-align: center; color: #ffffff; }
    .logo-badge { display: inline-flex; align-items: center; justify-content: center; width: 56px; height: 56px; background: rgba(255,255,255,0.2); border: 2px solid white; border-radius: 14px; font-size: 26px; font-weight: 900; margin-bottom: 16px; color: white; }
    .header h1 { margin: 0 0 10px 0; font-size: 28px; font-weight: 800; letter-spacing: -0.02em; }
    .header p { margin: 0; font-size: 16px; opacity: 0.95; }
    .body { padding: 40px 36px; }
    .greeting { font-size: 19px; font-weight: 700; margin-bottom: 14px; color: #111827; }
    .intro { font-size: 15px; line-height: 1.6; color: #4B5563; margin-bottom: 28px; }
    .details-box { background-color: #F9FAFB; border: 1px solid #E5E7EB; border-radius: 12px; padding: 22px; margin-bottom: 30px; }
    .detail-row { display: flex; justify-content: space-between; margin-bottom: 12px; font-size: 14px; }
    .detail-row:last-child { margin-bottom: 0; }
    .detail-label { color: #6B7280; font-weight: 600; }
    .detail-value { color: #111827; font-weight: 800; }
    .features-grid { margin-bottom: 32px; }
    .features-title { font-size: 14px; font-weight: 800; text-transform: uppercase; letter-spacing: 0.05em; color: #4F46E5; margin-bottom: 16px; }
    .feature-item { display: flex; align-items: flex-start; margin-bottom: 14px; font-size: 14px; color: #374151; line-height: 1.5; }
    .feature-icon { width: 24px; height: 24px; border-radius: 6px; background: #EEF2FF; color: #4F46E5; display: inline-flex; align-items: center; justify-content: center; font-weight: 800; margin-right: 14px; font-size: 13px; flex-shrink: 0; }
    .cta-container { text-align: center; margin: 34px 0 26px 0; }
    .btn { display: inline-block; background: linear-gradient(135deg, #4F46E5 0%%, #4338CA 100%%); color: #ffffff !important; text-decoration: none; font-size: 16px; font-weight: 700; padding: 15px 36px; border-radius: 12px; box-shadow: 0 4px 16px rgba(79, 70, 229, 0.4); }
    .footer { border-top: 1px solid #E5E7EB; padding: 26px 36px; text-align: center; font-size: 12px; color: #9CA3AF; line-height: 1.5; }
  </style>
</head>
<body>
  <div class="card">
    <div class="header">
      <div class="logo-badge">P</div>
      <h1>Welcome to ProjectEasyHR!</h1>
      <p>Modern HR, Payroll & People Operations for %s</p>
    </div>
    <div class="body">
      <div class="greeting">Hi %s,</div>
      <div class="intro">
        Congratulations! Your company workspace <strong>%s</strong> has been successfully initialized on ProjectEasyHR. You are now equipped with an enterprise-grade platform to build your hiring pipeline, onboard employees seamlessly, track live attendance, and automate payroll.
      </div>
      
      <div class="details-box">
        <div class="detail-row">
          <span class="detail-label">Company Name:</span>
          <span class="detail-value">%s</span>
        </div>
        <div class="detail-row">
          <span class="detail-label">Workspace URL:</span>
          <span class="detail-value">%s.easyhr.app</span>
        </div>
        <div class="detail-row">
          <span class="detail-label">Administrator Account:</span>
          <span class="detail-value">%s</span>
        </div>
        <div class="detail-row">
          <span class="detail-label">Status:</span>
          <span class="detail-value" style="color: #059669;">Active & Ready</span>
        </div>
      </div>

      <div class="features-grid">
        <div class="features-title">Recommended first steps:</div>
        <div class="feature-item">
          <span class="feature-icon">1</span>
          <span><strong>Complete Company Profile & Branding:</strong> Upload your company logo and define your brand color under Settings &bull; Branding.</span>
        </div>
        <div class="feature-item">
          <span class="feature-icon">2</span>
          <span><strong>Set up Departments & Positions:</strong> Organize your structural hierarchy in the Departments portal.</span>
        </div>
        <div class="feature-item">
          <span class="feature-icon">3</span>
          <span><strong>Publish Open Jobs:</strong> Your career board is live! Publish openings to attract and screen candidates.</span>
        </div>
        <div class="feature-item">
          <span class="feature-icon">4</span>
          <span><strong>Invite Your Team:</strong> Add employees to launch their automated single onboarding experience.</span>
        </div>
      </div>

      <div class="cta-container">
        <a href="%s" class="btn">Open Your HR Dashboard &rarr;</a>
      </div>
    </div>
    <div class="footer">
      Sent with care by <strong>ProjectEasyHR</strong> &bull; The Complete Human Capital Platform.<br/>
      Need support? Contact our people operations advisory team at support@projecteasyhr.com.
    </div>
  </div>
</body>
</html>`, companyName, ownerName, companyName, companyName, subdomain, toEmail, dashboardURL)

	return s.sendViaResend(ctx, toEmail, subject, htmlBody)
}

// 2. Template: Onboarding Invitation (Company-Branded Email)
func (s *Service) SendOnboardingInvitation(ctx context.Context, toEmail, employeeName, jobTitle, companyName, startDate, portalURL string) error {
	return s.SendOnboardingInvitationWithBranding(ctx, toEmail, employeeName, jobTitle, startDate, portalURL, BrandingInfo{
		CompanyName: companyName,
	})
}

func (s *Service) SendOnboardingInvitationWithBranding(ctx context.Context, toEmail, employeeName, jobTitle, startDate, portalURL string, branding BrandingInfo) error {
	if strings.TrimSpace(toEmail) == "" {
		return fmt.Errorf("recipient email is required")
	}

	if portalURL == "" {
		portalURL = "https://easyhr.pro/auth/accept-invite?redirect_url=/my-onboarding"
	}
	if branding.CompanyName == "" {
		branding.CompanyName = "ProjectEasyHR"
	}
	if branding.BrandColor == "" {
		branding.BrandColor = "#4F46E5"
	}
	if startDate == "" {
		startDate = time.Now().Format("Jan 02, 2006")
	}

	subject := fmt.Sprintf("Welcome to %s! Start your onboarding on ProjectEasyHR", branding.CompanyName)

	// Company logo HTML or branded initial box
	logoHTML := fmt.Sprintf(`<div style="display:inline-flex;align-items:center;justify-content:center;width:52px;height:52px;background:rgba(255,255,255,0.2);border:2px solid white;border-radius:12px;font-size:24px;font-weight:900;color:white;margin-bottom:12px;">%s</div>`, string(branding.CompanyName[0]))
	if branding.LogoURL != "" {
		logoHTML = fmt.Sprintf(`<div style="display:inline-block;background:white;padding:6px 14px;border-radius:12px;margin-bottom:14px;box-shadow:0 4px 12px rgba(0,0,0,0.15);"><img src="%s" alt="%s" style="height:34px;max-width:140px;object-fit:contain;display:block;" /></div>`, branding.LogoURL, branding.CompanyName)
	}

	htmlBody := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Welcome to %s</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif; background-color: #f3f4f6; margin: 0; padding: 24px; color: #111827; }
    .card { max-width: 620px; margin: 0 auto; background: #ffffff; border-radius: 16px; overflow: hidden; box-shadow: 0 10px 25px rgba(0,0,0,0.06); }
    .header { background: linear-gradient(135deg, %s 0%%, #1E1B4B 100%%); padding: 42px 36px; text-align: center; color: #ffffff; }
    .header h1 { margin: 0 0 8px 0; font-size: 26px; font-weight: 800; }
    .header p { margin: 0; font-size: 15px; opacity: 0.92; }
    .body { padding: 36px 36px; }
    .greeting { font-size: 19px; font-weight: 700; margin-bottom: 12px; color: #111827; }
    .intro { font-size: 15px; line-height: 1.6; color: #4B5563; margin-bottom: 26px; }
    .details-box { background-color: #F9FAFB; border: 1px solid #E5E7EB; border-radius: 12px; padding: 20px; margin-bottom: 28px; }
    .detail-row { display: flex; justify-content: space-between; margin-bottom: 10px; font-size: 14px; }
    .detail-row:last-child { margin-bottom: 0; }
    .detail-label { color: #6B7280; font-weight: 600; }
    .detail-value { color: #111827; font-weight: 800; }
    .steps-box { margin-bottom: 30px; }
    .steps-title { font-size: 13px; font-weight: 800; text-transform: uppercase; letter-spacing: 0.05em; color: %s; margin-bottom: 14px; }
    .step-item { display: flex; align-items: flex-start; margin-bottom: 12px; font-size: 14px; color: #374151; }
    .step-num { width: 22px; height: 22px; border-radius: 50%%; background: #EEF2FF; color: %s; display: inline-flex; align-items: center; justify-content: center; font-weight: 700; margin-right: 12px; font-size: 12px; flex-shrink: 0; }
    .cta-container { text-align: center; margin: 32px 0 24px 0; }
    .btn { display: inline-block; background: %s; color: #ffffff !important; text-decoration: none; font-size: 16px; font-weight: 700; padding: 14px 34px; border-radius: 10px; box-shadow: 0 4px 14px rgba(79, 70, 229, 0.4); }
    .footer { border-top: 1px solid #E5E7EB; padding: 24px 36px; text-align: center; font-size: 12px; color: #9CA3AF; line-height: 1.5; }
  </style>
</head>
<body>
  <div class="card">
    <div class="header">
      %s
      <h1>Welcome to %s!</h1>
      <p>Your team onboarding portal is ready</p>
    </div>
    <div class="body">
      <div class="greeting">Hi %s,</div>
      <div class="intro">
        Congratulations on your new position! The team at <strong>%s</strong> is thrilled to have you join. To ensure your day one is seamless, we have activated your personalized onboarding portal where you can complete your details, review team materials, and submit your payroll preferences.
      </div>
      
      <div class="details-box">
        <div class="detail-row">
          <span class="detail-label">Role:</span>
          <span class="detail-value">%s</span>
        </div>
        <div class="detail-row">
          <span class="detail-label">Organization:</span>
          <span class="detail-value">%s</span>
        </div>
        <div class="detail-row">
          <span class="detail-label">Start Date:</span>
          <span class="detail-value">%s</span>
        </div>
      </div>

      <div class="steps-box">
        <div class="steps-title">Your 4-step onboarding checklist:</div>
        <div class="step-item">
          <span class="step-num">1</span>
          <span><strong>Welcome & Orientation:</strong> Watch your team welcome message and explore company culture.</span>
        </div>
        <div class="step-item">
          <span class="step-num">2</span>
          <span><strong>Personal Profile:</strong> Fill in emergency contacts and residential address.</span>
        </div>
        <div class="step-item">
          <span class="step-num">3</span>
          <span><strong>Direct Deposit & Payroll:</strong> Set up your payout preferences safely and securely.</span>
        </div>
        <div class="step-item">
          <span class="step-num">4</span>
          <span><strong>Handbook Sign-off:</strong> Review company policies and activate your employee portal.</span>
        </div>
      </div>

      <div class="cta-container">
        <a href="%s" class="btn">Launch Onboarding Portal &rarr;</a>
      </div>
      <p style="text-align: center; font-size: 13px; color: #6B7280; margin: 0;">Or copy this link: <a href="%s" style="color: %s;">%s</a></p>
    </div>
    <div class="footer">
      Powered by <strong>ProjectEasyHR</strong> &bull; Human Resources & Talent for Modern Teams.<br/>
      If you did not expect this invitation, please contact your %s HR administrator.
    </div>
  </div>
</body>
</html>`, branding.CompanyName, branding.BrandColor, branding.BrandColor, branding.BrandColor, branding.BrandColor, logoHTML, branding.CompanyName, employeeName, branding.CompanyName, jobTitle, branding.CompanyName, startDate, portalURL, portalURL, branding.BrandColor, portalURL, branding.CompanyName)

	return s.sendViaResend(ctx, toEmail, subject, htmlBody)
}

// 3. Template: Job Offer Extended (Company-Branded Email)
func (s *Service) SendJobOfferNotification(ctx context.Context, toEmail, candidateName, jobTitle, startDate, acceptURL string, branding BrandingInfo) error {
	if strings.TrimSpace(toEmail) == "" {
		return fmt.Errorf("recipient email is required")
	}

	if branding.CompanyName == "" {
		branding.CompanyName = "ProjectEasyHR"
	}
	if branding.BrandColor == "" {
		branding.BrandColor = "#059669"
	}

	subject := fmt.Sprintf("Formal Job Offer: %s at %s", jobTitle, branding.CompanyName)

	logoHTML := fmt.Sprintf(`<div style="display:inline-flex;align-items:center;justify-content:center;width:52px;height:52px;background:rgba(255,255,255,0.2);border:2px solid white;border-radius:12px;font-size:24px;font-weight:900;color:white;margin-bottom:12px;">%s</div>`, string(branding.CompanyName[0]))
	if branding.LogoURL != "" {
		logoHTML = fmt.Sprintf(`<div style="display:inline-block;background:white;padding:6px 14px;border-radius:12px;margin-bottom:14px;box-shadow:0 4px 12px rgba(0,0,0,0.15);"><img src="%s" alt="%s" style="height:34px;max-width:140px;object-fit:contain;display:block;" /></div>`, branding.LogoURL, branding.CompanyName)
	}

	htmlBody := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Job Offer from %s</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif; background-color: #f3f4f6; margin: 0; padding: 24px; color: #111827; }
    .card { max-width: 620px; margin: 0 auto; background: #ffffff; border-radius: 16px; overflow: hidden; box-shadow: 0 10px 25px rgba(0,0,0,0.06); }
    .header { background: linear-gradient(135deg, %s 0%%, #064E3B 100%%); padding: 42px 36px; text-align: center; color: #ffffff; }
    .header h1 { margin: 0 0 8px 0; font-size: 26px; font-weight: 800; }
    .body { padding: 36px 36px; }
    .greeting { font-size: 19px; font-weight: 700; margin-bottom: 12px; }
    .intro { font-size: 15px; line-height: 1.6; color: #4B5563; margin-bottom: 26px; }
    .details-box { background-color: #ECFDF5; border: 1px solid #A7F3D0; border-radius: 12px; padding: 20px; margin-bottom: 28px; }
    .detail-row { display: flex; justify-content: space-between; margin-bottom: 10px; font-size: 14px; }
    .cta-container { text-align: center; margin: 32px 0 24px 0; }
    .btn { display: inline-block; background: %s; color: #ffffff !important; text-decoration: none; font-size: 16px; font-weight: 700; padding: 14px 34px; border-radius: 10px; }
    .footer { border-top: 1px solid #E5E7EB; padding: 24px 36px; text-align: center; font-size: 12px; color: #9CA3AF; }
  </style>
</head>
<body>
  <div class="card">
    <div class="header">
      %s
      <h1>Congratulations!</h1>
      <p>Job Offer from %s</p>
    </div>
    <div class="body">
      <div class="greeting">Dear %s,</div>
      <div class="intro">
        We were immensely impressed by your experience and interview discussions. On behalf of <strong>%s</strong>, we are thrilled to formally extend an offer of employment for the role of <strong>%s</strong>.
      </div>
      <div class="details-box">
        <div class="detail-row"><span>Position:</span><strong>%s</strong></div>
        <div class="detail-row"><span>Company:</span><strong>%s</strong></div>
        <div class="detail-row"><span>Proposed Start Date:</span><strong>%s</strong></div>
      </div>
      <div class="cta-container">
        <a href="%s" class="btn">Review Offer & Details &rarr;</a>
      </div>
    </div>
    <div class="footer">ProjectEasyHR &bull; Hiring & Talent Suite for %s</div>
  </div>
</body>
</html>`, branding.CompanyName, branding.BrandColor, branding.BrandColor, logoHTML, branding.CompanyName, candidateName, branding.CompanyName, jobTitle, jobTitle, branding.CompanyName, startDate, acceptURL, branding.CompanyName)

	return s.sendViaResend(ctx, toEmail, subject, htmlBody)
}

// SendRaw sends an arbitrary HTML email. Implements messaging.EmailSender.
func (s *Service) SendRaw(ctx context.Context, toEmail, subject, htmlBody string) error {
	return s.sendViaResend(ctx, toEmail, subject, htmlBody)
}

// SendWorkflowNotice sends a small branded transactional notice for HR actions.
// The body and link label are treated as text; callers cannot inject HTML.
func (s *Service) SendWorkflowNotice(ctx context.Context, toEmail, subject, heading, body, actionLabel, actionURL string, branding BrandingInfo) error {
	company := strings.TrimSpace(branding.CompanyName)
	if company == "" {
		company = "Your organization"
	}
	color := strings.TrimSpace(branding.BrandColor)
	if len(color) != 7 || !strings.HasPrefix(color, "#") {
		color = "#4F46E5"
	}
	logo := "<strong style=\"font-size:20px;color:white\">" + html.EscapeString(company) + "</strong>"
	if branding.LogoURL != "" {
		logo = fmt.Sprintf("<img src=\"%s\" alt=\"%s\" style=\"max-height:42px;max-width:180px\">", html.EscapeString(branding.LogoURL), html.EscapeString(company))
	}
	action := ""
	if actionLabel != "" && actionURL != "" {
		action = fmt.Sprintf(`<p style="margin:28px 0"><a href="%s" style="background:%s;color:#fff;padding:12px 20px;border-radius:8px;text-decoration:none;font-weight:700">%s</a></p>`, html.EscapeString(actionURL), color, html.EscapeString(actionLabel))
	}
	bodyHTML := fmt.Sprintf(`<!doctype html><html><body style="margin:0;background:#f4f5f9;font-family:Arial,sans-serif;color:#172033"><main style="max-width:600px;margin:32px auto;background:#fff;border-radius:14px;overflow:hidden"><header style="background:%s;padding:24px 30px">%s</header><section style="padding:30px"><h1 style="font-size:22px">%s</h1><p style="line-height:1.6;color:#475467">%s</p>%s<p style="font-size:13px;color:#667085">%s</p></section></main></body></html>`, color, logo, html.EscapeString(heading), html.EscapeString(body), action, html.EscapeString(company))
	return s.sendViaResend(ctx, toEmail, subject, bodyHTML)
}

// SendRawWithHeaders sends an arbitrary HTML email with custom headers (e.g. In-Reply-To, References) and returns the Resend ID.
func (s *Service) SendRawWithHeaders(ctx context.Context, toEmail, subject, htmlBody string, headers map[string]string) (string, error) {
	return s.sendViaResendWithHeaders(ctx, toEmail, subject, htmlBody, headers)
}

type PayslipNotificationInput struct {
	EmployeeName string
	PeriodStart  string
	PeriodEnd    string
	PayDate      string
	Currency     string
	BasePay      int64
	Earnings     int64
	Deductions   int64
	NetPay       int64
	PortalURL    string
	CompanyName  string
	LogoURL      string
	BrandColor   string
}

func formatMinorAmount(cents int64, currency string) string {
	major := float64(cents) / 100.0
	intPart := int64(major)
	if intPart < 0 {
		intPart = -intPart
	}
	formatted := formatIntWithCommas(intPart)
	if major < 0 {
		formatted = "-" + formatted
	}
	if currency == "" {
		currency = "UGX"
	}
	return fmt.Sprintf("%s %s", currency, formatted)
}

func formatIntWithCommas(n int64) string {
	in := strconv.FormatInt(n, 10)
	var out []byte
	for i, c := range in {
		if i > 0 && (len(in)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, byte(c))
	}
	if len(out) == 0 {
		return "0"
	}
	return string(out)
}

// SendPayslipNotification sends an executive, beautifully formatted corporate pay statement email.
func (s *Service) SendPayslipNotification(ctx context.Context, toEmail string, input PayslipNotificationInput) error {
	company := strings.TrimSpace(input.CompanyName)
	if company == "" {
		company = "Gitop"
	}
	color := strings.TrimSpace(input.BrandColor)
	if len(color) != 7 || !strings.HasPrefix(color, "#") {
		color = "#2563EB"
	}

	logoHTML := fmt.Sprintf(`<span style="font-size:20px;font-weight:800;color:#111827;letter-spacing:-0.5px;">%s</span>`, html.EscapeString(company))
	if input.LogoURL != "" {
		logoHTML = fmt.Sprintf(`<img src="%s" alt="%s" style="max-height:42px;max-width:180px;display:block;">`, html.EscapeString(input.LogoURL), html.EscapeString(company))
	}

	formattedBase := formatMinorAmount(input.BasePay, input.Currency)
	formattedEarnings := formatMinorAmount(input.Earnings, input.Currency)
	formattedDeductions := formatMinorAmount(input.Deductions, input.Currency)
	formattedNet := formatMinorAmount(input.NetPay, input.Currency)

	subject := fmt.Sprintf("Your Pay Statement is Ready — %s to %s", input.PeriodStart, input.PeriodEnd)

	htmlBody := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Pay Statement Notification</title>
</head>
<body style="margin: 0; padding: 0; background-color: #F4F5F7; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif; -webkit-font-smoothing: antialiased;">
  <table width="100%%" border="0" cellpadding="0" cellspacing="0" style="background-color: #F4F5F7; padding: 36px 16px;">
    <tr>
      <td align="center">
        <table width="100%%" border="0" cellpadding="0" cellspacing="0" style="max-width: 600px; background-color: #FFFFFF; border-radius: 16px; overflow: hidden; box-shadow: 0 4px 24px rgba(17, 24, 39, 0.06); border: 1px solid #E5E7EB;">
          <!-- Top Accent Bar -->
          <tr>
            <td style="height: 6px; background: linear-gradient(90deg, #2563EB 0%%, #4F46E5 50%%, #7C3AED 100%%);"></td>
          </tr>
          <!-- Header -->
          <tr>
            <td style="padding: 24px 32px; border-bottom: 1px solid #F3F4F6;">
              <table width="100%%" border="0" cellpadding="0" cellspacing="0">
                <tr>
                  <td align="left" valign="middle">
                    %s
                  </td>
                  <td align="right" valign="middle">
                    <span style="display: inline-block; background-color: #ECFDF5; color: #065F46; font-size: 11.5px; font-weight: 700; text-transform: uppercase; padding: 4px 10px; border-radius: 999px; letter-spacing: 0.5px; border: 1px solid #A7F3D0;">
                      Paid &bull; Disbursed
                    </span>
                  </td>
                </tr>
              </table>
            </td>
          </tr>
          <!-- Body -->
          <tr>
            <td style="padding: 32px 32px 24px 32px;">
              <h1 style="margin: 0 0 6px 0; font-size: 21px; font-weight: 800; color: #111827; letter-spacing: -0.02em;">
                Your Pay Statement is Ready
              </h1>
              <p style="margin: 0 0 24px 0; font-size: 13.5px; line-height: 1.6; color: #4B5563;">
                Hello <strong>%s</strong>, your official payroll statement for the pay cycle <strong>%s to %s</strong> has been calculated and disbursed.
              </p>

              <!-- Net Pay Hero Callout Box -->
              <table width="100%%" border="0" cellpadding="0" cellspacing="0" style="background: linear-gradient(135deg, #EFF6FF 0%%, #DBEAFE 100%%); border: 1.5px solid #93C5FD; border-radius: 12px; margin-bottom: 24px;">
                <tr>
                  <td style="padding: 20px 24px;">
                    <table width="100%%" border="0" cellpadding="0" cellspacing="0">
                      <tr>
                        <td align="left" valign="middle">
                          <div style="font-size: 11px; font-weight: 700; color: #1E40AF; text-transform: uppercase; letter-spacing: 0.5px;">
                            Net Disbursed Take-Home
                          </div>
                          <div style="font-size: 28px; font-weight: 900; color: #1D4ED8; margin-top: 4px; letter-spacing: -0.03em;">
                            %s
                          </div>
                        </td>
                        <td align="right" valign="middle">
                          <div style="font-size: 12px; color: #2563EB; font-weight: 600;">
                            Pay Date: %s
                          </div>
                        </td>
                      </tr>
                    </table>
                  </td>
                </tr>
              </table>

              <!-- Financial Breakdown Table -->
              <table width="100%%" border="0" cellpadding="0" cellspacing="0" style="border: 1px solid #E5E7EB; border-radius: 10px; margin-bottom: 28px; overflow: hidden;">
                <tr style="background-color: #F9FAFB;">
                  <td style="padding: 10px 16px; font-size: 11px; font-weight: 700; color: #6B7280; text-transform: uppercase;">
                    Component
                  </td>
                  <td align="right" style="padding: 10px 16px; font-size: 11px; font-weight: 700; color: #6B7280; text-transform: uppercase;">
                    Amount
                  </td>
                </tr>
                <tr>
                  <td style="padding: 12px 16px; font-size: 13.5px; color: #374151; border-top: 1px solid #F3F4F6;">
                    Base Pay (Regular Salary)
                  </td>
                  <td align="right" style="padding: 12px 16px; font-size: 13.5px; font-weight: 600; color: #111827; border-top: 1px solid #F3F4F6;">
                    %s
                  </td>
                </tr>
                <tr>
                  <td style="padding: 12px 16px; font-size: 13.5px; color: #374151; border-top: 1px solid #F3F4F6;">
                    Additional Earnings &amp; Allowances
                  </td>
                  <td align="right" style="padding: 12px 16px; font-size: 13.5px; font-weight: 600; color: #111827; border-top: 1px solid #F3F4F6;">
                    %s
                  </td>
                </tr>
                <tr>
                  <td style="padding: 12px 16px; font-size: 13.5px; color: #DC2626; border-top: 1px solid #F3F4F6;">
                    Total Deductions (Taxes &amp; Statutory)
                  </td>
                  <td align="right" style="padding: 12px 16px; font-size: 13.5px; font-weight: 600; color: #DC2626; border-top: 1px solid #F3F4F6;">
                    -%s
                  </td>
                </tr>
                <tr style="background-color: #F8FAFC;">
                  <td style="padding: 14px 16px; font-size: 14px; font-weight: 800; color: #111827; border-top: 1.5px solid #E5E7EB;">
                    Take-Home Net Salary
                  </td>
                  <td align="right" style="padding: 14px 16px; font-size: 15px; font-weight: 800; color: #1D4ED8; border-top: 1.5px solid #E5E7EB;">
                    %s
                  </td>
                </tr>
              </table>

              <!-- CTA Button -->
              <table width="100%%" border="0" cellpadding="0" cellspacing="0">
                <tr>
                  <td align="center">
                    <a href="%s" target="_blank" style="display: inline-block; background-color: #2563EB; color: #FFFFFF; font-size: 13.5px; font-weight: 700; padding: 13px 30px; border-radius: 9px; text-decoration: none; box-shadow: 0 2px 8px rgba(37,99,235,0.3);">
                      View Certified Pay Statement &rarr;
                    </a>
                  </td>
                </tr>
              </table>
            </td>
          </tr>
          <!-- Footer -->
          <tr>
            <td style="padding: 22px 32px; background-color: #F9FAFB; border-top: 1px solid #E5E7EB; text-align: center;">
              <p style="margin: 0 0 4px 0; font-size: 12px; font-weight: 600; color: #6B7280;">
                %s &bull; Certified Corporate Payroll
              </p>
              <p style="margin: 0; font-size: 11px; color: #9CA3AF; line-height: 1.5;">
                Confidential Document: This communication contains private compensation details intended solely for %s.
              </p>
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>
</body>
</html>`,
		logoHTML,
		html.EscapeString(input.EmployeeName),
		html.EscapeString(input.PeriodStart),
		html.EscapeString(input.PeriodEnd),
		formattedNet,
		html.EscapeString(input.PayDate),
		formattedBase,
		formattedEarnings,
		formattedDeductions,
		formattedNet,
		html.EscapeString(input.PortalURL),
		html.EscapeString(company),
		html.EscapeString(toEmail),
	)

	return s.sendViaResend(ctx, toEmail, subject, htmlBody)
}
