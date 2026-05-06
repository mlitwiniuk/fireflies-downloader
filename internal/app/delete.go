package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/mlitwiniuk/fireflies-downloader/internal/fireflies"
)

type DeleteOptions struct {
	OlderThan   string
	Confirm     bool
	DeleteDelay time.Duration
	PlanFile    string
}

type DeletePlan struct {
	GeneratedAt string                         `json:"generated_at"`
	Cutoff      string                         `json:"cutoff"`
	Confirmed   bool                           `json:"confirmed"`
	Filters     map[string]any                 `json:"filters"`
	Count       int                            `json:"count"`
	Candidates  []fireflies.TranscriptListItem `json:"candidates"`
	Results     []DeleteResult                 `json:"results,omitempty"`
}

type DeleteResult struct {
	ID      string          `json:"id"`
	Title   string          `json:"title,omitempty"`
	Deleted json.RawMessage `json:"deleted,omitempty"`
	Error   string          `json:"error,omitempty"`
}

func DeleteOldTranscripts(ctx context.Context, client *fireflies.Client, filter fireflies.ListFilter, opts DeleteOptions, stdout io.Writer) error {
	if opts.OlderThan == "" {
		opts.OlderThan = "3m"
	}
	if opts.DeleteDelay == 0 {
		opts.DeleteDelay = 7 * time.Second
	}
	if opts.PlanFile == "" {
		opts.PlanFile = "fireflies_delete_plan.json"
	}

	cutoff, err := cutoffFromRetention(opts.OlderThan, time.Now())
	if err != nil {
		return err
	}
	filter.ToDate = &cutoff

	fmt.Fprintf(stdout, "Listing transcripts older than %s (before %s)...\n", opts.OlderThan, formatFirefliesDate(cutoff))
	candidates, err := client.ListTranscripts(ctx, filter, func(fetched int) {
		fmt.Fprintf(stdout, "  fetched %d candidate records\r", fetched)
	})
	fmt.Fprintln(stdout)
	if err != nil {
		return err
	}

	plan := DeletePlan{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Cutoff:      formatFirefliesDate(cutoff),
		Confirmed:   opts.Confirm,
		Filters:     filterManifest(filter),
		Count:       len(candidates),
		Candidates:  candidates,
	}

	if !opts.Confirm {
		if err := writeJSONFile(opts.PlanFile, plan); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Dry run only. %d transcript(s) would be deleted.\n", len(candidates))
		fmt.Fprintf(stdout, "Wrote deletion plan: %s\n", opts.PlanFile)
		fmt.Fprintln(stdout, "Run again with --confirm to delete.")
		return nil
	}

	fmt.Fprintf(stdout, "Deleting %d transcript(s). Delay between deletes: %s\n", len(candidates), opts.DeleteDelay)
	plan.Results = make([]DeleteResult, 0, len(candidates))
	var failed int
	for index, item := range candidates {
		if index > 0 {
			if err := sleepContext(ctx, opts.DeleteDelay); err != nil {
				return err
			}
		}

		result := DeleteResult{ID: item.ID, Title: item.Title}
		deleted, err := client.DeleteTranscript(ctx, item.ID)
		if err != nil {
			result.Error = err.Error()
			failed++
		} else {
			result.Deleted = deleted
		}
		plan.Results = append(plan.Results, result)
		fmt.Fprintf(stdout, "  deleted %d/%d\r", index+1-failed, len(candidates))
	}
	fmt.Fprintln(stdout)

	if err := writeJSONFile(opts.PlanFile, plan); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Wrote deletion log: %s\n", opts.PlanFile)
	if failed > 0 {
		return fmt.Errorf("%d delete(s) failed; see %s", failed, opts.PlanFile)
	}
	return nil
}
