package rss

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const sampleRSS = `<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0">
  <channel>
    <title>News Headlines</title>
    <item>
      <title><![CDATA[Dublin housing plan &amp; budget approved]]></title>
      <link>https://www.rte.ie/news/1</link>
      <description>&lt;p&gt;Government unveils &lt;b&gt;new&lt;/b&gt; housing package.&lt;/p&gt;</description>
      <pubDate>Sun, 22 Nov 2026 07:00:00 +0000</pubDate>
    </item>
    <item>
      <title>Old Story</title>
      <link>https://www.rte.ie/news/2</link>
      <description>Old news item</description>
      <pubDate>Mon, 16 Nov 2026 07:00:00 +0000</pubDate>
    </item>
  </channel>
</rss>`

func TestParseFeed(t *testing.T) {
	articles, err := ParseFeed([]byte(sampleRSS), "RTÉ News", 10)
	if err != nil {
		t.Fatalf("ParseFeed failed: %v", err)
	}
	if len(articles) != 2 {
		t.Fatalf("expected 2 articles, got %d", len(articles))
	}
	if articles[0].Source != "RTÉ News" {
		t.Errorf("expected source RTÉ News, got %q", articles[0].Source)
	}
	if articles[0].Title != "Dublin housing plan & budget approved" {
		t.Errorf("unexpected title: %q", articles[0].Title)
	}
	if articles[0].Description != "Government unveils new housing package." {
		t.Errorf("unexpected description: %q", articles[0].Description)
	}
	if articles[0].PublishedAt.IsZero() {
		t.Errorf("expected non-zero PublishedAt")
	}

	// Verify UTF-8 BOM handling (e.g. Met Éireann warnings feed).
	bomRSS := append([]byte{0xEF, 0xBB, 0xBF}, []byte(sampleRSS)...)
	bomArticles, err := ParseFeed(bomRSS, "Met Éireann 気象警報", 10)
	if err != nil {
		t.Fatalf("ParseFeed with UTF-8 BOM failed: %v", err)
	}
	if len(bomArticles) != 2 || bomArticles[0].Source != "Met Éireann 気象警報" {
		t.Errorf("unexpected BOM parse result: %+v", bomArticles)
	}
}

func TestFetchAllPartialFailure(t *testing.T) {
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(sampleRSS))
	}))
	defer okSrv.Close()

	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer failSrv.Close()

	f := NewFetcher(10, 24*time.Hour)
	now := time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC)
	articles, err := f.FetchAll(context.Background(), []string{okSrv.URL, failSrv.URL}, now)
	if err != nil {
		t.Fatalf("expected success despite partial failure, got: %v", err)
	}
	if len(articles) != 2 {
		t.Fatalf("expected 2 articles (fallback since <5 recent), got %d", len(articles))
	}
}
