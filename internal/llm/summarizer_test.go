package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"news_bot/internal/rss"
)

func TestClaudeSummarizeHeadlinesAndRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "sk-ant-test" {
			t.Errorf("missing x-api-key header")
		}
		if r.Header.Get("anthropic-version") != AnthropicAPIVersion {
			t.Errorf("missing anthropic-version header")
		}
		n := calls.Add(1)
		var text string
		if n == 1 {
			text = "・" + strings.Repeat("アイルランド政府の新しい住宅政策について長い説明が続きます。", 15)
		} else {
			text = "- ダブリンで公共交通の運賃改定を発表\n- 西部地域に大雨の黄警報を発令\n- 公共サービス労組がスト日程を協議"
		}
		resp := map[string]any{
			"content": []any{
				map[string]any{"type": "thinking", "thinking": "internal"},
				map[string]any{"type": "text", "text": text},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewClaudeClient("sk-ant-test", "claude-sonnet-5-5")
	c.BaseURL = srv.URL
	s := NewSummarizer(c)

	articles := []rss.Article{
		{Source: "RTÉ Dublin", Title: "Transport fare update", Description: "New fares announced"},
	}
	post, err := s.SummarizeHeadlines(
		context.Background(),
		articles,
		time.Date(2026, 11, 26, 8, 0, 0, 0, time.UTC),
		"#アイルランドニュース #アイルランド",
	)
	if err != nil {
		t.Fatalf("SummarizeHeadlines failed: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("expected 2 API calls (1 retry), got %d", calls.Load())
	}
	if GraphemeCount(post) > MaxBlueskyGraphemes {
		t.Errorf("post exceeds %d graphemes: %d\n%s", MaxBlueskyGraphemes, GraphemeCount(post), post)
	}
	if !strings.Contains(post, "🇮🇪 アイルランドニュース (11/26)") {
		t.Errorf("missing header: %s", post)
	}
	if !strings.Contains(post, "・ダブリンで公共交通の運賃改定を発表") {
		t.Errorf("expected normalized bullet: %s", post)
	}
}

func TestGeminiSummarizeHeadlines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "test-key" {
			t.Errorf("missing api key header")
		}
		resp := map[string]any{
			"candidates": []any{
				map[string]any{
					"content": map[string]any{
						"parts": []any{
							map[string]any{"text": "internal thought", "thought": true},
							map[string]any{"text": "・政府、新たな住宅支援策を発表\n・ダブリン空港の拡張計画が承認"},
						},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewGeminiClient("test-key", "gemini-4-hydrogen")
	c.BaseURL = srv.URL
	s := NewSummarizer(c)

	post, err := s.SummarizeHeadlines(
		context.Background(),
		[]rss.Article{{Source: "RTÉ Ireland", Title: "Housing plan"}},
		time.Date(2026, 11, 26, 8, 0, 0, 0, time.UTC),
		"#アイルランドニュース #アイルランド",
	)
	if err != nil {
		t.Fatalf("SummarizeHeadlines failed: %v", err)
	}
	if strings.Contains(post, "internal thought") {
		t.Errorf("thought part leaked into output: %s", post)
	}
	if GraphemeCount(post) > MaxBlueskyGraphemes {
		t.Errorf("post exceeds limit: %d", GraphemeCount(post))
	}
}
