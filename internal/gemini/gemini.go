package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rivo/uniseg"

	"news_bot/internal/rss"
	"news_bot/prompts"
)

const (
	DefaultBaseURL         = "https://generativelanguage.googleapis.com"
	MaxBlueskyGraphemes    = 300
	defaultMaxRetryShorter = 2
)

// Client calls the Gemini generateContent REST API.
type Client struct {
	APIKey     string
	Model      string
	BaseURL    string
	HTTPClient *http.Client
}

// NewClient constructs a Gemini API client.
func NewClient(apiKey, model string) *Client {
	return &Client{
		APIKey:     apiKey,
		Model:      model,
		BaseURL:    DefaultBaseURL,
		HTTPClient: &http.Client{Timeout: 45 * time.Second},
	}
}

type generateRequest struct {
	SystemInstruction *content          `json:"systemInstruction,omitempty"`
	Contents          []content         `json:"contents"`
	GenerationConfig  *generationConfig `json:"generationConfig,omitempty"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type part struct {
	Text    string `json:"text,omitempty"`
	Thought bool   `json:"thought,omitempty"`
}

type generationConfig struct {
	Temperature     float64 `json:"temperature,omitempty"`
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
}

type generateResponse struct {
	Candidates []struct {
		Content struct {
			Parts []part `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error,omitempty"`
}

// SummarizeHeadlines generates a compact Japanese Bluesky post covering top Ireland news.
// The returned string is guaranteed to be within MaxBlueskyGraphemes (300 graphemes).
func (c *Client) SummarizeHeadlines(ctx context.Context, articles []rss.Article, localDate time.Time, hashtags string) (string, error) {
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

		generated, err := c.generateText(ctx, systemPrompt, prompt)
		if err != nil {
			return "", err
		}
		cleaned := sanitizeBody(generated)
		if cleaned == "" {
			if attempt < defaultMaxRetryShorter {
				continue
			}
			return "", errors.New("gemini returned empty summary")
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

func (c *Client) generateText(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	reqBody := generateRequest{
		SystemInstruction: &content{
			Parts: []part{{Text: systemPrompt}},
		},
		Contents: []content{
			{
				Role:  "user",
				Parts: []part{{Text: userPrompt}},
			},
		},
		GenerationConfig: &generationConfig{
			Temperature:     0.3,
			MaxOutputTokens: 8192,
		},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	endpoint := fmt.Sprintf("%s/v1beta/models/%s:generateContent", strings.TrimRight(c.BaseURL, "/"), c.Model)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.APIKey)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gemini request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return "", err
	}

	var parsed generateResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("gemini HTTP %d invalid JSON: %w", resp.StatusCode, err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("gemini API error (%d %s): %s", parsed.Error.Code, parsed.Error.Status, parsed.Error.Message)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("gemini API HTTP %d: %s", resp.StatusCode, string(raw))
	}
	if len(parsed.Candidates) == 0 {
		return "", errors.New("gemini returned no candidates")
	}

	var sb strings.Builder
	for _, p := range parsed.Candidates[0].Content.Parts {
		if p.Thought {
			continue
		}
		sb.WriteString(p.Text)
	}
	return strings.TrimSpace(sb.String()), nil
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
	// Strip accidental code fences or markdown bold markers.
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
		// Normalize common bullet markers to "・".
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
