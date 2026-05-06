package archive

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type transcriptCSVRecord struct {
	Result DownloadResult
	Data   map[string]any
}

func WriteCSVExports(outputDir string, results []DownloadResult) ([]string, error) {
	csvDir := filepath.Join(outputDir, "csv")
	if err := os.MkdirAll(csvDir, 0o755); err != nil {
		return nil, err
	}

	records, err := loadTranscriptCSVRecords(results)
	if err != nil {
		return nil, err
	}

	writers := []struct {
		name   string
		header []string
		write  func(*csv.Writer, []transcriptCSVRecord) error
	}{
		{"transcripts.csv", transcriptsCSVHeader(), writeTranscriptsCSV},
		{"sentences.csv", sentencesCSVHeader(), writeSentencesCSV},
		{"summaries.csv", summariesCSVHeader(), writeSummariesCSV},
		{"speakers.csv", speakersCSVHeader(), writeSpeakersCSV},
		{"meeting_attendees.csv", meetingAttendeesCSVHeader(), writeMeetingAttendeesCSV},
		{"meeting_attendance.csv", meetingAttendanceCSVHeader(), writeMeetingAttendanceCSV},
		{"analytics_speakers.csv", analyticsSpeakersCSVHeader(), writeAnalyticsSpeakersCSV},
		{"analytics_overview.csv", analyticsOverviewCSVHeader(), writeAnalyticsOverviewCSV},
		{"app_outputs.csv", appOutputsCSVHeader(), writeAppOutputsCSV},
		{"channels.csv", channelsCSVHeader(), writeChannelsCSV},
		{"shared_with.csv", sharedWithCSVHeader(), writeSharedWithCSV},
		{"downloaded_media.csv", downloadedMediaCSVHeader(), writeDownloadedMediaCSV},
	}

	paths := make([]string, 0, len(writers))
	for _, item := range writers {
		path := filepath.Join(csvDir, item.name)
		if err := writeCSVFile(path, item.header, func(writer *csv.Writer) error {
			return item.write(writer, records)
		}); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}

	return paths, nil
}

func loadTranscriptCSVRecords(results []DownloadResult) ([]transcriptCSVRecord, error) {
	records := make([]transcriptCSVRecord, 0, len(results))
	for _, result := range results {
		if result.Error != "" || result.File == "" {
			continue
		}

		raw, err := os.ReadFile(result.File)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", result.File, err)
		}

		var data map[string]any
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, fmt.Errorf("parse %s: %w", result.File, err)
		}

		records = append(records, transcriptCSVRecord{Result: result, Data: data})
	}
	return records, nil
}

