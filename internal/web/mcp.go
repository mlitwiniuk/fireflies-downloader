package web

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	mcpProtocolVersion = "2025-06-18"
	mcpMaxBodyBytes    = 1 << 20
	mcpMaxTextBytes    = 120_000
	mcpMaxRawJSONBytes = 1_000_000
)

type mcpRequest struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

type mcpToolResult struct {
	Content           []mcpContent `json:"content"`
	StructuredContent any          `json:"structuredContent,omitempty"`
	IsError           bool         `json:"isError"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/mcp" {
		http.NotFound(w, r)
		return
	}
	if !validMCPOrigin(r) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	setMCPCORSHeaders(w, r)

	switch r.Method {
	case http.MethodOptions:
		w.Header().Set("Allow", "POST, OPTIONS")
		w.WriteHeader(http.StatusNoContent)
		return
	case http.MethodGet:
		w.Header().Set("Allow", "POST")
		http.Error(w, "SSE streams are not supported; use POST for JSON-RPC", http.StatusMethodNotAllowed)
		return
	case http.MethodPost:
	default:
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !s.authorizeMCP(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="fireflies-mcp"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req mcpRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, mcpMaxBodyBytes))
	if err := decoder.Decode(&req); err != nil {
		writeMCPError(w, nil, -32700, "parse error", err.Error())
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeMCPError(w, nil, -32700, "parse error", "request body must contain one JSON-RPC message")
		return
	}
	if req.JSONRPC != "2.0" || strings.TrimSpace(req.Method) == "" {
		writeMCPError(w, req.ID, -32600, "invalid request", nil)
		return
	}

	if req.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	result, rpcErr := s.handleMCPRequest(r.Context(), req)
	if rpcErr != nil {
		writeMCPError(w, req.ID, rpcErr.Code, rpcErr.Message, rpcErr.Data)
		return
	}
	writeMCPResult(w, req.ID, result)
}

func (s *Server) authorizeMCP(r *http.Request) bool {
	if s.mcpToken == "" {
		return true
	}
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(header) < len("Bearer ")+1 || !strings.EqualFold(header[:len("Bearer ")], "Bearer ") {
		return false
	}
	token := strings.TrimSpace(header[len("Bearer "):])
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.mcpToken)) == 1
}

func validMCPOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return isLoopbackHost(hostName(parsed.Host)) && isLoopbackHost(hostName(r.Host))
}

func setMCPCORSHeaders(w http.ResponseWriter, r *http.Request) {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "authorization, content-type, mcp-method, mcp-name, mcp-protocol-version, mcp-session-id")
	w.Header().Add("Vary", "Origin")
}

func hostName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(value, "[]")
}

func isLoopbackHost(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "localhost" {
		return true
	}
	ip := net.ParseIP(value)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) handleMCPRequest(ctx context.Context, req mcpRequest) (any, *mcpError) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{
					"listChanged": false,
				},
			},
			"serverInfo": map[string]any{
				"name":    "fireflies-downloader",
				"version": "0.1.0",
			},
			"instructions": "Read-only access to the local Fireflies SQLite archive. Use search tools before fetching full transcripts, and treat sentiment/coaching data as heuristic Fireflies analytics.",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": mcpTools()}, nil
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := decodeMCPParams(req.Params, &params); err != nil {
			return nil, &mcpError{Code: -32602, Message: "invalid params", Data: err.Error()}
		}
		if strings.TrimSpace(params.Name) == "" {
			return nil, &mcpError{Code: -32602, Message: "tool name is required"}
		}
		result, err := s.callMCPTool(ctx, params.Name, params.Arguments)
		if err != nil {
			return mcpErrorToolResult(err), nil
		}
		return result, nil
	default:
		return nil, &mcpError{Code: -32601, Message: "method not found"}
	}
}

func decodeMCPParams(raw json.RawMessage, dest any) error {
	if len(raw) == 0 || string(raw) == "null" {
		raw = []byte("{}")
	}
	return json.Unmarshal(raw, dest)
}

func writeMCPResult(w http.ResponseWriter, id *json.RawMessage, result any) {
	writeMCPResponse(w, mcpResponse{
		JSONRPC: "2.0",
		ID:      mcpResponseID(id),
		Result:  result,
	})
}

func writeMCPError(w http.ResponseWriter, id *json.RawMessage, code int, message string, data any) {
	writeMCPResponse(w, mcpResponse{
		JSONRPC: "2.0",
		ID:      mcpResponseID(id),
		Error:   &mcpError{Code: code, Message: message, Data: data},
	})
}

func writeMCPResponse(w http.ResponseWriter, response mcpResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("MCP-Protocol-Version", mcpProtocolVersion)
	_ = json.NewEncoder(w).Encode(response)
}

func mcpResponseID(id *json.RawMessage) json.RawMessage {
	if id == nil {
		return json.RawMessage("null")
	}
	return *id
}

func mcpTools() []mcpTool {
	readOnly := map[string]any{"readOnlyHint": true, "destructiveHint": false}
	return []mcpTool{
		{
			Name:        "search_transcripts",
			Title:       "Search Transcripts",
			Description: "Search Fireflies transcripts by full text, participant, organizer, meeting type, and sentiment.",
			InputSchema: objectSchema(map[string]any{
				"query":        stringSchema("Full-text search over title, summaries, transcript text, and participants."),
				"person":       stringSchema("Person key, email, or speaker name to filter calls."),
				"organizer":    stringSchema("Organizer email to filter calls."),
				"meeting_type": stringSchema("Fireflies summary meeting type."),
				"sentiment":    enumSchema("Sentiment filter.", []string{"positive", "negative", "mixed"}),
				"page":         integerSchema("1-based page number.", 1, 10_000),
				"limit":        integerSchema("Results per page. Defaults to 10, maximum 50.", 1, 50),
			}, nil),
			Annotations: readOnly,
		},
		{
			Name:        "get_transcript",
			Title:       "Get Transcript",
			Description: "Fetch one transcript with markdown summaries, analytics, speakers, attendees, media links, app outputs, and optional sentence text/raw JSON.",
			InputSchema: objectSchema(map[string]any{
				"id":                stringSchema("Transcript ID."),
				"include_sentences": booleanSchema("Include sentence-level transcript text. Defaults to true."),
				"sentence_limit":    integerSchema("Maximum sentences to return. Defaults to 200, maximum 1000.", 1, 1000),
				"include_raw_json":  booleanSchema("Include stored raw Fireflies JSON, truncated at 1 MB."),
			}, []string{"id"}),
			Annotations: readOnly,
		},
		{
			Name:        "list_people",
			Title:       "List People",
			Description: "Browse people found in organizers, hosts, attendees, participants, and speaker analytics.",
			InputSchema: objectSchema(map[string]any{
				"query": stringSchema("Filter people by label or email."),
				"sort":  enumSchema("Sort order.", []string{"calls", "talk_minutes", "negative", "positive", "questions"}),
				"limit": integerSchema("Maximum people to return. Defaults to 50, maximum 200.", 1, 200),
			}, nil),
			Annotations: readOnly,
		},
		{
			Name:        "get_person",
			Title:       "Get Person",
			Description: "Fetch one person's call history, sentiment, and speaking metrics.",
			InputSchema: objectSchema(map[string]any{
				"person": stringSchema("Person key, email, or name."),
				"limit":  integerSchema("Maximum calls to return. Defaults to 25, maximum 100.", 1, 100),
			}, []string{"person"}),
			Annotations: readOnly,
		},
		{
			Name:        "get_insights",
			Title:       "Get Insights",
			Description: "Return sales-coaching signals from Fireflies analytics: sentiment trend, high-friction calls, high-positive calls, talk dominance, question gaps, and suggestions.",
			InputSchema: objectSchema(map[string]any{
				"limit": integerSchema("Maximum rows per insight section. Defaults to 10, maximum 25.", 1, 25),
			}, nil),
			Annotations: readOnly,
		},
		{
			Name:        "database_schema",
			Title:       "Database Schema",
			Description: "List SQLite archive tables and columns.",
			InputSchema: objectSchema(map[string]any{
				"table": stringSchema("Optional table name. Omit to list all archive tables."),
			}, nil),
			Annotations: readOnly,
		},
		{
			Name:        "query_database",
			Title:       "Query Database",
			Description: "Run a bounded read-only SELECT/WITH query against the local SQLite archive.",
			InputSchema: objectSchema(map[string]any{
				"sql":   stringSchema("A single SELECT or WITH query. Statements are read-only and semicolons are rejected except for one trailing terminator."),
				"limit": integerSchema("Maximum rows to return. Defaults to 50, maximum 200.", 1, 200),
			}, []string{"sql"}),
			Annotations: readOnly,
		},
	}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func stringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func booleanSchema(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func integerSchema(description string, minimum, maximum int) map[string]any {
	return map[string]any{"type": "integer", "description": description, "minimum": minimum, "maximum": maximum}
}

func enumSchema(description string, values []string) map[string]any {
	return map[string]any{"type": "string", "description": description, "enum": values}
}

func (s *Server) callMCPTool(ctx context.Context, name string, args map[string]any) (mcpToolResult, error) {
	switch name {
	case "search_transcripts":
		return s.mcpSearchTranscripts(ctx, args)
	case "get_transcript":
		return s.mcpGetTranscript(ctx, args)
	case "list_people":
		return s.mcpListPeople(ctx, args)
	case "get_person":
		return s.mcpGetPerson(ctx, args)
	case "get_insights":
		return s.mcpGetInsights(ctx, args)
	case "database_schema":
		return s.mcpDatabaseSchema(ctx, args)
	case "query_database":
		return s.mcpQueryDatabase(ctx, args)
	default:
		return mcpToolResult{}, fmt.Errorf("unknown tool %q", name)
	}
}

func mcpDecodeArgs(args map[string]any, dest any) error {
	if args == nil {
		args = map[string]any{}
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dest)
}

func (s *Server) mcpSearchTranscripts(ctx context.Context, rawArgs map[string]any) (mcpToolResult, error) {
	var args struct {
		Query       string `json:"query"`
		Person      string `json:"person"`
		Organizer   string `json:"organizer"`
		MeetingType string `json:"meeting_type"`
		Sentiment   string `json:"sentiment"`
		Page        int    `json:"page"`
		Limit       int    `json:"limit"`
	}
	if err := mcpDecodeArgs(rawArgs, &args); err != nil {
		return mcpToolResult{}, err
	}
	if args.Page <= 0 {
		args.Page = 1
	}
	limit := clampInt(args.Limit, 10, 1, 50)
	result, err := s.searchTranscripts(ctx, TranscriptSearch{
		Query:       strings.TrimSpace(args.Query),
		Person:      strings.TrimSpace(args.Person),
		Organizer:   strings.TrimSpace(args.Organizer),
		MeetingType: strings.TrimSpace(args.MeetingType),
		Sentiment:   strings.TrimSpace(args.Sentiment),
		Page:        args.Page,
		PerPage:     limit,
	})
	if err != nil {
		return mcpToolResult{}, err
	}

	rows := make([]map[string]any, 0, len(result.Rows))
	for _, row := range result.Rows {
		rows = append(rows, transcriptRowPayload(row))
	}
	payload := map[string]any{
		"query":       args.Query,
		"total":       result.Total,
		"page":        result.Page,
		"total_pages": result.TotalPages,
		"rows":        rows,
	}
	return mcpJSONResult(fmt.Sprintf("Found %d matching transcripts.", result.Total), payload), nil
}

func (s *Server) mcpGetTranscript(ctx context.Context, rawArgs map[string]any) (mcpToolResult, error) {
	var args struct {
		ID               string `json:"id"`
		IncludeSentences *bool  `json:"include_sentences"`
		SentenceLimit    int    `json:"sentence_limit"`
		IncludeRawJSON   bool   `json:"include_raw_json"`
	}
	if err := mcpDecodeArgs(rawArgs, &args); err != nil {
		return mcpToolResult{}, err
	}
	args.ID = strings.TrimSpace(args.ID)
	if args.ID == "" {
		return mcpToolResult{}, errors.New("id is required")
	}
	data, err := s.loadTranscriptDetail(ctx, args.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return mcpToolResult{}, fmt.Errorf("transcript %q was not found", args.ID)
	}
	if err != nil {
		return mcpToolResult{}, err
	}

	includeSentences := true
	if args.IncludeSentences != nil {
		includeSentences = *args.IncludeSentences
	}
	sentenceLimit := clampInt(args.SentenceLimit, 200, 1, 1000)
	payload := transcriptDetailPayload(data, includeSentences, sentenceLimit, args.IncludeRawJSON)
	title := data.Transcript.Title
	if title == "" {
		title = data.Transcript.ID
	}
	return mcpJSONResult("Transcript: "+title, payload), nil
}

func (s *Server) mcpListPeople(ctx context.Context, rawArgs map[string]any) (mcpToolResult, error) {
	var args struct {
		Query string `json:"query"`
		Sort  string `json:"sort"`
		Limit int    `json:"limit"`
	}
	if err := mcpDecodeArgs(rawArgs, &args); err != nil {
		return mcpToolResult{}, err
	}
	people, err := s.buildPeopleStats(ctx)
	if err != nil {
		return mcpToolResult{}, err
	}
	sortMCPPeople(people, args.Sort)
	query := strings.ToLower(strings.TrimSpace(args.Query))
	limit := clampInt(args.Limit, 50, 1, 200)

	rows := make([]map[string]any, 0, minInt(limit, len(people)))
	for _, person := range people {
		if query != "" && !strings.Contains(strings.ToLower(person.Label+" "+person.Email+" "+person.Key), query) {
			continue
		}
		rows = append(rows, personPayload(person))
		if len(rows) >= limit {
			break
		}
	}
	payload := map[string]any{"people": rows, "limit": limit}
	return mcpJSONResult(fmt.Sprintf("Returned %d people.", len(rows)), payload), nil
}

func (s *Server) mcpGetPerson(ctx context.Context, rawArgs map[string]any) (mcpToolResult, error) {
	var args struct {
		Person string `json:"person"`
		Limit  int    `json:"limit"`
	}
	if err := mcpDecodeArgs(rawArgs, &args); err != nil {
		return mcpToolResult{}, err
	}
	needle := strings.ToLower(strings.TrimSpace(args.Person))
	if needle == "" {
		return mcpToolResult{}, errors.New("person is required")
	}
	people, err := s.buildPeopleStats(ctx)
	if err != nil {
		return mcpToolResult{}, err
	}
	sortPeople(people)

	var selected *PersonStats
	var suggestions []map[string]any
	for i := range people {
		person := &people[i]
		haystack := strings.ToLower(person.Key + " " + person.Label + " " + person.Email)
		if person.Key == needle || strings.ToLower(person.Email) == needle || strings.ToLower(person.Label) == needle {
			selected = person
			break
		}
		if strings.Contains(haystack, needle) && len(suggestions) < 8 {
			copy := *person
			suggestions = append(suggestions, personPayload(copy))
			if selected == nil {
				selected = person
			}
		}
	}
	if selected == nil {
		return mcpJSONResult("No matching person found.", map[string]any{"suggestions": suggestions}), nil
	}

	limit := clampInt(args.Limit, 25, 1, 100)
	result, err := s.searchTranscripts(ctx, TranscriptSearch{Person: selected.Key, Page: 1, PerPage: limit})
	if err != nil {
		return mcpToolResult{}, err
	}
	calls := make([]map[string]any, 0, len(result.Rows))
	for _, row := range result.Rows {
		calls = append(calls, transcriptRowPayload(row))
	}
	payload := map[string]any{
		"person": personPayload(*selected),
		"calls":  calls,
	}
	return mcpJSONResult("Person: "+selected.Label, payload), nil
}

func (s *Server) mcpGetInsights(ctx context.Context, rawArgs map[string]any) (mcpToolResult, error) {
	var args struct {
		Limit int `json:"limit"`
	}
	if err := mcpDecodeArgs(rawArgs, &args); err != nil {
		return mcpToolResult{}, err
	}
	limit := clampInt(args.Limit, 10, 1, 25)
	insights, err := s.loadInsights(ctx)
	if err != nil {
		return mcpToolResult{}, err
	}
	payload := map[string]any{
		"stats":               statsPayload(insights.Stats),
		"suggestions":         suggestionsPayload(insights.Suggestions),
		"monthly_sentiment":   sentimentPayload(insights.MonthlySentiment),
		"people":              peoplePayload(limitPeople(insights.People, limit)),
		"speaker_performance": peoplePayload(limitPeople(insights.SpeakerPerformance, limit)),
		"high_friction_calls": transcriptRowsPayload(limitTranscriptRows(insights.RiskCalls, limit)),
		"high_positive_calls": transcriptRowsPayload(limitTranscriptRows(insights.PositiveCalls, limit)),
		"talk_dominance":      coachingMetricsPayload(limitCoachingMetrics(insights.TalkDominance, limit)),
		"question_gaps":       coachingMetricsPayload(limitCoachingMetrics(insights.QuestionGaps, limit)),
	}
	return mcpJSONResult("Sales-coaching insights from the archive.", payload), nil
}

func (s *Server) mcpDatabaseSchema(ctx context.Context, rawArgs map[string]any) (mcpToolResult, error) {
	var args struct {
		Table string `json:"table"`
	}
	if err := mcpDecodeArgs(rawArgs, &args); err != nil {
		return mcpToolResult{}, err
	}
	tables, err := s.loadSchema(ctx, strings.TrimSpace(args.Table))
	if err != nil {
		return mcpToolResult{}, err
	}
	payload := map[string]any{"tables": tables}
	return mcpJSONResult(fmt.Sprintf("Returned schema for %d tables.", len(tables)), payload), nil
}

func (s *Server) mcpQueryDatabase(ctx context.Context, rawArgs map[string]any) (mcpToolResult, error) {
	var args struct {
		SQL   string `json:"sql"`
		Limit int    `json:"limit"`
	}
	if err := mcpDecodeArgs(rawArgs, &args); err != nil {
		return mcpToolResult{}, err
	}
	query, err := sanitizeReadOnlyQuery(args.SQL)
	if err != nil {
		return mcpToolResult{}, err
	}
	limit := clampInt(args.Limit, 50, 1, 200)
	queryCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	rows, err := s.db.QueryContext(queryCtx, "SELECT * FROM ("+query+") LIMIT ?", limit+1)
	if err != nil {
		return mcpToolResult{}, err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return mcpToolResult{}, err
	}
	resultRows := make([]map[string]any, 0, limit)
	truncated := false
	for rows.Next() {
		if len(resultRows) >= limit {
			truncated = true
			break
		}
		row, err := scanSQLRow(rows, columns)
		if err != nil {
			return mcpToolResult{}, err
		}
		resultRows = append(resultRows, row)
	}
	if err := rows.Err(); err != nil {
		return mcpToolResult{}, err
	}
	payload := map[string]any{
		"columns":   columns,
		"rows":      resultRows,
		"limit":     limit,
		"truncated": truncated,
	}
	return mcpJSONResult(fmt.Sprintf("Returned %d rows.", len(resultRows)), payload), nil
}

func (s *Server) loadSchema(ctx context.Context, tableFilter string) ([]map[string]any, error) {
	var rows *sql.Rows
	var err error
	if tableFilter != "" {
		rows, err = s.db.QueryContext(ctx, `SELECT name, type FROM sqlite_master
			WHERE name = ? AND type IN ('table', 'view')
			ORDER BY name`, tableFilter)
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT name, type FROM sqlite_master
			WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite_%'
			ORDER BY name`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []map[string]any
	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err != nil {
			return nil, err
		}
		columns, err := s.loadTableColumns(ctx, name)
		if err != nil {
			return nil, err
		}
		tables = append(tables, map[string]any{"name": name, "type": kind, "columns": columns})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if tableFilter != "" && len(tables) == 0 {
		return nil, fmt.Errorf("table %q was not found", tableFilter)
	}
	return tables, nil
}

