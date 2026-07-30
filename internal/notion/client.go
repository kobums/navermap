// Package notion은 공식 Notion REST API의 최소 클라이언트.
// navermap-sync에 필요한 것(DB 전체 조회, 페이지 생성/수정/아카이브)만 구현한다.
package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	apiBase    = "https://api.notion.com/v1"
	apiVersion = "2022-06-28"
	// Notion rate limit은 평균 3 req/s. 쓰기 요청 사이에 이만큼 쉰다.
	writeInterval = 350 * time.Millisecond
)

type Client struct {
	Token      string
	DatabaseID string
	HTTP       *http.Client

	lastWrite time.Time
}

func NewClient(token, databaseID string) *Client {
	return &Client{
		Token:      token,
		DatabaseID: databaseID,
		HTTP:       &http.Client{Timeout: 30 * time.Second},
	}
}

// Page는 동기화에 필요한 만큼만 파싱한 Notion 페이지.
type Page struct {
	ID      string
	SID     string
	Lists   []string
	Visited bool
	Name    string
	Memo    string
}

// QueryAllPages는 DB의 전체 페이지를 100개씩 페이지네이션하며 가져온다.
func (c *Client) QueryAllPages(ctx context.Context) ([]Page, error) {
	var pages []Page
	var cursor string
	for {
		body := map[string]any{"page_size": 100}
		if cursor != "" {
			body["start_cursor"] = cursor
		}
		var resp struct {
			Results []struct {
				ID         string `json:"id"`
				Properties struct {
					SID struct {
						RichText []struct {
							PlainText string `json:"plain_text"`
						} `json:"rich_text"`
					} `json:"SID"`
					Name struct {
						Title []struct {
							PlainText string `json:"plain_text"`
						} `json:"title"`
					} `json:"이름"`
					Memo struct {
						RichText []struct {
							PlainText string `json:"plain_text"`
						} `json:"rich_text"`
					} `json:"메모"`
					Lists struct {
						MultiSelect []struct {
							Name string `json:"name"`
						} `json:"multi_select"`
					} `json:"리스트"`
					Visited struct {
						Checkbox bool `json:"checkbox"`
					} `json:"가봤음"`
				} `json:"properties"`
			} `json:"results"`
			HasMore    bool   `json:"has_more"`
			NextCursor string `json:"next_cursor"`
		}
		if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/databases/%s/query", c.DatabaseID), body, &resp); err != nil {
			return nil, err
		}
		for _, r := range resp.Results {
			p := Page{ID: r.ID, Visited: r.Properties.Visited.Checkbox}
			if len(r.Properties.SID.RichText) > 0 {
				p.SID = r.Properties.SID.RichText[0].PlainText
			}
			if len(r.Properties.Name.Title) > 0 {
				p.Name = r.Properties.Name.Title[0].PlainText
			}
			if len(r.Properties.Memo.RichText) > 0 {
				p.Memo = r.Properties.Memo.RichText[0].PlainText
			}
			for _, m := range r.Properties.Lists.MultiSelect {
				p.Lists = append(p.Lists, m.Name)
			}
			pages = append(pages, p)
		}
		if !resp.HasMore {
			return pages, nil
		}
		cursor = resp.NextCursor
	}
}

// CreatePage는 DB에 페이지를 만든다. properties는 Notion API 원형 그대로.
func (c *Client) CreatePage(ctx context.Context, properties map[string]any) error {
	c.throttle()
	body := map[string]any{
		"parent":     map[string]any{"database_id": c.DatabaseID},
		"properties": properties,
	}
	return c.do(ctx, http.MethodPost, "/pages", body, nil)
}

// UpdatePage는 페이지의 지정된 속성만 갱신한다.
func (c *Client) UpdatePage(ctx context.Context, pageID string, properties map[string]any) error {
	c.throttle()
	return c.do(ctx, http.MethodPatch, "/pages/"+pageID, map[string]any{"properties": properties}, nil)
}

// ArchivePage는 페이지를 휴지통으로 보낸다 (Notion에서 복구 가능).
func (c *Client) ArchivePage(ctx context.Context, pageID string) error {
	c.throttle()
	return c.do(ctx, http.MethodPatch, "/pages/"+pageID, map[string]any{"archived": true}, nil)
}

func (c *Client) throttle() {
	if wait := writeInterval - time.Since(c.lastWrite); wait > 0 {
		time.Sleep(wait)
	}
	c.lastWrite = time.Now()
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
		lastErr = c.tryDo(ctx, method, path, body, out)
		if lastErr == nil {
			return nil
		}
		if !isRetryable(lastErr) {
			return lastErr
		}
	}
	return lastErr
}

type httpError struct {
	Status int
	Body   string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("notion API HTTP %d: %s", e.Status, e.Body)
}

func isRetryable(err error) bool {
	if he, ok := err.(*httpError); ok {
		return he.Status == http.StatusTooManyRequests || he.Status >= 500
	}
	return true // 네트워크 오류
}

func (c *Client) tryDo(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiBase+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Notion-Version", apiVersion)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		msg := string(data)
		if len(msg) > 300 {
			msg = msg[:300] + "..."
		}
		return &httpError{Status: resp.StatusCode, Body: msg}
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}
