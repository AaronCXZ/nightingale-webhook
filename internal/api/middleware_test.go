package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// testHandler 返回 200 OK，用于验证中间件是否正确调用了 next
func testHandler(status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	})
}

func TestRecoveryMiddleware(t *testing.T) {
	// handler 会 panic，中间件应捕获并返回 500
	panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})

	wrapped := recoveryMiddleware(panicking)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("状态码应为 %d，got %d", http.StatusInternalServerError, rr.Code)
	}

	// 验证响应是 JSON 格式
	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("响应应为 JSON: %v", err)
	}
	if resp["error"] != "internal server error" {
		t.Errorf("error 字段应为 'internal server error'，got %q", resp["error"])
	}
}

func TestRecoveryMiddlewareNormal(t *testing.T) {
	// 正常请求不应受影响
	wrapped := recoveryMiddleware(testHandler(http.StatusOK, "ok"))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("状态码应为 %d，got %d", http.StatusOK, rr.Code)
	}
	if rr.Body.String() != "ok" {
		t.Errorf("body 应为 'ok'，got %q", rr.Body.String())
	}
}

func TestRequestIDMiddlewareAutoGenerate(t *testing.T) {
	// 无 X-Request-ID header，中间件自动生成并设置在响应 header 上
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := requestIDMiddleware(handler)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	wrapped.ServeHTTP(rr, req)

	respReqID := rr.Header().Get("X-Request-ID")
	if respReqID == "" {
		t.Error("X-Request-ID 响应 header 不应为空")
	}
	if len(respReqID) != 8 {
		t.Errorf("自动生成的 RequestID 应为 8 字符，got %q (len=%d)", respReqID, len(respReqID))
	}
}

func TestRequestIDMiddlewarePassThrough(t *testing.T) {
	// 已有 X-Request-ID header，应透传
	expectedID := "my-custom-id"
	var gotReqID string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReqID = r.Header.Get("X-Request-ID")
		w.WriteHeader(http.StatusOK)
	})

	wrapped := requestIDMiddleware(handler)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", expectedID)

	wrapped.ServeHTTP(rr, req)

	respReqID := rr.Header().Get("X-Request-ID")
	if respReqID != expectedID {
		t.Errorf("响应 X-Request-ID 应为 %q，got %q", expectedID, respReqID)
	}
	if gotReqID != expectedID {
		t.Errorf("请求 X-Request-ID 应为 %q，got %q", expectedID, gotReqID)
	}
}

func TestCORSMiddlewareOptions(t *testing.T) {
	// OPTIONS 请求应返回 204 + 正确 CORS header
	wrapped := corsMiddleware(testHandler(http.StatusOK, "ok"))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/", nil)

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("OPTIONS 状态码应为 %d，got %d", http.StatusNoContent, rr.Code)
	}

	assertCORSHeaders(t, rr)
}

func TestCORSMiddlewareNonOptions(t *testing.T) {
	// 非 OPTIONS 请求应正常通过，并设置 CORS header
	wrapped := corsMiddleware(testHandler(http.StatusOK, "ok"))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("状态码应为 %d，got %d", http.StatusOK, rr.Code)
	}
	if rr.Body.String() != "ok" {
		t.Errorf("body 应为 'ok'，got %q", rr.Body.String())
	}
	assertCORSHeaders(t, rr)
}

func assertCORSHeaders(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()

	if origin := rr.Header().Get("Access-Control-Allow-Origin"); origin != "*" {
		t.Errorf("Access-Control-Allow-Origin 应为 *，got %q", origin)
	}
	if methods := rr.Header().Get("Access-Control-Allow-Methods"); methods == "" {
		t.Error("Access-Control-Allow-Methods 不应为空")
	}
	if headers := rr.Header().Get("Access-Control-Allow-Headers"); headers == "" {
		t.Error("Access-Control-Allow-Headers 不应为空")
	}
}

func TestChainMiddleware(t *testing.T) {
	// 验证链式组合后所有中间件都生效
	var order []string

	m1 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "m1")
			next.ServeHTTP(w, r)
		})
	}
	m2 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "m2")
			next.ServeHTTP(w, r)
		})
	}
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "final")
		w.WriteHeader(http.StatusOK)
	})

	// m2 在最外层（先执行），m1 在里层
	chained := chainMiddleware(final, m1, m2)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	chained.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("状态码应为 %d，got %d", http.StatusOK, rr.Code)
	}

	// chainMiddleware 从右到左包装：m1(m2(final))
	// 执行顺序：m1 → m2 → final
	expected := []string{"m1", "m2", "final"}
	for i, v := range expected {
		if order[i] != v {
			t.Errorf("执行顺序[%d] 应为 %s，got %s", i, v, order[i])
		}
	}
}
