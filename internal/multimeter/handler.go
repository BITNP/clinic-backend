package multimeter

import (
	"context"
	"net/http"

	"clinic-backend/internal/storage"

	"github.com/gin-gonic/gin"
)

// DownloadURLSigner creates a signed, short-lived object URL.
type DownloadURLSigner interface {
	PresignGet(ctx context.Context, key string) (storage.SignedRequest, error)
}

// Handler serves public multimeter download links.
type Handler struct {
	signer DownloadURLSigner
}

// NewHandler creates a multimeter download handler.
func NewHandler(signer DownloadURLSigner) *Handler {
	return &Handler{signer: signer}
}

type downloadResponse struct {
	Platform    Platform `json:"platform"`
	DownloadURL *string  `json:"downloadUrl"`
}

// Download selects the executable matching the requester's platform.
func (h *Handler) Download(c *gin.Context) {
	c.Header("Accept-CH", "Sec-CH-UA-Platform, Sec-CH-UA-Arch")
	c.Header("Critical-CH", "Sec-CH-UA-Arch")
	c.Header("Vary", "User-Agent, Sec-CH-UA-Platform, Sec-CH-UA-Arch")
	c.Header("Cache-Control", "no-store")

	platform := classifyPlatform(
		c.Request.UserAgent(),
		c.GetHeader("Sec-CH-UA-Platform"),
		c.GetHeader("Sec-CH-UA-Arch"),
	)
	key, ok := executableKey(platform)
	if !ok {
		c.JSON(http.StatusOK, downloadResponse{Platform: platform})
		return
	}

	signed, err := h.signer.PresignGet(c.Request.Context(), key)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create multimeter download URL"})
		return
	}

	c.JSON(http.StatusOK, downloadResponse{
		Platform:    platform,
		DownloadURL: &signed.URL,
	})
}
