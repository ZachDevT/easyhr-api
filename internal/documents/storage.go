package documents

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Storage interface {
	UploadURL(ctx context.Context, objectKey, contentType string, sizeBytes int64, expiresIn time.Duration) (string, error)
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
	return &S3Storage{bucket: input.Bucket, presigner: s3.NewPresignClient(client)}, nil
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
