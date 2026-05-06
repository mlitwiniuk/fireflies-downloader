package fireflies

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestListTranscriptsCapsPageSizeToMax(t *testing.T) {
	var requestedLimit float64
	client := NewClient(ClientOptions{
		Endpoint: "https://fireflies.test/graphql",
		APIKey:   "test",
		Timeout:  5 * time.Second,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			var request graphQLRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			requestedLimit, _ = request.Variables["limit"].(float64)
			return jsonResponse(http.StatusOK, `{"data":{"transcripts":[{"id":"one"}]}}`), nil
		})},
	})

	items, err := client.ListTranscripts(context.Background(), ListFilter{PageSize: 50, Max: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if requestedLimit != 1 {
		t.Fatalf("got limit %v, want 1", requestedLimit)
	}
}

func TestExecuteRetriesHTTP429(t *testing.T) {
	var calls int32
	client := NewClient(ClientOptions{
		Endpoint: "https://fireflies.test/graphql",
		APIKey:   "test",
		Timeout:  5 * time.Second,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if atomic.AddInt32(&calls, 1) == 1 {
				return textResponse(http.StatusTooManyRequests, "rate limited"), nil
			}
			return jsonResponse(http.StatusOK, `{"data":{"ok":true}}`), nil
		})},
		MaxRetries:   2,
		RetryMinWait: time.Millisecond,
		RetryMaxWait: time.Millisecond,
	})

	var data struct {
		OK bool `json:"ok"`
	}
	if err := client.Execute(context.Background(), `query { ok }`, nil, &data); err != nil {
		t.Fatal(err)
	}
	if !data.OK {
		t.Fatal("expected ok response")
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("got %d calls, want 2", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonResponse(status int, body string) *http.Response {
	response := textResponse(status, body)
	response.Header.Set("Content-Type", "application/json")
	return response
}

func textResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}
