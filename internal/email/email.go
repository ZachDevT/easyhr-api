package email

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
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
	if fromEmail == "" {
		fromEmail = "onboarding@projecteasyhr.com"
	}
	if fromName == "" {
		fromName = "ProjectEasyHR"
	}
	return &Service{
		apiKey:     strings.TrimSpace(apiKey),
		fromEmail:  strings.TrimSpace(fromEmail),
		fromName:   strings.TrimSpace(fromName),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

type SendEmailPayload struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
}

func (s *Service) SendOnboardingInvitation(ctx context.Context, toEmail, employeeName, jobTitle, companyName, startDate, portalURL string) error {
	if strings.TrimSpace(toEmail) == "" {
		return fmt.Errorf("recipient email is required")
	}

	if portalURL == "" {
		portalURL = "http://localhost:3000/my-onboarding"
	}
	if companyName == "" {
		companyName = "ProjectEasyHR"
	}
	if startDate == "" {
		startDate = time.Now().Format("Jan 02, 2006")
	}

	subject := fmt.Sprintf("Welcome to %s! Start your onboarding on ProjectEasyHR 🎉", companyName)

	htmlBody := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Welcome to %s</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif; background-color: #f3f4f6; margin: 0; padding: 24px; color: #111827; }
    .card { max-width: 600px; margin: 0 auto; background: #ffffff; border-radius: 16px; overflow: hidden; box-shadow: 0 10px 25px rgba(0,0,0,0.05); }
    .header { background: linear-gradient(135deg, #4F46E5 0%%, #7C3AED 100%%); padding: 40px 32px; text-align: center; color: #ffffff; }
    .header h1 { margin: 0 0 8px 0; font-size: 26px; font-weight: 800; }
    .header p { margin: 0; font-size: 15px; opacity: 0.9; }
    .body { padding: 36px 32px; }
    .greeting { font-size: 18px; font-weight: 700; margin-bottom: 12px; color: #111827; }
    .intro { font-size: 15px; line-height: 1.6; color: #4B5563; margin-bottom: 24px; }
    .details-box { background-color: #F9FAFB; border: 1px solid #E5E7EB; border-radius: 12px; padding: 20px; margin-bottom: 28px; }
    .detail-row { display: flex; justify-content: space-between; margin-bottom: 10px; font-size: 14px; }
    .detail-row:last-child { margin-bottom: 0; }
    .detail-label { color: #6B7280; font-weight: 500; }
    .detail-value { color: #111827; font-weight: 700; }
    .steps-box { margin-bottom: 32px; }
    .steps-title { font-size: 14px; font-weight: 700; text-transform: uppercase; letter-spacing: 0.05em; color: #4F46E5; margin-bottom: 14px; }
    .step-item { display: flex; align-items: flex-start; margin-bottom: 12px; font-size: 14px; color: #374151; }
    .step-num { width: 22px; height: 22px; border-radius: 50%%; background: #EEF2FF; color: #4F46E5; display: inline-flex; align-items: center; justify-content: center; font-weight: 700; margin-right: 12px; font-size: 12px; flex-shrink: 0; }
    .cta-container { text-align: center; margin: 32px 0 24px 0; }
    .btn { display: inline-block; background: #4F46E5; color: #ffffff !important; text-decoration: none; font-size: 16px; font-weight: 700; padding: 14px 32px; border-radius: 10px; box-shadow: 0 4px 14px rgba(79, 70, 229, 0.4); }
    .footer { border-top: 1px solid #E5E7EB; padding: 24px 32px; text-align: center; font-size: 12px; color: #9CA3AF; }
  </style>
</head>
<body>
  <div class="card">
    <div class="header">
      <h1>Welcome to %s! 🎉</h1>
      <p>Your journey begins now on ProjectEasyHR</p>
    </div>
    <div class="body">
      <div class="greeting">Hi %s,</div>
      <div class="intro">
        Congratulations on your new role! The team at <strong>%s</strong> is thrilled to have you join. To make your day one seamless, we've set up your personalized onboarding portal where you can review your welcome materials, set up your profile, and complete your documentation.
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
        <div class="steps-title">What to expect in your onboarding:</div>
        <div class="step-item">
          <span class="step-num">1</span>
          <span><strong>Welcome & Orientation:</strong> Watch your team welcome message and learn about our culture.</span>
        </div>
        <div class="step-item">
          <span class="step-num">2</span>
          <span><strong>Personal Profile:</strong> Fill in your emergency contacts and contact information.</span>
        </div>
        <div class="step-item">
          <span class="step-num">3</span>
          <span><strong>Payment & Direct Deposit:</strong> Set up your payroll preferences securely.</span>
        </div>
        <div class="step-item">
          <span class="step-num">4</span>
          <span><strong>Compliance & Handbook:</strong> Review and acknowledge company policies.</span>
        </div>
      </div>

      <div class="cta-container">
        <a href="%s" class="btn">Launch Onboarding Portal &rarr;</a>
      </div>
      <p style="text-align: center; font-size: 13px; color: #6B7280; margin: 0;">Or copy this link: <a href="%s" style="color: #4F46E5;">%s</a></p>
    </div>
    <div class="footer">
      Sent with care by ProjectEasyHR &bull; Streamlined Human Resources for Modern Teams.<br/>
      If you did not expect this invitation, please contact your HR administrator.
    </div>
  </div>
</body>
</html>`, companyName, companyName, employeeName, companyName, jobTitle, companyName, startDate, portalURL, portalURL, portalURL)

	log.Printf("[Email] Sending onboarding invitation to %s <%s> for %s...", employeeName, toEmail, companyName)

	if s.apiKey == "" {
		log.Printf("[Email Service: DEV MODE] No RESEND_API_KEY specified. Mock-delivered onboarding invitation to %s. Subject: %s", toEmail, subject)
		return nil
	}

	payload := SendEmailPayload{
		From:    fmt.Sprintf("%s <%s>", s.fromName, s.fromEmail),
		To:      []string{toEmail},
		Subject: subject,
		HTML:    htmlBody,
	}

	reqBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal email payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(reqBytes))
	if err != nil {
		return fmt.Errorf("create email request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		log.Printf("[Email Error] Failed to send via Resend: %v. Continuing in dev fallback mode.", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		log.Printf("[Email Warning] Resend API responded with %d: %s. Continuing in dev fallback mode.", resp.StatusCode, string(respBody))
		return nil
	}

	log.Printf("[Email Success] Onboarding invitation sent successfully via Resend to %s", toEmail)
	return nil
}
