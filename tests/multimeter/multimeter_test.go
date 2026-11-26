package multimeter_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"clinic-backend/internal/app"
	"clinic-backend/internal/multimeter"
	"clinic-backend/internal/storage"

	"github.com/gin-gonic/gin"
)

const (
	linuxChromeUA   = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36"
	windowsChromeUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36"
)

type fakeDownloadStorage struct {
	headCalls int
	headKey   string
	headErr   error
	calls     int
	key       string
	err       error
}

func (f *fakeDownloadStorage) Head(_ context.Context, key string) (storage.ObjectMetadata, error) {
	f.headCalls++
	f.headKey = key
	if f.headErr != nil {
		return storage.ObjectMetadata{}, f.headErr
	}
	return storage.ObjectMetadata{Size: 1}, nil
}

func (f *fakeDownloadStorage) PresignGet(_ context.Context, key string) (storage.SignedRequest, error) {
	f.calls++
	f.key = key
	if f.err != nil {
		return storage.SignedRequest{}, f.err
	}
	return storage.SignedRequest{
		URL:    "https://s3.example.com/" + key + "?X-Amz-Signature=test",
		Method: http.MethodGet,
	}, nil
}

func TestDownloadClassifiesUserAgentAndReturnsLink(t *testing.T) {
	tests := []struct {
		name             string
		userAgent        string
		platformHint     string
		architectureHint string
		wantPlatform     multimeter.Platform
		wantKey          string
	}{
		{
			name:         "linux defaults to gnu",
			userAgent:    linuxChromeUA,
			wantPlatform: multimeter.PlatformLinuxGNU,
			wantKey:      "multimeter/latest/multimeter-x86_64-unknown-linux-gnu.tar.gz",
		},
		{
			name:         "linux musl marker",
			userAgent:    "Mozilla/5.0 (X11; Linux x86_64; musl) AppleWebKit/537.36",
			wantPlatform: multimeter.PlatformLinuxMusl,
			wantKey:      "multimeter/latest/multimeter-x86_64-unknown-linux-musl.tar.gz",
		},
		{
			name:         "alpine maps to musl",
			userAgent:    "Mozilla/5.0 (X11; Alpine Linux x86_64) AppleWebKit/537.36",
			wantPlatform: multimeter.PlatformLinuxMusl,
			wantKey:      "multimeter/latest/multimeter-x86_64-unknown-linux-musl.tar.gz",
		},
		{
			name:         "linux i386 has no executable",
			userAgent:    "Mozilla/5.0 (X11; Linux i386) AppleWebKit/537.36",
			wantPlatform: multimeter.PlatformMismatch,
		},
		{
			name:         "linux i686 has no executable",
			userAgent:    "Mozilla/5.0 (X11; Linux i686) AppleWebKit/537.36",
			wantPlatform: multimeter.PlatformMismatch,
		},
		{
			name:             "linux i386 ignores an ambiguous client hint",
			userAgent:        "Mozilla/5.0 (X11; Linux i386) AppleWebKit/537.36",
			platformHint:     `"Linux"`,
			architectureHint: `"x86"`,
			wantPlatform:     multimeter.PlatformMismatch,
		},
		{
			name:         "linux without a supported architecture marker",
			userAgent:    "Mozilla/5.0 (X11; Linux) AppleWebKit/537.36",
			wantPlatform: multimeter.PlatformMismatch,
		},
		{
			name:         "windows x64",
			userAgent:    windowsChromeUA,
			wantPlatform: multimeter.PlatformWindowsX86,
			wantKey:      "multimeter/latest/multimeter-x86_64-pc-windows-msvc.exe",
		},
		{
			name:         "windows arm user agent",
			userAgent:    "Mozilla/5.0 (Windows NT 10.0; ARM64) AppleWebKit/537.36",
			wantPlatform: multimeter.PlatformWindowsARM,
			wantKey:      "multimeter/latest/multimeter-aarch64-pc-windows-msvc.exe",
		},
		{
			name:         "windows 32-bit has no executable",
			userAgent:    "Mozilla/5.0 (Windows NT 10.0; Win32; x86) AppleWebKit/537.36",
			wantPlatform: multimeter.PlatformMismatch,
		},
		{
			name:         "windows arm32 has no executable",
			userAgent:    "Mozilla/5.0 (Windows NT 10.0; ARM) AppleWebKit/537.36",
			wantPlatform: multimeter.PlatformMismatch,
		},
		{
			name:             "windows arm32 ignores an ambiguous client hint",
			userAgent:        "Mozilla/5.0 (Windows NT 10.0; ARM) AppleWebKit/537.36",
			platformHint:     `"Windows"`,
			architectureHint: `"arm"`,
			wantPlatform:     multimeter.PlatformMismatch,
		},
		{
			name:             "client hint overrides windows user agent architecture",
			userAgent:        windowsChromeUA,
			platformHint:     `"Windows"`,
			architectureHint: `"arm"`,
			wantPlatform:     multimeter.PlatformWindowsARM,
			wantKey:          "multimeter/latest/multimeter-aarch64-pc-windows-msvc.exe",
		},
		{
			name:             "linux x86 client hints",
			platformHint:     `"Linux"`,
			architectureHint: `"x86"`,
			wantPlatform:     multimeter.PlatformLinuxGNU,
			wantKey:          "multimeter/latest/multimeter-x86_64-unknown-linux-gnu.tar.gz",
		},
		{
			name:             "linux arm has no executable",
			userAgent:        linuxChromeUA,
			platformHint:     `"Linux"`,
			architectureHint: `"arm"`,
			wantPlatform:     multimeter.PlatformMismatch,
		},
		{
			name:         "chromeos has no executable",
			userAgent:    "Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36",
			wantPlatform: multimeter.PlatformMismatch,
		},
		{
			name:         "chromeos cannot override the platform with a hint",
			userAgent:    "Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36",
			platformHint: `"Linux"`,
			wantPlatform: multimeter.PlatformMismatch,
		},
		{
			name:         "android has no executable",
			userAgent:    "Mozilla/5.0 (Linux; Android 14; Pixel 9) AppleWebKit/537.36",
			wantPlatform: multimeter.PlatformMismatch,
		},
		{
			name:         "android cannot override the platform with a hint",
			userAgent:    "Mozilla/5.0 (Linux; Android 14; Pixel 9) AppleWebKit/537.36",
			platformHint: `"Linux"`,
			wantPlatform: multimeter.PlatformMismatch,
		},
		{
			name:         "macos has no executable",
			userAgent:    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
			wantPlatform: multimeter.PlatformMismatch,
		},
		{
			name:         "x11 on freebsd is not linux",
			userAgent:    "Mozilla/5.0 (X11; FreeBSD amd64) AppleWebKit/537.36",
			wantPlatform: multimeter.PlatformMismatch,
		},
		{
			name:         "linux hint cannot override windows user agent",
			userAgent:    windowsChromeUA,
			platformHint: `"Linux"`,
			wantPlatform: multimeter.PlatformMismatch,
		},
		{
			name:         "windows hint cannot override linux user agent",
			userAgent:    linuxChromeUA,
			platformHint: `"Windows"`,
			wantPlatform: multimeter.PlatformMismatch,
		},
		{
			name:             "unknown architecture hint",
			userAgent:        windowsChromeUA,
			platformHint:     `"Windows"`,
			architectureHint: `"sparc"`,
			wantPlatform:     multimeter.PlatformMismatch,
		},
		{
			name:         "missing user agent",
			wantPlatform: multimeter.PlatformMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			downloadStorage := &fakeDownloadStorage{}
			recorder := doDownloadRequest(t, multimeter.NewHandler(downloadStorage), tt.userAgent, tt.platformHint, tt.architectureHint)
			if recorder.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
			}

			var response struct {
				Platform    multimeter.Platform `json:"platform"`
				DownloadURL *string             `json:"downloadUrl"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Platform != tt.wantPlatform {
				t.Fatalf("platform = %q, want %q", response.Platform, tt.wantPlatform)
			}
			if tt.wantKey == "" {
				if response.DownloadURL != nil {
					t.Fatalf("downloadUrl = %q, want null", *response.DownloadURL)
				}
				if downloadStorage.headCalls != 0 {
					t.Fatalf("head calls = %d, want 0", downloadStorage.headCalls)
				}
				if downloadStorage.calls != 0 {
					t.Fatalf("signer calls = %d, want 0", downloadStorage.calls)
				}
				return
			}

			if response.DownloadURL == nil {
				t.Fatal("downloadUrl is null")
			}
			if downloadStorage.headCalls != 1 {
				t.Fatalf("head calls = %d, want 1", downloadStorage.headCalls)
			}
			if downloadStorage.headKey != tt.wantKey {
				t.Fatalf("head key = %q, want %q", downloadStorage.headKey, tt.wantKey)
			}
			if downloadStorage.calls != 1 || downloadStorage.key != tt.wantKey {
				t.Fatalf("signer = (%d, %q), want (1, %q)", downloadStorage.calls, downloadStorage.key, tt.wantKey)
			}
			if want := "https://s3.example.com/" + tt.wantKey + "?X-Amz-Signature=test"; *response.DownloadURL != want {
				t.Fatalf("downloadUrl = %q, want %q", *response.DownloadURL, want)
			}
		})
	}
}

func TestDownloadSetsClientHintAndCacheHeaders(t *testing.T) {
	recorder := doDownloadRequest(t, multimeter.NewHandler(&fakeDownloadStorage{}), linuxChromeUA, "", "")
	if got := recorder.Header().Get("Accept-CH"); got != "Sec-CH-UA-Platform, Sec-CH-UA-Arch" {
		t.Fatalf("Accept-CH = %q", got)
	}
	if got := recorder.Header().Get("Critical-CH"); got != "Sec-CH-UA-Arch" {
		t.Fatalf("Critical-CH = %q", got)
	}
	if got := recorder.Header().Get("Vary"); got != "User-Agent, Sec-CH-UA-Platform, Sec-CH-UA-Arch" {
		t.Fatalf("Vary = %q", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

func TestDownloadSigningFailure(t *testing.T) {
	handler := multimeter.NewHandler(&fakeDownloadStorage{err: errors.New("signing unavailable")})
	recorder := doDownloadRequest(t, handler, linuxChromeUA, "", "")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestDownloadUnavailableWhenObjectCannotBeRead(t *testing.T) {
	downloadStorage := &fakeDownloadStorage{headErr: errors.New("access denied")}
	handler := multimeter.NewHandler(downloadStorage)
	recorder := doDownloadRequest(t, handler, linuxChromeUA, "", "")
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if downloadStorage.headCalls != 1 {
		t.Fatalf("head calls = %d, want 1", downloadStorage.headCalls)
	}
	if downloadStorage.calls != 0 {
		t.Fatalf("signer calls = %d, want 0", downloadStorage.calls)
	}
}

func TestDownloadRouteIsPublic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	deny := func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) }
	router := app.NewRouter(app.RouterConfig{
		MultimeterH: multimeter.NewHandler(&fakeDownloadStorage{}),
		AdminAuth:   deny,
		ClientAuth:  deny,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/downloads/stress-test-helper/download", nil)
	req.Header.Set("User-Agent", linuxChromeUA)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected public route to return 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestLoadMultimeterStorageServiceUsesExecutableBucket(t *testing.T) {
	values := map[string]string{
		"S3_ENDPOINT":          "https://s3.example.com",
		"S3_REGION":            "us-east-1",
		"S3_ACCESS_KEY_ID":     "photo-key",
		"S3_SECRET_ACCESS_KEY": "photo-secret",
	}
	service, err := app.LoadMultimeterStorageService(func(key string) string { return values[key] })
	if err != nil {
		t.Fatalf("LoadMultimeterStorageService() error = %v", err)
	}

	signed, err := service.PresignGet(context.Background(), "multimeter/latest/multimeter-x86_64-pc-windows-msvc.exe")
	if err != nil {
		t.Fatalf("PresignGet() error = %v", err)
	}
	parsed, err := url.Parse(signed.URL)
	if err != nil {
		t.Fatalf("parse signed URL: %v", err)
	}
	if want := "/clinic-executable/multimeter/latest/multimeter-x86_64-pc-windows-msvc.exe"; parsed.Path != want {
		t.Fatalf("path = %q, want %q", parsed.Path, want)
	}
	if got := parsed.Query().Get("X-Amz-Expires"); got != "600" {
		t.Fatalf("X-Amz-Expires = %q, want 600", got)
	}
}

func TestLoadMultimeterStorageServiceBucketOverride(t *testing.T) {
	values := map[string]string{
		"S3_ENDPOINT":                     "https://s3.example.com",
		"S3_REGION":                       "us-east-1",
		"S3_ACCESS_KEY_ID":                "photo-key",
		"S3_SECRET_ACCESS_KEY":            "photo-secret",
		"MULTIMETER_S3_BUCKET":            "other-executables",
		"MULTIMETER_S3_ACCESS_KEY_ID":     "multimeter-key",
		"MULTIMETER_S3_SECRET_ACCESS_KEY": "multimeter-secret",
	}
	service, err := app.LoadMultimeterStorageService(func(key string) string { return values[key] })
	if err != nil {
		t.Fatalf("LoadMultimeterStorageService() error = %v", err)
	}

	signed, err := service.PresignGet(context.Background(), "multimeter/latest/multimeter-x86_64-unknown-linux-gnu.tar.gz")
	if err != nil {
		t.Fatalf("PresignGet() error = %v", err)
	}
	parsed, err := url.Parse(signed.URL)
	if err != nil {
		t.Fatalf("parse signed URL: %v", err)
	}
	if want := "/other-executables/multimeter/latest/multimeter-x86_64-unknown-linux-gnu.tar.gz"; parsed.Path != want {
		t.Fatalf("path = %q, want %q", parsed.Path, want)
	}
}

func doDownloadRequest(t *testing.T, handler *multimeter.Handler, userAgent, platformHint, architectureHint string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/downloads/stress-test-helper/download", handler.Download)

	req := httptest.NewRequest(http.MethodGet, "/api/downloads/stress-test-helper/download", nil)
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	if platformHint != "" {
		req.Header.Set("Sec-CH-UA-Platform", platformHint)
	}
	if architectureHint != "" {
		req.Header.Set("Sec-CH-UA-Arch", architectureHint)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}
