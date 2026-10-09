package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"project/internal/query"
	"project/pkg/utils"

	"github.com/gin-gonic/gin"
)

var factoryID = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)
var factoryGrantID = regexp.MustCompile(`^[1-9][0-9]*$`)

func factoryAdmin(c *gin.Context) bool {
	claims, ok := c.Get("claims")
	if !ok {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "未登录"})
		return false
	}
	user, ok := claims.(*utils.UserClaims)
	if !ok || (user.Authority != "SYS_ADMIN" && user.TenantID == "") {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": "无出厂预制管理权限"})
		return false
	}
	return true
}

func factoryOwnedProduct(c *gin.Context, base, token string, payload interface{}) bool {
	user := c.MustGet("claims").(*utils.UserClaims)
	if user.Authority == "SYS_ADMIN" {
		return true
	}
	productKey := c.Query("productKey")
	if body, ok := payload.(map[string]interface{}); ok {
		productKey, _ = body["platformProductKey"].(string)
		if productKey == "" {
			productKey, _ = body["productKey"].(string)
		}
	}
	if batchID := c.Param("batchId"); batchID != "" {
		req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet,
			base+"/api/v1/internal/factory-batches/"+url.PathEscape(batchID), nil)
		if err != nil {
			c.AbortWithStatus(http.StatusBadGateway)
			return false
		}
		req.Header.Set("X-Yomi-Internal-Token", token)
		resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
		if err != nil {
			c.AbortWithStatus(http.StatusBadGateway)
			return false
		}
		defer resp.Body.Close()
		var result struct {
			Code int `json:"code"`
			Data struct {
				ProductKey         string `json:"productKey"`
				PlatformProductKey string `json:"platformProductKey"`
			} `json:"data"`
		}
		if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&result) != nil || result.Code != 0 {
			c.AbortWithStatus(http.StatusBadGateway)
			return false
		}
		productKey = result.Data.PlatformProductKey
		if productKey == "" {
			productKey = result.Data.ProductKey
		}
	}
	if !factoryID.MatchString(productKey) {
		c.AbortWithStatus(http.StatusForbidden)
		return false
	}
	q := query.Product
	_, err := q.WithContext(c.Request.Context()).Where(q.ProductKey.Eq(productKey), q.TenantID.Eq(user.TenantID)).First()
	if err != nil {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": "无此产品的出厂预制管理权限"})
		return false
	}
	return true
}