func (s *Server) loadTableColumns(ctx context.Context, table string) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info("+quoteSQLiteIdentifier(table)+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []map[string]any
	for rows.Next() {
		var cid, notNull, pk int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		columns = append(columns, map[string]any{
			"name":        name,
			"type":        dataType,
			"not_null":    notNull == 1,
			"primary_key": pk > 0,
			"default":     normalizeSQLValue(defaultValue),
		})
	}
	return columns, rows.Err()
}

func quoteSQLiteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func sanitizeReadOnlyQuery(value string) (string, error) {
	query := stripLeadingSQLComments(strings.TrimSpace(value))
	query = strings.TrimSpace(query)
	if query == "" {
		return "", errors.New("sql is required")
	}
	query = strings.TrimSpace(strings.TrimSuffix(query, ";"))
	if strings.Contains(query, ";") {
		return "", errors.New("only one SQL statement is allowed")
	}
	keyword := firstSQLKeyword(query)
	if keyword != "SELECT" && keyword != "WITH" {
		return "", errors.New("only SELECT or WITH queries are allowed")
	}
	return query, nil
}

func stripLeadingSQLComments(value string) string {
	for {
		value = strings.TrimSpace(value)
		switch {
		case strings.HasPrefix(value, "--"):
			if index := strings.IndexByte(value, '\n'); index >= 0 {
				value = value[index+1:]
				continue
			}
			return ""
		case strings.HasPrefix(value, "/*"):
			if index := strings.Index(value, "*/"); index >= 0 {
				value = value[index+2:]
				continue
			}
			return ""
		default:
			return value
		}
	}
}

