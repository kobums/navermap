package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kobums/navermap/internal/config"
	"github.com/kobums/navermap/internal/naver"
	"github.com/kobums/navermap/internal/places"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	version = "0.2.0"
	// 전체 리스트 병합 결과 캐시 유지 시간. 네이버 조회가 리스트당 수 초라
	// 코스 설계처럼 툴을 연달아 부를 때 매번 다시 가져오지 않는다.
	placesCacheTTL = 10 * time.Minute
)

type server struct {
	client *naver.Client
	config config.Config

	mu      sync.Mutex
	idCache map[string]string // url -> shareId

	placesMu    sync.Mutex
	placesCache map[string]*places.Place
	placesAt    time.Time
}

func main() {
	configPath := flag.String("config", "config.json", "리스트 설정 파일 경로")
	httpAddr := flag.String("http", "", "지정하면 stdio 대신 streamable HTTP로 서빙 (예: :8787)")
	flag.Parse()

	s := &server{client: naver.NewClient(), idCache: map[string]string{}}
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	s.config = cfg

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
	mcp.AddTool(m, &mcp.Tool{
		Name: "search_places",
		Description: "설정된 모든 리스트를 병합해 장소를 검색합니다. 데이트 코스 설계의 시작점. " +
			"query(이름/주소/카테고리/메모 부분일치), region(주소 부분일치, 예: '서울 마포'), " +
			"unvisitedOnly(안 가본 곳만) 필터를 조합할 수 있습니다.",
	}, s.searchPlaces)
	mcp.AddTool(m, &mcp.Tool{
		Name: "find_nearby",
		Description: "기준점 주변의 저장된 장소를 가까운 순으로 반환합니다. 코스에서 다음 장소를 고를 때 사용. " +
			"near에는 장소 이름(저장된 장소), SID, 또는 '위도,경도' 좌표를 넣을 수 있습니다.",
	}, s.findNearby)

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

// --- search_places / find_nearby ---

type searchPlacesArgs struct {
	Query         string `json:"query,omitempty" jsonschema:"이름/주소/카테고리/메모 부분일치"`
	Region        string `json:"region,omitempty" jsonschema:"주소 부분일치 필터, 예: '서울 마포' '수원'"`
	Category      string `json:"category,omitempty" jsonschema:"카테고리 이름 부분일치, 예: '카페' '음식점'"`
	UnvisitedOnly bool   `json:"unvisitedOnly,omitempty" jsonschema:"true면 가본카페 리스트에 없는 곳만"`
	Limit         int    `json:"limit,omitempty" jsonschema:"기본 20"`
}

type placeResult struct {
	SID        string   `json:"sid"`
	Name       string   `json:"name"`
	Category   string   `json:"category,omitempty"`
	Address    string   `json:"address,omitempty"`
	Lng        float64  `json:"lng"`
	Lat        float64  `json:"lat"`
	Memo       string   `json:"memo,omitempty"`
	Visited    bool     `json:"visited"`
	Lists      []string `json:"lists"`
	DistanceKm float64  `json:"distanceKm,omitempty"`
}

type searchPlacesResult struct {
	Total    int           `json:"total"`
	Returned int           `json:"returned"`
	Places   []placeResult `json:"places"`
}

func (s *server) searchPlaces(ctx context.Context, req *mcp.CallToolRequest, args searchPlacesArgs) (*mcp.CallToolResult, searchPlacesResult, error) {
	var out searchPlacesResult
	all, err := s.allPlaces(ctx)
	if err != nil {
		return nil, out, err
	}
	q := strings.ToLower(strings.TrimSpace(args.Query))
	region := strings.ToLower(strings.TrimSpace(args.Region))
	category := strings.ToLower(strings.TrimSpace(args.Category))

	var hits []*places.Place
	for _, p := range all {
		if !p.Available {
			continue
		}
		if args.UnvisitedOnly && p.Visited {
			continue
		}
		if q != "" && !containsAny(q, p.Title(), p.Name, p.Address, p.MCIDName, p.Memo) {
			continue
		}
		if region != "" && !containsAny(region, p.Address) {
			continue
		}
		if category != "" && !containsAny(category, p.MCIDName) {
			continue
		}
		hits = append(hits, p)
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Title() < hits[j].Title() })
	out.Total = len(hits)
	limit := args.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit < len(hits) {
		hits = hits[:limit]
	}
	out.Returned = len(hits)
	out.Places = toResults(hits)
	return nil, out, nil
}

type findNearbyArgs struct {
	Near          string  `json:"near" jsonschema:"기준점: 저장된 장소 이름, SID, 또는 '위도,경도'"`
	RadiusKm      float64 `json:"radiusKm,omitempty" jsonschema:"반경 km, 기본 1.5"`
	Category      string  `json:"category,omitempty" jsonschema:"카테고리 이름 부분일치"`
	UnvisitedOnly bool    `json:"unvisitedOnly,omitempty"`
	Limit         int     `json:"limit,omitempty" jsonschema:"기본 15"`
}

