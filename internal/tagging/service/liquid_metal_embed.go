package service

import (
	_ "embed"
)

// liquidMetalCSV is the liquid-metal laptop knowledge table. It is embedded at
// build time so the tagger prompt can be built without reading files at
// runtime.
//
//go:embed data/liquid-metal-laptops.csv
var liquidMetalCSV []byte

// liquidMetalPromptTemplate is the tagger prompt with a {{TABLE}} placeholder
// where the rendered liquid-metal laptop rows are inserted.
//
//go:embed data/liquid_metal_prompt.txt
var liquidMetalPromptTemplate string

// liquidMetalTagCSV is the liquid-metal tag catalog. It is embedded at build
// time and parsed when the tagger is built at startup.
//
//go:embed data/liquid-metal-tags.csv
var liquidMetalTagCSV []byte
