package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

type PageData struct {
	Title            string
	Active           string
	DBPath           string
	Dashboard        *DashboardData
	Insights         *InsightsData
	People           *PeopleData
	TranscriptList   *TranscriptListData
	TranscriptDetail *TranscriptDetailData
}

type DashboardData struct {
	Stats        Stats
	Months       []Bucket
	Organizers   []Bucket
	MeetingTypes []Bucket
	Recent       []TranscriptRow
}

type Stats struct {
	Transcripts       int
	Sentences         int
	DistinctSpeakers  int
	MediaFiles        int
	TotalMinutes      float64
	AverageMinutes    float64
	FirstDate         string
	LastDate          string
	ParticipantsCount int
}

type Bucket struct {
	Label   string
	Count   int
	Minutes float64
	Max     float64
}

type TranscriptListData struct {
	Query        string
	Organizer    string
	Person       string
	MeetingType  string
	Sentiment    string
	Organizers   []string
	People       []PersonOption
	MeetingTypes []string
	Result       TranscriptSearchResult
}

type TranscriptSearch struct {
	Query       string
	Organizer   string
	Person      string
	MeetingType string
	Sentiment   string
	Page        int
	PerPage     int
}

type TranscriptSearchResult struct {
	Rows       []TranscriptRow
	Total      int
	Page       int
	PerPage    int
	TotalPages int
	HasPrev    bool
	HasNext    bool
	PrevPage   int
	NextPage   int
}

type TranscriptRow struct {
	ID             string
	Title          string
	DateString     string
	Duration       float64
	OrganizerEmail string
	HostEmail      string
	MeetingType    string
	ShortSummary   string
	Participants   string
	SentenceCount  int
	MediaCount     int
	PositivePct    float64
	NegativePct    float64
}

type PeopleData struct {
	People   []PersonStats
	Selected *PersonStats
	Calls    []TranscriptRow
}

type PersonOption struct {
	Key   string
	Label string
}

type PersonStats struct {
	Key              string
	Label            string
	Email            string
	Calls            int
	SpokenCalls      int
	AttendeeCalls    int
	OrganizerCalls   int
	HostCalls        int
	TotalMinutes     float64
	TalkMinutes      float64
	TalkPct          float64
	WordCount        int
	Questions        int
	QuestionRate     float64
	WordsPerMinute   float64
	LongestMonologue float64
	FillerWords      int
	PositiveAvg      float64
	NegativeAvg      float64
	LastSeen         string
	callIDs          map[string]struct{}
	spokenIDs        map[string]struct{}
	attendeeIDs      map[string]struct{}
	organizerIDs     map[string]struct{}
	hostIDs          map[string]struct{}
	sentimentCallIDs map[string]struct{}
}

type InsightsData struct {
	Stats              Stats
	MonthlySentiment   []SentimentBucket
	People             []PersonStats
	SpeakerPerformance []PersonStats
	RiskCalls          []TranscriptRow
	PositiveCalls      []TranscriptRow
	TalkDominance      []CallCoachingMetric
	QuestionGaps       []CallCoachingMetric
	Suggestions        []CoachingSuggestion
}

type SentimentBucket struct {
	Label       string
	Positive    float64
	Neutral     float64
	Negative    float64
	Calls       int
	PositiveMax float64
	NegativeMax float64
}

type CallCoachingMetric struct {
	TranscriptID string
	Title        string
	DateString   string
	SpeakerName  string
	Value        float64
	Helper       string
}

type CoachingSuggestion struct {
	Title string
	Body  string
}

type TranscriptDetailData struct {
	Transcript TranscriptDetail
	Summary    Summary
	Analytics  AnalyticsOverview
	Sentences  []Sentence
	Speakers   []Speaker
	Attendees  []Attendee
	Attendance []Attendance
	Channels   []Channel
	SharedWith []SharedWith
	AppOutputs []AppOutput
	Media      []Media
}

type TranscriptDetail struct {
	ID             string
	Title          string
	DateString     string
	Duration       float64
	Privacy        string
	HostEmail      string
	OrganizerEmail string
	CalendarID     string
	CalendarType   string
	MeetingLink    string
	TranscriptURL  string
	AudioURL       string
	VideoURL       string
	IsLive         bool
	Participants   string
	FirefliesUsers string
	WorkspaceUsers string
	UserEmail      string
	UserName       string
	RawJSONFile    string
	RawJSON        string
	LastExportedAt string
	SentenceCount  int
	SpeakerCount   int
	AttendeeCount  int
	MediaCount     int
}

type Summary struct {
	Keywords               string
	ActionItems            string
	Outline                string
	Overview               string
	BulletGist             string
	Gist                   string
	ShortSummary           string
	ShortOverview          string
	MeetingType            string
	TopicsDiscussedJSON    string
	TranscriptChaptersJSON string
	Notes                  string
	ExtendedSectionsJSON   string
}

type AnalyticsOverview struct {
	NegativePct       float64
	NeutralPct        float64
	PositivePct       float64
	CategoryQuestions int
	CategoryDateTimes int
	CategoryMetrics   int
	CategoryTasks     int
}

type Sentence struct {
	Index       int
	SpeakerID   string
	SpeakerName string
	StartTime   string
	EndTime     string
	Text        string
	RawText     string
	Sentiment   string
}

type Speaker struct {
	ID               string
	Name             string
	Duration         float64
	WordCount        int
	LongestMonologue float64
	MonologuesCount  int
	FillerWords      int
	Questions        int
	DurationPct      float64
	WordsPerMinute   float64
}

type Attendee struct {
	DisplayName string
	Email       string
	PhoneNumber string
	Name        string
	Location    string
}

type Attendance struct {
	Name      string
	JoinTime  string
	LeaveTime string
}

type Channel struct {
	ID        string
	Title     string
	IsPrivate bool
	CreatedBy string
}

type SharedWith struct {
	Email     string
	Name      string
	PhotoURL  string
	ExpiresAt string
}

type AppOutput struct {
	AppID     string
	CreatedAt float64
	Title     string
	Prompt    string
	Response  string
}

