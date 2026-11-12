package app

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"text/template"
	"time"
)

const (
	defaultServiceDateReminderLeadTimes = "5h,3h"
	defaultServiceDateReminderAccount   = "qq"
	defaultServiceDateReminderTimeout   = 5 * time.Second
	defaultServiceDateReminderTemplate  = `【校医院工单提醒】服务将在 {{.LeadTime}} 后开始（{{.Date}} {{.StartTime}}，{{.Room}}），当前还有 {{.PendingCount}} 个待审核工单，请及时处理。`

	serviceDateReminderCatchup = time.Hour
)

// ServiceDateReminderConfig controls the pending-ticket reminder worker.
type ServiceDateReminderConfig struct {
	Enabled         bool
	LeadTimes       []time.Duration
	Horizon         time.Duration
	CatchupWindow   time.Duration
	MessageTemplate *template.Template
	Gateway         ServiceDateReminderGatewayConfig
}

// ServiceDateReminderGatewayConfig contains the account-scoped gateway
// settings used to deliver reminder messages.
type ServiceDateReminderGatewayConfig struct {
	BaseURL      string
	Account      string
	AccountToken string
	Timeout      time.Duration
}

// LoadServiceDateReminderConfig reads and validates service-date reminder
// settings. The message template and lead times are validated even while the
// worker is disabled; gateway credentials are required only when enabled.
func LoadServiceDateReminderConfig(getenv func(string) string) (ServiceDateReminderConfig, error) {
	enabled, err := parseBoolSetting(getenv("SERVICE_DATE_REMINDER_ENABLED"), false)
	if err != nil {
		return ServiceDateReminderConfig{}, fmt.Errorf("invalid SERVICE_DATE_REMINDER_ENABLED: %w", err)
	}

	leadTimes, err := parseServiceDateReminderLeadTimes(getenv("SERVICE_DATE_REMINDER_LEAD_TIMES"))
	if err != nil {
		return ServiceDateReminderConfig{}, fmt.Errorf("invalid SERVICE_DATE_REMINDER_LEAD_TIMES: %w", err)
	}

	messageTemplate, err := parseServiceDateReminderTemplate(getenv("SERVICE_DATE_REMINDER_TEMPLATE"))
	if err != nil {
		return ServiceDateReminderConfig{}, fmt.Errorf("invalid SERVICE_DATE_REMINDER_TEMPLATE: %w", err)
	}

	baseURL := strings.TrimRight(strings.TrimSpace(getenv("MESSAGE_GATEWAY_URL")), "/")
	if baseURL != "" {
		if err := validateServiceDateReminderGatewayURL(baseURL); err != nil {
			return ServiceDateReminderConfig{}, fmt.Errorf("invalid MESSAGE_GATEWAY_URL: %w", err)
		}
	}

	account := strings.TrimSpace(getenv("MESSAGE_GATEWAY_ACCOUNT"))
	if account == "" {
		account = defaultServiceDateReminderAccount
	}
	if err := validateServiceDateReminderGatewayAccount(account); err != nil {
		return ServiceDateReminderConfig{}, fmt.Errorf("invalid MESSAGE_GATEWAY_ACCOUNT: %w", err)
	}

	accountToken := strings.TrimSpace(getenv("MESSAGE_GATEWAY_ACCOUNT_TOKEN"))
	if enabled && baseURL == "" {
		return ServiceDateReminderConfig{}, fmt.Errorf("MESSAGE_GATEWAY_URL is required when SERVICE_DATE_REMINDER_ENABLED is true")
	}
	if enabled && accountToken == "" {
		return ServiceDateReminderConfig{}, fmt.Errorf("MESSAGE_GATEWAY_ACCOUNT_TOKEN is required when SERVICE_DATE_REMINDER_ENABLED is true")
	}

	timeout, err := parsePositiveDurationSetting(getenv("MESSAGE_GATEWAY_TIMEOUT"), defaultServiceDateReminderTimeout)
	if err != nil {
		return ServiceDateReminderConfig{}, fmt.Errorf("invalid MESSAGE_GATEWAY_TIMEOUT: %w", err)
	}

	return ServiceDateReminderConfig{
		Enabled:         enabled,
		LeadTimes:       leadTimes,
		Horizon:         leadTimes[0] + serviceDateReminderCatchup,
		CatchupWindow:   serviceDateReminderCatchup,
		MessageTemplate: messageTemplate,
		Gateway: ServiceDateReminderGatewayConfig{
			BaseURL:      baseURL,
			Account:      account,
			AccountToken: accountToken,
			Timeout:      timeout,
		},
	}, nil
}

func parseServiceDateReminderLeadTimes(raw string) ([]time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = defaultServiceDateReminderLeadTimes
	}

	parts := strings.Split(raw, ",")
	if len(parts) != 2 {
		return nil, fmt.Errorf("must contain exactly two comma-separated durations")
	}

	leadTimes := make([]time.Duration, 0, len(parts))
	seen := make(map[time.Duration]struct{}, len(parts))
	for _, part := range parts {
		leadTime, err := time.ParseDuration(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		if leadTime <= serviceDateReminderCatchup {
			return nil, fmt.Errorf("%s must be greater than the %s catch-up window", leadTime, serviceDateReminderCatchup)
		}
		if _, ok := seen[leadTime]; ok {
			return nil, fmt.Errorf("duplicate lead time %s", leadTime)
		}
		seen[leadTime] = struct{}{}
		leadTimes = append(leadTimes, leadTime)
	}

	sort.Slice(leadTimes, func(i, j int) bool {
		return leadTimes[i] > leadTimes[j]
	})
	return leadTimes, nil
}

func parseServiceDateReminderTemplate(raw string) (*template.Template, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = defaultServiceDateReminderTemplate
	}

	tmpl, err := template.New("service_date_reminder").
		Option("missingkey=error").
		Parse(raw)
	if err != nil {
		return nil, err
	}

	sample := map[string]string{
		"Date":         "2026-11-12",
		"StartTime":    "18:30",
		"Room":         "北区医务室",
		"PendingCount": "3",
		"LeadTime":     "5h0m",
	}
	var rendered strings.Builder
	if err := tmpl.Execute(&rendered, sample); err != nil {
		return nil, fmt.Errorf("template references an unsupported value: %w", err)
	}
	return tmpl, nil
}

func validateServiceDateReminderGatewayURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("scheme must be http or https")
	}
	if parsed.Host == "" {
		return fmt.Errorf("host is required")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("must not contain a query or fragment")
	}
	return nil
}

func validateServiceDateReminderGatewayAccount(account string) error {
	if account == "." || account == ".." {
		return fmt.Errorf("must be a valid path segment")
	}
	if strings.ContainsAny(account, "/?#") {
		return fmt.Errorf("must not contain '/', '?', or '#'")
	}
	return nil
}
