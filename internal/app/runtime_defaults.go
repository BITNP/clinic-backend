package app

func legacyRuntimeSettingValues(getenv func(string) string) map[string]string {
	names := []string{
		"SYNC_POLL_INTERVAL",
		"STAFF_VERSION",
		"RECORD_ACTION_WINDOW",
		"RECORD_PHOTO_MAX_COUNT",
		"RECORD_PHOTO_MAX_SIZE_BYTES",
		"SERVICE_DATE_REMINDER_ENABLED",
		"SERVICE_DATE_REMINDER_LEAD_TIMES",
		"MESSAGE_GATEWAY_ACCOUNT",
		"TAGGER_LLM_API_KEY",
		"TAGGER_LLM_BASE_URL",
		"TAGGER_LLM_MODEL",
		"TAGGER_AGENT_POOL_SIZE",
		"TAGGER_CONTEXT_TOKENS",
		"TAGGER_TIMEOUT",
	}
	values := make(map[string]string, len(names))
	for _, name := range names {
		values[name] = getenv(name)
	}
	return values
}
