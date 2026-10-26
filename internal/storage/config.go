// Package storage constructs S3 clients for the clinic photo store.
package storage

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Config describes the S3 endpoint and credentials used by the application.
type Config struct {
	// Endpoint is the S3 endpoint used for browser-facing signed URLs.
	Endpoint string
	// InternalEndpoint is used for backend operations when set. An empty value
	// means backend operations use Endpoint.
	InternalEndpoint string
	// Bucket is the configured photo bucket.
	Bucket string
	// Region is the S3 region used for signing requests.
	Region string
	// AccessKeyID is the application S3 access key ID.
	AccessKeyID string
	// SecretAccessKey is the application S3 secret access key.
	SecretAccessKey string
}

func (c Config) validate() error {
	switch {
	case strings.TrimSpace(c.Endpoint) == "":
		return errors.New("storage: endpoint is required")
	case strings.TrimSpace(c.Bucket) == "":
		return errors.New("storage: bucket is required")
	case strings.TrimSpace(c.Region) == "":
		return errors.New("storage: region is required")
	case strings.TrimSpace(c.AccessKeyID) == "":
		return errors.New("storage: access key ID is required")
	case strings.TrimSpace(c.SecretAccessKey) == "":
		return errors.New("storage: secret access key is required")
	}

	if err := validateEndpoint("endpoint", c.Endpoint); err != nil {
		return err
	}
	if c.InternalEndpoint != "" {
		if err := validateEndpoint("internal endpoint", c.InternalEndpoint); err != nil {
			return err
		}
	}

	return nil
}

func validateEndpoint(name, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("storage: %s is not a valid URL", name)
	}

	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("storage: %s must use http or https", name)
	}

	if parsed.User != nil {
		return fmt.Errorf("storage: %s must not contain credentials", name)
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return fmt.Errorf("storage: %s must not contain a query string", name)
	}
	if parsed.Fragment != "" || strings.Contains(raw, "#") {
		return fmt.Errorf("storage: %s must not contain a fragment", name)
	}
	if parsed.Hostname() == "" {
		return fmt.Errorf("storage: %s must include a hostname", name)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return fmt.Errorf("storage: %s must not contain a path", name)
	}

	return nil
}
