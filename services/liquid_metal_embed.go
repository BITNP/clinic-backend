package services

import _ "embed"

// liquidMetalCSV is the liquid-metal laptop knowledge table. It is embedded at
// build time so the tagger prompt can be built without reading files at
// runtime.
//
//go:embed data/liquid-metal-laptops.csv
var liquidMetalCSV []byte
