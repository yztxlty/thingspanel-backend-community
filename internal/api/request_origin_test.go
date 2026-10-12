package api

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

func TestThingsVisInternalAPIUsesProductionPrivateAddress(t *testing.T) {
	original := viper.GetString("thingsvis.internal_api_url")
	defer viper.Set("thingsvis.internal_api_url", original)
	viper.Set("thingsvis.internal_api_url", "http://192.168.0.2:28000/api/v1/")
	if got := thingsVisInternalAPIBase("https://yomipro.gwcz.online"); got != "http://192.168.0.2:28000/api/v1" {
		t.Fatalf("服务调用不应回绕公网：%s", got)
	}
	viper.Set("thingsvis.internal_api_url", "")
	if got := thingsVisInternalAPIBase("https://legacy.example"); got != "https://legacy.example/thingsvis-api" {
		t.Fatalf("未配置环境兼容被改变：%s", got)
	}
}

func TestRequestPublicOriginUsesBrowserOrigin(t *testing.T) {
	tests := []struct {
		name       string
		host       string
		origin     string
		referer    string
		forwarded  string
		tlsEnabled bool
		expected   string
	}{
		{
			name:     "production domain",
			host:     "c.thingspanel.cn",
			origin:   "http://c.thingspanel.cn",
			expected: "http://c.thingspanel.cn",
		},
		{
			name:     "local frontend port",
			host:     "127.0.0.1:9999",
			origin:   "http://127.0.0.1:9527",
			expected: "http://127.0.0.1:9527",
		},
		{
			name:     "referer fallback",
			host:     "c.thingspanel.cn",
			referer:  "https://c.thingspanel.cn/device/template",
			expected: "https://c.thingspanel.cn",
		},
		{
			name:       "request host fallback",
			host:       "c.thingspanel.cn",
			forwarded:  "https",
			tlsEnabled: true,
			expected:   "https://c.thingspanel.cn",
		},
	}

	gin.SetMode(gin.TestMode)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://"+tt.host+"/api/v1/test", nil)
			request.Host = tt.host
			request.Header.Set("Origin", tt.origin)
			request.Header.Set("Referer", tt.referer)
			request.Header.Set("X-Forwarded-Proto", tt.forwarded)
			if tt.tlsEnabled {
				request.TLS = &tls.ConnectionState{}
			}
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = request

			origin, err := requestPublicOrigin(context)
			if err != nil {
				t.Fatalf("requestPublicOrigin returned error: %v", err)
			}
			if origin != tt.expected {
				t.Fatalf("expected %s, got %s", tt.expected, origin)
			}
		})
	}
}

func TestRequestPublicOriginRejectsForeignOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	request := httptest.NewRequest(http.MethodPost, "https://c.thingspanel.cn/api/v1/test", nil)
	request.Host = "c.thingspanel.cn"
	request.Header.Set("Origin", "https://attacker.example")
	request.Header.Set("X-Forwarded-Proto", "https")
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = request

	origin, err := requestPublicOrigin(context)
	if err != nil {
		t.Fatalf("requestPublicOrigin returned error: %v", err)
	}
	if origin != "https://c.thingspanel.cn" {
		t.Fatalf("expected trusted request host, got %s", origin)
	}
}
