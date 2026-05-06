package fireflies

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultEndpoint   = "https://api.fireflies.ai/graphql"
	DefaultMaxRetries = 8
	defaultUserAgent  = "fireflies-downloader/0.1"
)

type ClientOptions struct {
	Endpoint     string
	APIKey       string
	Timeout      time.Duration
	HTTPClient   *http.Client
	MaxRetries   int
	RequestDelay time.Duration
	RetryMinWait time.Duration
	RetryMaxWait time.Duration
	UserAgent    string
	OnRetry      func(RetryEvent)
}

type RetryEvent struct {
	Attempt int
	Delay   time.Duration
	Reason  string
}

type Client struct {
	endpoint     string
	apiKey       string
	httpClient   *http.Client
	maxRetries   int
	requestDelay time.Duration
	retryMinWait time.Duration
	retryMaxWait time.Duration
	userAgent    string
	onRetry      func(RetryEvent)

	throttleMu      sync.Mutex
	lastRequestTime time.Time
}

func NewClient(opts ClientOptions) *Client {
	if opts.Endpoint == "" {
		opts.Endpoint = DefaultEndpoint
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 60 * time.Second
	}
	if opts.MaxRetries < 0 {
		opts.MaxRetries = 0
	}
	if opts.RequestDelay < 0 {
		opts.RequestDelay = 0
	}
	if opts.RetryMinWait <= 0 {
		opts.RetryMinWait = 10 * time.Second
	}
	if opts.RetryMaxWait <= 0 {
		opts.RetryMaxWait = 5 * time.Minute
	}
	if opts.RetryMaxWait < opts.RetryMinWait {
		opts.RetryMaxWait = opts.RetryMinWait
	}
	if opts.UserAgent == "" {
		opts.UserAgent = defaultUserAgent
	}

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: opts.Timeout}
	}

	return &Client{
		endpoint:     opts.Endpoint,
		apiKey:       opts.APIKey,
		httpClient:   httpClient,
		maxRetries:   opts.MaxRetries,
		requestDelay: opts.RequestDelay,
		retryMinWait: opts.RetryMinWait,
		retryMaxWait: opts.RetryMaxWait,
		userAgent:    opts.UserAgent,
		onRetry:      opts.OnRetry,
	}
}

func (c *Client) Endpoint() string {
	return c.endpoint
}

func (c *Client) HTTPClient() *http.Client {
	return c.httpClient
}

type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

type graphQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors GraphQLErrors   `json:"errors"`
}

