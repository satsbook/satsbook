package web

import "embed"

// templateFS holds all HTML templates, embedded at compile time.
//
//go:embed templates
var templateFS embed.FS

// staticFS holds static assets (JS, CSS), embedded at compile time.
// HTMX version: 2.0.4 (vendored, MIT license)
//
//go:embed static
var staticFS embed.FS
