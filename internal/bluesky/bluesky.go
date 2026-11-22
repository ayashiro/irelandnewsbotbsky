package bluesky

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/rivo/uniseg"
)

const MaxPostGraphemes = 300

// Client posts updates to Bluesky via the AT Protocol HTTP API.
type Client struct {
	PDSHost    string
	HTTPClient *http.Client
}

// NewClient creates a Bluesky client targeting the specified PDS host (e.g. https://bsky.social).
func NewClient(pdsHost string) *Client {
	return &Client{
		PDSHost:    strings.TrimRight(pdsHost, "/"),
		HTTPClient: &http.Client{Timeout: 20 * time.Second},
	}
}

type createSessionRequest struct {
	Identifier string `json:"identifier"`
	Password   string `json:"password"`
}

type Session struct {
	DID        string `json:"did"`
	Handle     string `json:"handle"`
	AccessJWT  string `json:"accessJwt"`
	RefreshJWT string `json:"refreshJwt"`
}

type createRecordRequest struct {
	Repo       string     `json:"repo"`
	Collection string     `json:"collection"`
	Record     postRecord `json:"record"`
}

type postRecord struct {
	Type      string   `json:"$type"`
	Text      string   `json:"text"`
	CreatedAt string   `json:"createdAt"`
	Langs     []string `json:"langs,omitempty"`
	Facets    []Facet  `json:"facets,omitempty"`
}

// Facet represents an AT Protocol RichText facet with UTF-8 byte offsets.
type Facet struct {
	Index    ByteSlice `json:"index"`
	Features []Feature `json:"features"`
}

type ByteSlice struct {
	ByteStart int `json:"byteStart"`
	ByteEnd   int `json:"byteEnd"`
}

type Feature struct {
	Type string `json:"$type"`
	Tag  string `json:"tag,omitempty"`
}

type CreateRecordResponse struct {
	URI string `json:"uri"`
	CID string `json:"cid"`
}

// Login authenticates with a Bluesky handle and App Password.
func (c *Client) Login(ctx context.Context, handle, appPassword string) (*Session, error) {
	payload, err := json.Marshal(createSessionRequest{
		Identifier: strings.TrimPrefix(strings.TrimSpace(handle), "@"),
		Password:   appPassword,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.PDSHost+"/xrpc/com.atproto.server.createSession", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("createSession request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bluesky login failed (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var sess Session
	if err := json.Unmarshal(body, &sess); err != nil {
		return nil, fmt.Errorf("decode createSession response: %w", err)
	}
	return &sess, nil
}

// Post publishes a text post (automatically attaching hashtag facets).
func (c *Client) Post(ctx context.Context, sess *Session, text string, langs []string) (*CreateRecordResponse, error) {
	if count := uniseg.GraphemeClusterCount(text); count > MaxPostGraphemes {
		return nil, fmt.Errorf("post text exceeds Bluesky limit (%d > %d graphemes)", count, MaxPostGraphemes)
	}

	rec := createRecordRequest{
		Repo:       sess.DID,
		Collection: "app.bsky.feed.post",
		Record: postRecord{
			Type:      "app.bsky.feed.post",
			Text:      text,
			CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Langs:     langs,
			Facets:    ExtractHashtagFacets(text),
		},
	}

	payload, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.PDSHost+"/xrpc/com.atproto.repo.createRecord", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+sess.AccessJWT)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("createRecord request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bluesky post failed (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out CreateRecordResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode createRecord response: %w", err)
	}
	return &out, nil
}

var hashtagRe = regexp.MustCompile(`(^|[\s　])(#[^\s　#]+)`)

// ExtractHashtagFacets finds #hashtags in text and returns AT Protocol facets
// with exact UTF-8 byte offsets.
func ExtractHashtagFacets(text string) []Facet {
	matches := hashtagRe.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return nil
	}
	facets := make([]Facet, 0, len(matches))
	for _, m := range matches {
		// Group 2 is the "#tag" portion.
		start, end := m[4], m[5]
		raw := text[start:end]
		trimmed := strings.TrimRight(raw, ".,!?;:、。！？）」】")
		if len(trimmed) <= 1 {
			continue
		}
		end = start + len(trimmed)
		tag := strings.TrimPrefix(trimmed, "#")
		facets = append(facets, Facet{
			Index: ByteSlice{
				ByteStart: start,
				ByteEnd:   end,
			},
			Features: []Feature{
				{
					Type: "app.bsky.richtext.facet#tag",
					Tag:  tag,
				},
			},
		})
	}
	return facets
}
