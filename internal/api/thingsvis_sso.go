package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"project/internal/dal"
	"project/internal/model"
	"project/pkg/utils"
)

type thingsVisIdentity struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	TenantID string `json:"tenantId"`
}
type thingsVisExchange struct {
	Platform      string            `json:"platform"`
	PlatformToken string            `json:"platformToken"`
	UserInfo      thingsVisIdentity `json:"userInfo"`
	Role          string            `json:"role"`
}

func thingsVisSSOPayload(user *model.User, token string) (*thingsVisExchange, error) {
	if user == nil || user.ID == "" || user.Email == "" || user.Status == nil || *user.Status != "N" || user.Authority == nil || token == "" {
		return nil, errors.New("用户身份不可用")
	}
	role, tenant := "", ""
	if user.TenantID != nil {
		tenant = strings.TrimSpace(*user.TenantID)
	}
	switch *user.Authority {
	case "SYS_ADMIN":
		role, tenant = "SUPER_ADMIN", "thingspanel-sys-admin"
	case "TENANT_ADMIN":
		role = "TENANT_ADMIN"
	case "TENANT_USER":
		role = "EDITOR"
	default:
		return nil, errors.New("未知用户权限")
	}
	if tenant == "" {
		return nil, errors.New("缺少租户空间")
	}
	name := user.Email
	if user.Name != nil && *user.Name != "" {
		name = *user.Name
	}
	return &thingsVisExchange{Platform: "thingspanel", PlatformToken: token, UserInfo: thingsVisIdentity{ID: user.ID, Email: user.Email, Name: name, TenantID: tenant}, Role: role}, nil
}

// ThingsVisSSO只使用已验证会话及数据库身份，不转发客户端声明的角色、用户或租户。
func (*UserApi) ThingsVisSSO(c *gin.Context) {
	value, ok := c.Get("claims")
	claims, valid := value.(*utils.UserClaims)
	if !ok || !valid || c.GetHeader("x-token") == "" {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	user, err := dal.GetUsersById(claims.ID)
	if err != nil {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	payload, err := thingsVisSSOPayload(user, c.GetHeader("x-token"))
	if err != nil {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	target := viper.GetString("thingsvis.server_url")
	if target == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "看板服务未配置"})
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	request, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, strings.TrimRight(target, "/")+"/api/v1/auth/sso", bytes.NewReader(data))
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "看板身份交换失败"})
		return
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
	if err != nil || len(body) > 1024*1024 || response.StatusCode != http.StatusOK || !json.Valid(body) {
		c.JSON(http.StatusBadGateway, gin.H{"error": "看板身份交换失败"})
		return
	}
	c.Data(http.StatusOK, "application/json", body)
}
