package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	snapshot "github.com/infrai-examples/fintech-nightly-snapshot"
)

func main() {
	once := flag.Bool("once", false, "read payment events from stdin and write one snapshot")
	businessDate := flag.String("date", "", "business date in YYYY-MM-DD; defaults to the previous UTC date")
	runAt := flag.String("at", "01:00", "daily UTC run time in HH:MM")
	inputPath := flag.String("input", "", "payment event JSON file to reopen for each scheduled run")
	flag.Parse()

	client, err := snapshot.NewInfraiStorage(os.Getenv("INFRAI_API_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	job := snapshot.SnapshotJob{Store: client, Bucket: envOr("SNAPSHOT_BUCKET", "fintech-nightly-snapshots"), Now: time.Now}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *once {
		if err := execute(ctx, job, dateOrPrevious(*businessDate), os.Stdin); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *inputPath == "" {
		log.Fatal("-input is required in scheduled mode")
	}
	hour, minute, err := parseRunAt(*runAt)
	if err != nil {
		log.Fatal(err)
	}
	for {
		next := nextRun(time.Now().UTC(), hour, minute)
		log.Printf("next snapshot at %s", next.Format(time.RFC3339))
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			if err := executeFile(ctx, job, next.AddDate(0, 0, -1).Format(time.DateOnly), *inputPath); err != nil {
				log.Printf("snapshot run: %v", err)
			}
		}
	}
}

func executeFile(ctx context.Context, job snapshot.SnapshotJob, date, path string) error {
	input, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open payment events: %w", err)
	}
	defer input.Close()
	return execute(ctx, job, date, input)
}

func execute(ctx context.Context, job snapshot.SnapshotJob, date string, input *os.File) error {
	var events []snapshot.PaymentEvent
	if err := json.NewDecoder(input).Decode(&events); err != nil {
		return fmt.Errorf("decode payment events: %w", err)
	}
	key, err := job.Run(ctx, date, events)
	if err != nil {
		var apiErr *snapshot.APIError
		if errors.As(err, &apiErr) && apiErr.HTTPStatus >= 400 && apiErr.HTTPStatus < 500 {
			return fmt.Errorf("snapshot rejected (%d): %s", apiErr.HTTPStatus, apiErr.Message)
		}
		return err
	}
	log.Printf("stored %d payment events at %s/%s", len(events), job.Bucket, key)
	return nil
}

func dateOrPrevious(value string) string {
	if value != "" {
		return value
	}
	return time.Now().UTC().AddDate(0, 0, -1).Format(time.DateOnly)
}

func parseRunAt(value string) (int, int, error) {
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return 0, 0, fmt.Errorf("parse -at: %w", err)
	}
	return parsed.Hour(), parsed.Minute(), nil
}

func nextRun(now time.Time, hour, minute int) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, time.UTC)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
