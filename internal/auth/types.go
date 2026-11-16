package auth

import "time"

// UserProfile 用户简明信息
type UserProfile struct {
	ID       int    `json:"id"`
	Email    string `json:"email"`
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar,omitempty"`
	Role     string `json:"role,omitempty"`
}

// StoredAuth 本地持久化保存的凭据结构 (~/.hoxi/auth.json)
type StoredAuth struct {
	DeviceToken    string      `json:"deviceToken"`
	User           UserProfile `json:"user"`
	ExpiresAt      string      `json:"expiresAt,omitempty"`
	LastVerifiedAt time.Time   `json:"lastVerifiedAt"`
	ServerBaseURL  string      `json:"serverBaseUrl,omitempty"`
}

// AuthStatusSnapshot 前端读取的认证状态快照
type AuthStatusSnapshot struct {
	IsLoggedIn     bool         `json:"isLoggedIn"`
	IsAuthorizing  bool         `json:"isAuthorizing"`
	PendingState   string       `json:"pendingState,omitempty"`
	User           *UserProfile `json:"user,omitempty"`
	ExpiresAt      string       `json:"expiresAt,omitempty"`
	ServerBaseURL  string       `json:"serverBaseUrl"`
	LastVerifiedAt string       `json:"lastVerifiedAt,omitempty"`
}

// StartAuthResponse 发起一键授权的响应体
type StartAuthResponse struct {
	State   string `json:"state"`
	AuthURL string `json:"authUrl"`
}

// ExchangeTokenRequest 手动/Deep Link 兑换设备 Token 的请求体
type ExchangeTokenRequest struct {
	HandoffToken string `json:"handoffToken"`
	State        string `json:"state"`
}

// MCPTokenRequest 请求签发 hoxi.ai MCP Scoped Access Token 的请求体
type MCPTokenRequest struct {
	AppID   string   `json:"appId,omitempty"`
	AppName string   `json:"appName,omitempty"`
	Scopes  []string `json:"scopes,omitempty"`
	// StoreName / SiteURL 是可选的门店提示，仅原样透传给 hoxi.ai 用于按店铺
	// 生成一键授权入口（如 Shopify 需先知道 *.myshopify.com 域名）。
	StoreName string `json:"storeName,omitempty"`
	SiteURL   string `json:"siteUrl,omitempty"`
}
