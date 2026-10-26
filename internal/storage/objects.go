package storage

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// ObjectMetadata describes an object stored in the configured bucket.
type ObjectMetadata struct {
	Size        int64
	ContentType string
	ETag        string
}

// Object is a streaming object returned by Get. The caller must close Body.
type Object struct {
	Body     io.ReadCloser
	Metadata ObjectMetadata
}

// Put uploads body to key and returns the object ETag. It may overwrite an
// existing object.
func (c *Client) Put(ctx context.Context, key string, body io.ReadSeeker, size int64, contentType string) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}

	input := &s3.PutObjectInput{
		Bucket:        aws.String(c.bucket),
		Key:           aws.String(key),
		Body:          body,
		ContentLength: aws.Int64(size),
	}
	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}

	output, err := c.backend.PutObject(ctx, input)
	if err != nil {
		return "", fmt.Errorf("storage: put object %q: %w", key, err)
	}

	return aws.ToString(output.ETag), nil
}

// Get opens key for streaming. The caller must close the returned body.
func (c *Client) Get(ctx context.Context, key string) (*Object, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}

	output, err := c.backend.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, notFoundError("get", key, err)
		}
		return nil, fmt.Errorf("storage: get object %q: %w", key, err)
	}
	if output.Body == nil {
		return nil, fmt.Errorf("storage: get object %q: response body is empty", key)
	}

	return &Object{
		Body:     output.Body,
		Metadata: objectMetadata(output.ContentLength, output.ContentType, output.ETag),
	}, nil
}

// Head returns object metadata without downloading the object.
func (c *Client) Head(ctx context.Context, key string) (ObjectMetadata, error) {
	if err := validateKey(key); err != nil {
		return ObjectMetadata{}, err
	}

	output, err := c.backend.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return ObjectMetadata{}, notFoundError("head", key, err)
		}
		return ObjectMetadata{}, fmt.Errorf("storage: head object %q: %w", key, err)
	}

	return objectMetadata(output.ContentLength, output.ContentType, output.ETag), nil
}

// Delete removes key. Deleting an object that is already absent succeeds.
func (c *Client) Delete(ctx context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}

	_, err := c.backend.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("storage: delete object %q: %w", key, err)
	}

	return nil
}

func objectMetadata(size *int64, contentType, etag *string) ObjectMetadata {
	return ObjectMetadata{
		Size:        aws.ToInt64(size),
		ContentType: aws.ToString(contentType),
		ETag:        aws.ToString(etag),
	}
}

func validateKey(key string) error {
	if key == "" {
		return errors.New("storage: object key is required")
	}
	return nil
}
