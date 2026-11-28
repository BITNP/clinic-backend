package app

import (
	"bytes"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"text/template"
	"time"

	configservice "clinic-backend/internal/config/service"
)

const (
	defaultServiceDateReminderAccount = "qq"
	defaultServiceDateReminderTimeout = 5 * time.Second
)

// ServiceDateReminderConfig controls the pending-ticket reminder worker.
type ServiceDateReminderConfig struct {
	Enabled                   bool
	LeadTimes                 []time.Duration
	MessageTemplate           *template.Template
	CompletionDelay           time.Duration
	CompletionMessageTemplate *template.Template
	Gateway                   ServiceDateReminderGatewayConfig
}

// ServiceDateReminderGatewayConfig contains the account-scoped gateway
// settings used to deliver reminder messages.
type ServiceDateReminderGatewayConfig struct {
	BaseURL      string
	Account      string
	AccountToken string
	Timeout      time.Duration
}

// LoadServiceDateReminderConfig validates one database runtime-settings
// snapshot and converts it into reminder delivery configuration.
func LoadServiceDateReminderConfig(settings configservice.RuntimeSettings) (ServiceDateReminderConfig, error) {
	leadTimes := append([]time.Duration(nil), settings.ServiceDateReminderLeadTimes...)
	if err := validateServiceDateReminderLeadTimes(leadTimes); err != nil {
		return ServiceDateReminderConfig{}, fmt.Errorf("invalid SERVICE_DATE_REMINDER_LEAD_TIMES: %w", err)
	}
	sort.Slice(leadTimes, func(i, j int) bool {
		return leadTimes[i] > leadTimes[j]
	})

	messageTemplate, err := parseServiceDateReminderTemplate(
		"service_date_reminder",
		settings.ServiceDateReminderTemplate,
		configservice.DefaultServiceDateReminderTemplate,
	)
	if err != nil {
		return ServiceDateReminderConfig{}, fmt.Errorf("invalid SERVICE_DATE_REMINDER_TEMPLATE: %w", err)
	}
	completionDelay := settings.ServiceDateCompletionReminderDelay
	if completionDelay == 0 {
		completionDelay = configservice.DefaultServiceDateCompletionReminderDelay
	}
	if completionDelay <= 0 || completionDelay%time.Second != 0 {
		return ServiceDateReminderConfig{}, fmt.Errorf(
			"invalid SERVICE_DATE_COMPLETION_REMINDER_DELAY: %s must be positive and use whole seconds",
			completionDelay,
		)
	}
	completionMessageTemplate, err := parseServiceDateReminderTemplate(
		"service_date_completion_reminder",
		settings.ServiceDateCompletionReminderTemplate,
		configservice.DefaultServiceDateCompletionReminderTemplate,
	)
	if err != nil {
		return ServiceDateReminderConfig{}, fmt.Errorf("invalid SERVICE_DATE_COMPLETION_REMINDER_TEMPLATE: %w", err)
	}

	baseURL := strings.TrimRight(strings.TrimSpace(settings.MessageGatewayURL), "/")
	if baseURL != "" {
		if err := validateServiceDateReminderGatewayURL(baseURL); err != nil {
			return ServiceDateReminderConfig{}, fmt.Errorf("invalid MESSAGE_GATEWAY_URL: %w", err)
		}
	}

	account := strings.TrimSpace(settings.MessageGatewayAccount)
	if account == "" {
		account = defaultServiceDateReminderAccount
	}
	if err := validateServiceDateReminderGatewayAccount(account); err != nil {
		return ServiceDateReminderConfig{}, fmt.Errorf("invalid MESSAGE_GATEWAY_ACCOUNT: %w", err)
	}

	accountToken := strings.TrimSpace(settings.MessageGatewayAccountToken)
	if settings.ServiceDateReminderEnabled && baseURL == "" {
		return ServiceDateReminderConfig{}, fmt.Errorf("MESSAGE_GATEWAY_URL is required when SERVICE_DATE_REMINDER_ENABLED is true")
	}
	if settings.ServiceDateReminderEnabled && accountToken == "" {
		return ServiceDateReminderConfig{}, fmt.Errorf("MESSAGE_GATEWAY_ACCOUNT_TOKEN is required when SERVICE_DATE_REMINDER_ENABLED is true")
	}

	timeout := settings.MessageGatewayTimeout
	if timeout <= 0 {
		timeout = defaultServiceDateReminderTimeout
	}

	return ServiceDateReminderConfig{
		Enabled:                   settings.ServiceDateReminderEnabled,
		LeadTimes:                 leadTimes,
		MessageTemplate:           messageTemplate,
		CompletionDelay:           completionDelay,
		CompletionMessageTemplate: completionMessageTemplate,
		Gateway: ServiceDateReminderGatewayConfig{
			BaseURL:      baseURL,
			Account:      account,
			AccountToken: accountToken,
			Timeout:      timeout,
		},
	}, nil
}

func validateServiceDateReminderLeadTimes(leadTimes []time.Duration) error {
	if len(leadTimes) == 0 {
		return fmt.Errorf("at least one lead time is required")
	}
	seen := make(map[time.Duration]struct{}, len(leadTimes))
	for _, leadTime := range leadTimes {
		if leadTime <= 0 || leadTime%time.Second != 0 {
			return fmt.Errorf("%s must be positive and use whole seconds", leadTime)
		}
		if _, ok := seen[leadTime]; ok {
			return fmt.Errorf("duplicate lead time %s", leadTime)
		}
		seen[leadTime] = struct{}{}
	}
	return nil
}

func parseServiceDateReminderTemplate(name, raw, defaultTemplate string) (*template.Template, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = defaultTemplate
	}

	tmpl, err := template.New(name).
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
	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, sample); err != nil {
		return nil, fmt.Errorf("template references an unsupported value: %w", err)
	}
	if strings.TrimSpace(rendered.String()) == "" {
		return nil, fmt.Errorf("template must render non-empty text")
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
