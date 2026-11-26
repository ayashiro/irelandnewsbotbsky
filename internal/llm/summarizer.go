package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rivo/uniseg"

	"news_bot/internal/rss"
	"news_bot/prompts"
)

const (
	MaxBlueskyGraphemes    = 300
	defaultMaxRetryShorter = 2
)

// TextGenerator is implemented by LLM backends (Claude, Gemini, etc.).
type TextGenerator interface {
	GenerateText(ctx context.Context, systemPrompt, userPrompt string) (string, error)
	ProviderName() string
	ModelName() string
}

// Summarizer generates compact Japanese Bluesky headline posts from RSS articles.
type Summarizer struct {
	Generator TextGenerator
}

// NewSummarizer creates a Summarizer backed by the given TextGenerator.
func NewSummarizer(gen TextGenerator) *Summarizer {
	return &Summarizer{Generator: gen}
}

// SummarizeHeadlines generates a compact Japanese Bluesky post covering top Ireland news.
// The returned string is guaranteed to be within MaxBlueskyGraphemes (300 graphemes).
func (s *Summarizer) SummarizeHeadlines(ctx context.Context, articles []rss.Article, localDate time.Time, hashtags string) (string, error) {
	if len(articles) == 0 {
		return "", errors.New("no articles provided")
	}

	header := fmt.Sprintf("🇮🇪 アイルランドニュース (%d/%d)", localDate.Month(), localDate.Day())
	footer := strings.TrimSpace(hashtags)

	overhead := GraphemeCount(header) + 2 // "\n\n"
	if footer != "" {
		overhead += 2 + GraphemeCount(footer) // "\n\n" + footer
	}
	maxBodyGraphemes := MaxBlueskyGraphemes - overhead
	if maxBodyGraphemes < 60 {
		return "", fmt.Errorf("header and hashtags leave too little space (%d graphemes)", maxBodyGraphemes)
	}

	articleBlock := formatArticlesForPrompt(articles, 50)
	targetBody := maxBodyGraphemes - 15
	if targetBody < 60 {
		targetBody = maxBodyGraphemes
	}

	systemPrompt := strings.TrimSpace(prompts.SystemPrompt)
	userPrompt := strings.NewReplacer(
		"{{TARGET_CHARS}}", fmt.Sprintf("%d", targetBody),
		"{{MAX_CHARS}}", fmt.Sprintf("%d", maxBodyGraphemes),
		"{{ARTICLES}}", articleBlock,
	).Replace(strings.TrimSpace(prompts.UserPromptTemplate))

	var body string
	for attempt := 0; attempt <= defaultMaxRetryShorter; attempt++ {
		prompt := userPrompt
		if attempt > 0 && body != "" {
			prompt = fmt.Sprintf(
				"前回の出力は %d 文字で上限(%d文字)を超えました。件数を3件に絞るなどして、必ず %d 文字以内の箇条書き（「・」始まり、前置きなし）に短く書き直してください。\n\n前回の出力:\n%s",
				GraphemeCount(body), maxBodyGraphemes, maxBodyGraphemes-20, body,
			)
		}

		generated, err := s.Generator.GenerateText(ctx, systemPrompt, prompt)
		if err != nil {
			return "", err
		}
		cleaned := sanitizeBody(generated)
		if cleaned == "" {
			if attempt < defaultMaxRetryShorter {
				continue
			}
			return "", fmt.Errorf("%s returned empty summary", s.Generator.ProviderName())
		}
		body = cleaned

		post := assemblePost(header, body, footer)
		if GraphemeCount(post) <= MaxBlueskyGraphemes {
			return post, nil
		}
	}

	// Final safety net: truncate body cleanly to guarantee Bluesky's 300-grapheme limit.
	body = truncateGraphemes(body, maxBodyGraphemes)
	return assemblePost(header, body, footer), nil
}

func formatArticlesForPrompt(articles []rss.Article, limit int) string {
	if len(articles) > limit {
		articles = articles[:limit]
	}
	var sb strings.Builder
	for i, a := range articles {
		fmt.Fprintf(&sb, "%d. [%s] %s\n", i+1, a.Source, a.Title)
		if a.Description != "" {
			fmt.Fprintf(&sb, "   %s\n", a.Description)
		}
	}
	return sb.String()
}

func sanitizeBody(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```text")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.ReplaceAll(s, "**", "")

	lines := strings.Split(s, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		l := strings.TrimSpace(line)
		if l == "" {
			continue
		}
		for _, prefix := range []string{"- ", "* ", "• ", "・ "} {
			if strings.HasPrefix(l, prefix) {
				l = "・" + strings.TrimSpace(strings.TrimPrefix(l, prefix))
				break
			}
		}
		cleaned = append(cleaned, l)
	}
	return strings.Join(cleaned, "\n")
}

func assemblePost(header, body, footer string) string {
	if footer == "" {
		return header + "\n\n" + body
	}
	return header + "\n\n" + body + "\n\n" + footer
}

// GraphemeCount returns the number of Unicode grapheme clusters (matching Bluesky's limit).
func GraphemeCount(s string) int {
	return uniseg.GraphemeClusterCount(s)
}

func truncateGraphemes(s string, max int) string {
	if GraphemeCount(s) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	var sb strings.Builder
	g := uniseg.NewGraphemes(s)
	count := 0
	for g.Next() {
		if count >= max-1 {
			break
		}
		sb.WriteString(g.Str())
		count++
	}
	sb.WriteString("…")
	return strings.TrimSpace(sb.String())
}
