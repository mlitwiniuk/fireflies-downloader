package archive

type DownloadManifest struct {
	ExportedAt    string           `json:"exported_at"`
	Endpoint      string           `json:"endpoint"`
	Profile       string           `json:"profile"`
	StrictProfile bool             `json:"strict_profile"`
	IncludeMedia  bool             `json:"include_media"`
	CSVFiles      []string         `json:"csv_files,omitempty"`
	SQLiteFile    string           `json:"sqlite_file,omitempty"`
	Filters       map[string]any   `json:"filters"`
	Count         int              `json:"count"`
	Succeeded     int              `json:"succeeded"`
	Skipped       int              `json:"skipped"`
	Failed        int              `json:"failed"`
	Items         []DownloadResult `json:"items"`
}

type DownloadResult struct {
	ID          string            `json:"id"`
	Title       string            `json:"title,omitempty"`
	Date        float64           `json:"date,omitempty"`
	DateString  string            `json:"dateString,omitempty"`
	File        string            `json:"file,omitempty"`
	Profile     string            `json:"profile,omitempty"`
	Skipped     bool              `json:"skipped,omitempty"`
	Warning     string            `json:"warning,omitempty"`
	Error       string            `json:"error,omitempty"`
	MediaFiles  map[string]string `json:"media_files,omitempty"`
	MediaErrors map[string]string `json:"media_errors,omitempty"`
}
