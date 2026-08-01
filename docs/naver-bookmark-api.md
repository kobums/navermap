# 네이버 지도 저장 리스트 내부 API (2026-07-30 확인)

공유 URL 뒤에 있는 비공식 API. 인증(쿠키) 불필요, 특별한 헤더 없이 plain GET으로 동작 확인.
내부 API이므로 스키마/경로가 예고 없이 바뀔 수 있음 — 파싱 레이어를 인터페이스로 분리하고 실패 시 알림 필수.

## 1. 공유 URL 해석

```
GET https://naver.me/{shortCode}
→ 307 Location: https://map.naver.com/p/favorite/sharedPlace/folder/{shareId}
```

- `shareId`: 32자리 hex. 이것만 있으면 아래 API 호출 가능.
- 리다이렉트는 따라가지 말고 `Location` 헤더에서 shareId만 추출하면 됨.

## 2. 리스트 조회 (핵심 엔드포인트)

```
GET https://pages.map.naver.com/save-pages/api/maps-bookmark/v3/shares/{shareId}/bookmarks?start={offset}&limit={limit}
```

- `limit=500` 정상 동작 확인, `start` 오프셋 페이지네이션 (644개 리스트: start=0 → 500개, start=500 → 144개 반환 확인).
- 폴더 메타데이터만 필요하면 `GET .../v3/shares/{shareId}` (동일한 `folder` 객체 + 앞부분 bookmarkList 반환).
- `GET .../v3/shares/{shareId}/categories` 도 존재 (카테고리 집계).

### 응답 구조 (top-level)

```json
{
  "folder": { ... },
  "bookmarkList": [ ... ],
  "unavailableCount": 0,
  "mismatchedCount": 0,
  "removed": []
}
```

### folder 객체 주요 필드

| 필드 | 예시 | 비고 |
|---|---|---|
| `folderId` | 74361762 | 내부 숫자 ID |
| `shareId` | "b8079a9d..." | 공유 ID (upsert 키로는 이쪽) |
| `name` | "카페" | 리스트 이름 |
| `memo` | "가본 카페는..." | 리스트 설명 |
| `externalLink` | "https://naver.me/..." | 작성자가 걸어둔 외부 링크 |
| `bookmarkCount` | 227 | 전체 개수 (페이지네이션 종료 판정용) |
| `placeUserProfile` | {nick, reviewPageUrl, ...} | 작성자 프로필 (없으면 null) |
| `lastUseTime` / `creationTime` | epoch ms | 변경 감지에 lastUseTime 활용 가능 |
| `followCount` / `viewCount` | 2 / 40 | |

### bookmarkList[] 항목 주요 필드

| 필드 | 예시 | 비고 |
|---|---|---|
| `bookmarkId` | 1271258954 | 북마크 고유 ID |
| `sid` | "1384498557" | **네이버 플레이스 ID** — place.naver.com/restaurant/{sid} 등과 연결되는 핵심 키 |
| `name` / `displayName` | "오고르" / "" | displayName은 사용자가 바꾼 이름 |
| `address` | "경기 수원시 팔달구 ..." | 지번/도로명 혼재 |
| `px` / `py` | 127.0157735 / 37.286409 | 경도 / 위도 |
| `mcid` / `mcidName` | "CAFE" / "카페" | 카테고리 코드/이름 |
| `memo` | 사용자 메모 | null 가능 |
| `creationTime` / `lastUpdateTime` / `useTime` | epoch ms | |
| `available` | true | 폐업 등으로 false 가능 |
| `bookmarkMismatchInfo` | {isMatched, details} | 장소 정보 불일치 여부 |
| `rcode` | "02115131" | 행정동 코드 |
| `cidPath` | ["220036", ...] | 카테고리 계층 경로 |
| `type` | "place" | 주소 북마크 등 다른 type 존재 가능성 있음 |

## 3. 확인된 대상 리스트

| 리스트 | 공유 URL | shareId | 폴더명 | 개수(추가 시점) |
|---|---|---|---|---|
| 아직 가보지 못한 카페 | https://naver.me/GvfDBFQX | b8079a9d428142a4a68175fd5b7c0a8f | 카페 | 227 |
| 커피 인플루언서 추천 | https://naver.me/GOPkA2jH | 0b5b3fdc66fa405abc8440105d786ad7 | 사냥도감 | 644 |
| 가보자 곰 추천 | https://naver.me/5MVmJw5C | 863dc82b56c94070879b3cfa2c706b1f | 가보자곰카페 | 422 |
| 가본카페 (visited) | https://naver.me/FP8OQYlq | 2ed836c5d4b342ea84a53d2a6aed3985 | 가본카페 | 110 |
| 가보고싶은곳 | https://naver.me/x0O9xRnV | 13102cadd2714851a3e17b6daa808016 | 가보고싶은곳 | 25 |
| 가본 식당 (visited) | https://naver.me/5ulZWjgv | cae6273084ef49769526a63d12989ac2 | 가본 식당 | 0 |
| 맛집 | https://naver.me/xcAMNdKB | 17e7cd6792e3418b88ef0528abd6b19f | 맛집 | 183 |
| 준영의 K리그 맛집리스트 | https://naver.me/5wWyDcpn | dbc19f3e06084549a4d5f569841b62f8 | 준영의 K리그 맛집리스트 | 637 |
| 번개로드 | https://naver.me/5GpWi0cI | 418d7c4078a84832a072495778193383 | 번개로드 | 404 |
| 버거맵🍔 | https://naver.me/FW62I4ea | 1649509a58a84433a961d70d5d27776c | 버거맵🍔 | 111 |
| 여자친구 맛집 | https://naver.me/5nhAxoZs | 463d5609231d45009c0419aa1278bf06 | 맛집 | 260 |

## 4. 운영 메모

- 간헐적으로 빈 응답(비 JSON)이 올 수 있음 → 재시도 1회로 해결됨. 클라이언트에 retry + backoff 넣을 것.
- 로그인 필요한 쓰기 계열(`map.naver.com/p/api/bookmark?...`)은 405 "로그인정보가 없습니다" 반환 — 쓰기는 이 프로젝트 범위 밖 (자체 DB를 마스터로 사용).
- 데이터 출처: 웹 앱 `pages.map.naver.com/save-pages` (버전 1.1.18-cd13dc3) 번들 분석으로 확인.
