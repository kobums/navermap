# navermap

네이버 지도 저장 리스트(공유 URL)를 읽어 MCP로 제공하고 Notion으로 동기화하는 Go 프로젝트.

## 작업 규칙

- **기능을 추가/변경하면 문서를 같은 커밋에서 함께 갱신한다.**
  - MCP 툴 변경 → README.md 툴 표
  - 네이버 내부 API 관련 발견 → docs/naver-bookmark-api.md
  - 배포/운영 변경 → README.md 서버 배포 섹션
- 네이버 내부 API 호출 코드는 전부 `internal/naver`에 격리한다 (스키마 변경 시 그 레이어만 교체).
- 쓰기(네이버 계정에 장소 저장)는 로그인 쿠키가 필요해 범위 밖. 자체 데이터(Notion)가 마스터.

## 구조

- `internal/naver` — 공유 URL 해석 + v3 shares API 클라이언트 (무인증, 재시도/페이지네이션)
- `internal/places` — 여러 리스트를 SID 기준으로 병합한 Place 모델 (sync와 mcp가 공유)
- `internal/notion` — 공식 Notion REST API 최소 클라이언트
- `internal/config` — config.json 로더 (리스트 목록 + notion.databaseId)
- `cmd/navermap-mcp` — MCP 서버 (stdio / streamable HTTP), `fetch` 디버그 서브커맨드
- `cmd/navermap-sync` — 네이버 → Notion 동기화 배치 (SID upsert, `-dry-run`)

## 배포

서버(Vultr, root@140.82.12.99)의 `/data`에서 docker-compose로 운영. 독립형 `docker-compose`만 있음(`docker compose` 플러그인 없음). 이미지는 `make push`로 Docker Hub `kobums/navermap`에 푸시 후 서버에서 pull. compose SSOT는 `~/develop/vultr_docker/docker-compose.yml` — 서버 파일이 드리프트될 수 있으니 덮어쓰기 전 diff 필수. 동기화 cron은 host crontab에서 6시간마다.
