package storage

import (
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Client holds the configured S3 bucket and SDK clients. Storage operations
// are added in later stages.
type Client struct {
	bucket  string
	backend *s3.Client
	presign *s3.Client
}

// NewClient validates cfg and constructs S3 SDK clients without contacting the
// endpoint, checking the bucket, or verifying credentials and connectivity.
func NewClient(cfg Config) (*Client, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	backendEndpoint := cfg.Endpoint
	if cfg.InternalEndpoint != "" {
		backendEndpoint = cfg.InternalEndpoint
	}

	credentialsProvider := credentials.NewStaticCredentialsProvider(
		cfg.AccessKeyID,
		cfg.SecretAccessKey,
		"",
	)

	newS3Client := func(endpoint string) *s3.Client {
		return s3.New(s3.Options{
			Region:       cfg.Region,
			Credentials:  credentialsProvider,
			BaseEndpoint: aws.String(strings.TrimSuffix(endpoint, "/")),
			UsePathStyle: true,
		})
	}

	return &Client{
		bucket:  cfg.Bucket,
		backend: newS3Client(backendEndpoint),
		presign: newS3Client(cfg.Endpoint),
	}, nil
}