type findNearbyResult struct {
	Center   string        `json:"center" jsonschema:"해석된 기준점"`
	Total    int           `json:"total"`
	Returned int           `json:"returned"`
	Places   []placeResult `json:"places"`
}

func (s *server) findNearby(ctx context.Context, req *mcp.CallToolRequest, args findNearbyArgs) (*mcp.CallToolResult, findNearbyResult, error) {
	var out findNearbyResult
	all, err := s.allPlaces(ctx)
	if err != nil {
		return nil, out, err
	}
	lat, lng, centerName, centerSID, err := resolveCenter(all, args.Near)
	if err != nil {
		return nil, out, err
	}
	out.Center = centerName

	radius := args.RadiusKm
	if radius <= 0 {
		radius = 1.5
	}
	category := strings.ToLower(strings.TrimSpace(args.Category))

	var hits []*places.Place
	dist := map[string]float64{}
	for _, p := range all {
		if !p.Available || p.SID == centerSID {
			continue
		}
		if args.UnvisitedOnly && p.Visited {
			continue
		}
		if category != "" && !containsAny(category, p.MCIDName) {
			continue
		}
		d := places.DistanceKm(lat, lng, p.Py, p.Px)
		if d > radius {
			continue
		}
		dist[p.SID] = d
		hits = append(hits, p)
	}
	sort.Slice(hits, func(i, j int) bool { return dist[hits[i].SID] < dist[hits[j].SID] })
	out.Total = len(hits)
	limit := args.Limit
	if limit <= 0 {
		limit = 15
	}
	if limit < len(hits) {
		hits = hits[:limit]
	}
	out.Returned = len(hits)
	out.Places = toResults(hits)
	for i := range out.Places {
		out.Places[i].DistanceKm = round2(dist[out.Places[i].SID])
	}
	return nil, out, nil
}

// resolveCenter는 '위도,경도' 좌표, SID, 장소 이름(완전 일치 우선, 부분 일치 차선)
// 순서로 기준점을 해석한다.
func resolveCenter(all map[string]*places.Place, near string) (lat, lng float64, name, sid string, err error) {
	near = strings.TrimSpace(near)
	if la, ln, ok := parseLatLng(near); ok {
		return la, ln, near, "", nil
	}
	if p, ok := all[near]; ok {
		return p.Py, p.Px, p.Title(), p.SID, nil
	}
	lower := strings.ToLower(near)
	var partial *places.Place
	for _, p := range all {
		title := strings.ToLower(p.Title())
		if title == lower || strings.ToLower(p.Name) == lower {
			return p.Py, p.Px, p.Title(), p.SID, nil
		}
		if partial == nil && strings.Contains(title, lower) {
			partial = p
		}
	}
	if partial != nil {
		return partial.Py, partial.Px, partial.Title(), partial.SID, nil
	}
	return 0, 0, "", "", fmt.Errorf("기준점을 찾을 수 없음: %q (저장된 장소 이름, SID, 또는 '위도,경도')", near)
}

func parseLatLng(s string) (lat, lng float64, ok bool) {
	a, b, found := strings.Cut(s, ",")
	if !found {
		return 0, 0, false
	}
	lat, err1 := strconv.ParseFloat(strings.TrimSpace(a), 64)
	lng, err2 := strconv.ParseFloat(strings.TrimSpace(b), 64)
	if err1 != nil || err2 != nil || lat < 33 || lat > 39 || lng < 124 || lng > 132 {
		return 0, 0, false
	}
	return lat, lng, true
}

func toResults(hits []*places.Place) []placeResult {
	results := make([]placeResult, len(hits))
	for i, p := range hits {
		results[i] = placeResult{
			SID:      p.SID,
			Name:     p.Title(),
			Category: p.MCIDName,
			Address:  p.Address,
			Lng:      p.Px,
			Lat:      p.Py,
			Memo:     p.Memo,
			Visited:  p.Visited,
			Lists:    p.Lists,
		}
	}
	return results
}

func containsAny(needle string, haystacks ...string) bool {
	for _, h := range haystacks {
		if strings.Contains(strings.ToLower(h), needle) {
			return true
		}
	}
	return false
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}

// allPlaces는 설정된 모든 리스트를 병합해 돌려준다. TTL 캐시.
func (s *server) allPlaces(ctx context.Context) (map[string]*places.Place, error) {
	s.placesMu.Lock()
	defer s.placesMu.Unlock()
	if s.placesCache != nil && time.Since(s.placesAt) < placesCacheTTL {
		return s.placesCache, nil
	}
	all, err := places.FetchAll(ctx, s.client, s.config.Lists)
	if err != nil {
		return nil, err
	}
	s.placesCache = all
	s.placesAt = time.Now()
	return all, nil
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
