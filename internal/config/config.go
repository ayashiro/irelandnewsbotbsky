package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultClaudeModel    = "claude-sonnet-5-5"
	DefaultGeminiModel    = "gemini-4-hydrogen"
	DefaultBlueskyPDSHost = "https://bsky.social"
	DefaultTimezone       = "Europe/Dublin"
	DefaultHashtags       = "#アイルランドニュース #アイルランド"
	DefaultMaxItemsPerRSS = 15
	DefaultLookbackHours  = 24
)

var DefaultRSSFeeds = []string{
	"https://www.met.ie/warningsxml/rss.xml",
	"https://www.rte.ie/feeds/rss/?index=/news/ireland/",
	"https://www.rte.ie/feeds/rss/?index=/news/dublin/",
	"https://www.breakingnews.ie/feed/ireland.rss",
	"https://www.thejournal.ie/feed/",
	"https://www.dublinlive.ie/news/?service=rss",
}

// Config holds all runtime settings loaded from environment variables.
type Config struct {
	LLMProvider        string // "claude" or "gemini"
	AnthropicAPIKey    string
	ClaudeModel        string
	GeminiAPIKey       string
	GeminiModel        string
	BlueskyHandle      string
	BlueskyAppPassword string
	BlueskyPDSHost     string
	RSSFeeds           []string
	MaxItemsPerFeed    int
	LookbackHours      int
	Timezone           string
	Location           *time.Location
	Hashtags           string
	DryRun             bool
}

// LoadDotEnv loads KEY=VALUE pairs from a local .env file if present.
// Existing environment variables are never overwritten.
func LoadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 {
			if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
				v = v[1 : len(v)-1]
			}
		}
		if k == "" {
			continue
		}
		if _, exists := os.LookupEnv(k); !exists {
			_ = os.Setenv(k, v)
		}
	}
	return scanner.Err()
}

// Load reads configuration from environment variables and validates required fields.
func Load(dryRunFlag, rssOnlyFlag bool) (*Config, error) {
	dryRun := dryRunFlag || parseBool(os.Getenv("DRY_RUN"))

	tzName := getEnvOrDefault("TIMEZONE", DefaultTimezone)
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return nil, fmt.Errorf("invalid TIMEZONE %q: %w", tzName, err)
	}

	feeds := DefaultRSSFeeds
	if raw := strings.TrimSpace(os.Getenv("RSS_FEEDS")); raw != "" {
		var parsed []string
		for _, part := range strings.Split(raw, ",") {
			if u := strings.TrimSpace(part); u != "" {
				parsed = append(parsed, u)
			}
		}
		if len(parsed) > 0 {
			feeds = parsed
		}
	}

	maxItems := DefaultMaxItemsPerRSS
	if raw := strings.TrimSpace(os.Getenv("MAX_ITEMS_PER_FEED")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid MAX_ITEMS_PER_FEED %q", raw)
		}
		maxItems = n
	}

	lookback := DefaultLookbackHours
	if raw := strings.TrimSpace(os.Getenv("LOOKBACK_HOURS")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid LOOKBACK_HOURS %q", raw)
		}
		lookback = n
	}

	hashtags := DefaultHashtags
	if raw, ok := os.LookupEnv("POST_HASHTAGS"); ok {
		hashtags = strings.TrimSpace(raw)
	}

	anthropicKey := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	if anthropicKey == "" {
		anthropicKey = strings.TrimSpace(os.Getenv("CLAUDE_API_KEY"))
	}
	geminiKey := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))

	provider := strings.ToLower(strings.TrimSpace(os.Getenv("LLM_PROVIDER")))
	if provider == "" {
		if anthropicKey != "" {
			provider = "claude"
		} else {
			provider = "gemini"
		}
	}

	cfg := &Config{
		LLMProvider:        provider,
		AnthropicAPIKey:    anthropicKey,
		ClaudeModel:        getEnvOrDefault("CLAUDE_MODEL", DefaultClaudeModel),
		GeminiAPIKey:       geminiKey,
		GeminiModel:        getEnvOrDefault("GEMINI_MODEL", DefaultGeminiModel),
		BlueskyHandle:      strings.TrimPrefix(strings.TrimSpace(os.Getenv("BLUESKY_HANDLE")), "@"),
		BlueskyAppPassword: strings.TrimSpace(os.Getenv("BLUESKY_APP_PASSWORD")),
		BlueskyPDSHost:     strings.TrimRight(getEnvOrDefault("BLUESKY_PDS_HOST", DefaultBlueskyPDSHost), "/"),
		RSSFeeds:           feeds,
		MaxItemsPerFeed:    maxItems,
		LookbackHours:      lookback,
		Timezone:           tzName,
		Location:           loc,
		Hashtags:           hashtags,
		DryRun:             dryRun,
	}

	if !rssOnlyFlag {
		switch cfg.LLMProvider {
		case "claude":
			if cfg.AnthropicAPIKey == "" {
				return nil, errors.New("ANTHROPIC_API_KEY (or CLAUDE_API_KEY) is required when LLM_PROVIDER=claude")
			}
		case "gemini":
			if cfg.GeminiAPIKey == "" {
				return nil, errors.New("GEMINI_API_KEY is required when LLM_PROVIDER=gemini")
			}
		default:
			return nil, fmt.Errorf("unsupported LLM_PROVIDER %q (supported: claude, gemini)", cfg.LLMProvider)
		}
	}

	if !rssOnlyFlag && !cfg.DryRun {
		if cfg.BlueskyHandle == "" {
			return nil, errors.New("BLUESKY_HANDLE is required unless DRY_RUN is enabled")
		}
		if cfg.BlueskyAppPassword == "" {
			return nil, errors.New("BLUESKY_APP_PASSWORD is required unless DRY_RUN is enabled")
		}
	}

	return cfg, nil
}

func getEnvOrDefault(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
