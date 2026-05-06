package web

import (
	"bytes"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"

	_ "modernc.org/sqlite"
)

//go:embed templates/*.gohtml static/*
var assetsFS embed.FS

type Server struct {
	db        *sql.DB
	templates *template.Template
	static    http.Handler
	dbPath    string
	mcpToken  string
	markdown  goldmark.Markdown
}

type Options struct {
	DBPath   string
	MCPToken string
}

func New(options Options) (*Server, error) {
	if strings.TrimSpace(options.DBPath) == "" {
		return nil, errors.New("database path cannot be empty")
	}

	db, err := sql.Open("sqlite", options.DBPath)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA query_only = ON"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		_ = db.Close()
		return nil, err
	}

	tmpl, err := parseTemplates()
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	staticFS, err := fs.Sub(assetsFS, "static")
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	server := &Server{
		db:        db,
		templates: tmpl,
		static:    http.FileServer(http.FS(staticFS)),
		dbPath:    options.DBPath,
		mcpToken:  strings.TrimSpace(options.MCPToken),
		markdown:  goldmark.New(goldmark.WithExtensions(extension.GFM)),
	}
	return server, nil
}

func (s *Server) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", s.static))
	mux.HandleFunc("/", s.handleDashboard)
	mux.HandleFunc("/insights", s.handleInsights)
	mux.HandleFunc("/mcp", s.handleMCP)
	mux.HandleFunc("/people", s.handlePeople)
	mux.HandleFunc("/transcripts", s.handleTranscripts)
	mux.HandleFunc("/transcripts/", s.handleTranscript)
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func parseTemplates() (*template.Template, error) {
	funcs := template.FuncMap{
		"formatDate":       formatDate,
		"formatDuration":   formatDuration,
		"formatHours":      formatHours,
		"formatNumber":     formatNumber,
		"formatPercent":    formatPercent,
		"barWidth":         barWidth,
		"sentenceTime":     sentenceTime,
		"jsonPretty":       jsonPretty,
		"markdown":         markdownHTML,
		"truncate":         truncate,
		"joinNonEmpty":     joinNonEmpty,
		"hasValue":         hasValue,
		"add":              func(a, b int) int { return a + b },
		"sub":              func(a, b int) int { return a - b },
		"sequence":         sequence,
		"currentPageClass": currentPageClass,
	}
	return template.New("").Funcs(funcs).ParseFS(assetsFS, "templates/*.gohtml")
}

