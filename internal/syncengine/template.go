// Package syncengine drives the copy loop from detected cameras into the
// active profile's destination. Filesystem existence is the source of truth
// for "already synced"; the DB only records history + upload queue.
package syncengine

import (
	"path/filepath"
	"strings"
	"time"
)

// RenderTemplate expands folder templates like "{camera}/{yyyy}/{mm}-{dd}".
// Supported placeholders (ported from Python config.get_destination_path):
//
//	{camera}  camera model name
//	{type}    Photo | Video
//	{yyyy}    2025 · {yy} 25
//	{mm} 01-12 · {m} 1-12
//	{dd} 01-31 · {d} 1-31
//	{date}    2025-12-25
//	{year} {month} {day}  (legacy aliases)
func RenderTemplate(tmpl, camera, fileType string, date time.Time) string {
	if strings.TrimSpace(tmpl) == "" {
		tmpl = "{camera}/{yyyy}/{yyyy}-{mm}-{dd}"
	}
	year := date.Format("2006")
	typeTitle := fileType
	if typeTitle != "" {
		typeTitle = strings.ToUpper(typeTitle[:1]) + typeTitle[1:]
	}
	repl := map[string]string{
		"{camera}": camera,
		"{type}":   typeTitle,
		"{yyyy}":   year,
		"{yy}":     year[2:],
		"{mm}":     date.Format("01"),
		"{m}":      strings.TrimLeft(date.Format("01"), "0"),
		"{dd}":     date.Format("02"),
		"{d}":      strings.TrimLeft(date.Format("02"), "0"),
		"{date}":   date.Format("2006-01-02"),
		"{year}":   year,
		"{month}":  date.Format("01"),
		"{day}":    date.Format("02"),
	}
	out := tmpl
	for k, v := range repl {
		out = strings.ReplaceAll(out, k, v)
	}
	// "{m}"/"{d}" on the 1st of January: TrimLeft leaves "" when value is "0X"
	// only if the digit is 0; month "10" trims to "10" correctly. But a month
	// like "10" has no leading zero so TrimLeft("10","0") == "10" — fine.
	return filepath.FromSlash(out)
}
