package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsUseRouteTemplatesForUnknownIdentifiers(t *testing.T) {
	handler := NewHandler()
	for _, path := range []string{"/unknown-one", "/unknown-two", "/healthz"} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	body := response.Body.String()
	if !strings.Contains(body, `rfq_http_requests_total{route="unmatched",status="4xx"} 2`) {
		t.Fatal(body)
	}
	if strings.Contains(body, "unknown-one") || strings.Contains(body, "unknown-two") {
		t.Fatal("unbounded route labels")
	}
	if !strings.Contains(body, `le="+Inf"`) {
		t.Fatal("missing cumulative histogram")
	}
}
