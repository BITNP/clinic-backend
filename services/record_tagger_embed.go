package services

import _ "embed"

// recordTagCSV is the built-in record tag catalog. It is embedded at build
// time so the catalog can be seeded without reading files at runtime.
//
//go:embed data/record-tags.csv
var recordTagCSV []byte

// recordTaggerPrompt is the prompt registered for the record tag set. It is
// embedded at build time, mirroring the liquid-metal prompt handling.
//
//go:embed data/record_tagger_prompt.txt
var recordTaggerPrompt string
