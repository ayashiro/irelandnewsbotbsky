package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"
	_ "time/tzdata"

	"news_bot/internal/bluesky"
	"news_bot/internal/config"
	"news_bot/internal/gemini"
	"news_bot/internal/rss"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "Fetch RSS and generate summary without posting to Bluesky")
	rssOnly := flag.Bool("rss-only", false, "Fetch and print RSS headlines only (no Gemini or Bluesky API calls)")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := config.LoadDotEnv(".env"); err != nil {
		slog.Warn("failed to load .env", "error", err)
	}

	cfg, err := config.Load(*dryRun, *rssOnly)
	if err != nil {
		slog.Error("configuration error", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := run(ctx, cfg, *rssOnly); err != nil {
		slog.Error("job failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg *config.Config, rssOnly bool) error {
	nowLocal := time.Now().In(cfg.Location)
	slog.Info("starting Ireland news bot",
		"local_time", nowLocal.Format(time.RFC3339),
		"timezone", cfg.Timezone,
		"feeds", len(cfg.RSSFeeds),
		"dry_run", cfg.DryRun,
	)

	fetcher := rss.NewFetcher(cfg.MaxItemsPerFeed, time.Duration(cfg.LookbackHours)*time.Hour)
	articles, err := fetcher.FetchAll(ctx, cfg.RSSFeeds, nowLocal)
	if err != nil {
		return fmt.Errorf("fetch RSS: %w", err)
	}
	slog.Info("collected articles", "count", len(articles))

	if rssOnly {
		for i, a := range articles {
			if i >= 15 {
				break
			}
			fmt.Printf("%2d. [%s] %s (%s)\n", i+1, a.Source, a.Title, a.PublishedAt.In(cfg.Location).Format("01/02 15:04"))
		}
		return nil
	}

	gemClient := gemini.NewClient(cfg.GeminiAPIKey, cfg.GeminiModel)
	postText, err := gemClient.SummarizeHeadlines(ctx, articles, nowLocal, cfg.Hashtags)
	if err != nil {
		return fmt.Errorf("summarize with Gemini: %w", err)
	}

	graphemes := gemini.GraphemeCount(postText)
	slog.Info("generated post text", "graphemes", graphemes, "model", cfg.GeminiModel)

	if cfg.DryRun {
		fmt.Println("========== DRY RUN POST ==========")
		fmt.Println(postText)
		fmt.Printf("==================================\n(%d / %d graphemes)\n", graphemes, bluesky.MaxPostGraphemes)
		return nil
	}

	bskyClient := bluesky.NewClient(cfg.BlueskyPDSHost)
	sess, err := bskyClient.Login(ctx, cfg.BlueskyHandle, cfg.BlueskyAppPassword)
	if err != nil {
		return fmt.Errorf("bluesky login: %w", err)
	}

	res, err := bskyClient.Post(ctx, sess, postText, []string{"ja"})
	if err != nil {
		return fmt.Errorf("bluesky post: %w", err)
	}

	slog.Info("posted to Bluesky successfully", "uri", res.URI, "cid", res.CID, "text", postText)
	return nil
}