func factoryBase() (string, string, error) {
	base := strings.TrimRight(os.Getenv("YOMI_FACTORY_API_BASE_URL"), "/")
	token := os.Getenv("YOMI_INTERNAL_TOKEN")
	if token == "" {
		token = os.Getenv("YOMI_INTERNAL_EVENT_TOKEN")
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || token == "" {
		return "", "", errors.New("出厂预制内部服务未配置")
	}
	privateHTTP := parsed.Scheme == "http" && (parsed.Hostname() == "yomi-server" || parsed.Hostname() == "host.docker.internal" || parsed.Hostname() == "127.0.0.1")
	if parsed.Scheme != "https" && !privateHTTP {
		return "", "", errors.New("出厂预制内部服务地址不安全")
	}
	return base, token, nil
}

func proxyFactory(c *gin.Context, method, path string, payload interface{}) {
	if !factoryAdmin(c) {
		return
	}
	base, token, err := factoryBase()
	if err != nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"message": err.Error()})
		return
	}
	if !factoryOwnedProduct(c, base, token, payload) {
		return
	}
	upstreamPath := "/api/v1/internal/factory-batches" + path
	if path == "/connection-preview" {
		upstreamPath = "/api/v1/internal/factory-connection-preview"
		if data, ok := payload.(map[string]interface{}); ok {
			delete(data, "platformProductKey")
		}
	}
	var body io.Reader
	if payload != nil {
		encoded, encodeErr := json.Marshal(payload)
		if encodeErr != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "请求无效"})
			return
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), method, base+upstreamPath, body)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"message": "出厂预制服务不可用"})
		return
	}
	req.Header.Set("X-Yomi-Internal-Token", token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"message": "出厂预制服务不可用"})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		status, message := http.StatusBadGateway, "出厂预制操作失败"
		var failure struct {
			Detail  string `json:"detail"`
			Message string `json:"message"`
			Action  string `json:"action"`
			Errors  []struct {
				Field  string `json:"field"`
				Reason string `json:"reason"`
			} `json:"errors"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&failure)
		switch resp.StatusCode {
		case http.StatusConflict:
			status, message = http.StatusConflict, "批次编号或设备编号已被占用"
		case http.StatusUnprocessableEntity:
			status, message = http.StatusUnprocessableEntity, "出厂预制参数无效"
		case http.StatusNotFound:
			status, message = http.StatusNotFound, "出厂预制记录不存在"
		case http.StatusServiceUnavailable:
			status, message = http.StatusServiceUnavailable, "出厂预制服务暂不可用"
		case http.StatusForbidden:
			status, message = http.StatusForbidden, "当前操作无权限"
		case http.StatusGone:
			status, message = http.StatusGone, "当前授权或资料重取期限已结束"
		}
		if accurate, ok := map[string]string{
			"factory_connection_invalid":            "请填写有效的 HTTPS 基础地址，不要包含接口路径",
			"factory_connection_origin_mismatch":    "地址与当前环境批准的设备服务地址不一致",
			"factory_connection_not_configured":     "当前环境尚未配置设备服务地址",
			"factory_connection_discovery_required": "设备服务尚未提供完整动态接口目录",
			"factory_connection_unavailable":        "无法检查设备服务，请检查网络和 HTTPS 配置后重试",
			"factory_connection_evidence_required":  "缺少设备地址读回或动态发现能力证明",
			"factory_connection_evidence_mismatch":  "设备读回地址或发现能力与批次要求不符",
			"factory_product_mismatch":              "设备产品标识不匹配，请核对工位配置",
			"factory_recovery_unavailable":          "设备状态不允许恢复，请核对烧录、证明和质检记录",
			"factory_recovery_test_only":            "恢复当前台仅在测试环境开放",
			"factory_claim_window_closed":           "资料重取窗口已结束，请在后台恢复当前台",
			"factory_claim_status_changed":          "设备状态已变化，不能再重取密钥",
			"factory_station_expired":               "工位令牌已到期，请重新授权",
			"factory_station_revoked":               "工位令牌已撤销，请重新授权",
			"factory_batch_not_ready":               "批次已暂停或关闭，请核对批次状态",
			"factory_batch_exhausted":               "当前批次设备编号已领完",
		}[failure.Detail]; ok {
			message = accurate
		}
		if failure.Message != "" {
			message = failure.Message
		}
		for _, item := range failure.Errors {
			message += "；" + item.Field + "：" + item.Reason
		}
		if failure.Action != "" {
			message += "。" + failure.Action
		}
		c.AbortWithStatusJSON(status, gin.H{"detail": failure.Detail, "message": message, "action": failure.Action, "errors": failure.Errors})
		return
	}
	var result struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&result); err != nil || result.Code != 0 {
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"message": "出厂预制操作失败"})
		return
	}
	var data interface{}
	if err := json.Unmarshal(result.Data, &data); err != nil {
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"message": "出厂预制响应无效"})
		return
	}
	c.Set("data", data)
}

func factoryBatchPath(c *gin.Context) (string, bool) {
	id := c.Param("batchId")
	if !factoryID.MatchString(id) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "批次编号无效"})
		return "", false
	}
	return "/" + url.PathEscape(id), true
}

func readFactoryBody(c *gin.Context) (map[string]interface{}, bool) {
	var body map[string]interface{}
	if err := json.NewDecoder(io.LimitReader(c.Request.Body, 16<<10)).Decode(&body); err != nil || body == nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "请求无效"})
		return nil, false
	}
	return body, true
}

func (*ProductApi) ListFactoryBatches(c *gin.Context) {
	product := c.Query("productKey")
	if !factoryID.MatchString(product) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "产品标识无效"})
		return
	}
	query := url.Values{"productKey": {product}, "page": {c.DefaultQuery("page", "1")}}
	proxyFactory(c, http.MethodGet, "?"+query.Encode(), nil)
}

func factoryProductBody(c *gin.Context) (map[string]interface{}, bool) {
	body, ok := readFactoryBody(c)
	if !ok || !factoryAdmin(c) {
		return nil, false
	}
	platformKey, _ := body["productKey"].(string)
	if !factoryID.MatchString(platformKey) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "平台产品标识无效"})
		return nil, false
	}
	q := query.Product
	product, err := q.WithContext(c.Request.Context()).Where(q.ProductKey.Eq(platformKey)).First()
	if err != nil || product.ProductModel == nil || !factoryID.MatchString(*product.ProductModel) {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{"message": "请先配置产品型号标识，再创建出厂批次"})
		return nil, false
	}
	body["platformProductKey"] = platformKey
	body["productKey"] = *product.ProductModel
	return body, true
}

func (*ProductApi) CreateFactoryBatch(c *gin.Context) {
	body, ok := factoryProductBody(c)
	if !ok {
		return
	}
	body["createdBy"] = c.MustGet("claims").(*utils.UserClaims).ID
	proxyFactory(c, http.MethodPost, "", body)
}

func (*ProductApi) PreviewFactoryConnection(c *gin.Context) {
	body, ok := factoryProductBody(c)
	if !ok {
		return
	}
	proxyFactory(c, http.MethodPost, "/connection-preview", body)
}

func (*ProductApi) GetFactoryBatch(c *gin.Context) {
	path, ok := factoryBatchPath(c)
	if !ok {
		return
	}
	query := url.Values{"page": {c.DefaultQuery("page", "1")}}
	if status := c.Query("status"); status != "" {
		query.Set("status", status)
	}
	proxyFactory(c, http.MethodGet, path+"?"+query.Encode(), nil)
}

func (*ProductApi) SetFactoryBatchStatus(c *gin.Context) {
	path, ok := factoryBatchPath(c)
	if !ok {
		return
	}
	body, ok := readFactoryBody(c)
	if ok {
		proxyFactory(c, http.MethodPost, path+"/status", body)
	}
}

func (*ProductApi) IssueFactoryStationGrant(c *gin.Context) {
	path, ok := factoryBatchPath(c)
	if !ok {
		return
	}
	body, ok := readFactoryBody(c)
	if ok {
		proxyFactory(c, http.MethodPost, path+"/stations", body)
	}
}

func (*ProductApi) RevokeFactoryStationGrant(c *gin.Context) {
	path, ok := factoryBatchPath(c)
	if !ok {
		return
	}
	grantID := c.Param("grantId")
	if !factoryGrantID.MatchString(grantID) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "工位授权编号无效"})
		return
	}
	proxyFactory(c, http.MethodPost, path+"/stations/"+grantID+"/revoke", nil)
}

func (*ProductApi) EnableFactoryUnit(c *gin.Context) {
	path, ok := factoryBatchPath(c)
	if !ok || !factoryID.MatchString(c.Param("deviceId")) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "设备编号无效"})
		return
	}
	proxyFactory(c, http.MethodPost, path+"/units/"+url.PathEscape(c.Param("deviceId"))+"/enable", nil)
}

func (*ProductApi) RestoreFactoryUnit(c *gin.Context) {
	path, ok := factoryBatchPath(c)
	if !ok || !factoryID.MatchString(c.Param("deviceId")) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "设备编号无效"})
		return
	}
	body, ok := readFactoryBody(c)
	if !ok || !factoryAdmin(c) {
		return
	}
	body["operatorId"] = c.MustGet("claims").(*utils.UserClaims).ID
	proxyFactory(c, http.MethodPost, path+"/units/"+url.PathEscape(c.Param("deviceId"))+"/restore", body)
}