type Media struct {
	Kind  string
	Path  string
	Error string
}

func (s *Server) loadStats(ctx context.Context) (Stats, error) {
	var stats Stats
	var first, last sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT
		COUNT(*),
		COALESCE(SUM(duration_minutes), 0),
		COALESCE(AVG(NULLIF(duration_minutes, 0)), 0),
		MIN(date_string),
		MAX(date_string)
		FROM transcripts`).Scan(&stats.Transcripts, &stats.TotalMinutes, &stats.AverageMinutes, &first, &last); err != nil {
		return stats, err
	}
	stats.FirstDate = nullString(first)
	stats.LastDate = nullString(last)

	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sentences`).Scan(&stats.Sentences)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT speaker_name) FROM sentences WHERE speaker_name <> ''`).Scan(&stats.DistinctSpeakers)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM downloaded_media WHERE COALESCE(path, '') <> ''`).Scan(&stats.MediaFiles)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM meeting_attendees`).Scan(&stats.ParticipantsCount)
	return stats, nil
}

func (s *Server) loadMonthlyActivity(ctx context.Context) ([]Bucket, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		COALESCE(substr(date_string, 1, 7), 'Unknown') AS month,
		COUNT(*),
		COALESCE(SUM(duration_minutes), 0)
		FROM transcripts
		GROUP BY month
		ORDER BY month`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var buckets []Bucket
	var max float64
	for rows.Next() {
		var item Bucket
		if err := rows.Scan(&item.Label, &item.Count, &item.Minutes); err != nil {
			return nil, err
		}
		if float64(item.Count) > max {
			max = float64(item.Count)
		}
		buckets = append(buckets, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(buckets) > 18 {
		buckets = buckets[len(buckets)-18:]
	}
	for i := range buckets {
		buckets[i].Max = max
	}
	return buckets, nil
}

func (s *Server) loadTopOrganizers(ctx context.Context) ([]Bucket, error) {
	return s.loadBuckets(ctx, `SELECT
		COALESCE(NULLIF(organizer_email, ''), 'Unknown'),
		COUNT(*),
		COALESCE(SUM(duration_minutes), 0)
		FROM transcripts
		GROUP BY COALESCE(NULLIF(organizer_email, ''), 'Unknown')
		ORDER BY COUNT(*) DESC
		LIMIT 8`)
}

func (s *Server) loadMeetingTypes(ctx context.Context) ([]Bucket, error) {
	return s.loadBuckets(ctx, `SELECT
		COALESCE(NULLIF(meeting_type, ''), 'Unclassified'),
		COUNT(*),
		0
		FROM summaries
		GROUP BY COALESCE(NULLIF(meeting_type, ''), 'Unclassified')
		ORDER BY COUNT(*) DESC
		LIMIT 8`)
}

func (s *Server) loadBuckets(ctx context.Context, query string) ([]Bucket, error) {
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Bucket
	var max float64
	for rows.Next() {
		var item Bucket
		if err := rows.Scan(&item.Label, &item.Count, &item.Minutes); err != nil {
			return nil, err
		}
		if float64(item.Count) > max {
			max = float64(item.Count)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Max = max
	}
	return items, nil
}

func (s *Server) loadRecentTranscripts(ctx context.Context, limit int) ([]TranscriptRow, error) {
	return s.queryTranscriptRows(ctx, `SELECT
		t.id, t.title, t.date_string, COALESCE(t.duration_minutes, 0),
		t.organizer_email, t.host_email, COALESCE(s.meeting_type, ''),
		COALESCE(s.short_summary, ''), COALESCE(t.participants_json, ''),
		(SELECT COUNT(*) FROM sentences se WHERE se.transcript_id = t.id),
		(SELECT COUNT(*) FROM downloaded_media dm WHERE dm.transcript_id = t.id AND COALESCE(dm.path, '') <> ''),
		COALESCE(a.positive_pct, 0), COALESCE(a.negative_pct, 0)
		FROM transcripts t
		LEFT JOIN summaries s ON s.transcript_id = t.id
		LEFT JOIN analytics_overview a ON a.transcript_id = t.id
		ORDER BY t.date_ms DESC
		LIMIT ?`, limit)
}

func (s *Server) loadOrganizerOptions(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT organizer_email
		FROM transcripts
		WHERE COALESCE(organizer_email, '') <> ''
		ORDER BY organizer_email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var organizers []string
	for rows.Next() {
		var organizer string
		if err := rows.Scan(&organizer); err != nil {
			return nil, err
		}
		organizers = append(organizers, organizer)
	}
	return organizers, rows.Err()
}

func (s *Server) loadMeetingTypeOptions(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT meeting_type
		FROM summaries
		WHERE COALESCE(meeting_type, '') <> ''
		ORDER BY meeting_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var types []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		types = append(types, value)
	}
	return types, rows.Err()
}

func (s *Server) loadPersonOptions(ctx context.Context) ([]PersonOption, error) {
	people, err := s.buildPeopleStats(ctx)
	if err != nil {
		return nil, err
	}
	options := make([]PersonOption, 0, len(people))
	for _, person := range people {
		options = append(options, PersonOption{Key: person.Key, Label: person.Label})
	}
	sort.Slice(options, func(i, j int) bool {
		return strings.ToLower(options[i].Label) < strings.ToLower(options[j].Label)
	})
	return options, nil
}

func (s *Server) searchTranscripts(ctx context.Context, search TranscriptSearch) (TranscriptSearchResult, error) {
	if search.Page < 1 {
		search.Page = 1
	}
	if search.PerPage <= 0 {
		search.PerPage = 30
	}
	offset := (search.Page - 1) * search.PerPage
	fts := ftsQuery(search.Query)

	var (
		rows  []TranscriptRow
		err   error
		total int
	)
	if fts != "" {
		total, err = s.countFTS(ctx, fts, search)
		if err != nil {
			return TranscriptSearchResult{}, err
		}
		rows, err = s.queryFTS(ctx, fts, search, search.PerPage, offset)
	} else {
		total, err = s.countTranscripts(ctx, search)
		if err != nil {
			return TranscriptSearchResult{}, err
		}
		rows, err = s.queryPlainTranscripts(ctx, search, search.PerPage, offset)
	}
	if err != nil {
		return TranscriptSearchResult{}, err
	}

	totalPages := int(math.Ceil(float64(total) / float64(search.PerPage)))
	if totalPages < 1 {
		totalPages = 1
	}
	return TranscriptSearchResult{
		Rows:       rows,
		Total:      total,
		Page:       search.Page,
		PerPage:    search.PerPage,
		TotalPages: totalPages,
		HasPrev:    search.Page > 1,
		HasNext:    search.Page < totalPages,
		PrevPage:   search.Page - 1,
		NextPage:   search.Page + 1,
	}, nil
}

func (s *Server) countTranscripts(ctx context.Context, search TranscriptSearch) (int, error) {
	var count int
	query := `SELECT COUNT(*)
		FROM transcripts t
		LEFT JOIN summaries s ON s.transcript_id = t.id
		LEFT JOIN analytics_overview a ON a.transcript_id = t.id`
	where, args := transcriptFilterSQL(search, "t", "s", "a")
	query += where
	return count, s.db.QueryRowContext(ctx, query, args...).Scan(&count)
}

func (s *Server) countFTS(ctx context.Context, fts string, search TranscriptSearch) (int, error) {
	var count int
	query := `SELECT COUNT(*)
		FROM transcript_search_fts f
		JOIN transcript_search_docs d ON d.rowid = f.rowid
		JOIN transcripts t ON t.id = d.transcript_id
		LEFT JOIN summaries s ON s.transcript_id = t.id
		LEFT JOIN analytics_overview a ON a.transcript_id = t.id
		WHERE transcript_search_fts MATCH ?`
	args := []any{fts}
	where, filterArgs := transcriptFilterClauses(search, "t", "s", "a", true)
	query += where
	args = append(args, filterArgs...)
	return count, s.db.QueryRowContext(ctx, query, args...).Scan(&count)
}

func (s *Server) queryPlainTranscripts(ctx context.Context, search TranscriptSearch, limit, offset int) ([]TranscriptRow, error) {
	query := `SELECT
		t.id, t.title, t.date_string, COALESCE(t.duration_minutes, 0),
		t.organizer_email, t.host_email, COALESCE(s.meeting_type, ''),
		COALESCE(s.short_summary, ''), COALESCE(t.participants_json, ''),
		(SELECT COUNT(*) FROM sentences se WHERE se.transcript_id = t.id),
		(SELECT COUNT(*) FROM downloaded_media dm WHERE dm.transcript_id = t.id AND COALESCE(dm.path, '') <> ''),
		COALESCE(a.positive_pct, 0), COALESCE(a.negative_pct, 0)
		FROM transcripts t
		LEFT JOIN summaries s ON s.transcript_id = t.id
		LEFT JOIN analytics_overview a ON a.transcript_id = t.id`
	where, args := transcriptFilterSQL(search, "t", "s", "a")
	query += where
	query += ` ORDER BY t.date_ms DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	return s.queryTranscriptRows(ctx, query, args...)
}

func (s *Server) queryFTS(ctx context.Context, fts string, search TranscriptSearch, limit, offset int) ([]TranscriptRow, error) {
	query := `SELECT
		t.id, t.title, t.date_string, COALESCE(t.duration_minutes, 0),
		t.organizer_email, t.host_email, COALESCE(s.meeting_type, ''),
		COALESCE(s.short_summary, ''), COALESCE(t.participants_json, ''),
		(SELECT COUNT(*) FROM sentences se WHERE se.transcript_id = t.id),
		(SELECT COUNT(*) FROM downloaded_media dm WHERE dm.transcript_id = t.id AND COALESCE(dm.path, '') <> ''),
		COALESCE(a.positive_pct, 0), COALESCE(a.negative_pct, 0)
		FROM transcript_search_fts f
		JOIN transcript_search_docs d ON d.rowid = f.rowid
		JOIN transcripts t ON t.id = d.transcript_id
		LEFT JOIN summaries s ON s.transcript_id = t.id
		LEFT JOIN analytics_overview a ON a.transcript_id = t.id
		WHERE transcript_search_fts MATCH ?`
	args := []any{fts}
	where, filterArgs := transcriptFilterClauses(search, "t", "s", "a", true)
	query += where
	args = append(args, filterArgs...)
	query += ` ORDER BY rank, t.date_ms DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	return s.queryTranscriptRows(ctx, query, args...)
}

func transcriptFilterSQL(search TranscriptSearch, transcriptAlias, summaryAlias, analyticsAlias string) (string, []any) {
	return transcriptFilterClauses(search, transcriptAlias, summaryAlias, analyticsAlias, false)
}

func transcriptFilterClauses(search TranscriptSearch, transcriptAlias, summaryAlias, analyticsAlias string, prefixAnd bool) (string, []any) {
	var clauses []string
	var args []any
	if search.Organizer != "" {
		clauses = append(clauses, transcriptAlias+`.organizer_email = ?`)
		args = append(args, search.Organizer)
	}
	if search.MeetingType != "" {
		clauses = append(clauses, summaryAlias+`.meeting_type = ?`)
		args = append(args, search.MeetingType)
	}
	if search.Person != "" {
		like := "%" + search.Person + "%"
		clauses = append(clauses, `(
			LOWER(COALESCE(`+transcriptAlias+`.organizer_email, '')) = LOWER(?)
			OR LOWER(COALESCE(`+transcriptAlias+`.host_email, '')) = LOWER(?)
			OR LOWER(COALESCE(`+transcriptAlias+`.participants_json, '')) LIKE LOWER(?)
			OR EXISTS (SELECT 1 FROM meeting_attendees ma WHERE ma.transcript_id = `+transcriptAlias+`.id AND LOWER(COALESCE(ma.email, '') || ' ' || COALESCE(ma.name, '') || ' ' || COALESCE(ma.display_name, '')) LIKE LOWER(?))
			OR EXISTS (SELECT 1 FROM sentences se WHERE se.transcript_id = `+transcriptAlias+`.id AND LOWER(COALESCE(se.speaker_name, '')) = LOWER(?))
		)`)
		args = append(args, search.Person, search.Person, like, like, search.Person)
	}
	switch search.Sentiment {
	case "positive":
		clauses = append(clauses, analyticsAlias+`.positive_pct >= 50`)
	case "negative":
		clauses = append(clauses, analyticsAlias+`.negative_pct >= 20`)
	case "mixed":
		clauses = append(clauses, analyticsAlias+`.positive_pct >= 25 AND `+analyticsAlias+`.negative_pct >= 10`)
	}
	if len(clauses) == 0 {
		return "", args
	}
	prefix := " WHERE "
	if prefixAnd {
		prefix = " AND "
	}
	return prefix + strings.Join(clauses, " AND "), args
}

func (s *Server) queryTranscriptRows(ctx context.Context, query string, args ...any) ([]TranscriptRow, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TranscriptRow
	for rows.Next() {
		var item TranscriptRow
		if err := rows.Scan(
			&item.ID,
			&item.Title,
			&item.DateString,
			&item.Duration,
			&item.OrganizerEmail,
			&item.HostEmail,
			&item.MeetingType,
			&item.ShortSummary,
			&item.Participants,
			&item.SentenceCount,
			&item.MediaCount,
			&item.PositivePct,
			&item.NegativePct,
		); err != nil {
			return nil, err
		}
		item.Participants = compactJSONList(item.Participants)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Server) loadTranscriptDetail(ctx context.Context, id string) (*TranscriptDetailData, error) {
	detail, err := s.loadTranscript(ctx, id)
	if err != nil {
		return nil, err
	}
	summary, err := s.loadSummary(ctx, id)
	if err != nil && !errorsIsNoRows(err) {
		return nil, err
	}
	analytics, err := s.loadAnalyticsOverview(ctx, id)
	if err != nil && !errorsIsNoRows(err) {
		return nil, err
	}

	data := &TranscriptDetailData{
		Transcript: detail,
		Summary:    summary,
		Analytics:  analytics,
	}
	if data.Sentences, err = s.loadSentences(ctx, id); err != nil {
		return nil, err
	}
	if data.Speakers, err = s.loadSpeakers(ctx, id); err != nil {
		return nil, err
	}
	if data.Attendees, err = s.loadAttendees(ctx, id); err != nil {
		return nil, err
	}
	if data.Attendance, err = s.loadAttendance(ctx, id); err != nil {
		return nil, err
	}
	if data.Channels, err = s.loadChannels(ctx, id); err != nil {
		return nil, err
	}
	if data.SharedWith, err = s.loadSharedWith(ctx, id); err != nil {
		return nil, err
	}
	if data.AppOutputs, err = s.loadAppOutputs(ctx, id); err != nil {
		return nil, err
	}
	if data.Media, err = s.loadMedia(ctx, id); err != nil {
		return nil, err
	}
	return data, nil
}

func (s *Server) loadPeople(ctx context.Context, selectedKey string) (*PeopleData, error) {
	people, err := s.buildPeopleStats(ctx)
	if err != nil {
		return nil, err
	}
	sortPeople(people)

	data := &PeopleData{People: people}
	if selectedKey != "" {
		for i := range people {
			if people[i].Key == selectedKey {
				selected := people[i]
				data.Selected = &selected
				result, err := s.searchTranscripts(ctx, TranscriptSearch{
					Person:  selected.Key,
					Page:    1,
					PerPage: 50,
				})
				if err != nil {
					return nil, err
				}
				data.Calls = result.Rows
				break
			}
		}
	}
	return data, nil
}

func (s *Server) loadInsights(ctx context.Context) (*InsightsData, error) {
	stats, err := s.loadStats(ctx)
	if err != nil {
		return nil, err
	}
	monthly, err := s.loadMonthlySentiment(ctx)
	if err != nil {
		return nil, err
	}
	people, err := s.buildPeopleStats(ctx)
	if err != nil {
		return nil, err
	}
	sortPeople(people)
	if len(people) > 15 {
		people = people[:15]
	}
	speakers := make([]PersonStats, 0, len(people))
	allPeople, err := s.buildPeopleStats(ctx)
	if err != nil {
		return nil, err
	}
	for _, person := range allPeople {
		if person.SpokenCalls > 0 {
			speakers = append(speakers, person)
		}
	}
	sort.Slice(speakers, func(i, j int) bool {
		if speakers[i].SpokenCalls == speakers[j].SpokenCalls {
			return speakers[i].TalkMinutes > speakers[j].TalkMinutes
		}
		return speakers[i].SpokenCalls > speakers[j].SpokenCalls
	})
	if len(speakers) > 15 {
		speakers = speakers[:15]
	}
	risk, err := s.loadSentimentCalls(ctx, "negative", 10)
	if err != nil {
		return nil, err
	}
	positive, err := s.loadSentimentCalls(ctx, "positive", 10)
	if err != nil {
		return nil, err
	}
	dominance, err := s.loadTalkDominance(ctx)
	if err != nil {
		return nil, err
	}
	gaps, err := s.loadQuestionGaps(ctx)
	if err != nil {
		return nil, err
	}

	return &InsightsData{
		Stats:              stats,
		MonthlySentiment:   monthly,
		People:             people,
		SpeakerPerformance: speakers,
		RiskCalls:          risk,
		PositiveCalls:      positive,
		TalkDominance:      dominance,
		QuestionGaps:       gaps,
		Suggestions:        coachingSuggestions(stats, speakers, risk, gaps),
	}, nil
}

func (s *Server) buildPeopleStats(ctx context.Context) ([]PersonStats, error) {
	people := map[string]*PersonStats{}
	ensure := func(rawLabel, rawEmail string) *PersonStats {
		label := strings.TrimSpace(rawLabel)
		email := strings.TrimSpace(rawEmail)
		if label == "" {
			label = email
		}
		key := personKey(label, email)
		if key == "" {
			return nil
		}
		person := people[key]
		if person == nil {
			person = &PersonStats{
				Key:              key,
				Label:            personLabel(label, email),
				Email:            email,
				callIDs:          map[string]struct{}{},
				spokenIDs:        map[string]struct{}{},
				attendeeIDs:      map[string]struct{}{},
				organizerIDs:     map[string]struct{}{},
				hostIDs:          map[string]struct{}{},
				sentimentCallIDs: map[string]struct{}{},
			}
			people[key] = person
		}
		if person.Email == "" && email != "" {
			person.Email = email
		}
		if person.Label == "" || strings.Contains(person.Label, "@") && label != "" && !strings.Contains(label, "@") {
			person.Label = personLabel(label, email)
		}
		return person
	}

	rows, err := s.db.QueryContext(ctx, `SELECT
		t.id, COALESCE(t.date_string, ''), COALESCE(t.duration_minutes, 0),
		COALESCE(t.organizer_email, ''), COALESCE(t.host_email, ''),
		COALESCE(t.participants_json, ''), COALESCE(a.positive_pct, 0), COALESCE(a.negative_pct, 0)
		FROM transcripts t
		LEFT JOIN analytics_overview a ON a.transcript_id = t.id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, dateString, organizer, host, participants string
		var duration, positive, negative float64
		if err := rows.Scan(&id, &dateString, &duration, &organizer, &host, &participants, &positive, &negative); err != nil {
			rows.Close()
			return nil, err
		}
		if person := ensure(organizer, organizer); person != nil {
			person.addCall(id, duration, dateString)
			person.addSentiment(id, positive, negative)
			person.organizerIDs[id] = struct{}{}
		}
		if person := ensure(host, host); person != nil {
			person.addCall(id, duration, dateString)
			person.addSentiment(id, positive, negative)
			person.hostIDs[id] = struct{}{}
		}
		for _, participant := range participantValues(participants) {
			if person := ensure(participant, participant); person != nil {
				person.addCall(id, duration, dateString)
				person.addSentiment(id, positive, negative)
				person.attendeeIDs[id] = struct{}{}
			}
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := s.addAttendeePeople(ctx, ensure); err != nil {
		return nil, err
	}
	if err := s.addSpeakerPeople(ctx, ensure); err != nil {
		return nil, err
	}

	out := make([]PersonStats, 0, len(people))
	for _, person := range people {
		person.finalize()
		if person.Calls > 0 || person.SpokenCalls > 0 {
			out = append(out, *person)
		}
	}
	return out, nil
}

func (s *Server) addAttendeePeople(ctx context.Context, ensure func(string, string) *PersonStats) error {
	rows, err := s.db.QueryContext(ctx, `SELECT
		ma.transcript_id, COALESCE(ma.display_name, ''), COALESCE(ma.email, ''),
		COALESCE(ma.name, ''), COALESCE(t.duration_minutes, 0), COALESCE(t.date_string, ''),
		COALESCE(a.positive_pct, 0), COALESCE(a.negative_pct, 0)
		FROM meeting_attendees ma
		JOIN transcripts t ON t.id = ma.transcript_id
		LEFT JOIN analytics_overview a ON a.transcript_id = t.id`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id, displayName, email, name, dateString string
		var duration, positive, negative float64
		if err := rows.Scan(&id, &displayName, &email, &name, &duration, &dateString, &positive, &negative); err != nil {
			return err
		}
		label := displayName
		if label == "" {
			label = name
		}
		person := ensure(label, email)
		if person == nil {
			continue
		}
		person.addCall(id, duration, dateString)
		person.addSentiment(id, positive, negative)
		person.attendeeIDs[id] = struct{}{}
	}
	return rows.Err()
}

func (s *Server) addSpeakerPeople(ctx context.Context, ensure func(string, string) *PersonStats) error {
	rows, err := s.db.QueryContext(ctx, `SELECT
		a.transcript_id, COALESCE(a.name, ''), COALESCE(a.duration, 0),
		COALESCE(a.word_count, 0), COALESCE(a.questions, 0), COALESCE(a.duration_pct, 0),
		COALESCE(a.words_per_minute, 0), COALESCE(a.longest_monologue, 0), COALESCE(a.filler_words, 0),
		COALESCE(t.duration_minutes, 0), COALESCE(t.date_string, ''),
		COALESCE(o.positive_pct, 0), COALESCE(o.negative_pct, 0)
		FROM analytics_speakers a
		JOIN transcripts t ON t.id = a.transcript_id
		LEFT JOIN analytics_overview o ON o.transcript_id = t.id
		WHERE COALESCE(a.name, '') <> ''`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id, name, dateString string
		var talkMinutes, durationPct, wordsPerMinute, longestMonologue, callDuration, positive, negative float64
		var wordCount, questions, fillerWords int
		if err := rows.Scan(&id, &name, &talkMinutes, &wordCount, &questions, &durationPct, &wordsPerMinute, &longestMonologue, &fillerWords, &callDuration, &dateString, &positive, &negative); err != nil {
			return err
		}
		person := ensure(name, "")
		if person == nil {
			continue
		}
		person.addCall(id, callDuration, dateString)
		person.addSentiment(id, positive, negative)
		person.spokenIDs[id] = struct{}{}
		person.TalkMinutes += talkMinutes
		person.WordCount += wordCount
		person.Questions += questions
		person.FillerWords += fillerWords
		if longestMonologue > person.LongestMonologue {
			person.LongestMonologue = longestMonologue
		}
		_ = durationPct
		_ = wordsPerMinute
	}
	return rows.Err()
}

func (s *Server) loadMonthlySentiment(ctx context.Context) ([]SentimentBucket, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		COALESCE(substr(t.date_string, 1, 7), 'Unknown'),
		COUNT(*),
		COALESCE(AVG(a.positive_pct), 0),
		COALESCE(AVG(a.neutral_pct), 0),
		COALESCE(AVG(a.negative_pct), 0)
		FROM transcripts t
		JOIN analytics_overview a ON a.transcript_id = t.id
		GROUP BY 1
		ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []SentimentBucket
	var positiveMax, negativeMax float64
	for rows.Next() {
		var item SentimentBucket
		if err := rows.Scan(&item.Label, &item.Calls, &item.Positive, &item.Neutral, &item.Negative); err != nil {
			return nil, err
		}
		if item.Positive > positiveMax {
			positiveMax = item.Positive
		}
		if item.Negative > negativeMax {
			negativeMax = item.Negative
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(items) > 18 {
		items = items[len(items)-18:]
	}
	for i := range items {
		items[i].PositiveMax = positiveMax
		items[i].NegativeMax = negativeMax
	}
	return items, nil
}

func (s *Server) loadSentimentCalls(ctx context.Context, mode string, limit int) ([]TranscriptRow, error) {
	order := "a.negative_pct DESC, a.positive_pct ASC"
	if mode == "positive" {
		order = "a.positive_pct DESC, a.negative_pct ASC"
	}
	return s.queryTranscriptRows(ctx, `SELECT
		t.id, t.title, t.date_string, COALESCE(t.duration_minutes, 0),
		t.organizer_email, t.host_email, COALESCE(s.meeting_type, ''),
		COALESCE(s.short_summary, ''), COALESCE(t.participants_json, ''),
		(SELECT COUNT(*) FROM sentences se WHERE se.transcript_id = t.id),
		(SELECT COUNT(*) FROM downloaded_media dm WHERE dm.transcript_id = t.id AND COALESCE(dm.path, '') <> ''),
		COALESCE(a.positive_pct, 0), COALESCE(a.negative_pct, 0)
		FROM transcripts t
		LEFT JOIN summaries s ON s.transcript_id = t.id
		JOIN analytics_overview a ON a.transcript_id = t.id
		ORDER BY `+order+`
		LIMIT ?`, limit)
}

func (s *Server) loadTalkDominance(ctx context.Context) ([]CallCoachingMetric, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		t.id, t.title, t.date_string, COALESCE(a.name, ''), COALESCE(a.duration_pct, 0)
		FROM analytics_speakers a
		JOIN transcripts t ON t.id = a.transcript_id
		WHERE COALESCE(a.duration_pct, 0) >= 55
		ORDER BY a.duration_pct DESC
		LIMIT 12`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []CallCoachingMetric
	for rows.Next() {
		var item CallCoachingMetric
		if err := rows.Scan(&item.TranscriptID, &item.Title, &item.DateString, &item.SpeakerName, &item.Value); err != nil {
			return nil, err
		}
		item.Helper = "talk share"
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Server) loadQuestionGaps(ctx context.Context) ([]CallCoachingMetric, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		t.id, t.title, t.date_string, '', COALESCE(SUM(a.questions), 0)
		FROM transcripts t
		LEFT JOIN analytics_speakers a ON a.transcript_id = t.id
		GROUP BY t.id
		HAVING COALESCE(SUM(a.questions), 0) <= 2 AND COALESCE(t.duration_minutes, 0) >= 10
		ORDER BY t.date_ms DESC
		LIMIT 12`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []CallCoachingMetric
	for rows.Next() {
		var item CallCoachingMetric
		if err := rows.Scan(&item.TranscriptID, &item.Title, &item.DateString, &item.SpeakerName, &item.Value); err != nil {
			return nil, err
		}
		item.Helper = "questions"
		items = append(items, item)
	}
	return items, rows.Err()
}

func coachingSuggestions(stats Stats, speakers []PersonStats, risk []TranscriptRow, gaps []CallCoachingMetric) []CoachingSuggestion {
	var suggestions []CoachingSuggestion
	if stats.AverageMinutes > 45 {
		suggestions = append(suggestions, CoachingSuggestion{
			Title: "Shorten discovery loops",
			Body:  fmt.Sprintf("Average call length is %s. Review long calls for repeated explanations and move recurring context into follow-up material.", formatDuration(stats.AverageMinutes)),
		})
	}
	if len(gaps) > 0 {
		suggestions = append(suggestions, CoachingSuggestion{
			Title: "Ask more explicit questions",
			Body:  fmt.Sprintf("%d recent calls had two or fewer tracked questions. Aim for clear discovery questions before pitching.", len(gaps)),
		})
	}
	if len(risk) > 0 && risk[0].NegativePct >= 25 {
		suggestions = append(suggestions, CoachingSuggestion{
			Title: "Review high-friction calls",
			Body:  fmt.Sprintf("The highest negative sentiment call is at %.0f%%. Compare its transcript with high-positive calls to find objection patterns.", risk[0].NegativePct),
		})
	}
	for _, speaker := range speakers {
		if speaker.TalkPct >= 55 && speaker.SpokenCalls >= 3 {
			suggestions = append(suggestions, CoachingSuggestion{
				Title: "Balance talk time",
				Body:  fmt.Sprintf("%s averages %.0f%% talk share across speaking calls. Try summarizing sooner and handing the floor back with one targeted question.", speaker.Label, speaker.TalkPct),
			})
			break
		}
	}
	if len(suggestions) == 0 {
		suggestions = append(suggestions, CoachingSuggestion{
			Title: "Use the archive as a review loop",
			Body:  "Compare high-positive calls against negative or low-question calls. Look for which openings, questions, and handoffs change the conversation quality.",
		})
	}
	return suggestions
}

func (p *PersonStats) addCall(id string, duration float64, dateString string) {
	if id == "" {
		return
	}
	if _, ok := p.callIDs[id]; !ok {
		p.callIDs[id] = struct{}{}
		p.TotalMinutes += duration
	}
	if dateString > p.LastSeen {
		p.LastSeen = dateString
	}
}

func (p *PersonStats) addSentiment(id string, positive, negative float64) {
	if id == "" {
		return
	}
	if _, ok := p.sentimentCallIDs[id]; ok {
		return
	}
	p.sentimentCallIDs[id] = struct{}{}
	p.PositiveAvg += positive
	p.NegativeAvg += negative
}

func (p *PersonStats) finalize() {
	p.Calls = len(p.callIDs)
	p.SpokenCalls = len(p.spokenIDs)
	p.AttendeeCalls = len(p.attendeeIDs)
	p.OrganizerCalls = len(p.organizerIDs)
	p.HostCalls = len(p.hostIDs)
	if p.TotalMinutes > 0 {
		p.TalkPct = p.TalkMinutes / p.TotalMinutes * 100
	}
	if p.TalkMinutes > 0 {
		p.WordsPerMinute = float64(p.WordCount) / p.TalkMinutes
		p.QuestionRate = float64(p.Questions) / p.TalkMinutes * 60
	}
	if calls := len(p.sentimentCallIDs); calls > 0 {
		p.PositiveAvg = p.PositiveAvg / float64(calls)
		p.NegativeAvg = p.NegativeAvg / float64(calls)
	}
}

func sortPeople(people []PersonStats) {
	sort.Slice(people, func(i, j int) bool {
		if people[i].Calls == people[j].Calls {
			if people[i].TalkMinutes == people[j].TalkMinutes {
				return strings.ToLower(people[i].Label) < strings.ToLower(people[j].Label)
			}
			return people[i].TalkMinutes > people[j].TalkMinutes
		}
		return people[i].Calls > people[j].Calls
	})
}

func personKey(label, email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	label = strings.ToLower(strings.TrimSpace(label))
	if email != "" && strings.Contains(email, "@") {
		return email
	}
	if strings.Contains(label, "@") {
		return label
	}
	return label
}

func personLabel(label, email string) string {
	label = strings.TrimSpace(label)
	email = strings.TrimSpace(email)
	if label != "" && !strings.Contains(label, "@") {
		return label
	}
	if email != "" {
		return email
	}
	return label
}

func participantValues(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" || value == "null" {
		return nil
	}
	var raw []string
	if err := json.Unmarshal([]byte(value), &raw); err != nil {
		return splitLoosePeople(value)
	}
	var out []string
	for _, item := range raw {
		out = append(out, splitLoosePeople(item)...)
	}
	return uniqueStrings(out)
}

func splitLoosePeople(value string) []string {
	value = strings.Trim(value, `[]" `)
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\t'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, value := range values {
		key := strings.ToLower(strings.TrimSpace(value))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, strings.TrimSpace(value))
	}
	return out
}

func (s *Server) loadTranscript(ctx context.Context, id string) (TranscriptDetail, error) {
	var item TranscriptDetail
	var isLive sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT
		id, title, date_string, COALESCE(duration_minutes, 0), COALESCE(privacy, ''),
		COALESCE(host_email, ''), COALESCE(organizer_email, ''), COALESCE(calendar_id, ''),
		COALESCE(calendar_type, ''), COALESCE(meeting_link, ''), COALESCE(transcript_url, ''),
		COALESCE(audio_url, ''), COALESCE(video_url, ''), is_live,
		COALESCE(participants_json, ''), COALESCE(fireflies_users_json, ''), COALESCE(workspace_users_json, ''),
		COALESCE(user_email, ''), COALESCE(user_name, ''), COALESCE(raw_json_file, ''),
		COALESCE(raw_json, ''), COALESCE(last_exported_at, ''),
		(SELECT COUNT(*) FROM sentences WHERE transcript_id = transcripts.id),
		(SELECT COUNT(*) FROM speakers WHERE transcript_id = transcripts.id),
		(SELECT COUNT(*) FROM meeting_attendees WHERE transcript_id = transcripts.id),
		(SELECT COUNT(*) FROM downloaded_media WHERE transcript_id = transcripts.id AND COALESCE(path, '') <> '')
		FROM transcripts
		WHERE id = ?`, id).Scan(
		&item.ID,
		&item.Title,
		&item.DateString,
		&item.Duration,
		&item.Privacy,
		&item.HostEmail,
		&item.OrganizerEmail,
		&item.CalendarID,
		&item.CalendarType,
		&item.MeetingLink,
		&item.TranscriptURL,
		&item.AudioURL,
		&item.VideoURL,
		&isLive,
		&item.Participants,
		&item.FirefliesUsers,
		&item.WorkspaceUsers,
		&item.UserEmail,
		&item.UserName,
		&item.RawJSONFile,
		&item.RawJSON,
		&item.LastExportedAt,
		&item.SentenceCount,
		&item.SpeakerCount,
		&item.AttendeeCount,
		&item.MediaCount,
	)
	if err != nil {
		return item, err
	}
	item.IsLive = isLive.Valid && isLive.Int64 == 1
	item.Participants = compactJSONList(item.Participants)
	item.FirefliesUsers = compactJSONList(item.FirefliesUsers)
	item.WorkspaceUsers = compactJSONList(item.WorkspaceUsers)
	return item, nil
}

func (s *Server) loadSummary(ctx context.Context, id string) (Summary, error) {
	var item Summary
	err := s.db.QueryRowContext(ctx, `SELECT
		COALESCE(keywords, ''), COALESCE(action_items, ''), COALESCE(outline, ''),
		COALESCE(overview, ''), COALESCE(bullet_gist, ''), COALESCE(gist, ''),
		COALESCE(short_summary, ''), COALESCE(short_overview, ''), COALESCE(meeting_type, ''),
		COALESCE(topics_discussed_json, ''), COALESCE(transcript_chapters_json, ''),
		COALESCE(notes, ''), COALESCE(extended_sections_json, '')
		FROM summaries WHERE transcript_id = ?`, id).Scan(
		&item.Keywords,
		&item.ActionItems,
		&item.Outline,
		&item.Overview,
		&item.BulletGist,
		&item.Gist,
		&item.ShortSummary,
		&item.ShortOverview,
		&item.MeetingType,
		&item.TopicsDiscussedJSON,
		&item.TranscriptChaptersJSON,
		&item.Notes,
		&item.ExtendedSectionsJSON,
	)
	return item, err
}

func (s *Server) loadAnalyticsOverview(ctx context.Context, id string) (AnalyticsOverview, error) {
	var item AnalyticsOverview
	err := s.db.QueryRowContext(ctx, `SELECT
		COALESCE(negative_pct, 0), COALESCE(neutral_pct, 0), COALESCE(positive_pct, 0),
		COALESCE(category_questions, 0), COALESCE(category_date_times, 0),
		COALESCE(category_metrics, 0), COALESCE(category_tasks, 0)
		FROM analytics_overview WHERE transcript_id = ?`, id).Scan(
		&item.NegativePct,
		&item.NeutralPct,
		&item.PositivePct,
		&item.CategoryQuestions,
		&item.CategoryDateTimes,
		&item.CategoryMetrics,
		&item.CategoryTasks,
	)
	return item, err
}

func (s *Server) loadSentences(ctx context.Context, id string) ([]Sentence, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		sentence_index, COALESCE(speaker_id, ''), COALESCE(speaker_name, ''),
		COALESCE(start_time, ''), COALESCE(end_time, ''), COALESCE(text, ''),
		COALESCE(raw_text, ''), COALESCE(ai_filter_sentiment, '')
		FROM sentences WHERE transcript_id = ?
		ORDER BY sentence_index`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Sentence
	for rows.Next() {
		var item Sentence
		if err := rows.Scan(&item.Index, &item.SpeakerID, &item.SpeakerName, &item.StartTime, &item.EndTime, &item.Text, &item.RawText, &item.Sentiment); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Server) loadSpeakers(ctx context.Context, id string) ([]Speaker, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		COALESCE(s.speaker_id, ''), COALESCE(s.name, ''),
		COALESCE(a.duration, 0), COALESCE(a.word_count, 0), COALESCE(a.longest_monologue, 0),
		COALESCE(a.monologues_count, 0), COALESCE(a.filler_words, 0), COALESCE(a.questions, 0),
		COALESCE(a.duration_pct, 0), COALESCE(a.words_per_minute, 0)
		FROM speakers s
		LEFT JOIN analytics_speakers a
			ON a.transcript_id = s.transcript_id
			AND (a.speaker_id = s.speaker_id OR a.name = s.name)
		WHERE s.transcript_id = ?
		ORDER BY s.name`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Speaker
	for rows.Next() {
		var item Speaker
		if err := rows.Scan(&item.ID, &item.Name, &item.Duration, &item.WordCount, &item.LongestMonologue, &item.MonologuesCount, &item.FillerWords, &item.Questions, &item.DurationPct, &item.WordsPerMinute); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Server) loadAttendees(ctx context.Context, id string) ([]Attendee, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		COALESCE(display_name, ''), COALESCE(email, ''), COALESCE(phone_number, ''),
		COALESCE(name, ''), COALESCE(location, '')
		FROM meeting_attendees WHERE transcript_id = ?
		ORDER BY row_index`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Attendee
	for rows.Next() {
		var item Attendee
		if err := rows.Scan(&item.DisplayName, &item.Email, &item.PhoneNumber, &item.Name, &item.Location); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Server) loadAttendance(ctx context.Context, id string) ([]Attendance, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT COALESCE(name, ''), COALESCE(join_time, ''), COALESCE(leave_time, '')
		FROM meeting_attendance WHERE transcript_id = ?
		ORDER BY row_index`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Attendance
	for rows.Next() {
		var item Attendance
		if err := rows.Scan(&item.Name, &item.JoinTime, &item.LeaveTime); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Server) loadChannels(ctx context.Context, id string) ([]Channel, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT COALESCE(channel_id, ''), COALESCE(title, ''), COALESCE(is_private, 0), COALESCE(created_by, '')
		FROM channels WHERE transcript_id = ?
		ORDER BY title`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Channel
	for rows.Next() {
		var item Channel
		var isPrivate int
		if err := rows.Scan(&item.ID, &item.Title, &isPrivate, &item.CreatedBy); err != nil {
			return nil, err
		}
		item.IsPrivate = isPrivate == 1
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Server) loadSharedWith(ctx context.Context, id string) ([]SharedWith, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT COALESCE(email, ''), COALESCE(name, ''), COALESCE(photo_url, ''), COALESCE(expires_at, '')
		FROM shared_with WHERE transcript_id = ?
		ORDER BY email`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []SharedWith
	for rows.Next() {
		var item SharedWith
		if err := rows.Scan(&item.Email, &item.Name, &item.PhotoURL, &item.ExpiresAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Server) loadAppOutputs(ctx context.Context, id string) ([]AppOutput, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT COALESCE(app_id, ''), COALESCE(created_at, 0), COALESCE(title, ''), COALESCE(prompt, ''), COALESCE(response, '')
		FROM app_outputs WHERE transcript_id = ?
		ORDER BY row_index`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []AppOutput
	for rows.Next() {
		var item AppOutput
		if err := rows.Scan(&item.AppID, &item.CreatedAt, &item.Title, &item.Prompt, &item.Response); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Server) loadMedia(ctx context.Context, id string) ([]Media, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT COALESCE(kind, ''), COALESCE(path, ''), COALESCE(error, '')
		FROM downloaded_media WHERE transcript_id = ?
		ORDER BY kind`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Media
	for rows.Next() {
		var item Media
		if err := rows.Scan(&item.Kind, &item.Path, &item.Error); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func compactJSONList(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "null" {
		return ""
	}
	var items []string
	if err := scanJSONStringArray(value, &items); err == nil {
		return strings.Join(items, ", ")
	}
	return value
}

func scanJSONStringArray(value string, dest *[]string) error {
	var raw []any
	if err := jsonUnmarshal([]byte(value), &raw); err != nil {
		return err
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		text := strings.TrimSpace(fmt.Sprint(item))
		if text != "" {
			out = append(out, text)
		}
	}
	*dest = out
	return nil
}

var jsonUnmarshal = func(data []byte, v any) error {
	return jsonDecoder(data, v)
}

func jsonDecoder(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func nullString(value sql.NullString) string {
	if value.Valid {
		return value.String
	}
	return ""
}

func errorsIsNoRows(err error) bool {
	return err == sql.ErrNoRows
}
