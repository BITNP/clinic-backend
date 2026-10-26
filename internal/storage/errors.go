package storage

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// ErrObjectNotFound is returned by Get and Head when the object does not exist.
var ErrObjectNotFound = errors.New("object not found")

func notFoundError(operation, key string, err error) error {
	return fmt.Errorf("storage: %s object %q: %w: %w", operation, key, ErrObjectNotFound, err)
}

func isNotFound(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound", "NoSuchObject":
			return true
		case "NoSuchBucket":
			return false
		}
	}

	var responseErr *smithyhttp.ResponseError
	return errors.As(err, &responseErr) && responseErr.HTTPStatusCode() == http.StatusNotFound
}
