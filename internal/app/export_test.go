package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mlitwiniuk/fireflies-downloader/internal/fireflies"
)

func TestDownloadOneBackfillsMediaForSkippedTranscript(t *testing.T) {
	dir := t.TempDir()
	transcriptDir := filepath.Join(dir, "transcripts")
	mediaDir := filepath.Join(dir, "media")
	if err := os.MkdirAll(transcriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	var mediaHits int32
	httpClient := &http.Client{Transport: appRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.String() {
		case "https://media.test/audio":
			atomic.AddInt32(&mediaHits, 1)
			return textResponse(http.StatusOK, "audio-data", "audio/mpeg"), nil
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
			return nil, nil
		}
	})}

	writeExistingTranscript(t, transcriptDir, "call-1", `{"id":"call-1","title":"Call","audio_url":"https://media.test/audio"}`)
	client := fireflies.NewClient(fireflies.ClientOptions{
		Endpoint:   "https://fireflies.test/graphql",
		APIKey:     "test",
		HTTPClient: httpClient,
		MaxRetries: 0,
	})

	result := downloadOne(context.Background(), client, fireflies.TranscriptListItem{ID: "call-1", Title: "Call"}, DownloadOptions{
		Profile:      "complete",
		IncludeMedia: true,
	}, transcriptDir, mediaDir)

	if result.Error != "" {
		t.Fatal(result.Error)
	}
	if !result.Skipped {
		t.Fatal("expected existing transcript JSON to be marked skipped")
	}
	if got := atomic.LoadInt32(&mediaHits); got != 1 {
		t.Fatalf("got %d media requests, want 1", got)
	}
	mediaPath := result.MediaFiles["audio_url"]
	if mediaPath == "" {
		t.Fatalf("expected audio media file, got %#v", result.MediaFiles)
	}
	assertFileContent(t, mediaPath, "audio-data")
}

func TestDownloadOneDoesNotRequestMediaThatAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	transcriptDir := filepath.Join(dir, "transcripts")
	mediaDir := filepath.Join(dir, "media")
	if err := os.MkdirAll(transcriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	var mediaHits int32
	httpClient := &http.Client{Transport: appRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&mediaHits, 1)
		t.Fatalf("media server should not be called when a local file already exists")
		return nil, nil
	})}

	writeExistingTranscript(t, transcriptDir, "call-1", `{"id":"call-1","title":"Call","audio_url":"https://media.test/audio"}`)
	existingMedia := filepath.Join(mediaDir, "call-1.audio.mp3")
	if err := os.WriteFile(existingMedia, []byte("existing-audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := fireflies.NewClient(fireflies.ClientOptions{
		Endpoint:   "https://fireflies.test/graphql",
		APIKey:     "test",
		HTTPClient: httpClient,
		MaxRetries: 0,
	})

	result := downloadOne(context.Background(), client, fireflies.TranscriptListItem{ID: "call-1", Title: "Call"}, DownloadOptions{
		Profile:      "complete",
		IncludeMedia: true,
	}, transcriptDir, mediaDir)

	if result.Error != "" {
		t.Fatal(result.Error)
	}
	if got := atomic.LoadInt32(&mediaHits); got != 0 {
		t.Fatalf("got %d media requests, want 0", got)
	}
	if result.MediaFiles["audio_url"] != existingMedia {
		t.Fatalf("got media path %q, want %q", result.MediaFiles["audio_url"], existingMedia)
	}
	assertFileContent(t, existingMedia, "existing-audio")
}

func TestDownloadOneRefreshesSkippedTranscriptWhenMediaURLIsStale(t *testing.T) {
	dir := t.TempDir()
	transcriptDir := filepath.Join(dir, "transcripts")
	mediaDir := filepath.Join(dir, "media")
	if err := os.MkdirAll(transcriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	var graphQLHits int32
	var freshMediaHits int32
	httpClient := &http.Client{Transport: appRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.String() {
		case "https://media.test/expired":
			return textResponse(http.StatusForbidden, "expired", "text/plain"), nil
		case "https://fireflies.test/graphql":
			atomic.AddInt32(&graphQLHits, 1)
			return textResponse(http.StatusOK, `{"data":{"transcript":{"id":"call-1","title":"Call","audio_url":"https://media.test/fresh"}}}`, "application/json"), nil
		case "https://media.test/fresh":
			atomic.AddInt32(&freshMediaHits, 1)
			return textResponse(http.StatusOK, "fresh-audio", "audio/mpeg"), nil
		default:
			t.Fatalf("unexpected request to %s", r.URL.String())
			return nil, nil
		}
	})}

	writeExistingTranscript(t, transcriptDir, "call-1", `{"id":"call-1","title":"Call","audio_url":"https://media.test/expired"}`)
	client := fireflies.NewClient(fireflies.ClientOptions{
		Endpoint:   "https://fireflies.test/graphql",
		APIKey:     "test",
		HTTPClient: httpClient,
		MaxRetries: 0,
	})

	result := downloadOne(context.Background(), client, fireflies.TranscriptListItem{ID: "call-1", Title: "Call"}, DownloadOptions{
		Profile:      "complete",
		IncludeMedia: true,
	}, transcriptDir, mediaDir)

	if result.Error != "" {
		t.Fatal(result.Error)
	}
	if result.MediaErrors != nil {
		t.Fatalf("expected stale media retry to clear errors, got %#v", result.MediaErrors)
	}
	if got := atomic.LoadInt32(&graphQLHits); got != 1 {
		t.Fatalf("got %d transcript refresh requests, want 1", got)
	}
	if got := atomic.LoadInt32(&freshMediaHits); got != 1 {
		t.Fatalf("got %d fresh media requests, want 1", got)
	}
	assertFileContent(t, result.MediaFiles["audio_url"], "fresh-audio")

	raw, err := os.ReadFile(filepath.Join(transcriptDir, "call-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "/fresh") {
		t.Fatalf("expected refreshed transcript JSON to contain fresh media URL: %s", raw)
	}
}

func writeExistingTranscript(t *testing.T, transcriptDir, id, raw string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(transcriptDir, safeFileName(id)+".json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != want {
		t.Fatalf("got file content %q, want %q", raw, want)
	}
}

type appRoundTripFunc func(*http.Request) (*http.Response, error)

func (f appRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func textResponse(status int, body, contentType string) *http.Response {
	response := &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
	if contentType != "" {
		response.Header.Set("Content-Type", contentType)
	}
	return response
}
