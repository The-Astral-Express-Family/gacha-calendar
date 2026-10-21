// Package assets embeds the frontend build output and the fallback visuals.
// The real dist content is produced by `pnpm build` in frontend/ and copied
// here by the build script; a committed placeholder keeps `go build` working.
package assets

import "embed"

//go:embed all:dist
var Dist embed.FS

//go:embed fallback
var Fallback embed.FS

// DistRoot is the dist subtree ("dist").
const DistRoot = "dist"

// FallbackRoot is the fallback subtree ("fallback").
const FallbackRoot = "fallback"