func firstSQLKeyword(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToUpper(fields[0])
}

func scanSQLRow(rows *sql.Rows, columns []string) (map[string]any, error) {
	values := make([]any, len(columns))
	destinations := make([]any, len(columns))
	for i := range values {
		destinations[i] = &values[i]
	}
	if err := rows.Scan(destinations...); err != nil {
		return nil, err
	}
	out := make(map[string]any, len(columns))
	for i, column := range columns {
		out[column] = normalizeSQLValue(values[i])
	}
	return out, nil
}

func normalizeSQLValue(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case []byte:
		return string(typed)
	case time.Time:
		return typed.Format(time.RFC3339Nano)
	default:
		return typed
	}
}

func mcpJSONResult(title string, payload any) mcpToolResult {
	return mcpToolResult{
		Content: []mcpContent{{
			Type: "text",
			Text: title + "\n\n" + marshalMCPTextJSON(payload),
		}},
		StructuredContent: payload,
		IsError:           false,
	}
}

func mcpErrorToolResult(err error) mcpToolResult {
	return mcpToolResult{
		Content: []mcpContent{{Type: "text", Text: err.Error()}},
		IsError: true,
	}
}

func marshalMCPTextJSON(value any) string {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	if len(raw) > mcpMaxTextBytes {
		raw = append(raw[:mcpMaxTextBytes], []byte("\n... truncated in text content; inspect structuredContent for the full bounded payload")...)
	}
	return string(raw)
}

