# navermap

네이버 지도 저장 리스트(공유 URL)를 읽어오는 MCP 서버.

공유 URL 뒤의 비공식 API(`pages.map.naver.com/save-pages/api/maps-bookmark/v3`)를 사용한다.
인증이 필요 없는 읽기 전용이며, API 상세는 [docs/naver-bookmark-api.md](docs/naver-bookmark-api.md) 참고.

## 빌드 & 확인

```sh
go build ./cmd/navermap-mcp

# 디버그: 리스트 하나 가져와서 요약 출력
./navermap-mcp fetch "https://naver.me/GvfDBFQX"
./navermap-mcp fetch "아직 가보지 못한 카페"   # config.json에 있는 이름도 가능
```

## MCP 서버로 사용

stdio (Claude Code 로컬 등록):

```sh
claude mcp add navermap -- /path/to/navermap-mcp -config /path/to/config.json
```

streamable HTTP (서버 상시 배포용):

```sh
./navermap-mcp -http :8787 -config config.json
```

## 툴

| 툴 | 설명 |
|---|---|
| `list_folders` | config.json에 등록된 리스트 전체의 메타데이터 |
| `get_bookmarks` | 리스트의 장소 목록. `list`에 리스트 이름/공유 URL/shareId, `query` 부분일치 필터, `offset`/`limit`, `full`(원본 필드 전체) 지원 |
| `resolve_share` | 공유 URL → shareId + 폴더 메타데이터 |

## config.json

```json
{
  "lists": [
    { "name": "아직 가보지 못한 카페", "url": "https://naver.me/GvfDBFQX" }
  ]
}
```

## 주의

내부 API라 예고 없이 바뀔 수 있다. 클라이언트에 재시도(3회, 지수 백오프)가 들어있지만,
스키마가 바뀌면 `internal/naver`의 타입/클라이언트만 고치면 되도록 분리되어 있다.
