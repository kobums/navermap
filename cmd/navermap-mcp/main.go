package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/kobums/navermap/internal/naver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const version = "0.1.0"

// Config는 서버가 아는 저장 리스트 목록.
type Config struct {
	Lists []ListEntry `json:"lists"`
}

type ListEntry struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type server struct {
	client *naver.Client
	config Config

	mu      sync.Mutex
	idCache map[string]string // url -> shareId
}

func main() {
	configPath := flag.String("config", "config.json", "리스트 설정 파일 경로")
	httpAddr := flag.String("http", "", "지정하면 stdio 대신 streamable HTTP로 서빙 (예: :8787)")
	flag.Parse()

	s := &server{client: naver.NewClient(), idCache: map[string]string{}}
	if data, err := os.ReadFile(*configPath); err == nil {
		if err := json.Unmarshal(data, &s.config); err != nil {
			log.Fatalf("설정 파일 파싱 실패 %s: %v", *configPath, err)
		}
	} else if !os.IsNotExist(err) {
		log.Fatalf("설정 파일 읽기 실패 %s: %v", *configPath, err)
	}

	// 디버그용: navermap-mcp fetch <URL|shareId>
	if args := flag.Args(); len(args) == 2 && args[0] == "fetch" {
		if err := fetchCmd(s, args[1]); err != nil {
			log.Fatal(err)
		}
		return
	}

	m := mcp.NewServer(&mcp.Implementation{Name: "navermap", Version: version}, nil)
	mcp.AddTool(m, &mcp.Tool{
		Name:        "list_folders",
		Description: "설정된 네이버 지도 저장 리스트 전체의 메타데이터(이름, 장소 수, 작성자 등)를 반환합니다.",
	}, s.listFolders)
	mcp.AddTool(m, &mcp.Tool{
		Name: "get_bookmarks",
		Description: "저장 리스트의 장소 목록을 반환합니다. list에는 설정된 리스트 이름, 공유 URL(naver.me/... 또는 map.naver.com/...), " +
			"또는 32자리 shareId 중 아무거나 넣을 수 있습니다. query로 이름/주소/카테고리/메모 부분일치 필터링이 가능합니다.",
	}, s.getBookmarks)
	mcp.AddTool(m, &mcp.Tool{
		Name:        "resolve_share",
		Description: "네이버 지도 공유 URL에서 shareId를 추출하고 폴더 메타데이터를 반환합니다.",
	}, s.resolveShare)

	if *httpAddr != "" {
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return m }, nil)
		log.Printf("navermap MCP 서버 시작: http://%s/ (lists=%d)", *httpAddr, len(s.config.Lists))
		log.Fatal(http.ListenAndServe(*httpAddr, handler))
	}
	if err := m.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}

// --- list_folders ---

type listFoldersArgs struct{}

type folderSummary struct {
	ConfigName    string `json:"configName,omitempty"`
	ShareID       string `json:"shareId"`
	Name          string `json:"name"`
	Memo          string `json:"memo,omitempty"`
	BookmarkCount int    `json:"bookmarkCount"`
	AuthorNick    string `json:"authorNick,omitempty"`
	ExternalLink  string `json:"externalLink,omitempty"`
	Error         string `json:"error,omitempty"`
}

type listFoldersResult struct {
	Folders []folderSummary `json:"folders"`
}

func (s *server) listFolders(ctx context.Context, req *mcp.CallToolRequest, _ listFoldersArgs) (*mcp.CallToolResult, listFoldersResult, error) {
	out := listFoldersResult{Folders: make([]folderSummary, len(s.config.Lists))}
	var wg sync.WaitGroup
	for i, entry := range s.config.Lists {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fs := folderSummary{ConfigName: entry.Name}
			share, err := s.fetchShareMeta(ctx, entry.URL)
			if err != nil {
				fs.Error = err.Error()
			} else {
				fillSummary(&fs, &share.Folder)
			}
			out.Folders[i] = fs
		}()
	}
	wg.Wait()
	return nil, out, nil
}

func fillSummary(fs *folderSummary, f *naver.Folder) {
	fs.ShareID = f.ShareID
	fs.Name = f.Name
	fs.Memo = f.Memo
	fs.BookmarkCount = f.BookmarkCount
	fs.ExternalLink = f.ExternalLink
	if f.PlaceUserProfile != nil {
		fs.AuthorNick = f.PlaceUserProfile.Nick
	}
}

// --- get_bookmarks ---

type getBookmarksArgs struct {
	List    string `json:"list" jsonschema:"설정된 리스트 이름, 공유 URL, 또는 shareId"`
	Query   string `json:"query,omitempty" jsonschema:"이름/주소/카테고리/메모 부분일치 필터 (선택)"`
	Full    bool   `json:"full,omitempty" jsonschema:"true면 원본 필드 전체 반환. 기본은 요약 필드만"`
	Offset  int    `json:"offset,omitempty"`
	Limit   int    `json:"limit,omitempty" jsonschema:"기본 0 = 전체"`
	Include bool   `json:"includeUnavailable,omitempty" jsonschema:"true면 폐업 등 이용 불가 장소도 포함"`
}

