package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mlitwiniuk/fireflies-downloader/internal/app"
	"github.com/mlitwiniuk/fireflies-downloader/internal/fireflies"
	"github.com/mlitwiniuk/fireflies-downloader/internal/web"
)

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stdout)
		return nil
	}

	switch args[0] {
	case "download":
		return runDownload(ctx, args[1:], stdout, stderr)
	case "serve":
		return runServe(ctx, args[1:], stdout, stderr)
	case "delete-old":
		return runDeleteOld(ctx, args[1:], stdout, stderr)
	case "list":
		return runList(ctx, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runServe(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)

	dbPath := fs.String("db", filepath.Join("fireflies_export", "fireflies.sqlite"), "path to the exported SQLite database")
	addr := fs.String("addr", "127.0.0.1:8080", "HTTP listen address")
	mcpToken := fs.String("mcp-token", os.Getenv("FIREFLIES_MCP_TOKEN"), "optional bearer token for the /mcp endpoint; defaults to FIREFLIES_MCP_TOKEN")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	if strings.TrimSpace(*mcpToken) == "" && !isLoopbackListenAddr(*addr) {
		return fmt.Errorf("refusing to expose unauthenticated /mcp on non-loopback address %q; pass --mcp-token or bind to 127.0.0.1", *addr)
	}

	server, err := web.New(web.Options{DBPath: *dbPath, MCPToken: *mcpToken})
	if err != nil {
		return err
	}
	defer server.Close()

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(stdout, "Serving Fireflies archive at http://%s\n", *addr)
		fmt.Fprintf(stdout, "MCP endpoint: http://%s/mcp\n", *addr)
		if strings.TrimSpace(*mcpToken) != "" {
			fmt.Fprintln(stdout, "MCP authentication: bearer token required")
		}
		fmt.Fprintf(stdout, "Database: %s\n", *dbPath)
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return nil
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func isLoopbackListenAddr(addr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		host = strings.TrimSpace(addr)
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func runDownload(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("download", flag.ContinueOnError)
	fs.SetOutput(stderr)

	common := addCommonFlags(fs)
	filters := addFilterFlags(fs)
	outputDir := fs.String("output", "fireflies_export", "directory where transcript JSON files and manifest are written")
	profile := fs.String("profile", "complete", "data profile: complete, portable, or minimal")
	strictProfile := fs.Bool("strict-profile", false, "fail instead of falling back when the selected profile is unavailable")
	concurrency := fs.Int("concurrency", 1, "number of transcript detail workers; API requests are still globally paced")
	includeMedia := fs.Bool("include-media", false, "download audio/video files when audio_url/video_url are returned")
	noCSV := fs.Bool("no-csv", false, "skip generated CSV exports")
	noSQLite := fs.Bool("no-sqlite", false, "skip generated SQLite database")
	sqlitePath := fs.String("sqlite", "", "path to SQLite DB; defaults to <output>/fireflies.sqlite")
	overwrite := fs.Bool("overwrite", false, "overwrite existing transcript and media files")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	client, err := newClientFromFlags(common, stderr)
	if err != nil {
		return err
	}
	filter, err := buildListFilter(filters)
	if err != nil {
		return err
	}
	filter.PageSize = *common.pageSize
	dbPath := strings.TrimSpace(*sqlitePath)
	if dbPath == "" {
		dbPath = filepath.Join(*outputDir, "fireflies.sqlite")
	}

	opts := app.DownloadOptions{
		OutputDir:     *outputDir,
		Profile:       strings.ToLower(strings.TrimSpace(*profile)),
		StrictProfile: *strictProfile,
		Concurrency:   *concurrency,
		IncludeMedia:  *includeMedia,
		WriteCSV:      !*noCSV,
		WriteSQLite:   !*noSQLite,
		SQLitePath:    dbPath,
		Overwrite:     *overwrite,
	}
	return app.DownloadTranscripts(ctx, client, filter, opts, stdout)
}

func runDeleteOld(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("delete-old", flag.ContinueOnError)
	fs.SetOutput(stderr)

	common := addCommonFlags(fs)
	filters := addFilterFlags(fs)
	olderThan := fs.String("older-than", "3m", "retention window; m/mo means calendar months, also supports d, w, y, or Go durations")
	confirm := fs.Bool("confirm", false, "actually delete matching transcripts; omitted means dry run")
	deleteDelay := fs.Duration("delete-delay", 7*time.Second, "delay between delete requests")
	planFile := fs.String("plan-file", "fireflies_delete_plan.json", "where to write dry-run plan or delete log")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	client, err := newClientFromFlags(common, stderr)
	if err != nil {
		return err
	}
	filter, err := buildListFilter(filters)
	if err != nil {
		return err
	}
	filter.PageSize = *common.pageSize

	opts := app.DeleteOptions{
		OlderThan:   *olderThan,
		Confirm:     *confirm,
		DeleteDelay: *deleteDelay,
		PlanFile:    *planFile,
	}
	return app.DeleteOldTranscripts(ctx, client, filter, opts, stdout)
}

func runList(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(stderr)

	common := addCommonFlags(fs)
	filters := addFilterFlags(fs)

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	client, err := newClientFromFlags(common, stderr)
	if err != nil {
		return err
	}
	filter, err := buildListFilter(filters)
	if err != nil {
		return err
	}
	filter.PageSize = *common.pageSize

	items, err := client.ListTranscripts(ctx, filter, nil)
	if err != nil {
		return err
	}
	for _, item := range items {
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", item.ID, item.DateString, item.OrganizerEmail, item.Title)
	}
	return nil
}

type commonFlags struct {
	apiKey       *string
	endpoint     *string
	timeout      *time.Duration
	pageSize     *int
	requestDelay *time.Duration
	maxRetries   *int
	retryMinWait *time.Duration
	retryMaxWait *time.Duration
}

func addCommonFlags(fs *flag.FlagSet) commonFlags {
	defaultAPIKey := os.Getenv("FIREFLIES_API_KEY")
	defaultEndpoint := os.Getenv("FIREFLIES_API_URL")
	if defaultEndpoint == "" {
		defaultEndpoint = fireflies.DefaultEndpoint
	}

	return commonFlags{
		apiKey:       fs.String("api-key", defaultAPIKey, "Fireflies API key; defaults to FIREFLIES_API_KEY"),
		endpoint:     fs.String("endpoint", defaultEndpoint, "Fireflies GraphQL endpoint"),
		timeout:      fs.Duration("timeout", 60*time.Second, "HTTP request timeout"),
		pageSize:     fs.Int("page-size", 50, "transcripts per page; Fireflies max is 50"),
		requestDelay: fs.Duration("request-delay", 1100*time.Millisecond, "minimum delay between Fireflies API requests"),
		maxRetries:   fs.Int("max-retries", fireflies.DefaultMaxRetries, "maximum retries for throttled or transient API requests"),
		retryMinWait: fs.Duration("retry-min-wait", 10*time.Second, "minimum retry wait when the API does not provide retryAfter"),
		retryMaxWait: fs.Duration("retry-max-wait", 5*time.Minute, "maximum retry wait for throttled or transient API requests"),
	}
}

func newClientFromFlags(flags commonFlags, stderr io.Writer) (*fireflies.Client, error) {
	apiKey := strings.TrimSpace(*flags.apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("missing API key; set FIREFLIES_API_KEY or pass --api-key")
	}

	endpoint := strings.TrimSpace(*flags.endpoint)
	if endpoint == "" {
		endpoint = fireflies.DefaultEndpoint
	}
	return fireflies.NewClient(fireflies.ClientOptions{
		Endpoint:     endpoint,
		APIKey:       apiKey,
		Timeout:      *flags.timeout,
		MaxRetries:   *flags.maxRetries,
		RequestDelay: *flags.requestDelay,
		RetryMinWait: *flags.retryMinWait,
		RetryMaxWait: *flags.retryMaxWait,
		OnRetry: func(event fireflies.RetryEvent) {
			fmt.Fprintf(stderr, "retrying Fireflies request after %s (%s, attempt %d)\n", event.Delay, event.Reason, event.Attempt)
		},
	}), nil
}

type filterFlags struct {
	from         *string
	to           *string
	userID       *string
	mine         *bool
	organizers   *string
	participants *string
	channelID    *string
	keyword      *string
	scope        *string
	max          *int
}

func addFilterFlags(fs *flag.FlagSet) filterFlags {
	return filterFlags{
		from:         fs.String("from", "", "only transcripts created at or after this date; YYYY-MM-DD or RFC3339"),
		to:           fs.String("to", "", "only transcripts created before this date; YYYY-MM-DD or RFC3339"),
		userID:       fs.String("user-id", "", "Fireflies user_id filter"),
		mine:         fs.Bool("mine", false, "only transcripts organized by the API key owner"),
		organizers:   fs.String("organizers", "", "comma-separated organizer emails"),
		participants: fs.String("participants", "", "comma-separated participant emails"),
		channelID:    fs.String("channel-id", "", "Fireflies channel id filter"),
		keyword:      fs.String("keyword", "", "keyword search"),
		scope:        fs.String("scope", "", "keyword scope: title, sentences, or all"),
		max:          fs.Int("max", 0, "maximum transcripts to process; 0 means no limit"),
	}
}

func buildListFilter(flags filterFlags) (fireflies.ListFilter, error) {
	fromDate, err := app.ParseDateTimeFlag(*flags.from)
	if err != nil {
		return fireflies.ListFilter{}, err
	}
	toDate, err := app.ParseDateTimeFlag(*flags.to)
	if err != nil {
		return fireflies.ListFilter{}, err
	}

	filter := fireflies.ListFilter{
		FromDate:     fromDate,
		ToDate:       toDate,
		UserID:       strings.TrimSpace(*flags.userID),
		Organizers:   app.SplitCSV(*flags.organizers),
		Participants: app.SplitCSV(*flags.participants),
		ChannelID:    strings.TrimSpace(*flags.channelID),
		Keyword:      strings.TrimSpace(*flags.keyword),
		Scope:        strings.TrimSpace(*flags.scope),
		Max:          *flags.max,
	}
	if *flags.mine {
		value := true
		filter.Mine = &value
	}
	return filter, nil
}

func printUsage(out io.Writer) {
	fmt.Fprintln(out, `Fireflies transcript downloader

Usage:
  fireflies-downloader download [flags]
  fireflies-downloader serve [flags]
  fireflies-downloader list [flags]
  fireflies-downloader delete-old [flags]

Environment:
  FIREFLIES_API_KEY   required unless --api-key is provided
  FIREFLIES_API_URL   optional; defaults to https://api.fireflies.ai/graphql
  FIREFLIES_MCP_TOKEN optional bearer token for the local /mcp endpoint

Examples:
  go run ./cmd/fireflies-downloader download --output fireflies_export
  go run ./cmd/fireflies-downloader serve --db fireflies_export/fireflies.sqlite
  go run ./cmd/fireflies-downloader download --include-media --request-delay 2s
  go run ./cmd/fireflies-downloader delete-old --older-than 3m
  go run ./cmd/fireflies-downloader delete-old --older-than 3m --confirm

Use "go run ./cmd/fireflies-downloader <command> -h" for command-specific flags.`)
}
