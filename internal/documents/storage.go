package documents

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Storage interface {
	UploadURL(ctx context.Context, objectKey, contentType string, sizeBytes int64, expiresIn time.Duration) (string, error)
	DownloadURL(ctx context.Context, objectKey string, expiresIn time.Duration) (string, error)
	AttachmentURL(ctx context.Context, objectKey, filename string, expiresIn time.Duration) (string, error)
	PutObject(ctx context.Context, objectKey, contentType string, body io.Reader, sizeBytes int64) error
	GetObject(ctx context.Context, objectKey string) (io.ReadCloser, string, int64, error)
}

type S3Config struct {
	Bucket          string
	Region          string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	UsePathStyle    bool
}

type S3Storage struct {
	bucket    string
	client    *s3.Client
	presigner *s3.PresignClient
}

func NewS3Storage(ctx context.Context, input S3Config) (*S3Storage, error) {
	if input.Bucket == "" || input.Region == "" || input.AccessKeyID == "" || input.SecretAccessKey == "" {
		return nil, fmt.Errorf("S3 bucket, region, access key ID, and secret access key are required")
	}
	options := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(input.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(input.AccessKeyID, input.SecretAccessKey, "")),
	}
	if input.Endpoint != "" {
		endpoint, err := url.Parse(input.Endpoint)
		if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
			return nil, fmt.Errorf("S3 endpoint must be an absolute URL")
		}
		options = append(options, awsconfig.WithBaseEndpoint(input.Endpoint))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("load S3 configuration: %w", err)
	}
	client := s3.NewFromConfig(cfg, func(options *s3.Options) { options.UsePathStyle = input.UsePathStyle })
	return &S3Storage{bucket: input.Bucket, client: client, presigner: s3.NewPresignClient(client)}, nil
}

func (storage *S3Storage) UploadURL(ctx context.Context, objectKey, contentType string, sizeBytes int64, expiresIn time.Duration) (string, error) {
	result, err := storage.presigner.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(storage.bucket),
		Key:           aws.String(objectKey),
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(sizeBytes),
	}, s3.WithPresignExpires(expiresIn))
	if err != nil {
		return "", fmt.Errorf("presign document upload: %w", err)
	}
	return result.URL, nil
}

func (storage *S3Storage) DownloadURL(ctx context.Context, objectKey string, expiresIn time.Duration) (string, error) {
	result, err := storage.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(storage.bucket),
		Key:    aws.String(objectKey),
	}, s3.WithPresignExpires(expiresIn))
	if err != nil {
		return "", fmt.Errorf("presign document download: %w", err)
	}
	return result.URL, nil
}

// AttachmentURL presigns a GET that forces the browser to save the file with the given filename.
func (storage *S3Storage) AttachmentURL(ctx context.Context, objectKey, filename string, expiresIn time.Duration) (string, error) {
	safe := strings.Map(func(r rune) rune {
		if r == '"' || r == '\\' || r < 32 {
			return '_'
		}
		return r
	}, filename)
	if safe == "" {
		safe = "document"
	}
	result, err := storage.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket:                     aws.String(storage.bucket),
		Key:                        aws.String(objectKey),
		ResponseContentDisposition: aws.String(fmt.Sprintf(`attachment; filename="%s"`, safe)),
	}, s3.WithPresignExpires(expiresIn))
	if err != nil {
		return "", fmt.Errorf("presign document attachment: %w", err)
	}
	return result.URL, nil
}

func (storage *S3Storage) PutObject(ctx context.Context, objectKey, contentType string, body io.Reader, sizeBytes int64) error {
	input := &s3.PutObjectInput{
		Bucket:      aws.String(storage.bucket),
		Key:         aws.String(objectKey),
		ContentType: aws.String(contentType),
		Body:        body,
	}
	if sizeBytes > 0 {
		input.ContentLength = aws.Int64(sizeBytes)
	}
	_, err := storage.client.PutObject(ctx, input)
	if err != nil {
		return fmt.Errorf("put object to storage: %w", err)
	}
	return nil
}

func (storage *S3Storage) GetObject(ctx context.Context, objectKey string) (io.ReadCloser, string, int64, error) {
	output, err := storage.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(storage.bucket),
		Key:    aws.String(objectKey),
	})
	if err != nil {
		return nil, "", 0, fmt.Errorf("get object from storage: %w", err)
	}
	contentType := "application/octet-stream"
	if output.ContentType != nil && *output.ContentType != "" {
		contentType = *output.ContentType
	}
	var size int64
	if output.ContentLength != nil {
		size = *output.ContentLength
	}
	return output.Body, contentType, size, nil
}
