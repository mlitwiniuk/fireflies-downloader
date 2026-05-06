package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

func WriteSQLiteExports(dbPath string, manifest DownloadManifest) error {
	if dbPath == "" {
		return fmt.Errorf("sqlite path cannot be empty")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return err
	}

	records, err := loadTranscriptCSVRecords(manifest.Items)
	if err != nil {
		return err
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode = WAL"); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		return err
	}
	if err := ensureSQLiteSchema(ctx, db); err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	runID, err := insertExportRun(ctx, tx, manifest)
	if err != nil {
		return err
	}
	for _, record := range records {
		if err := insertTranscriptRecord(ctx, tx, runID, manifest.ExportedAt, record); err != nil {
			return err
		}
	}

	if err := rebuildSearchIndex(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func ensureSQLiteSchema(ctx context.Context, db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS export_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			exported_at TEXT NOT NULL,
			endpoint TEXT,
			profile TEXT,
			strict_profile INTEGER NOT NULL DEFAULT 0,
			include_media INTEGER NOT NULL DEFAULT 0,
			filters_json TEXT,
			transcript_count INTEGER NOT NULL DEFAULT 0,
			succeeded_count INTEGER NOT NULL DEFAULT 0,
			skipped_count INTEGER NOT NULL DEFAULT 0,
			failed_count INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS transcripts (
			id TEXT PRIMARY KEY,
			run_id INTEGER,
			title TEXT,
			date_ms REAL,
			date_string TEXT,
			duration_minutes REAL,
			privacy TEXT,
			host_email TEXT,
			organizer_email TEXT,
			calendar_id TEXT,
			cal_id TEXT,
			calendar_type TEXT,
			meeting_link TEXT,
			transcript_url TEXT,
			audio_url TEXT,
			video_url TEXT,
			is_live INTEGER,
			participants_json TEXT,
			fireflies_users_json TEXT,
			workspace_users_json TEXT,
			user_id TEXT,
			user_email TEXT,
			user_name TEXT,
			raw_json_file TEXT,
			raw_json TEXT NOT NULL,
			profile TEXT,
			last_exported_at TEXT NOT NULL,
			FOREIGN KEY(run_id) REFERENCES export_runs(id)
		)`,
		`CREATE TABLE IF NOT EXISTS summaries (
			transcript_id TEXT PRIMARY KEY,
			keywords TEXT,
			action_items TEXT,
			outline TEXT,
			shorthand_bullet TEXT,
			overview TEXT,
			bullet_gist TEXT,
			gist TEXT,
			short_summary TEXT,
			short_overview TEXT,
			meeting_type TEXT,
			topics_discussed_json TEXT,
			transcript_chapters_json TEXT,
			notes TEXT,
			extended_sections_json TEXT,
			FOREIGN KEY(transcript_id) REFERENCES transcripts(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS sentences (
			transcript_id TEXT NOT NULL,
			sentence_index INTEGER NOT NULL,
			speaker_id TEXT,
			speaker_name TEXT,
			start_time TEXT,
			end_time TEXT,
			text TEXT,
			raw_text TEXT,
			ai_filter_task TEXT,
			ai_filter_pricing TEXT,
			ai_filter_metric TEXT,
			ai_filter_question TEXT,
			ai_filter_date_and_time TEXT,
			ai_filter_text_cleanup TEXT,
			ai_filter_sentiment TEXT,
			PRIMARY KEY(transcript_id, sentence_index),
			FOREIGN KEY(transcript_id) REFERENCES transcripts(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS speakers (
			transcript_id TEXT NOT NULL,
			speaker_id TEXT NOT NULL,
			name TEXT,
			PRIMARY KEY(transcript_id, speaker_id),
			FOREIGN KEY(transcript_id) REFERENCES transcripts(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS meeting_attendees (
			transcript_id TEXT NOT NULL,
			row_index INTEGER NOT NULL,
			display_name TEXT,
			email TEXT,
			phone_number TEXT,
			name TEXT,
			location TEXT,
			PRIMARY KEY(transcript_id, row_index),
			FOREIGN KEY(transcript_id) REFERENCES transcripts(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS meeting_attendance (
			transcript_id TEXT NOT NULL,
			row_index INTEGER NOT NULL,
			name TEXT,
			join_time TEXT,
			leave_time TEXT,
			PRIMARY KEY(transcript_id, row_index),
			FOREIGN KEY(transcript_id) REFERENCES transcripts(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS analytics_overview (
			transcript_id TEXT PRIMARY KEY,
			negative_pct REAL,
			neutral_pct REAL,
			positive_pct REAL,
			category_questions INTEGER,
			category_date_times INTEGER,
			category_metrics INTEGER,
			category_tasks INTEGER,
			FOREIGN KEY(transcript_id) REFERENCES transcripts(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS analytics_speakers (
			transcript_id TEXT NOT NULL,
			row_index INTEGER NOT NULL,
			speaker_id TEXT,
			name TEXT,
			duration REAL,
			word_count INTEGER,
			longest_monologue REAL,
			monologues_count INTEGER,
			filler_words INTEGER,
			questions INTEGER,
			duration_pct REAL,
			words_per_minute REAL,
			PRIMARY KEY(transcript_id, row_index),
			FOREIGN KEY(transcript_id) REFERENCES transcripts(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS app_outputs (
			transcript_id TEXT NOT NULL,
			row_index INTEGER NOT NULL,
			output_transcript_id TEXT,
			user_id TEXT,
			app_id TEXT,
			created_at REAL,
			title TEXT,
			prompt TEXT,
			response TEXT,
			PRIMARY KEY(transcript_id, row_index),
			FOREIGN KEY(transcript_id) REFERENCES transcripts(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS channels (
			transcript_id TEXT NOT NULL,
			channel_id TEXT NOT NULL,
			title TEXT,
			is_private INTEGER,
			created_by TEXT,
			created_at TEXT,
			updated_at TEXT,
			members_json TEXT,
			PRIMARY KEY(transcript_id, channel_id),
			FOREIGN KEY(transcript_id) REFERENCES transcripts(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS shared_with (
			transcript_id TEXT NOT NULL,
			email TEXT NOT NULL,
			name TEXT,
			photo_url TEXT,
			expires_at TEXT,
			PRIMARY KEY(transcript_id, email),
			FOREIGN KEY(transcript_id) REFERENCES transcripts(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS downloaded_media (
			transcript_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			path TEXT,
			error TEXT,
			PRIMARY KEY(transcript_id, kind),
			FOREIGN KEY(transcript_id) REFERENCES transcripts(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS transcript_search_docs (
			transcript_id TEXT PRIMARY KEY,
			title TEXT,
			summary TEXT,
			transcript_text TEXT,
			participants TEXT,
			FOREIGN KEY(transcript_id) REFERENCES transcripts(id) ON DELETE CASCADE
		)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS transcript_search_fts USING fts5(
			transcript_id UNINDEXED,
			title,
			summary,
			transcript_text,
			participants,
			content='transcript_search_docs',
			content_rowid='rowid'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_transcripts_date_ms ON transcripts(date_ms)`,
		`CREATE INDEX IF NOT EXISTS idx_transcripts_date_string ON transcripts(date_string)`,
		`CREATE INDEX IF NOT EXISTS idx_transcripts_organizer_email ON transcripts(organizer_email)`,
		`CREATE INDEX IF NOT EXISTS idx_transcripts_host_email ON transcripts(host_email)`,
		`CREATE INDEX IF NOT EXISTS idx_sentences_transcript_id ON sentences(transcript_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sentences_speaker_name ON sentences(speaker_name)`,
		`CREATE INDEX IF NOT EXISTS idx_summaries_meeting_type ON summaries(meeting_type)`,
		`CREATE INDEX IF NOT EXISTS idx_downloaded_media_transcript_id ON downloaded_media(transcript_id)`,
	}

	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func insertExportRun(ctx context.Context, tx *sql.Tx, manifest DownloadManifest) (int64, error) {
	filters, err := json.Marshal(manifest.Filters)
	if err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO export_runs (
		exported_at, endpoint, profile, strict_profile, include_media, filters_json,
		transcript_count, succeeded_count, skipped_count, failed_count
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		manifest.ExportedAt,
		manifest.Endpoint,
		manifest.Profile,
		boolInt(manifest.StrictProfile),
		boolInt(manifest.IncludeMedia),
		string(filters),
		manifest.Count,
		manifest.Succeeded,
		manifest.Skipped,
		manifest.Failed,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func insertTranscriptRecord(ctx context.Context, tx *sql.Tx, runID int64, exportedAt string, record transcriptCSVRecord) error {
	id := transcriptID(record)
	if id == "" {
		return nil
	}

	if err := deleteTranscriptChildRows(ctx, tx, id); err != nil {
		return err
	}

	raw, err := os.ReadFile(record.Result.File)
	if err != nil {
		return err
	}

	data := record.Data
	user := objectValue(data["user"])
	if _, err := tx.ExecContext(ctx, `INSERT INTO transcripts (
		id, run_id, title, date_ms, date_string, duration_minutes, privacy,
		host_email, organizer_email, calendar_id, cal_id, calendar_type,
		meeting_link, transcript_url, audio_url, video_url, is_live,
		participants_json, fireflies_users_json, workspace_users_json,
		user_id, user_email, user_name, raw_json_file, raw_json, profile, last_exported_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		run_id=excluded.run_id,
		title=excluded.title,
		date_ms=excluded.date_ms,
		date_string=excluded.date_string,
		duration_minutes=excluded.duration_minutes,
		privacy=excluded.privacy,
		host_email=excluded.host_email,
		organizer_email=excluded.organizer_email,
		calendar_id=excluded.calendar_id,
		cal_id=excluded.cal_id,
		calendar_type=excluded.calendar_type,
		meeting_link=excluded.meeting_link,
		transcript_url=excluded.transcript_url,
		audio_url=excluded.audio_url,
		video_url=excluded.video_url,
		is_live=excluded.is_live,
		participants_json=excluded.participants_json,
		fireflies_users_json=excluded.fireflies_users_json,
		workspace_users_json=excluded.workspace_users_json,
		user_id=excluded.user_id,
		user_email=excluded.user_email,
		user_name=excluded.user_name,
		raw_json_file=excluded.raw_json_file,
		raw_json=excluded.raw_json,
		profile=excluded.profile,
		last_exported_at=excluded.last_exported_at`,
		id,
		runID,
		valueString(data["title"]),
		numberOrNil(data["date"]),
		valueString(data["dateString"]),
		numberOrNil(data["duration"]),
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
		boolOrNil(data["is_live"]),
		jsonValue(data["participants"]),
		jsonValue(data["fireflies_users"]),
		jsonValue(data["workspace_users"]),
		valueString(user["user_id"]),
		valueString(user["email"]),
		valueString(user["name"]),
		record.Result.File,
		string(raw),
		record.Result.Profile,
		exportedAt,
	); err != nil {
		return err
	}

	if err := insertSummary(ctx, tx, record); err != nil {
		return err
	}
	if err := insertSentences(ctx, tx, record); err != nil {
		return err
	}
	if err := insertSpeakers(ctx, tx, record); err != nil {
		return err
	}
	if err := insertMeetingAttendees(ctx, tx, record); err != nil {
		return err
	}
	if err := insertMeetingAttendance(ctx, tx, record); err != nil {
		return err
	}
	if err := insertAnalytics(ctx, tx, record); err != nil {
		return err
	}
	if err := insertAppOutputs(ctx, tx, record); err != nil {
		return err
	}
	if err := insertChannels(ctx, tx, record); err != nil {
		return err
	}
	if err := insertSharedWith(ctx, tx, record); err != nil {
		return err
	}
	if err := insertDownloadedMedia(ctx, tx, record); err != nil {
		return err
	}
	return insertSearchDoc(ctx, tx, record)
}

func deleteTranscriptChildRows(ctx context.Context, tx *sql.Tx, transcriptID string) error {
	tables := []string{
		"summaries",
		"sentences",
		"speakers",
		"meeting_attendees",
		"meeting_attendance",
		"analytics_overview",
		"analytics_speakers",
		"app_outputs",
		"channels",
		"shared_with",
		"downloaded_media",
		"transcript_search_docs",
	}
	for _, table := range tables {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE transcript_id = ?", transcriptID); err != nil {
			return err
		}
	}
	return nil
}

func insertSummary(ctx context.Context, tx *sql.Tx, record transcriptCSVRecord) error {
	summary := objectValue(record.Data["summary"])
	if len(summary) == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO summaries (
		transcript_id, keywords, action_items, outline, shorthand_bullet, overview,
		bullet_gist, gist, short_summary, short_overview, meeting_type,
		topics_discussed_json, transcript_chapters_json, notes, extended_sections_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		transcriptID(record),
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
		jsonValue(summary["topics_discussed"]),
		jsonValue(summary["transcript_chapters"]),
		valueString(summary["notes"]),
		jsonValue(summary["extended_sections"]),
	)
	return err
}

func insertSentences(ctx context.Context, tx *sql.Tx, record transcriptCSVRecord) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO sentences (
		transcript_id, sentence_index, speaker_id, speaker_name, start_time, end_time,
		text, raw_text, ai_filter_task, ai_filter_pricing, ai_filter_metric,
		ai_filter_question, ai_filter_date_and_time, ai_filter_text_cleanup, ai_filter_sentiment
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for rowIndex, item := range arrayValue(record.Data["sentences"]) {
		sentence := objectValue(item)
		filters := objectValue(sentence["ai_filters"])
		index := intOrDefault(sentence["index"], rowIndex)
		if _, err := stmt.ExecContext(ctx,
			transcriptID(record),
			index,
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
		); err != nil {
			return err
		}
	}
	return nil
}

func insertSpeakers(ctx context.Context, tx *sql.Tx, record transcriptCSVRecord) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO speakers (transcript_id, speaker_id, name) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for rowIndex, item := range arrayValue(record.Data["speakers"]) {
		speaker := objectValue(item)
		id := valueString(speaker["id"])
		if id == "" {
			id = fmt.Sprintf("row_%d", rowIndex)
		}
		if _, err := stmt.ExecContext(ctx, transcriptID(record), id, valueString(speaker["name"])); err != nil {
			return err
		}
	}
	return nil
}

func insertMeetingAttendees(ctx context.Context, tx *sql.Tx, record transcriptCSVRecord) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO meeting_attendees (
		transcript_id, row_index, display_name, email, phone_number, name, location
	) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for rowIndex, item := range arrayValue(record.Data["meeting_attendees"]) {
		attendee := objectValue(item)
		if _, err := stmt.ExecContext(ctx,
			transcriptID(record),
			rowIndex,
			valueString(attendee["displayName"]),
			valueString(attendee["email"]),
			valueString(attendee["phoneNumber"]),
			valueString(attendee["name"]),
			valueString(attendee["location"]),
		); err != nil {
			return err
		}
	}
	return nil
}

func insertMeetingAttendance(ctx context.Context, tx *sql.Tx, record transcriptCSVRecord) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO meeting_attendance (
		transcript_id, row_index, name, join_time, leave_time
	) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for rowIndex, item := range arrayValue(record.Data["meeting_attendance"]) {
		attendance := objectValue(item)
		if _, err := stmt.ExecContext(ctx,
			transcriptID(record),
			rowIndex,
			valueString(attendance["name"]),
			valueString(attendance["join_time"]),
			valueString(attendance["leave_time"]),
		); err != nil {
			return err
		}
	}
	return nil
}

func insertAnalytics(ctx context.Context, tx *sql.Tx, record transcriptCSVRecord) error {
	analytics := objectValue(record.Data["analytics"])
	if len(analytics) > 0 {
		sentiments := objectValue(analytics["sentiments"])
		categories := objectValue(analytics["categories"])
		if _, err := tx.ExecContext(ctx, `INSERT INTO analytics_overview (
			transcript_id, negative_pct, neutral_pct, positive_pct,
			category_questions, category_date_times, category_metrics, category_tasks
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			transcriptID(record),
			numberOrNil(sentiments["negative_pct"]),
			numberOrNil(sentiments["neutral_pct"]),
			numberOrNil(sentiments["positive_pct"]),
			intOrNil(categories["questions"]),
			intOrNil(categories["date_times"]),
			intOrNil(categories["metrics"]),
			intOrNil(categories["tasks"]),
		); err != nil {
			return err
		}
	}

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO analytics_speakers (
		transcript_id, row_index, speaker_id, name, duration, word_count, longest_monologue,
		monologues_count, filler_words, questions, duration_pct, words_per_minute
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for rowIndex, item := range arrayValue(analytics["speakers"]) {
		speaker := objectValue(item)
		if _, err := stmt.ExecContext(ctx,
			transcriptID(record),
			rowIndex,
			valueString(speaker["speaker_id"]),
			valueString(speaker["name"]),
			numberOrNil(speaker["duration"]),
			intOrNil(speaker["word_count"]),
			numberOrNil(speaker["longest_monologue"]),
			intOrNil(speaker["monologues_count"]),
			intOrNil(speaker["filler_words"]),
			intOrNil(speaker["questions"]),
			numberOrNil(speaker["duration_pct"]),
			numberOrNil(speaker["words_per_minute"]),
		); err != nil {
			return err
		}
	}
	return nil
}

func insertAppOutputs(ctx context.Context, tx *sql.Tx, record transcriptCSVRecord) error {
	apps := objectValue(record.Data["apps_preview"])
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO app_outputs (
		transcript_id, row_index, output_transcript_id, user_id, app_id, created_at, title, prompt, response
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for rowIndex, item := range arrayValue(apps["outputs"]) {
		output := objectValue(item)
		if _, err := stmt.ExecContext(ctx,
			transcriptID(record),
			rowIndex,
			valueString(output["transcript_id"]),
			valueString(output["user_id"]),
			valueString(output["app_id"]),
			numberOrNil(output["created_at"]),
			valueString(output["title"]),
			valueString(output["prompt"]),
			valueString(output["response"]),
		); err != nil {
			return err
		}
	}
	return nil
}

func insertChannels(ctx context.Context, tx *sql.Tx, record transcriptCSVRecord) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO channels (
		transcript_id, channel_id, title, is_private, created_by, created_at, updated_at, members_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for rowIndex, item := range arrayValue(record.Data["channels"]) {
		channel := objectValue(item)
		id := valueString(channel["id"])
		if id == "" {
			id = fmt.Sprintf("row_%d", rowIndex)
		}
		if _, err := stmt.ExecContext(ctx,
			transcriptID(record),
			id,
			valueString(channel["title"]),
			boolOrNil(channel["is_private"]),
			valueString(channel["created_by"]),
			valueString(channel["created_at"]),
			valueString(channel["updated_at"]),
			jsonValue(channel["members"]),
		); err != nil {
			return err
		}
	}
	return nil
}

func insertSharedWith(ctx context.Context, tx *sql.Tx, record transcriptCSVRecord) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO shared_with (
		transcript_id, email, name, photo_url, expires_at
	) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for rowIndex, item := range arrayValue(record.Data["shared_with"]) {
		shared := objectValue(item)
		email := valueString(shared["email"])
		if email == "" {
			email = fmt.Sprintf("row_%d", rowIndex)
		}
		if _, err := stmt.ExecContext(ctx,
			transcriptID(record),
			email,
			valueString(shared["name"]),
			valueString(shared["photo_url"]),
			valueString(shared["expires_at"]),
		); err != nil {
			return err
		}
	}
	return nil
}

func insertDownloadedMedia(ctx context.Context, tx *sql.Tx, record transcriptCSVRecord) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO downloaded_media (
		transcript_id, kind, path, error
	) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	kinds := map[string]struct{}{}
	for kind := range record.Result.MediaFiles {
		kinds[kind] = struct{}{}
	}
	for kind := range record.Result.MediaErrors {
		kinds[kind] = struct{}{}
	}
	for _, kind := range sortedKeys(kinds) {
		if _, err := stmt.ExecContext(ctx,
			transcriptID(record),
			kind,
			record.Result.MediaFiles[kind],
			record.Result.MediaErrors[kind],
		); err != nil {
			return err
		}
	}
	return nil
}

func insertSearchDoc(ctx context.Context, tx *sql.Tx, record transcriptCSVRecord) error {
	sentences := make([]string, 0)
	for _, item := range arrayValue(record.Data["sentences"]) {
		sentence := objectValue(item)
		text := valueString(sentence["text"])
		rawText := valueString(sentence["raw_text"])
		if text != "" {
			sentences = append(sentences, text)
		}
		if rawText != "" && rawText != text {
			sentences = append(sentences, rawText)
		}
	}

	summary := objectValue(record.Data["summary"])
	summaryText := strings.Join([]string{
		joinValues(summary["keywords"]),
		valueString(summary["action_items"]),
		valueString(summary["outline"]),
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
	}, "\n")

	_, err := tx.ExecContext(ctx, `INSERT INTO transcript_search_docs (
		transcript_id, title, summary, transcript_text, participants
	) VALUES (?, ?, ?, ?, ?)
	ON CONFLICT(transcript_id) DO UPDATE SET
		title=excluded.title,
		summary=excluded.summary,
		transcript_text=excluded.transcript_text,
		participants=excluded.participants`,
		transcriptID(record),
		transcriptTitle(record),
		summaryText,
		strings.Join(sentences, "\n"),
		strings.Join([]string{
			joinValues(record.Data["participants"]),
			joinValues(record.Data["fireflies_users"]),
			joinValues(record.Data["workspace_users"]),
		}, "\n"),
	)
	return err
}

func rebuildSearchIndex(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO transcript_search_fts(transcript_search_fts) VALUES ('rebuild')`)
	return err
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func boolOrNil(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case bool:
		return boolInt(typed)
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes":
			return 1
		case "false", "0", "no":
			return 0
		}
	}
	return nil
}

func numberOrNil(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return typed
	case int64:
		return typed
	case json.Number:
		if parsed, err := typed.Float64(); err == nil {
			return parsed
		}
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		var number json.Number = json.Number(typed)
		if parsed, err := number.Float64(); err == nil {
			return parsed
		}
	}
	return nil
}

func intOrNil(value any) any {
	number := numberOrNil(value)
	switch typed := number.(type) {
	case nil:
		return nil
	case int:
		return typed
	case int64:
		return typed
	case float64:
		return int64(typed)
	}
	return nil
}

func intOrDefault(value any, fallback int) int {
	number := intOrNil(value)
	switch typed := number.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	}
	return fallback
}
