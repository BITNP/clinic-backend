package storage

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Check verifies that the configured bucket exists and is accessible with the
// configured credentials. It does not create or modify anything.
func (c *Client) Check(ctx context.Context) error {
	if _, err := c.backend.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(c.bucket),
	}); err != nil {
		return fmt.Errorf("storage: check bucket %q: %w", c.bucket, err)
	}
	return nil
}