func transcriptRowsPayload(rows []TranscriptRow) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, transcriptRowPayload(row))
	}
	return out
}

func transcriptRowPayload(row TranscriptRow) map[string]any {
	return map[string]any{
		"id":               row.ID,
		"title":            row.Title,
		"date":             row.DateString,
		"duration_minutes": row.Duration,
		"organizer_email":  row.OrganizerEmail,
		"host_email":       row.HostEmail,
		"meeting_type":     row.MeetingType,
		"short_summary":    row.ShortSummary,
		"participants":     row.Participants,
		"sentence_count":   row.SentenceCount,
		"media_count":      row.MediaCount,
		"positive_pct":     row.PositivePct,
		"negative_pct":     row.NegativePct,
	}
}

func transcriptDetailPayload(data *TranscriptDetailData, includeSentences bool, sentenceLimit int, includeRawJSON bool) map[string]any {
	transcript := map[string]any{
		"id":               data.Transcript.ID,
		"title":            data.Transcript.Title,
		"date":             data.Transcript.DateString,
		"duration_minutes": data.Transcript.Duration,
		"privacy":          data.Transcript.Privacy,
		"host_email":       data.Transcript.HostEmail,
		"organizer_email":  data.Transcript.OrganizerEmail,
		"calendar_id":      data.Transcript.CalendarID,
		"calendar_type":    data.Transcript.CalendarType,
		"meeting_link":     data.Transcript.MeetingLink,
		"transcript_url":   data.Transcript.TranscriptURL,
		"audio_url":        data.Transcript.AudioURL,
		"video_url":        data.Transcript.VideoURL,
		"is_live":          data.Transcript.IsLive,
		"participants":     data.Transcript.Participants,
		"fireflies_users":  data.Transcript.FirefliesUsers,
		"workspace_users":  data.Transcript.WorkspaceUsers,
		"user_email":       data.Transcript.UserEmail,
		"user_name":        data.Transcript.UserName,
		"raw_json_file":    data.Transcript.RawJSONFile,
		"last_exported_at": data.Transcript.LastExportedAt,
		"sentence_count":   data.Transcript.SentenceCount,
		"speaker_count":    data.Transcript.SpeakerCount,
		"attendee_count":   data.Transcript.AttendeeCount,
		"media_count":      data.Transcript.MediaCount,
	}
	if includeRawJSON {
		raw := data.Transcript.RawJSON
		truncated := false
		if len(raw) > mcpMaxRawJSONBytes {
			raw = strings.ToValidUTF8(raw[:mcpMaxRawJSONBytes], "")
			truncated = true
		}
		transcript["raw_json"] = raw
		transcript["raw_json_truncated"] = truncated
	}

	payload := map[string]any{
		"transcript":  transcript,
		"summary":     summaryPayload(data.Summary),
		"analytics":   analyticsPayload(data.Analytics),
		"speakers":    speakersPayload(data.Speakers),
		"attendees":   attendeesPayload(data.Attendees),
		"attendance":  attendancePayload(data.Attendance),
		"channels":    channelsPayload(data.Channels),
		"shared_with": sharedWithPayload(data.SharedWith),
		"app_outputs": appOutputsPayload(data.AppOutputs),
		"media":       mediaPayload(data.Media),
	}
	if includeSentences {
		payload["sentences"] = sentencesPayload(data.Sentences, sentenceLimit)
		payload["sentences_truncated"] = len(data.Sentences) > sentenceLimit
	}
	return payload
}