func markdownHTML(value string) template.HTML {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var buffer bytes.Buffer
	md := goldmark.New(goldmark.WithExtensions(extension.GFM))
	if err := md.Convert([]byte(value), &buffer); err != nil {
		return template.HTML(template.HTMLEscapeString(value))
	}
	return template.HTML(buffer.String())
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data any) {
	var buffer bytes.Buffer
	if err := s.templates.ExecuteTemplate(&buffer, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buffer.Bytes())
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()

	stats, err := s.loadStats(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	months, err := s.loadMonthlyActivity(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	organizers, err := s.loadTopOrganizers(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	meetingTypes, err := s.loadMeetingTypes(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	recent, err := s.loadRecentTranscripts(ctx, 12)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.render(w, r, "dashboard.gohtml", PageData{
		Title:  "Dashboard",
		Active: "dashboard",
		DBPath: s.dbPath,
		Dashboard: &DashboardData{
			Stats:        stats,
			Months:       months,
			Organizers:   organizers,
			MeetingTypes: meetingTypes,
			Recent:       recent,
		},
	})
}

func (s *Server) handleInsights(w http.ResponseWriter, r *http.Request) {
	data, err := s.loadInsights(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, r, "insights.gohtml", PageData{
		Title:    "Insights",
		Active:   "insights",
		DBPath:   s.dbPath,
		Insights: data,
	})
}

func (s *Server) handlePeople(w http.ResponseWriter, r *http.Request) {
	personKey := strings.TrimSpace(r.URL.Query().Get("person"))
	data, err := s.loadPeople(r.Context(), personKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, r, "people.gohtml", PageData{
		Title:  "People",
		Active: "people",
		DBPath: s.dbPath,
		People: data,
	})
}

func (s *Server) handleTranscripts(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	organizer := strings.TrimSpace(r.URL.Query().Get("organizer"))
	person := strings.TrimSpace(r.URL.Query().Get("person"))
	meetingType := strings.TrimSpace(r.URL.Query().Get("type"))
	sentiment := strings.TrimSpace(r.URL.Query().Get("sentiment"))
	page := intQuery(r, "page", 1)
	if page < 1 {
		page = 1
	}
	perPage := 30

	organizers, err := s.loadOrganizerOptions(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	people, err := s.loadPersonOptions(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	meetingTypes, err := s.loadMeetingTypeOptions(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	result, err := s.searchTranscripts(r.Context(), TranscriptSearch{
		Query:       query,
		Organizer:   organizer,
		Person:      person,
		MeetingType: meetingType,
		Sentiment:   sentiment,
		Page:        page,
		PerPage:     perPage,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.render(w, r, "transcripts.gohtml", PageData{
		Title:  "Transcripts",
		Active: "transcripts",
		DBPath: s.dbPath,
		TranscriptList: &TranscriptListData{
			Query:        query,
			Organizer:    organizer,
			Person:       person,
			MeetingType:  meetingType,
			Sentiment:    sentiment,
			Organizers:   organizers,
			People:       people,
			MeetingTypes: meetingTypes,
			Result:       result,
		},
	})
}

func (s *Server) handleTranscript(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(path.Base(r.URL.Path), "/")
	if id == "" || id == "transcripts" {
		http.Redirect(w, r, "/transcripts", http.StatusSeeOther)
		return
	}

	data, err := s.loadTranscriptDetail(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.render(w, r, "detail.gohtml", PageData{
		Title:            data.Transcript.Title,
		Active:           "transcripts",
		DBPath:           s.dbPath,
		TranscriptDetail: data,
	})
}

func intQuery(r *http.Request, key string, fallback int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

var ftsTokenPattern = regexp.MustCompile(`[[:alnum:]_@.\-]+`)

func ftsQuery(input string) string {
	terms := ftsTokenPattern.FindAllString(strings.ToLower(input), -1)
	if len(terms) == 0 {
		return ""
	}
	if len(terms) > 8 {
		terms = terms[:8]
	}
	for i, term := range terms {
		terms[i] = `"` + strings.ReplaceAll(term, `"`, `""`) + `"`
	}
	return strings.Join(terms, " AND ")
}

func formatDate(value string) string {
	if value == "" {
		return "Unknown"
	}
	layouts := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z", "2006-01-02"}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.Format("Jan 2, 2006")
		}
	}
	if len(value) >= 10 {
		return value[:10]
	}
	return value
}

func formatDuration(minutes float64) string {
	if minutes <= 0 {
		return "0m"
	}
	hours := int(minutes) / 60
	mins := int(minutes) % 60
	if hours > 0 {
		return fmt.Sprintf("%dh %02dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

func formatHours(minutes float64) string {
	if minutes <= 0 {
		return "0"
	}
	return fmt.Sprintf("%.1f", minutes/60)
}

func formatNumber(value int) string {
	raw := strconv.Itoa(value)
	if len(raw) <= 3 {
		return raw
	}
	var out []byte
	for i, r := range reverse(raw) {
		if i > 0 && i%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, byte(r))
	}
	return reverse(string(out))
}

func reverse(value string) string {
	runes := []rune(value)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

func formatPercent(value float64) string {
	if value <= 0 {
		return "0%"
	}
	return fmt.Sprintf("%.0f%%", value)
}

func barWidth(value, max any) string {
	valueFloat := numericValue(value)
	maxFloat := numericValue(max)
	if maxFloat <= 0 || valueFloat <= 0 {
		return "0%"
	}
	width := valueFloat / maxFloat * 100
	if width < 3 {
		width = 3
	}
	if width > 100 {
		width = 100
	}
	return fmt.Sprintf("%.1f%%", width)
}

func numericValue(value any) float64 {
	switch typed := value.(type) {
	case nil:
		return 0
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case float64:
		return typed
	case float32:
		return float64(typed)
	default:
		parsed, _ := strconv.ParseFloat(fmt.Sprint(value), 64)
		return parsed
	}
}

func sentenceTime(start, end string) string {
	if start == "" && end == "" {
		return ""
	}
	if end == "" {
		return start
	}
	if start == "" {
		return end
	}
	return start + "-" + end
}

func jsonPretty(value string) string {
	if value == "" {
		return ""
	}
	var parsed any
	if err := json.Unmarshal([]byte(value), &parsed); err != nil {
		return value
	}
	raw, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return value
	}
	return string(raw)
}

func truncate(value string, max int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	if max <= 1 {
		return string(runes[:max])
	}
	return string(runes[:max-1]) + "..."
}

func joinNonEmpty(values ...string) string {
	var parts []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, " / ")
}

func hasValue(value string) bool {
	return strings.TrimSpace(value) != ""
}

func sequence(start, end int) []int {
	if end < start {
		return nil
	}
	out := make([]int, 0, end-start+1)
	for i := start; i <= end; i++ {
		out = append(out, i)
	}
	return out
}

func currentPageClass(current, candidate int) string {
	if current == candidate {
		return "is-current"
	}
	return ""
}