func writeCSVFile(path string, header []string, writeRows func(*csv.Writer) error) error {
	tmp := path + ".tmp"
	file, err := os.Create(tmp)
	if err != nil {
		return err
	}

	writer := csv.NewWriter(file)
	if err := writer.Write(header); err != nil {
		_ = file.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := writeRows(writer); err != nil {
		_ = file.Close()
		_ = os.Remove(tmp)
		return err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		_ = file.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func transcriptsCSVHeader() []string {
	return []string{
		"id", "title", "date", "date_string", "duration", "privacy",
		"host_email", "organizer_email", "calendar_id", "cal_id", "calendar_type",
		"meeting_link", "transcript_url", "audio_url", "video_url", "is_live",
		"participants", "fireflies_users", "workspace_users", "channel_ids", "channel_titles",
		"user_id", "user_email", "user_name",
		"summary_keywords", "summary_action_items", "summary_outline", "summary_shorthand_bullet",
		"summary_overview", "summary_bullet_gist", "summary_gist", "summary_short_summary",
		"summary_short_overview", "summary_meeting_type", "summary_topics_discussed",
		"summary_transcript_chapters", "summary_notes", "summary_extended_sections_json",
		"raw_json_file",
	}
}

func writeTranscriptsCSV(writer *csv.Writer, records []transcriptCSVRecord) error {
	for _, record := range records {
		data := record.Data
		summary := objectValue(data["summary"])
		user := objectValue(data["user"])
		channels := arrayValue(data["channels"])

		if err := writer.Write([]string{
			valueString(data["id"]),
			valueString(data["title"]),
			valueString(data["date"]),
			valueString(data["dateString"]),
			valueString(data["duration"]),
			valueString(data["privacy"]),
			valueString(data["host_email"]),
			valueString(data["organizer_email"]),
			valueString(data["calendar_id"]),
			valueString(data["cal_id"]),
			valueString(data["calendar_type"]),
			valueString(data["meeting_link"]),
			valueString(data["transcript_url"]),
			valueString(data["audio_url"]),
			valueString(data["video_url"]),
			valueString(data["is_live"]),
			joinValues(data["participants"]),
			joinValues(data["fireflies_users"]),
			joinValues(data["workspace_users"]),
			joinObjectField(channels, "id"),
			joinObjectField(channels, "title"),
			valueString(user["user_id"]),
			valueString(user["email"]),
			valueString(user["name"]),
			joinValues(summary["keywords"]),
			valueString(summary["action_items"]),
			valueString(summary["outline"]),
			valueString(summary["shorthand_bullet"]),
			valueString(summary["overview"]),
			valueString(summary["bullet_gist"]),
			valueString(summary["gist"]),
			valueString(summary["short_summary"]),
			valueString(summary["short_overview"]),
			valueString(summary["meeting_type"]),
			joinValues(summary["topics_discussed"]),
			joinValues(summary["transcript_chapters"]),
			valueString(summary["notes"]),
			jsonValue(summary["extended_sections"]),
			record.Result.File,
		}); err != nil {
			return err
		}
	}
	return nil
}

func summariesCSVHeader() []string {
	return []string{
		"transcript_id", "title", "date_string", "keywords", "action_items", "outline",
		"shorthand_bullet", "overview", "bullet_gist", "gist", "short_summary",
		"short_overview", "meeting_type", "topics_discussed", "transcript_chapters",
		"notes", "extended_sections_json",
	}
}

func writeSummariesCSV(writer *csv.Writer, records []transcriptCSVRecord) error {
	for _, record := range records {
		summary := objectValue(record.Data["summary"])
		if err := writer.Write([]string{
			transcriptID(record),
			transcriptTitle(record),
			transcriptDateString(record),
			joinValues(summary["keywords"]),
			valueString(summary["action_items"]),
			valueString(summary["outline"]),
			valueString(summary["shorthand_bullet"]),
			valueString(summary["overview"]),
			valueString(summary["bullet_gist"]),
			valueString(summary["gist"]),
			valueString(summary["short_summary"]),
			valueString(summary["short_overview"]),
			valueString(summary["meeting_type"]),
			joinValues(summary["topics_discussed"]),
			joinValues(summary["transcript_chapters"]),
			valueString(summary["notes"]),
			jsonValue(summary["extended_sections"]),
		}); err != nil {
			return err
		}
	}
	return nil
}

func sentencesCSVHeader() []string {
	return []string{
		"transcript_id", "title", "date_string", "index", "speaker_id", "speaker_name",
		"start_time", "end_time", "text", "raw_text", "ai_filter_task", "ai_filter_pricing",
		"ai_filter_metric", "ai_filter_question", "ai_filter_date_and_time",
		"ai_filter_text_cleanup", "ai_filter_sentiment",
	}
}

func writeSentencesCSV(writer *csv.Writer, records []transcriptCSVRecord) error {
	for _, record := range records {
		for _, item := range arrayValue(record.Data["sentences"]) {
			sentence := objectValue(item)
			filters := objectValue(sentence["ai_filters"])
			if err := writer.Write([]string{
				transcriptID(record),
				transcriptTitle(record),
				transcriptDateString(record),
				valueString(sentence["index"]),
				valueString(sentence["speaker_id"]),
				valueString(sentence["speaker_name"]),
				valueString(sentence["start_time"]),
				valueString(sentence["end_time"]),
				valueString(sentence["text"]),
				valueString(sentence["raw_text"]),
				valueString(filters["task"]),
				valueString(filters["pricing"]),
				valueString(filters["metric"]),
				valueString(filters["question"]),
				valueString(filters["date_and_time"]),
				valueString(filters["text_cleanup"]),
				valueString(filters["sentiment"]),
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func speakersCSVHeader() []string {
	return []string{"transcript_id", "title", "date_string", "speaker_id", "name"}
}

func writeSpeakersCSV(writer *csv.Writer, records []transcriptCSVRecord) error {
	for _, record := range records {
		for _, item := range arrayValue(record.Data["speakers"]) {
			speaker := objectValue(item)
			if err := writer.Write([]string{
				transcriptID(record),
				transcriptTitle(record),
				transcriptDateString(record),
				valueString(speaker["id"]),
				valueString(speaker["name"]),
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func meetingAttendeesCSVHeader() []string {
	return []string{"transcript_id", "title", "date_string", "display_name", "email", "phone_number", "name", "location"}
}

func writeMeetingAttendeesCSV(writer *csv.Writer, records []transcriptCSVRecord) error {
	for _, record := range records {
		for _, item := range arrayValue(record.Data["meeting_attendees"]) {
			attendee := objectValue(item)
			if err := writer.Write([]string{
				transcriptID(record),
				transcriptTitle(record),
				transcriptDateString(record),
				valueString(attendee["displayName"]),
				valueString(attendee["email"]),
				valueString(attendee["phoneNumber"]),
				valueString(attendee["name"]),
				valueString(attendee["location"]),
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func meetingAttendanceCSVHeader() []string {
	return []string{"transcript_id", "title", "date_string", "name", "join_time", "leave_time"}
}

func writeMeetingAttendanceCSV(writer *csv.Writer, records []transcriptCSVRecord) error {
	for _, record := range records {
		for _, item := range arrayValue(record.Data["meeting_attendance"]) {
			attendance := objectValue(item)
			if err := writer.Write([]string{
				transcriptID(record),
				transcriptTitle(record),
				transcriptDateString(record),
				valueString(attendance["name"]),
				valueString(attendance["join_time"]),
				valueString(attendance["leave_time"]),
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func analyticsSpeakersCSVHeader() []string {
	return []string{
		"transcript_id", "title", "date_string", "speaker_id", "name", "duration",
		"word_count", "longest_monologue", "monologues_count", "filler_words",
		"questions", "duration_pct", "words_per_minute",
	}
}

func writeAnalyticsSpeakersCSV(writer *csv.Writer, records []transcriptCSVRecord) error {
	for _, record := range records {
		analytics := objectValue(record.Data["analytics"])
		for _, item := range arrayValue(analytics["speakers"]) {
			speaker := objectValue(item)
			if err := writer.Write([]string{
				transcriptID(record),
				transcriptTitle(record),
				transcriptDateString(record),
				valueString(speaker["speaker_id"]),
				valueString(speaker["name"]),
				valueString(speaker["duration"]),
				valueString(speaker["word_count"]),
				valueString(speaker["longest_monologue"]),
				valueString(speaker["monologues_count"]),
				valueString(speaker["filler_words"]),
				valueString(speaker["questions"]),
				valueString(speaker["duration_pct"]),
				valueString(speaker["words_per_minute"]),
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func analyticsOverviewCSVHeader() []string {
	return []string{
		"transcript_id", "title", "date_string", "negative_pct", "neutral_pct",
		"positive_pct", "category_questions", "category_date_times", "category_metrics",
		"category_tasks",
	}
}

func writeAnalyticsOverviewCSV(writer *csv.Writer, records []transcriptCSVRecord) error {
	for _, record := range records {
		analytics := objectValue(record.Data["analytics"])
		sentiments := objectValue(analytics["sentiments"])
		categories := objectValue(analytics["categories"])
		if err := writer.Write([]string{
			transcriptID(record),
			transcriptTitle(record),
			transcriptDateString(record),
			valueString(sentiments["negative_pct"]),
			valueString(sentiments["neutral_pct"]),
			valueString(sentiments["positive_pct"]),
			valueString(categories["questions"]),
			valueString(categories["date_times"]),
			valueString(categories["metrics"]),
			valueString(categories["tasks"]),
		}); err != nil {
			return err
		}
	}
	return nil
}

func appOutputsCSVHeader() []string {
	return []string{
		"transcript_id", "title", "date_string", "output_transcript_id", "user_id",
		"app_id", "created_at", "output_title", "prompt", "response",
	}
}

func writeAppOutputsCSV(writer *csv.Writer, records []transcriptCSVRecord) error {
	for _, record := range records {
		apps := objectValue(record.Data["apps_preview"])
		for _, item := range arrayValue(apps["outputs"]) {
			output := objectValue(item)
			if err := writer.Write([]string{
				transcriptID(record),
				transcriptTitle(record),
				transcriptDateString(record),
				valueString(output["transcript_id"]),
				valueString(output["user_id"]),
				valueString(output["app_id"]),
				valueString(output["created_at"]),
				valueString(output["title"]),
				valueString(output["prompt"]),
				valueString(output["response"]),
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func channelsCSVHeader() []string {
	return []string{
		"transcript_id", "title", "date_string", "channel_id", "channel_title",
		"is_private", "created_by", "created_at", "updated_at", "members_json",
	}
}

func writeChannelsCSV(writer *csv.Writer, records []transcriptCSVRecord) error {
	for _, record := range records {
		for _, item := range arrayValue(record.Data["channels"]) {
			channel := objectValue(item)
			if err := writer.Write([]string{
				transcriptID(record),
				transcriptTitle(record),
				transcriptDateString(record),
				valueString(channel["id"]),
				valueString(channel["title"]),
				valueString(channel["is_private"]),
				valueString(channel["created_by"]),
				valueString(channel["created_at"]),
				valueString(channel["updated_at"]),
				jsonValue(channel["members"]),
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func sharedWithCSVHeader() []string {
	return []string{"transcript_id", "title", "date_string", "email", "name", "photo_url", "expires_at"}
}

func writeSharedWithCSV(writer *csv.Writer, records []transcriptCSVRecord) error {
	for _, record := range records {
		for _, item := range arrayValue(record.Data["shared_with"]) {
			shared := objectValue(item)
			if err := writer.Write([]string{
				transcriptID(record),
				transcriptTitle(record),
				transcriptDateString(record),
				valueString(shared["email"]),
				valueString(shared["name"]),
				valueString(shared["photo_url"]),
				valueString(shared["expires_at"]),
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func downloadedMediaCSVHeader() []string {
	return []string{"transcript_id", "title", "date_string", "kind", "path", "error"}
}

func writeDownloadedMediaCSV(writer *csv.Writer, records []transcriptCSVRecord) error {
	for _, record := range records {
		kinds := map[string]struct{}{}
		for kind := range record.Result.MediaFiles {
			kinds[kind] = struct{}{}
		}
		for kind := range record.Result.MediaErrors {
			kinds[kind] = struct{}{}
		}
		for _, kind := range sortedKeys(kinds) {
			if err := writer.Write([]string{
				transcriptID(record),
				transcriptTitle(record),
				transcriptDateString(record),
				kind,
				record.Result.MediaFiles[kind],
				record.Result.MediaErrors[kind],
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func transcriptID(record transcriptCSVRecord) string {
	if id := valueString(record.Data["id"]); id != "" {
		return id
	}
	return record.Result.ID
}

func transcriptTitle(record transcriptCSVRecord) string {
	if title := valueString(record.Data["title"]); title != "" {
		return title
	}
	return record.Result.Title
}

func transcriptDateString(record transcriptCSVRecord) string {
	if dateString := valueString(record.Data["dateString"]); dateString != "" {
		return dateString
	}
	return record.Result.DateString
}

func objectValue(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	if out, ok := value.(map[string]any); ok {
		return out
	}
	return map[string]any{}
}

func arrayValue(value any) []any {
	if value == nil {
		return nil
	}
	if out, ok := value.([]any); ok {
		return out
	}
	return nil
}

func valueString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case json.Number:
		return typed.String()
	default:
		return jsonValue(value)
	}
}

func joinValues(value any) string {
	items := arrayValue(value)
	if len(items) == 0 {
		return valueString(value)
	}

	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, valueString(item))
	}
	return strings.Join(parts, "; ")
}

func joinObjectField(items []any, field string) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		value := valueString(objectValue(item)[field])
		if value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, "; ")
}

func jsonValue(value any) string {
	if value == nil {
		return ""
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	if string(raw) == "null" {
		return ""
	}
	return string(raw)
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}