func summaryPayload(summary Summary) map[string]any {
	return map[string]any{
		"keywords":                 summary.Keywords,
		"action_items_markdown":    summary.ActionItems,
		"outline_markdown":         summary.Outline,
		"overview_markdown":        summary.Overview,
		"bullet_gist_markdown":     summary.BulletGist,
		"gist_markdown":            summary.Gist,
		"short_summary_markdown":   summary.ShortSummary,
		"short_overview_markdown":  summary.ShortOverview,
		"meeting_type":             summary.MeetingType,
		"topics_discussed_json":    summary.TopicsDiscussedJSON,
		"transcript_chapters_json": summary.TranscriptChaptersJSON,
		"notes_markdown":           summary.Notes,
		"extended_sections_json":   summary.ExtendedSectionsJSON,
	}
}

func analyticsPayload(analytics AnalyticsOverview) map[string]any {
	return map[string]any{
		"negative_pct":        analytics.NegativePct,
		"neutral_pct":         analytics.NeutralPct,
		"positive_pct":        analytics.PositivePct,
		"category_questions":  analytics.CategoryQuestions,
		"category_date_times": analytics.CategoryDateTimes,
		"category_metrics":    analytics.CategoryMetrics,
		"category_tasks":      analytics.CategoryTasks,
	}
}

