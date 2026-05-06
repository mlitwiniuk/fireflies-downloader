package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mlitwiniuk/fireflies-downloader/internal/archive"
)

func TestMCPInitializeAndTools(t *testing.T) {
	server := newTestMCPServer(t, "secret")
	defer server.Close()

	initResponse := postMCP(t, server, "secret", `{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "initialize",
		"params": {
			"protocolVersion": "2025-06-18",
			"capabilities": {},
			"clientInfo": {"name": "test", "version": "0"}
		}
	}`)
	if initResponse["error"] != nil {
		t.Fatalf("initialize returned error: %#v", initResponse["error"])
	}
	result := initResponse["result"].(map[string]any)
	if result["protocolVersion"] != mcpProtocolVersion {
		t.Fatalf("got protocol version %v", result["protocolVersion"])
	}

	listResponse := postMCP(t, server, "secret", `{
		"jsonrpc": "2.0",
		"id": 2,
		"method": "tools/list"
	}`)
	if listResponse["error"] != nil {
		t.Fatalf("tools/list returned error: %#v", listResponse["error"])
	}
	tools := listResponse["result"].(map[string]any)["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("expected tools to be listed")
	}
	if !mcpToolListed(tools, "search_transcripts") {
		t.Fatal("search_transcripts was not listed")
	}
}

func TestMCPToolCalls(t *testing.T) {
	server := newTestMCPServer(t, "")
	defer server.Close()

	searchResponse := postMCP(t, server, "", `{
		"jsonrpc": "2.0",
		"id": "search",
		"method": "tools/call",
		"params": {
			"name": "search_transcripts",
			"arguments": {"query": "renewal", "limit": 5}
		}
	}`)
	assertMCPToolOK(t, searchResponse)
	searchPayload := toolStructuredContent(t, searchResponse)
	if int(searchPayload["total"].(float64)) != 1 {
		t.Fatalf("got search total %v", searchPayload["total"])
	}

	transcriptResponse := postMCP(t, server, "", `{
		"jsonrpc": "2.0",
		"id": "transcript",
		"method": "tools/call",
		"params": {
			"name": "get_transcript",
			"arguments": {"id": "transcript-1", "sentence_limit": 1}
		}
	}`)
	assertMCPToolOK(t, transcriptResponse)
	transcriptPayload := toolStructuredContent(t, transcriptResponse)
	summary := transcriptPayload["summary"].(map[string]any)
	if summary["short_summary_markdown"] != "**Discussed** renewal budget." {
		t.Fatalf("markdown summary was not preserved: %#v", summary["short_summary_markdown"])
	}

	sqlResponse := postMCP(t, server, "", `{
		"jsonrpc": "2.0",
		"id": "sql",
		"method": "tools/call",
		"params": {
			"name": "query_database",
			"arguments": {"sql": "SELECT id, title FROM transcripts", "limit": 5}
		}
	}`)
	assertMCPToolOK(t, sqlResponse)
	sqlPayload := toolStructuredContent(t, sqlResponse)
	rows := sqlPayload["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("got %d SQL rows", len(rows))
	}

	writeResponse := postMCP(t, server, "", `{
		"jsonrpc": "2.0",
		"id": "write",
		"method": "tools/call",
		"params": {
			"name": "query_database",
			"arguments": {"sql": "DELETE FROM transcripts"}
		}
	}`)
	result := writeResponse["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("expected write query to be returned as a tool error: %#v", result)
	}
}

func TestMCPSecurityChecks(t *testing.T) {
	server := newTestMCPServer(t, "secret")
	defer server.Close()

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized without token, got %d", recorder.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "http://127.0.0.1/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Origin", "https://example.com")
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden bad origin, got %d", recorder.Code)
	}
}

func newTestMCPServer(t *testing.T, token string) *Server {
	t.Helper()
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "transcript-1.json")
	raw := `{
	  "id": "transcript-1",
	  "title": "Renewal Call",
	  "date": 1778068800000,
	  "dateString": "2026-05-06T12:00:00.000Z",
	  "duration": 42,
	  "organizer_email": "owner@example.com",
	  "host_email": "owner@example.com",
	  "participants": ["buyer@example.com"],
	  "summary": {
	    "keywords": ["renewal", "budget"],
	    "short_summary": "**Discussed** renewal budget.",
	    "meeting_type": "sales",
	    "topics_discussed": ["pricing"]
	  },
	  "sentences": [
	    {
	      "index": 0,
	      "speaker_name": "Alice",
	      "text": "We discussed the renewal budget.",
	      "raw_text": "We discussed the renewal budget.",
	      "start_time": "0",
	      "end_time": "3",
	      "ai_filters": {"sentiment": "positive"}
	    }
	  ],
	  "speakers": [{"id": "1", "name": "Alice"}],
	  "meeting_attendees": [{"displayName": "Buyer", "email": "buyer@example.com"}],
	  "analytics": {
	    "sentiments": {"positive_pct": 70, "neutral_pct": 25, "negative_pct": 5},
	    "categories": {"questions": 4, "date_times": 1, "metrics": 2, "tasks": 1},
	    "speakers": [{
	      "speaker_id": "1",
	      "name": "Alice",
	      "duration": 20,
	      "word_count": 500,
	      "questions": 4,
	      "duration_pct": 48,
	      "words_per_minute": 150
	    }]
	  }
	}`
	if err := os.WriteFile(transcriptPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := archive.DownloadManifest{
		ExportedAt: "2026-05-06T12:30:00Z",
		Endpoint:   "https://api.fireflies.ai/graphql",
		Profile:    "complete",
		Count:      1,
		Succeeded:  1,
		Items: []archive.DownloadResult{{
			ID:         "transcript-1",
			Title:      "Renewal Call",
			DateString: "2026-05-06T12:00:00.000Z",
			File:       transcriptPath,
			Profile:    "complete",
		}},
	}
	dbPath := filepath.Join(dir, "fireflies.sqlite")
	if err := archive.WriteSQLiteExports(dbPath, manifest); err != nil {
		t.Fatal(err)
	}
	server, err := New(Options{DBPath: dbPath, MCPToken: token})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func postMCP(t *testing.T, server *Server, token, body string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("got HTTP %d: %s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func mcpToolListed(tools []any, name string) bool {
	for _, item := range tools {
		tool := item.(map[string]any)
		if tool["name"] == name {
			return true
		}
	}
	return false
}

func assertMCPToolOK(t *testing.T, response map[string]any) {
	t.Helper()
	if response["error"] != nil {
		t.Fatalf("tool call returned protocol error: %#v", response["error"])
	}
	result := response["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("tool call returned tool error: %#v", result["content"])
	}
}

func toolStructuredContent(t *testing.T, response map[string]any) map[string]any {
	t.Helper()
	result := response["result"].(map[string]any)
	payload, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("missing structuredContent: %#v", result)
	}
	return payload
}
