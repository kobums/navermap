// Package places는 여러 저장 리스트를 SID(플레이스 ID) 기준으로 병합한
// Place 모델. navermap-sync와 navermap-mcp가 공유한다.
package places

import (
	"context"
	"fmt"
	"log"
	"math"

	"github.com/kobums/navermap/internal/config"
	"github.com/kobums/navermap/internal/naver"
)

type Place struct {
	naver.Bookmark
	Lists   []string
	Visited bool
}

// FetchAll은 설정된 모든 리스트를 가져와 SID 기준으로 병합한다.
// 리스트 하나라도 실패하면 전체 실패를 돌려준다 — 부분 데이터로
// 다운스트림(아카이브 등)이 오작동하는 것을 막기 위해서다.
func FetchAll(ctx context.Context, client *naver.Client, lists []config.ListEntry) (map[string]*Place, error) {
	merged := map[string]*Place{}
	for _, entry := range lists {
		shareID, err := client.ResolveShareID(ctx, entry.URL)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name, err)
		}
		share, err := client.GetAllBookmarks(ctx, shareID)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name, err)
		}
		skipped := 0
		for _, b := range share.BookmarkList {
			if b.SID == "" {
				skipped++ // 주소 북마크 등 플레이스가 아닌 항목
				continue
			}
			p, ok := merged[b.SID]
			if !ok {
				p = &Place{Bookmark: b}
				merged[b.SID] = p
			}
			p.Lists = append(p.Lists, entry.Name)
			p.Visited = p.Visited || entry.Visited
			if p.Memo == "" && b.Memo != "" {
				p.Memo = b.Memo
			}
		}
		log.Printf("  %s: %d곳 (플레이스 아님 %d곳 제외)", entry.Name, len(share.BookmarkList)-skipped, skipped)
	}
	return merged, nil
}

// DistanceKm은 두 좌표 사이의 하버사인 거리(km).
func DistanceKm(lat1, lng1, lat2, lng2 float64) float64 {
	const earthRadiusKm = 6371.0
	rad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat := rad(lat2 - lat1)
	dLng := rad(lng2 - lng1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(rad(lat1))*math.Cos(rad(lat2))*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthRadiusKm * math.Asin(math.Sqrt(a))
}