func sentencesPayload(sentences []Sentence, limit int) []map[string]any {
	if len(sentences) > limit {
		sentences = sentences[:limit]
	}
	out := make([]map[string]any, 0, len(sentences))
	for _, sentence := range sentences {
		out = append(out, map[string]any{
			"index":        sentence.Index,
			"speaker_id":   sentence.SpeakerID,
			"speaker_name": sentence.SpeakerName,
			"start_time":   sentence.StartTime,
			"end_time":     sentence.EndTime,
			"text":         sentence.Text,
			"raw_text":     sentence.RawText,
			"sentiment":    sentence.Sentiment,
		})
	}
	return out
}

func speakersPayload(speakers []Speaker) []map[string]any {
	out := make([]map[string]any, 0, len(speakers))
	for _, speaker := range speakers {
		out = append(out, map[string]any{
			"id":                speaker.ID,
			"name":              speaker.Name,
			"duration_minutes":  speaker.Duration,
			"word_count":        speaker.WordCount,
			"longest_monologue": speaker.LongestMonologue,
			"monologues_count":  speaker.MonologuesCount,
			"filler_words":      speaker.FillerWords,
			"questions":         speaker.Questions,
			"duration_pct":      speaker.DurationPct,
			"words_per_minute":  speaker.WordsPerMinute,
		})
	}
	return out
}

