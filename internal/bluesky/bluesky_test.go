package bluesky

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractHashtagFacetsUTF8Offsets(t *testing.T) {
	text := "🇮🇪 ニュース\n\n#アイルランドニュース #アイルランド"
	facets := ExtractHashtagFacets(text)
	if len(facets) != 2 {
		t.Fatalf("expected 2 facets, got %d", len(facets))
	}
	for i, wantTag := range []string{"アイルランドニュース", "アイルランド"} {
		f := facets[i]
		gotSlice := text[f.Index.ByteStart:f.Index.ByteEnd]
		if gotSlice != "#"+wantTag {
			t.Errorf("facet %d slice = %q, want %q", i, gotSlice, "#"+wantTag)
		}
		if f.Features[0].Tag != wantTag {
			t.Errorf("facet %d tag = %q, want %q", i, f.Features[0].Tag, wantTag)
		}
	}
}

func TestLoginAndPost(t *testing.T) {
	var gotAuth string
	var gotRecord createRecordRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/xrpc/com.atproto.server.createSession":
			_ = json.NewEncoder(w).Encode(Session{
				DID:       "did:plc:example123",
				Handle:    "bot.bsky.social",
				AccessJWT: "jwt-token",
			})
		case "/xrpc/com.atproto.repo.createRecord":
			gotAuth = r.Header.Get("Authorization")
			_ = json.NewDecoder(r.Body).Decode(&gotRecord)
			_ = json.NewEncoder(w).Encode(CreateRecordResponse{
				URI: "at://did:plc:example123/app.bsky.feed.post/abc",
				CID: "bafy123",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	sess, err := c.Login(context.Background(), "@bot.bsky.social", "xxxx-xxxx-xxxx-xxxx")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	res, err := c.Post(context.Background(), sess, "テスト投稿 #アイルランド", []string{"ja"})
	if err != nil {
		t.Fatalf("Post failed: %v", err)
	}
	if gotAuth != "Bearer jwt-token" {
		t.Errorf("unexpected Authorization header: %q", gotAuth)
	}
	if gotRecord.Repo != "did:plc:example123" {
		t.Errorf("unexpected repo: %q", gotRecord.Repo)
	}
	if len(gotRecord.Record.Facets) != 1 {
		t.Errorf("expected 1 facet, got %d", len(gotRecord.Record.Facets))
	}
	if res.URI == "" {
		t.Errorf("expected non-empty URI")
	}
}