type compactPlace struct {
	SID      string  `json:"sid"`
	Name     string  `json:"name"`
	Category string  `json:"category,omitempty"`
	Address  string  `json:"address,omitempty"`
	Lng      float64 `json:"lng"`
	Lat      float64 `json:"lat"`
	Memo     string  `json:"memo,omitempty"`
}

type getBookmarksResult struct {
	Folder     folderSummary    `json:"folder"`
	Total      int              `json:"total"`
	Returned   int              `json:"returned"`
	Places     []compactPlace   `json:"places,omitempty"`
	FullPlaces []naver.Bookmark `json:"fullPlaces,omitempty"`
}

func (s *server) getBookmarks(ctx context.Context, req *mcp.CallToolRequest, args getBookmarksArgs) (*mcp.CallToolResult, getBookmarksResult, error) {
	var out getBookmarksResult
	shareID, err := s.resolveList(ctx, args.List)
	if err != nil {
		return nil, out, err
	}
	share, err := s.client.GetAllBookmarks(ctx, shareID)
	if err != nil {
		return nil, out, err
	}

	fillSummary(&out.Folder, &share.Folder)
	q := strings.ToLower(strings.TrimSpace(args.Query))
	var filtered []naver.Bookmark
	for _, b := range share.BookmarkList {
		if !b.Available && !args.Include {
			continue
		}
		if q != "" && !matches(b, q) {
			continue
		}
		filtered = append(filtered, b)
	}
	out.Total = len(filtered)

	if args.Offset > len(filtered) {
		filtered = nil
	} else {
		filtered = filtered[args.Offset:]
	}
	if args.Limit > 0 && args.Limit < len(filtered) {
		filtered = filtered[:args.Limit]
	}
	out.Returned = len(filtered)

	if args.Full {
		out.FullPlaces = filtered
	} else {
		out.Places = make([]compactPlace, len(filtered))
		for i, b := range filtered {
			out.Places[i] = compactPlace{
				SID:      b.SID,
				Name:     b.Title(),
				Category: b.MCIDName,
				Address:  b.Address,
				Lng:      b.Px,
				Lat:      b.Py,
				Memo:     b.Memo,
			}
		}
	}
	return nil, out, nil
}

func matches(b naver.Bookmark, q string) bool {
	for _, s := range []string{b.Name, b.DisplayName, b.Address, b.MCIDName, b.Memo} {
		if strings.Contains(strings.ToLower(s), q) {
			return true
		}
	}
	return false
}

// --- resolve_share ---

type resolveShareArgs struct {
	URL string `json:"url" jsonschema:"naver.me 단축 URL 또는 map.naver.com 공유 URL"`
}

func (s *server) resolveShare(ctx context.Context, req *mcp.CallToolRequest, args resolveShareArgs) (*mcp.CallToolResult, folderSummary, error) {
	var out folderSummary
	share, err := s.fetchShareMeta(ctx, args.URL)
	if err != nil {
		return nil, out, err
	}
	fillSummary(&out, &share.Folder)
	return nil, out, nil
}

// --- helpers ---

// resolveList는 설정된 리스트 이름, URL, shareId 어느 것이든 shareId로 바꾼다.
func (s *server) resolveList(ctx context.Context, list string) (string, error) {
	target := strings.TrimSpace(list)
	for _, entry := range s.config.Lists {
		if strings.EqualFold(entry.Name, target) {
			target = entry.URL
			break
		}
	}
	s.mu.Lock()
	cached, ok := s.idCache[target]
	s.mu.Unlock()
	if ok {
		return cached, nil
	}
	shareID, err := s.client.ResolveShareID(ctx, target)
	if err != nil {
		return "", fmt.Errorf("리스트를 찾을 수 없음 %q: %w", list, err)
	}
	s.mu.Lock()
	s.idCache[target] = shareID
	s.mu.Unlock()
	return shareID, nil
}

func (s *server) fetchShareMeta(ctx context.Context, urlOrID string) (*naver.ShareResponse, error) {
	shareID, err := s.resolveList(ctx, urlOrID)
	if err != nil {
		return nil, err
	}
	return s.client.GetShare(ctx, shareID)
}

func fetchCmd(s *server, target string) error {
	ctx := context.Background()
	shareID, err := s.resolveList(ctx, target)
	if err != nil {
		return err
	}
	share, err := s.client.GetAllBookmarks(ctx, shareID)
	if err != nil {
		return err
	}
	fmt.Printf("폴더: %s (shareId=%s)\n장소: %d개 (응답 %d개)\n",
		share.Folder.Name, share.Folder.ShareID, share.Folder.BookmarkCount, len(share.BookmarkList))
	for i, b := range share.BookmarkList {
		if i >= 5 {
			fmt.Printf("... 외 %d개\n", len(share.BookmarkList)-5)
			break
		}
		fmt.Printf("  [%s] %s — %s (%s)\n", b.MCIDName, b.Title(), b.Address, b.SID)
	}
	return nil
}
