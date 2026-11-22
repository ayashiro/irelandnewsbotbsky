package rss

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Article represents a single news item fetched from an RSS feed.
type Article struct {
	Source      string
	Title       string
	Description string
	Link        string
	PublishedAt time.Time
}

// Fetcher downloads and parses RSS 2.0 feeds concurrently.
type Fetcher struct {
	Client          *http.Client
	UserAgent       string
	MaxItemsPerFeed int
	Lookback        time.Duration
}

// NewFetcher creates a Fetcher with sensible defaults.
func NewFetcher(maxItemsPerFeed int, lookback time.Duration) *Fetcher {
	return &Fetcher{
		Client:          &http.Client{Timeout: 15 * time.Second},
		UserAgent:       "IrelandNewsBot/1.0 (+https://bsky.app)",
		MaxItemsPerFeed: maxItemsPerFeed,
		Lookback:        lookback,
	}
}

type rssDocument struct {
	Channel struct {
		Title string    `xml:"title"`
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
}

var (
	htmlTagRe = regexp.MustCompile(`<[^>]*>`)
	spaceRe   = regexp.MustCompile(`\s+`)
)

// FetchAll fetches all given RSS URLs concurrently, deduplicates articles,
// and returns the freshest items sorted newest-first.
// Partial failures are logged; an error is returned only if no articles at all could be fetched.
func (f *Fetcher) FetchAll(ctx context.Context, feedURLs []string, now time.Time) ([]Article, error) {
	if len(feedURLs) == 0 {
		return nil, errors.New("no RSS feed URLs configured")
	}

	type result struct {
		url      string
		articles []Article
		err      error
	}

	results := make([]result, len(feedURLs))
	var wg sync.WaitGroup
	for i, u := range feedURLs {
		wg.Add(1)
		go func(idx int, feedURL string) {
			defer wg.Done()
			items, err := f.fetchOne(ctx, feedURL)
			results[idx] = result{url: feedURL, articles: items, err: err}
		}(i, u)
	}
	wg.Wait()

	var all []Article
	var errs []error
	for _, r := range results {
		if r.err != nil {
			slog.Warn("failed to fetch RSS feed", "url", r.url, "error", r.err)
			errs = append(errs, fmt.Errorf("%s: %w", r.url, r.err))
			continue
		}
		slog.Info("fetched RSS feed", "url", r.url, "count", len(r.articles))
		all = append(all, r.articles...)
	}

	if len(all) == 0 {
		return nil, fmt.Errorf("failed to fetch any articles: %w", errors.Join(errs...))
	}

	// Deduplicate by normalized title.
	seen := make(map[string]struct{}, len(all))
	deduped := make([]Article, 0, len(all))
	for _, a := range all {
		key := strings.ToLower(strings.TrimSpace(a.Title))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, a)
	}

	sort.SliceStable(deduped, func(i, j int) bool {
		return deduped[i].PublishedAt.After(deduped[j].PublishedAt)
	})

	// Prefer articles within Lookback window; fall back to all fetched articles if too few.
	if f.Lookback > 0 {
		cutoff := now.Add(-f.Lookback)
		recent := make([]Article, 0, len(deduped))
		for _, a := range deduped {
			if a.PublishedAt.IsZero() || !a.PublishedAt.Before(cutoff) {
				recent = append(recent, a)
			}
		}
		if len(recent) >= 5 {
			return recent, nil
		}
		slog.Warn("fewer than 5 articles within lookback window; using full fetched list",
			"recent_count", len(recent), "total_count", len(deduped))
	}

	return deduped, nil
}

func (f *Fetcher) fetchOne(ctx context.Context, feedURL string) ([]Article, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", f.UserAgent)
	req.Header.Set("Accept", "application/rss+xml, application/xml, text/xml;q=0.9, */*;q=0.8")

	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, err
	}

	return ParseFeed(body, inferSourceName(feedURL), f.MaxItemsPerFeed)
}

// ParseFeed parses RSS 2.0 XML bytes into a slice of Articles.
func ParseFeed(data []byte, fallbackSource string, maxItems int) ([]Article, error) {
	// Strip UTF-8 BOM if present (e.g. Met Éireann warnings XML).
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})

	var doc rssDocument
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("xml unmarshal: %w", err)
	}

	source := fallbackSource
	if source == "" {
		source = cleanText(doc.Channel.Title)
	}

	limit := len(doc.Channel.Items)
	if maxItems > 0 && limit > maxItems {
		limit = maxItems
	}

	articles := make([]Article, 0, limit)
	for _, it := range doc.Channel.Items[:limit] {
		title := cleanText(it.Title)
		if title == "" {
			continue
		}
		desc := cleanText(it.Description)
		if len([]rune(desc)) > 240 {
			desc = string([]rune(desc)[:240]) + "..."
		}
		articles = append(articles, Article{
			Source:      source,
			Title:       title,
			Description: desc,
			Link:        strings.TrimSpace(it.Link),
			PublishedAt: parsePubDate(it.PubDate),
		})
	}
	return articles, nil
}

func cleanText(s string) string {
	s = htmlTagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = spaceRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func inferSourceName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	host := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
	lowerURL := strings.ToLower(rawURL)
	switch {
	case strings.Contains(host, "met.ie"):
		return "Met Éireann 気象警報"
	case strings.Contains(host, "rte.ie") && strings.Contains(lowerURL, "/news/dublin"):
		return "RTÉ Dublin"
	case strings.Contains(host, "rte.ie") && strings.Contains(lowerURL, "/news/ireland"):
		return "RTÉ Ireland"
	case strings.Contains(host, "rte.ie"):
		return "RTÉ News"
	case strings.Contains(host, "breakingnews.ie"):
		return "BreakingNews.ie"
	case strings.Contains(host, "thejournal.ie"):
		return "TheJournal.ie"
	case strings.Contains(host, "dublinlive.ie"):
		return "Dublin Live"
	case strings.Contains(host, "irishtimes.com"):
		return "The Irish Times"
	default:
		return host
	}
}

var pubDateLayouts = []string{
	time.RFC1123Z,
	time.RFC1123,
	time.RFC3339,
	time.RFC3339Nano,
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST",
	"2006-01-02T15:04:05-0700",
}

func parsePubDate(raw string) time.Time {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range pubDateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
