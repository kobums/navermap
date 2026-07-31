// navermap-sync는 네이버 지도 저장 리스트를 Notion DB로 동기화한다.
//
//	NOTION_TOKEN=secret_xxx navermap-sync -config config.json [-dry-run]
//
// 동작:
//   - config의 모든 리스트를 네이버에서 가져와 SID(플레이스 ID) 기준으로 병합
//   - Notion DB 전체를 조회해 SID 기준 upsert (생성/변경된 것만 갱신)
//   - 모든 리스트에서 사라진 장소는 아카이브 (SID 없는 수동 페이지는 건드리지 않음)
//   - visited 플래그가 붙은 리스트의 장소는 "가봤음" 체크 (수동 체크는 해제하지 않음)
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/kobums/navermap/internal/config"
	"github.com/kobums/navermap/internal/naver"
	"github.com/kobums/navermap/internal/notion"
	"github.com/kobums/navermap/internal/places"
)

func main() {
	configPath := flag.String("config", "config.json", "설정 파일 경로")
	dryRun := flag.Bool("dry-run", false, "Notion에 쓰지 않고 계획만 출력")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	if len(cfg.Lists) == 0 {
		log.Fatal("설정에 리스트가 없습니다")
	}
	token := os.Getenv("NOTION_TOKEN")
	if token == "" && !*dryRun {
		log.Fatal("NOTION_TOKEN 환경변수가 필요합니다 (dry-run은 네이버 조회만 하므로 불필요)")
	}
	if cfg.Notion.DatabaseID == "" && !*dryRun {
		log.Fatal("설정에 notion.databaseId가 필요합니다")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	client := naver.NewClient()
	all, err := places.FetchAll(ctx, client, cfg.Lists)
	if err != nil {
		// 리스트 하나라도 실패하면 전체 중단 — 부분 데이터로 아카이브가 오작동하는 것을 막는다.
		log.Fatalf("네이버 조회 실패, 동기화 중단: %v", err)
	}
	log.Printf("네이버: 리스트 %d개에서 장소 %d곳 (SID 기준 병합)", len(cfg.Lists), len(all))

	nc := notion.NewClient(token, cfg.Notion.DatabaseID)
	var existing []notion.Page
	if token != "" && cfg.Notion.DatabaseID != "" {
		if existing, err = nc.QueryAllPages(ctx); err != nil {
			log.Fatalf("Notion DB 조회 실패: %v", err)
		}
	}
	bySID := map[string]notion.Page{}
	for _, p := range existing {
		if p.SID != "" {
			bySID[p.SID] = p
		}
	}
	log.Printf("Notion: 기존 페이지 %d개 (SID 있는 것 %d개)", len(existing), len(bySID))

	var creates, updates, archives, unchanged int
	for _, sid := range sortedKeys(all) {
		m := all[sid]
		page, exists := bySID[sid]
		switch {
		case !exists:
			creates++
			if *dryRun {
				logSample("생성", creates, m.Title(), m.Lists)
				continue
			}
			if err := nc.CreatePage(ctx, placeProperties(m, false)); err != nil {
				log.Printf("생성 실패 %s(%s): %v", m.Title(), sid, err)
			}
		case needsUpdate(page, m):
			updates++
			if *dryRun {
				logSample("갱신", updates, m.Title(), m.Lists)
				continue
			}
			// 수동으로 체크한 가봤음은 유지
			keepVisited := page.Visited || m.Visited
			props := placeProperties(m, keepVisited)
			if err := nc.UpdatePage(ctx, page.ID, props); err != nil {
				log.Printf("갱신 실패 %s(%s): %v", m.Title(), sid, err)
			}
		default:
			unchanged++
		}
	}
	for sid, page := range bySID {
		if _, still := all[sid]; still {
			continue
		}
		archives++
		if *dryRun {
			logSample("아카이브", archives, page.Name, page.Lists)
			continue
		}
		if err := nc.ArchivePage(ctx, page.ID); err != nil {
			log.Printf("아카이브 실패 %s(%s): %v", page.Name, sid, err)
		}
	}

	mode := ""
	if *dryRun {
		mode = " (dry-run)"
	}
	log.Printf("완료%s: 생성 %d, 갱신 %d, 변화 없음 %d, 아카이브 %d", mode, creates, updates, unchanged, archives)
}

// needsUpdate는 조회해온 필드(이름/메모/리스트/가봤음) 기준으로 변경 여부를 판단한다.
func needsUpdate(page notion.Page, m *places.Place) bool {
	if page.Name != m.Title() || page.Memo != m.Memo {
		return true
	}
	if m.Visited && !page.Visited {
		return true
	}
	return !sameSet(page.Lists, m.Lists)
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := map[string]bool{}
	for _, s := range a {
		set[s] = true
	}
	for _, s := range b {
		if !set[s] {
			return false
		}
	}
	return true
}

func placeProperties(m *places.Place, visited bool) map[string]any {
	b := m.Bookmark
	listOpts := make([]map[string]any, len(m.Lists))
	for i, name := range m.Lists {
		listOpts[i] = map[string]any{"name": name}
	}
	props := map[string]any{
		"이름":   map[string]any{"title": []any{textContent(b.Title())}},
		"SID":  map[string]any{"rich_text": []any{textContent(b.SID)}},
		"주소":   map[string]any{"rich_text": []any{textContent(b.Address)}},
		"리스트":  map[string]any{"multi_select": listOpts},
		"가봤음":  map[string]any{"checkbox": visited || m.Visited},
		"이용가능": map[string]any{"checkbox": b.Available},
		"지도":   map[string]any{"url": "https://map.naver.com/p/entry/place/" + b.SID},
		"위도":   map[string]any{"number": b.Py},
		"경도":   map[string]any{"number": b.Px},
		"저장일":  map[string]any{"date": map[string]any{"start": time.UnixMilli(b.CreationTime).Format("2006-01-02")}},
	}
	if b.MCIDName != "" {
		props["카테고리"] = map[string]any{"select": map[string]any{"name": b.MCIDName}}
	}
	if region := regionOf(b.Address); region != "" {
		props["지역"] = map[string]any{"select": map[string]any{"name": region}}
	}
	if b.Memo != "" {
		props["메모"] = map[string]any{"rich_text": []any{textContent(b.Memo)}}
	}
	return props
}

func textContent(s string) map[string]any {
	// Notion rich_text 한 조각의 최대 길이는 2000자
	if len(s) > 2000 {
		s = s[:2000]
	}
	return map[string]any{"text": map[string]any{"content": s}}
}

// regionOf는 주소 첫 토큰에서 시/도를 뽑는다. 긴 접두어를 먼저 검사한다.
var regionPrefixes = []struct{ prefix, region string }{
	{"충청북", "충북"}, {"충청남", "충남"}, {"전라북", "전북"}, {"전라남", "전남"},
	{"경상북", "경북"}, {"경상남", "경남"},
	{"서울", "서울"}, {"부산", "부산"}, {"대구", "대구"}, {"인천", "인천"},
	{"광주", "광주"}, {"대전", "대전"}, {"울산", "울산"}, {"세종", "세종"},
	{"경기", "경기"}, {"강원", "강원"}, {"충북", "충북"}, {"충남", "충남"},
	{"전북", "전북"}, {"전남", "전남"}, {"경북", "경북"}, {"경남", "경남"},
	{"제주", "제주"},
}

func regionOf(address string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(address), " ")
	for _, p := range regionPrefixes {
		if strings.HasPrefix(first, p.prefix) {
			return p.region
		}
	}
	return ""
}

func sortedKeys(m map[string]*places.Place) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func logSample(kind string, n int, name string, lists []string) {
	if n <= 5 {
		log.Printf("  [%s] %s (%s)", kind, name, strings.Join(lists, ", "))
	}
}
