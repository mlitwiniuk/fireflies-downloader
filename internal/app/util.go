package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mlitwiniuk/fireflies-downloader/internal/fireflies"
)

func ParseDateTimeFlag(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}

	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02",
	}

	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return &parsed, nil
		}
	}
	return nil, fmt.Errorf("invalid date %q; use YYYY-MM-DD or RFC3339", value)
}

func formatFirefliesDate(value time.Time) string {
	return fireflies.FormatDateTime(value)
}

var retentionPattern = regexp.MustCompile(`^([0-9]+)\s*([a-zA-Z]+)$`)

func cutoffFromRetention(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return time.Time{}, fmt.Errorf("older-than cannot be empty")
	}

	matches := retentionPattern.FindStringSubmatch(value)
	if len(matches) == 3 {
		amount, _ := strconv.Atoi(matches[1])
		unit := matches[2]
		switch unit {
		case "m", "mo", "mon", "month", "months":
			return now.AddDate(0, -amount, 0), nil
		case "d", "day", "days":
			return now.AddDate(0, 0, -amount), nil
		case "w", "week", "weeks":
			return now.AddDate(0, 0, -7*amount), nil
		case "y", "yr", "year", "years":
			return now.AddDate(-amount, 0, 0), nil
		}
	}

	duration, err := time.ParseDuration(value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid older-than %q; use values like 3m, 90d, 12w, 1y, or 720h", value)
	}
	return now.Add(-duration), nil
}

func SplitCSV(value string) []string {
	if value == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomic(path, data)
}

func writeRawJSONFile(path string, raw json.RawMessage) error {
	var buffer bytes.Buffer
	if err := json.Indent(&buffer, raw, "", "  "); err != nil {
		return err
	}
	buffer.WriteByte('\n')
	return writeFileAtomic(path, buffer.Bytes())
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func safeFileName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}

	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "\x00", "_")
	value = replacer.Replace(value)
	var builder strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('_')
		}
	}
	out := strings.Trim(builder.String(), "._")
	if out == "" {
		return "unknown"
	}
	return out
}

func extensionFromContentType(contentType string) string {
	if contentType == "" {
		return ""
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.Split(contentType, ";")[0]
	}
	switch strings.ToLower(mediaType) {
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/mp4", "audio/x-m4a":
		return ".m4a"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/ogg":
		return ".ogg"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	}

	extensions, err := mime.ExtensionsByType(mediaType)
	if err != nil || len(extensions) == 0 {
		return ""
	}
	return extensions[0]
}

func extensionFromURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	ext := filepath.Ext(parsed.Path)
	if len(ext) > 12 {
		return ""
	}
	return ext
}

func filterManifest(filter fireflies.ListFilter) map[string]any {
	out := map[string]any{
		"page_size": filter.PageSize,
	}
	if filter.Max > 0 {
		out["max"] = filter.Max
	}
	if filter.FromDate != nil {
		out["from_date"] = formatFirefliesDate(*filter.FromDate)
	}
	if filter.ToDate != nil {
		out["to_date"] = formatFirefliesDate(*filter.ToDate)
	}
	if filter.UserID != "" {
		out["user_id"] = filter.UserID
	}
	if filter.Mine != nil {
		out["mine"] = *filter.Mine
	}
	if len(filter.Organizers) > 0 {
		out["organizers"] = filter.Organizers
	}
	if len(filter.Participants) > 0 {
		out["participants"] = filter.Participants
	}
	if filter.ChannelID != "" {
		out["channel_id"] = filter.ChannelID
	}
	if filter.Keyword != "" {
		out["keyword"] = filter.Keyword
	}
	if filter.Scope != "" {
		out["scope"] = filter.Scope
	}
	return out
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
