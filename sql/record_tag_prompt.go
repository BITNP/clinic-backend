package sql

import _ "embed"

// RecordTagPrompt seeds the singleton general prompt used when registering the
// clinic record tag set with the tagger. It is idempotent via ON CONFLICT.
//
//go:embed record_tag_prompt.sql
var RecordTagPrompt string
