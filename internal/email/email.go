package email

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
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

	log.Printf("[Email Warning] Resend status %d: %s. Handled gracefully.", statusCode, respStr)
	return "", nil
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

	subject := fmt.Sprintf("Welcome to ProjectEasyHR, %s! Your workspace %s is live 🚀", ownerName, companyName)

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
		portalURL = "http://localhost:3000/my-onboarding"
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

	subject := fmt.Sprintf("Welcome to %s! Start your onboarding on ProjectEasyHR 🎉", branding.CompanyName)

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
      <h1>Welcome to %s! 🎉</h1>
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

	subject := fmt.Sprintf("Formal Job Offer: %s at %s 🎊", jobTitle, branding.CompanyName)

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
      <h1>Congratulations! 🎉</h1>
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

// SendRawWithHeaders sends an arbitrary HTML email with custom headers (e.g. In-Reply-To, References) and returns the Resend ID.
func (s *Service) SendRawWithHeaders(ctx context.Context, toEmail, subject, htmlBody string, headers map[string]string) (string, error) {
	return s.sendViaResendWithHeaders(ctx, toEmail, subject, htmlBody, headers)
}
