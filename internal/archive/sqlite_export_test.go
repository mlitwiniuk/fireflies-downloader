package archive

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteSQLiteExports(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "transcript-1.json")
	raw := `{
	  "id": "transcript-1",
	  "title": "Renewal Call",
	  "date": 1778068800000,
	  "dateString": "2026-05-06T12:00:00.000Z",
	  "duration": 42,
	  "organizer_email": "owner@example.com",
	  "participants": ["buyer@example.com"],
	  "summary": {
	    "keywords": "renewal, budget",
	    "short_summary": "Discussed renewal budget.",
	    "topics_discussed": ["pricing"]
	  },
	  "sentences": [
	    {
	      "index": 0,
	      "speaker_name": "Alice",
	      "text": "We discussed the renewal budget.",
	      "raw_text": "We discussed the renewal budget.",
	      "start_time": "0",
	      "end_time": "3"
	    }
	  ],
	  "speakers": [{"id": "1", "name": "Alice"}]
	}`
	if err := os.WriteFile(transcriptPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest := DownloadManifest{
		ExportedAt: "2026-05-06T12:30:00Z",
		Endpoint:   "https://api.fireflies.ai/graphql",
		Profile:    "complete",
		Count:      1,
		Succeeded:  1,
		Items: []DownloadResult{
			{
				ID:         "transcript-1",
				Title:      "Renewal Call",
				DateString: "2026-05-06T12:00:00.000Z",
				File:       transcriptPath,
				Profile:    "complete",
				MediaFiles: map[string]string{"audio_url": filepath.Join(dir, "audio.mp3")},
			},
		},
	}

	dbPath := filepath.Join(dir, "fireflies.sqlite")
	if err := WriteSQLiteExports(dbPath, manifest); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var title string
	if err := db.QueryRow(`SELECT title FROM transcripts WHERE id = ?`, "transcript-1").Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Renewal Call" {
		t.Fatalf("got title %q", title)
	}

	var text string
	if err := db.QueryRow(`SELECT text FROM sentences WHERE transcript_id = ? AND sentence_index = 0`, "transcript-1").Scan(&text); err != nil {
		t.Fatal(err)
	}
	if text != "We discussed the renewal budget." {
		t.Fatalf("got sentence %q", text)
	}

	var searchID string
	if err := db.QueryRow(`SELECT d.transcript_id
		FROM transcript_search_fts f
		JOIN transcript_search_docs d ON d.rowid = f.rowid
		WHERE transcript_search_fts MATCH 'renewal'
		LIMIT 1`).Scan(&searchID); err != nil {
		t.Fatal(err)
	}
	if searchID != "transcript-1" {
		t.Fatalf("got search id %q", searchID)
	}
}
