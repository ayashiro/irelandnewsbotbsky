package llm

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
)

const (
	DefaultAnthropicBaseURL = "https://api.anthropic.com"
	AnthropicAPIVersion     = "2023-06-01"
)

// ClaudeClient calls the Anthropic Messages REST API.
type ClaudeClient struct {
	APIKey     string
	Model      string
	BaseURL    string
	HTTPClient *http.Client
}

// NewClaudeClient constructs a Claude API client.
func NewClaudeClient(apiKey, model string) *ClaudeClient {
	return &ClaudeClient{
		APIKey:     apiKey,
		Model:      model,
		BaseURL:    DefaultAnthropicBaseURL,
		HTTPClient: &http.Client{Timeout: 45 * time.Second},
	}
}

func (c *ClaudeClient) ProviderName() string { return "claude" }
func (c *ClaudeClient) ModelName() string    { return c.Model }

type claudeRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	System    string          `json:"system,omitempty"`
	Messages  []claudeMessage `json:"messages"`
}

type claudeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type claudeResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"content"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// GenerateText sends a system + user prompt to the Anthropic Messages API.
func (c *ClaudeClient) GenerateText(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	reqBody := claudeRequest{
		Model:     c.Model,
		MaxTokens: 1024,
		System:    systemPrompt,
		Messages: []claudeMessage{
			{
				Role:    "user",
				Content: userPrompt,
			},
		},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	endpoint := strings.TrimRight(c.BaseURL, "/") + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("anthropic-version", AnthropicAPIVersion)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("claude request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return "", err
	}

	var parsed claudeResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("claude HTTP %d invalid JSON: %w", resp.StatusCode, err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("claude API error (%s): %s", parsed.Error.Type, parsed.Error.Message)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("claude API HTTP %d: %s", resp.StatusCode, string(raw))
	}
	if len(parsed.Content) == 0 {
		return "", errors.New("claude returned empty content")
	}

	var sb strings.Builder
	for _, block := range parsed.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	return strings.TrimSpace(sb.String()), nil
}