type GraphQLError struct {
	Message    string         `json:"message"`
	Path       []any          `json:"path,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

type GraphQLErrors []GraphQLError

func (e GraphQLErrors) Error() string {
	if len(e) == 0 {
		return "graphql error"
	}

	parts := make([]string, 0, len(e))
	for _, item := range e {
		if item.Message != "" {
			parts = append(parts, item.Message)
		}
	}
	if len(parts) == 0 {
		return "graphql error"
	}
	return strings.Join(parts, "; ")
}

func (e GraphQLErrors) retryAfter() (time.Duration, bool) {
	for _, item := range e {
		if !isRetryableGraphQLError(item) {
			continue
		}

		for _, key := range []string{"retryAfter", "retry_after"} {
			if raw, ok := item.Extensions[key]; ok {
				if duration, ok := parseRetryAfterValue(raw); ok {
					return duration, true
				}
			}
		}
	}

	return 0, false
}

func (e GraphQLErrors) retryable() bool {
	for _, item := range e {
		if isRetryableGraphQLError(item) {
			return true
		}
	}
	return false
}

func isRetryableGraphQLError(item GraphQLError) bool {
	code, _ := item.Extensions["code"].(string)
	status := numberLikeString(item.Extensions["status"])
	message := strings.ToLower(item.Message)

	if strings.EqualFold(code, "too_many_requests") || status == "429" {
		return true
	}
	return strings.Contains(message, "too many requests") ||
		strings.Contains(message, "rate limit") ||
		strings.Contains(message, "throttl")
}

func parseRetryAfterValue(raw any) (time.Duration, bool) {
	switch value := raw.(type) {
	case string:
		if value == "" {
			return 0, false
		}
		if duration, err := time.ParseDuration(value); err == nil {
			return duration, true
		}
		if ts, err := time.Parse(time.RFC3339, value); err == nil {
			return time.Until(ts), true
		}
		if number, err := strconv.ParseFloat(value, 64); err == nil {
			return retryAfterNumber(number)
		}
	case float64:
		return retryAfterNumber(value)
	case int:
		return retryAfterNumber(float64(value))
	case int64:
		return retryAfterNumber(float64(value))
	}

	return 0, false
}

func retryAfterNumber(value float64) (time.Duration, bool) {
	if value <= 0 {
		return 0, false
	}

	if value > 1_000_000_000_000 {
		return time.Until(time.UnixMilli(int64(value))), true
	}
	if value > 1_000_000_000 {
		return time.Until(time.Unix(int64(value), 0)), true
	}
	return time.Duration(value * float64(time.Second)), true
}

func (c *Client) Execute(ctx context.Context, query string, variables map[string]any, dataTarget any) error {
	body, err := json.Marshal(graphQLRequest{Query: query, Variables: variables})
	if err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if err := c.waitForRequestSlot(ctx); err != nil {
			return err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", c.userAgent)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		responseBody, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}

		var envelope graphQLResponse
		_ = json.Unmarshal(responseBody, &envelope)

		if len(envelope.Errors) > 0 {
			if attempt < c.maxRetries && envelope.Errors.retryable() {
				serverDelay, _ := envelope.Errors.retryAfter()
				delay := c.retryDelay(attempt, serverDelay)
				c.reportRetry(attempt+1, delay, envelope.Errors.Error())
				if err := sleepContext(ctx, delay); err != nil {
					return err
				}
				continue
			}
			return envelope.Errors
		}

		if isRetryableHTTPStatus(resp.StatusCode) && attempt < c.maxRetries {
			delay := c.retryDelay(attempt, retryAfterHeader(resp.Header.Get("Retry-After")))
			c.reportRetry(attempt+1, delay, fmt.Sprintf("HTTP %d", resp.StatusCode))
			if err := sleepContext(ctx, delay); err != nil {
				return err
			}
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("fireflies API returned HTTP %d: %s", resp.StatusCode, bodySnippet(responseBody))
		}

		if dataTarget == nil {
			return nil
		}
		if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
			return errors.New("fireflies API returned no data")
		}
		if err := json.Unmarshal(envelope.Data, dataTarget); err != nil {
			return err
		}
		return nil
	}

	return lastErr
}

func (c *Client) waitForRequestSlot(ctx context.Context) error {
	if c.requestDelay <= 0 {
		return nil
	}

	c.throttleMu.Lock()
	defer c.throttleMu.Unlock()

	now := time.Now()
	next := c.lastRequestTime.Add(c.requestDelay)
	if !c.lastRequestTime.IsZero() && now.Before(next) {
		if err := sleepContext(ctx, time.Until(next)); err != nil {
			return err
		}
	}
	c.lastRequestTime = time.Now()
	return nil
}

func (c *Client) retryDelay(attempt int, serverDelay time.Duration) time.Duration {
	if serverDelay > 0 {
		if serverDelay > c.retryMaxWait {
			return c.retryMaxWait
		}
		return serverDelay
	}

	delay := c.retryMinWait
	for i := 0; i < attempt; i++ {
		delay *= 2
		if delay >= c.retryMaxWait {
			return c.retryMaxWait
		}
	}
	if delay < c.retryMinWait {
		return c.retryMinWait
	}
	return delay
}

func (c *Client) reportRetry(attempt int, delay time.Duration, reason string) {
	if c.onRetry == nil {
		return
	}
	c.onRetry(RetryEvent{Attempt: attempt, Delay: delay, Reason: reason})
}

func isRetryableHTTPStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout,
		http.StatusTooManyRequests,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
		http.StatusInternalServerError:
		return true
	default:
		return false
	}
}

func retryAfterHeader(header string) time.Duration {
	if header == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(header); err == nil {
		return time.Duration(seconds) * time.Second
	}
	if ts, err := http.ParseTime(header); err == nil {
		return time.Until(ts)
	}
	return 0
}

func bodySnippet(body []byte) string {
	const max = 500
	text := strings.TrimSpace(string(body))
	if len(text) <= max {
		return text
	}
	return text[:max] + "..."
}

type ListFilter struct {
	PageSize     int
	Max          int
	FromDate     *time.Time
	ToDate       *time.Time
	UserID       string
	Mine         *bool
	Organizers   []string
	Participants []string
	ChannelID    string
	Keyword      string
	Scope        string
}

type TranscriptListItem struct {
	ID             string          `json:"id"`
	Title          string          `json:"title,omitempty"`
	Date           float64         `json:"date,omitempty"`
	DateString     string          `json:"dateString,omitempty"`
	Duration       json.RawMessage `json:"duration,omitempty"`
	OrganizerEmail string          `json:"organizer_email,omitempty"`
	HostEmail      string          `json:"host_email,omitempty"`
	Participants   []string        `json:"participants,omitempty"`
	TranscriptURL  string          `json:"transcript_url,omitempty"`
	IsLive         *bool           `json:"is_live,omitempty"`
}

func (c *Client) ListTranscripts(ctx context.Context, filter ListFilter, progress func(fetched int)) ([]TranscriptListItem, error) {
	pageSize := filter.PageSize
	if pageSize <= 0 || pageSize > 50 {
		pageSize = 50
	}

	var all []TranscriptListItem
	for skip := 0; ; skip += pageSize {
		currentPageSize := pageSize
		if filter.Max > 0 {
			remaining := filter.Max - len(all)
			if remaining <= 0 {
				return all, nil
			}
			if remaining < currentPageSize {
				currentPageSize = remaining
			}
		}

		var data struct {
			Transcripts []TranscriptListItem `json:"transcripts"`
		}
		variables := filter.variables(currentPageSize, skip)
		if err := c.Execute(ctx, listTranscriptsQuery, variables, &data); err != nil {
			return all, err
		}

		all = append(all, data.Transcripts...)
		if progress != nil {
			progress(len(all))
		}

		if filter.Max > 0 && len(all) >= filter.Max {
			all = all[:filter.Max]
			return all, nil
		}
		if len(data.Transcripts) < currentPageSize {
			return all, nil
		}
	}
}

func (f ListFilter) variables(limit, skip int) map[string]any {
	vars := map[string]any{
		"limit": limit,
		"skip":  skip,
	}
	if f.FromDate != nil {
		vars["fromDate"] = FormatDateTime(*f.FromDate)
	}
	if f.ToDate != nil {
		vars["toDate"] = FormatDateTime(*f.ToDate)
	}
	if f.UserID != "" {
		vars["userId"] = f.UserID
	}
	if f.Mine != nil {
		vars["mine"] = *f.Mine
	}
	if len(f.Organizers) > 0 {
		vars["organizers"] = f.Organizers
	}
	if len(f.Participants) > 0 {
		vars["participants"] = f.Participants
	}
	if f.ChannelID != "" {
		vars["channelId"] = f.ChannelID
	}
	if f.Keyword != "" {
		vars["keyword"] = f.Keyword
	}
	if f.Scope != "" {
		vars["scope"] = f.Scope
	}
	return vars
}

type TranscriptFetch struct {
	Raw     json.RawMessage
	Profile string
	Warning string
}

func (c *Client) GetTranscript(ctx context.Context, id, profile string) (json.RawMessage, error) {
	query, err := transcriptQuery(profile)
	if err != nil {
		return nil, err
	}

	var data struct {
		Transcript json.RawMessage `json:"transcript"`
	}
	if err := c.Execute(ctx, query, map[string]any{"transcriptId": id}, &data); err != nil {
		return nil, err
	}
	if len(data.Transcript) == 0 || string(data.Transcript) == "null" {
		return nil, fmt.Errorf("transcript %s was not returned", id)
	}
	return data.Transcript, nil
}

func (c *Client) GetTranscriptWithFallback(ctx context.Context, id, profile string, strict bool) (TranscriptFetch, error) {
	profiles := []string{profile}
	if !strict {
		profiles = fallbackProfiles(profile)
	}

	var firstErr error
	for index, current := range profiles {
		raw, err := c.GetTranscript(ctx, id, current)
		if err == nil {
			fetch := TranscriptFetch{Raw: raw, Profile: current}
			if index > 0 && firstErr != nil {
				fetch.Warning = fmt.Sprintf("profile %q failed, exported with %q: %v", profile, current, firstErr)
			}
			return fetch, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}

	if firstErr != nil {
		return TranscriptFetch{}, firstErr
	}
	return TranscriptFetch{}, fmt.Errorf("unable to fetch transcript %s", id)
}

func (c *Client) DeleteTranscript(ctx context.Context, id string) (json.RawMessage, error) {
	var data struct {
		DeleteTranscript json.RawMessage `json:"deleteTranscript"`
	}
	if err := c.Execute(ctx, deleteTranscriptMutation, map[string]any{"id": id}, &data); err != nil {
		return nil, err
	}
	if len(data.DeleteTranscript) == 0 || string(data.DeleteTranscript) == "null" {
		return nil, fmt.Errorf("deleteTranscript returned no payload for %s", id)
	}
	return data.DeleteTranscript, nil
}

func FormatDateTime(value time.Time) string {
	return value.UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z")
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func numberLikeString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	default:
		return ""
	}
}
