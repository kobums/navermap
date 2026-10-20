package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/http"
	"os"
)

// basicAuthFromEnv는 MCP_BASIC_USER / MCP_BASIC_PASS가 둘 다 설정되면 HTTP MCP 핸들러를
// Basic 인증으로 감싼다. 둘 다 비어 있으면 인증 없이(기존 동작) 그대로 돌려준다.
// 하나만 설정된 경우는 설정 실수로 보고 에러를 낸다 (조용히 공개되는 것을 막기 위해).
func basicAuthFromEnv(next http.Handler) (http.Handler, bool, error) {
	user, pass := os.Getenv("MCP_BASIC_USER"), os.Getenv("MCP_BASIC_PASS")
	if user == "" && pass == "" {
		return next, false, nil
	}
	if user == "" || pass == "" {
		return nil, false, fmt.Errorf("MCP_BASIC_USER와 MCP_BASIC_PASS는 둘 다 설정하거나 둘 다 비워야 합니다")
	}
	return basicAuth(next, user, pass), true, nil
}

func basicAuth(next http.Handler, user, pass string) http.Handler {
	wantUser := sha256.Sum256([]byte(user))
	wantPass := sha256.Sum256([]byte(pass))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		gotUser := sha256.Sum256([]byte(u))
		gotPass := sha256.Sum256([]byte(p))
		// 해시끼리 상수 시간 비교 (길이·내용으로 타이밍 추측 불가). 두 비교를 모두 수행한다.
		userOK := subtle.ConstantTimeCompare(gotUser[:], wantUser[:]) == 1
		passOK := subtle.ConstantTimeCompare(gotPass[:], wantPass[:]) == 1
		if !ok || !userOK || !passOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="navermap-mcp", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
