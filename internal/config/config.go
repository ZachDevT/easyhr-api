package config

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Environment       string
	HTTPAddress       string
	DatabaseURL       string
	AllowedOrigins    []string
	Clerk             ClerkConfig
	Documents         DocumentStorageConfig
	PlatformJWTSecret string
	DocuSign          DocuSignConfig
}

type ClerkConfig struct {
	SecretKey         string
	Issuer            string
	AuthorizedParties []string
	RequiredAudiences []string
}

type DocumentStorageConfig struct {
	Bucket          string
	Region          string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	UsePathStyle    bool
}

type DocuSignConfig struct {
	APIUrl     string
	AccountID  string
	PrivateKey []byte
}

func Load() (Config, error) {
	cfg := Config{
		Environment: defaultString(os.Getenv("EASYHR_ENV"), "development"),
		HTTPAddress: defaultString(os.Getenv("EASYHR_HTTP_ADDRESS"), ":8080"),
		DatabaseURL: strings.TrimSpace(os.Getenv("EASYHR_DATABASE_URL")),
		AllowedOrigins: splitCSV(defaultString(
			os.Getenv("EASYHR_ALLOWED_ORIGINS"),
			"http://localhost:3000,http://localhost:3001",
		)),
		Clerk: ClerkConfig{
			SecretKey:         strings.TrimSpace(os.Getenv("EASYHR_CLERK_SECRET_KEY")),
			Issuer:            strings.TrimSpace(os.Getenv("EASYHR_CLERK_ISSUER")),
			AuthorizedParties: splitCSV(os.Getenv("EASYHR_CLERK_AUTHORIZED_PARTIES")),
			RequiredAudiences: splitCSV(os.Getenv("EASYHR_CLERK_AUDIENCES")),
		},
		Documents: DocumentStorageConfig{
			Bucket:          strings.TrimSpace(os.Getenv("EASYHR_DOCUMENTS_S3_BUCKET")),
			Region:          strings.TrimSpace(os.Getenv("EASYHR_DOCUMENTS_S3_REGION")),
			Endpoint:        strings.TrimSpace(os.Getenv("EASYHR_DOCUMENTS_S3_ENDPOINT")),
			AccessKeyID:     strings.TrimSpace(os.Getenv("EASYHR_DOCUMENTS_S3_ACCESS_KEY_ID")),
			SecretAccessKey: strings.TrimSpace(os.Getenv("EASYHR_DOCUMENTS_S3_SECRET_ACCESS_KEY")),
			UsePathStyle:    strings.EqualFold(strings.TrimSpace(os.Getenv("EASYHR_DOCUMENTS_S3_USE_PATH_STYLE")), "true"),
		},
		PlatformJWTSecret: defaultString(os.Getenv("EASYHR_PLATFORM_JWT_SECRET"), "fallback_secret"),
		DocuSign: DocuSignConfig{
			APIUrl:     defaultString(os.Getenv("EASYHR_DOCUSIGN_API_URL"), "https://demo.docusign.net"),
			AccountID:  defaultString(os.Getenv("EASYHR_DOCUSIGN_ACCOUNT_ID"), "dummy-account"),
			PrivateKey: []byte(os.Getenv("EASYHR_DOCUSIGN_PRIVATE_KEY")),
		},
	}

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("EASYHR_DATABASE_URL is required")
	}
	if len(cfg.AllowedOrigins) == 0 {
		return Config{}, fmt.Errorf("EASYHR_ALLOWED_ORIGINS must contain at least one origin")
	}
	if cfg.Clerk.SecretKey == "" {
		return Config{}, fmt.Errorf("EASYHR_CLERK_SECRET_KEY is required")
	}
	if cfg.Clerk.Issuer == "" {
		return Config{}, fmt.Errorf("EASYHR_CLERK_ISSUER is required")
	}
	if len(cfg.Clerk.AuthorizedParties) == 0 {
		return Config{}, fmt.Errorf("EASYHR_CLERK_AUTHORIZED_PARTIES must contain at least one origin")
	}
	if cfg.Documents.Bucket == "" || cfg.Documents.Region == "" || cfg.Documents.AccessKeyID == "" || cfg.Documents.SecretAccessKey == "" {
		return Config{}, fmt.Errorf("document S3 bucket, region, access key ID, and secret access key are required")
	}
	return cfg, nil
}

func defaultString(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}

func splitCSV(value string) []string {
	items := strings.Split(value, ",")
	result := make([]string, 0, len(items))
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}
