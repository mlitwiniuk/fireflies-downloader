package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mlitwiniuk/fireflies-downloader/internal/archive"
	"github.com/mlitwiniuk/fireflies-downloader/internal/fireflies"
)

type DownloadOptions struct {
	OutputDir     string
	Profile       string
	StrictProfile bool
	Concurrency   int
	IncludeMedia  bool
	WriteCSV      bool
	WriteSQLite   bool
	SQLitePath    string
	Overwrite     bool
}

func DownloadTranscripts(ctx context.Context, client *fireflies.Client, filter fireflies.ListFilter, opts DownloadOptions, stdout io.Writer) error {
	if opts.OutputDir == "" {
		opts.OutputDir = "fireflies_export"
	}
	if opts.Profile == "" {
		opts.Profile = "complete"
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 1
	}
	if opts.SQLitePath == "" {
		opts.SQLitePath = filepath.Join(opts.OutputDir, "fireflies.sqlite")
	}

	transcriptDir := filepath.Join(opts.OutputDir, "transcripts")
	mediaDir := filepath.Join(opts.OutputDir, "media")
	if err := os.MkdirAll(transcriptDir, 0o755); err != nil {
		return err
	}
	if opts.IncludeMedia {
		if err := os.MkdirAll(mediaDir, 0o755); err != nil {
			return err
		}
	}

	fmt.Fprintln(stdout, "Listing transcripts...")
	items, err := client.ListTranscripts(ctx, filter, func(fetched int) {
		fmt.Fprintf(stdout, "  fetched %d transcript metadata records\r", fetched)
	})
	fmt.Fprintln(stdout)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Found %d transcript(s).\n", len(items))

	manifest := archive.DownloadManifest{
		ExportedAt:    time.Now().UTC().Format(time.RFC3339),
		Endpoint:      client.Endpoint(),
		Profile:       opts.Profile,
		StrictProfile: opts.StrictProfile,
		IncludeMedia:  opts.IncludeMedia,
		Filters:       filterManifest(filter),
		Count:         len(items),
		Items:         make([]archive.DownloadResult, len(items)),
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for worker := 0; worker < opts.Concurrency; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				result := downloadOne(ctx, client, items[index], opts, transcriptDir, mediaDir)
				mu.Lock()
				manifest.Items[index] = result
				switch {
				case result.Error != "":
					manifest.Failed++
				case result.Skipped:
					manifest.Skipped++
				default:
					manifest.Succeeded++
				}
				done := manifest.Succeeded + manifest.Skipped + manifest.Failed
				fmt.Fprintf(stdout, "  processed %d/%d\r", done, len(items))
				mu.Unlock()
			}
		}()
	}

	for index := range items {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return ctx.Err()
		case jobs <- index:
		}
	}
	close(jobs)
	wg.Wait()
	fmt.Fprintln(stdout)

	manifestPath := filepath.Join(opts.OutputDir, "manifest.json")
	indexPath := filepath.Join(opts.OutputDir, "index.json")
	if err := writeJSONFile(indexPath, items); err != nil {
		return err
	}
	if opts.WriteCSV {
		csvFiles, err := archive.WriteCSVExports(opts.OutputDir, manifest.Items)
		if err != nil {
			return err
		}
		manifest.CSVFiles = csvFiles
		fmt.Fprintf(stdout, "Wrote CSV exports: %s\n", filepath.Join(opts.OutputDir, "csv"))
	}
	if opts.WriteSQLite {
		if err := archive.WriteSQLiteExports(opts.SQLitePath, manifest); err != nil {
			return err
		}
		manifest.SQLiteFile = opts.SQLitePath
		fmt.Fprintf(stdout, "Wrote SQLite database: %s\n", opts.SQLitePath)
	}
	if err := writeJSONFile(manifestPath, manifest); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Wrote manifest: %s\n", manifestPath)
	if manifest.Failed > 0 {
		return fmt.Errorf("%d transcript(s) failed; see manifest.json", manifest.Failed)
	}
	return nil
}

func downloadOne(ctx context.Context, client *fireflies.Client, item fireflies.TranscriptListItem, opts DownloadOptions, transcriptDir, mediaDir string) archive.DownloadResult {
	result := archive.DownloadResult{
		ID:         item.ID,
		Title:      item.Title,
		Date:       item.Date,
		DateString: item.DateString,
	}

	if item.ID == "" {
		result.Error = "missing transcript id"
		return result
	}

	filename := safeFileName(item.ID) + ".json"
	path := filepath.Join(transcriptDir, filename)
	result.File = path

	if !opts.Overwrite && fileExists(path) {
		result.Skipped = true
		return result
	}

	fetch, err := client.GetTranscriptWithFallback(ctx, item.ID, opts.Profile, opts.StrictProfile)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Profile = fetch.Profile
	result.Warning = fetch.Warning

	if err := writeRawJSONFile(path, fetch.Raw); err != nil {
		result.Error = err.Error()
		return result
	}

	if opts.IncludeMedia {
		mediaFiles, mediaErrors := downloadTranscriptMedia(ctx, client.HTTPClient(), fetch.Raw, mediaDir, item.ID, opts.Overwrite)
		result.MediaFiles = mediaFiles
		result.MediaErrors = mediaErrors
	}

	return result
}

func downloadTranscriptMedia(ctx context.Context, httpClient *http.Client, raw json.RawMessage, mediaDir, id string, overwrite bool) (map[string]string, map[string]string) {
	var transcript map[string]any
	if err := json.Unmarshal(raw, &transcript); err != nil {
		return nil, map[string]string{"json": err.Error()}
	}

	files := map[string]string{}
	errors := map[string]string{}
	for _, kind := range []string{"audio_url", "video_url"} {
		url, _ := transcript[kind].(string)
		if url == "" {
			continue
		}

		base := filepath.Join(mediaDir, safeFileName(id)+"."+strings.TrimSuffix(kind, "_url"))
		path, err := downloadURL(ctx, httpClient, url, base, overwrite)
		if err != nil {
			errors[kind] = err.Error()
			continue
		}
		files[kind] = path
	}

	if len(files) == 0 {
		files = nil
	}
	if len(errors) == 0 {
		errors = nil
	}
	return files, errors
}

func downloadURL(ctx context.Context, httpClient *http.Client, url, destBase string, overwrite bool) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "fireflies-downloader/0.1")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	ext := extensionFromContentType(resp.Header.Get("Content-Type"))
	if ext == "" {
		ext = extensionFromURL(url)
	}
	if ext == "" {
		ext = ".bin"
	}

	dest := destBase + ext
	if !overwrite && fileExists(dest) {
		return dest, nil
	}

	tmp := dest + ".tmp"
	file, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(file, resp.Body)
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return "", closeErr
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return dest, nil
}
