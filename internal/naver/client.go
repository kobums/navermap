package naver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"
)

const (
	apiBase   = "https://pages.map.naver.com/save-pages/api/maps-bookmark/v3"
	pageLimit = 500
	userAgent = "navermap-mcp/0.1 (+https://github.com/kobums/navermap)"
)

var shareIDPattern = regexp.MustCompile(`\b([0-9a-f]{32})\b`)

// Client는 네이버 지도 저장 리스트 공유 API 클라이언트. 인증 불필요.
// 내부 API라 간헐적으로 빈 응답이 오므로 모든 호출에 재시도가 들어간다.
type Client struct {
	HTTP       *http.Client
	MaxRetries int
}

func NewClient() *Client {
	return &Client{
		HTTP:       &http.Client{Timeout: 15 * time.Second},
		MaxRetries: 3,
	}
}

// ResolveShareID는 naver.me 단축 URL, map.naver.com 공유 URL, 또는 32자리 hex
// shareId 문자열 어느 것을 받아도 shareId를 돌려준다.
func (c *Client) ResolveShareID(ctx context.Context, urlOrID string) (string, error) {
	if m := shareIDPattern.FindString(urlOrID); m != "" {
		return m, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlOrID, nil)
	if err != nil {
		return "", fmt.Errorf("공유 URL 파싱 실패: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	noRedirect := &http.Client{
		Timeout: c.HTTP.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		return "", fmt.Errorf("공유 URL 요청 실패: %w", err)
	}
	defer resp.Body.Close()

	loc := resp.Header.Get("Location")
	if m := shareIDPattern.FindString(loc); m != "" {
		return m, nil
	}
	return "", fmt.Errorf("리다이렉트에서 shareId를 찾지 못함 (status=%d, location=%q)", resp.StatusCode, loc)
}

// GetShare는 폴더 메타데이터를 가져온다. bookmarkList 앞부분도 딸려오지만
// 전체 목록이 필요하면 GetAllBookmarks를 쓸 것.
func (c *Client) GetShare(ctx context.Context, shareID string) (*ShareResponse, error) {
	var out ShareResponse
	url := fmt.Sprintf("%s/shares/%s", apiBase, shareID)
	if err := c.getJSON(ctx, url, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetAllBookmarks는 페이지네이션을 따라가며 폴더의 전체 북마크를 가져온다.
func (c *Client) GetAllBookmarks(ctx context.Context, shareID string) (*ShareResponse, error) {
	var result *ShareResponse
	for start := 0; ; start += pageLimit {
		var page ShareResponse
		url := fmt.Sprintf("%s/shares/%s/bookmarks?start=%d&limit=%d", apiBase, shareID, start, pageLimit)
		if err := c.getJSON(ctx, url, &page); err != nil {
			return nil, err
		}
		if result == nil {
			result = &page
		} else {
			result.BookmarkList = append(result.BookmarkList, page.BookmarkList...)
		}
		if len(page.BookmarkList) < pageLimit || len(result.BookmarkList) >= result.Folder.BookmarkCount {
			break
		}
	}
	return result, nil
}

func (c *Client) getJSON(ctx context.Context, url string, out any) error {
	var lastErr error
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(500*(1<<(attempt-1))) * time.Millisecond
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		lastErr = c.tryGetJSON(ctx, url, out)
		if lastErr == nil {
			return nil
		}
	}
	return fmt.Errorf("%d회 재시도 후 실패: %w", c.MaxRetries, lastErr)
}

func (c *Client) tryGetJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(body, 200))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("JSON 파싱 실패 (body %d bytes): %w", len(body), err)
	}
	return nil
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "..."
	}
	return string(b)
}