func attendeesPayload(attendees []Attendee) []map[string]any {
	out := make([]map[string]any, 0, len(attendees))
	for _, attendee := range attendees {
		out = append(out, map[string]any{
			"display_name": attendee.DisplayName,
			"email":        attendee.Email,
			"phone_number": attendee.PhoneNumber,
			"name":         attendee.Name,
			"location":     attendee.Location,
		})
	}
	return out
}

func attendancePayload(attendance []Attendance) []map[string]any {
	out := make([]map[string]any, 0, len(attendance))
	for _, item := range attendance {
		out = append(out, map[string]any{
			"name":       item.Name,
			"join_time":  item.JoinTime,
			"leave_time": item.LeaveTime,
		})
	}
	return out
}

func channelsPayload(channels []Channel) []map[string]any {
	out := make([]map[string]any, 0, len(channels))
	for _, channel := range channels {
		out = append(out, map[string]any{
			"id":         channel.ID,
			"title":      channel.Title,
			"is_private": channel.IsPrivate,
			"created_by": channel.CreatedBy,
		})
	}
	return out
}

func sharedWithPayload(items []SharedWith) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"email":      item.Email,
			"name":       item.Name,
			"photo_url":  item.PhotoURL,
			"expires_at": item.ExpiresAt,
		})
	}
	return out
}

