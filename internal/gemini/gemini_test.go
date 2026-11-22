package gemini

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

func TestSummarizeHeadlinesSuccessAndRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "test-key" {
			t.Errorf("missing api key header")
		}
		n := calls.Add(1)
		var text string
		if n == 1 {
			// Intentionally exceed 300 graphemes to verify retry.
			text = "・" + strings.Repeat("アイルランド政府の新しい住宅政策について長い説明が続きます。", 15)
		} else {
			text = "- 政府、新たな住宅支援策を発表\n- ダブリン空港の拡張計画が承認\n- コーク大学病院の安全対策を協議"
		}
		resp := map[string]any{
			"candidates": []any{
				map[string]any{
					"content": map[string]any{
						"parts": []any{
							map[string]any{"text": "internal thought", "thought": true},
							map[string]any{"text": text},
						},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewClient("test-key", "gemini-4-hydrogen")
	c.BaseURL = srv.URL

	articles := []rss.Article{
		{Source: "RTÉ News", Title: "Housing plan unveiled", Description: "Details of plan"},
	}
	post, err := c.SummarizeHeadlines(
		context.Background(),
		articles,
		time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC),
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
	if strings.Contains(post, "internal thought") {
		t.Errorf("thought part leaked into output: %s", post)
	}
	if !strings.Contains(post, "🇮🇪 アイルランドニュース (11/22)") {
		t.Errorf("missing header: %s", post)
	}
	if !strings.Contains(post, "・政府、新たな住宅支援策を発表") {
		t.Errorf("expected normalized bullet: %s", post)
	}
}