func appOutputsPayload(items []AppOutput) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"app_id":     item.AppID,
			"created_at": item.CreatedAt,
			"title":      item.Title,
			"prompt":     item.Prompt,
			"response":   item.Response,
		})
	}
	return out
}

func mediaPayload(items []Media) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"kind":  item.Kind,
			"path":  item.Path,
			"error": item.Error,
		})
	}
	return out
}

func peoplePayload(people []PersonStats) []map[string]any {
	out := make([]map[string]any, 0, len(people))
	for _, person := range people {
		out = append(out, personPayload(person))
	}
	return out
}

func personPayload(person PersonStats) map[string]any {
	return map[string]any{
		"key":               person.Key,
		"label":             person.Label,
		"email":             person.Email,
		"calls":             person.Calls,
		"spoken_calls":      person.SpokenCalls,
		"attendee_calls":    person.AttendeeCalls,
		"organizer_calls":   person.OrganizerCalls,
		"host_calls":        person.HostCalls,
		"total_minutes":     person.TotalMinutes,
		"talk_minutes":      person.TalkMinutes,
		"talk_pct":          person.TalkPct,
		"word_count":        person.WordCount,
		"questions":         person.Questions,
		"question_rate":     person.QuestionRate,
		"words_per_minute":  person.WordsPerMinute,
		"longest_monologue": person.LongestMonologue,
		"filler_words":      person.FillerWords,
		"positive_avg":      person.PositiveAvg,
		"negative_avg":      person.NegativeAvg,
		"last_seen":         person.LastSeen,
	}
}

func statsPayload(stats Stats) map[string]any {
	return map[string]any{
		"transcripts":        stats.Transcripts,
		"sentences":          stats.Sentences,
		"distinct_speakers":  stats.DistinctSpeakers,
		"media_files":        stats.MediaFiles,
		"total_minutes":      stats.TotalMinutes,
		"average_minutes":    stats.AverageMinutes,
		"first_date":         stats.FirstDate,
		"last_date":          stats.LastDate,
		"participants_count": stats.ParticipantsCount,
	}
}

func suggestionsPayload(items []CoachingSuggestion) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{"title": item.Title, "body": item.Body})
	}
	return out
}

func sentimentPayload(items []SentimentBucket) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"label":        item.Label,
			"positive_pct": item.Positive,
			"neutral_pct":  item.Neutral,
			"negative_pct": item.Negative,
			"calls":        item.Calls,
		})
	}
	return out
}

func coachingMetricsPayload(items []CallCoachingMetric) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"transcript_id": item.TranscriptID,
			"title":         item.Title,
			"date":          item.DateString,
			"speaker_name":  item.SpeakerName,
			"value":         item.Value,
			"helper":        item.Helper,
		})
	}
	return out
}

func sortMCPPeople(people []PersonStats, mode string) {
	switch strings.TrimSpace(mode) {
	case "talk_minutes":
		sort.Slice(people, func(i, j int) bool { return people[i].TalkMinutes > people[j].TalkMinutes })
	case "negative":
		sort.Slice(people, func(i, j int) bool { return people[i].NegativeAvg > people[j].NegativeAvg })
	case "positive":
		sort.Slice(people, func(i, j int) bool { return people[i].PositiveAvg > people[j].PositiveAvg })
	case "questions":
		sort.Slice(people, func(i, j int) bool { return people[i].Questions > people[j].Questions })
	default:
		sortPeople(people)
	}
}

func limitPeople(items []PersonStats, limit int) []PersonStats {
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitTranscriptRows(items []TranscriptRow, limit int) []TranscriptRow {
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitCoachingMetrics(items []CallCoachingMetric, limit int) []CallCoachingMetric {
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}

func clampInt(value, fallback, minimum, maximum int) int {
	if value == 0 {
		value = fallback
	}
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
