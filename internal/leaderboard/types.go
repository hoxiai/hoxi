package leaderboard

type ModelItem struct {
	Rank          int      `json:"rank"`
	Slug          string   `json:"slug"`
	Name          string   `json:"name"`
	Vendor        string   `json:"vendor"`
	APIName       string   `json:"apiName"`
	Price         struct {
		Currency   string  `json:"currency"`
		Input      float64 `json:"input"`
		Output     float64 `json:"output"`
		CacheRead  float64 `json:"cacheRead,omitempty"`
		CacheWrite float64 `json:"cacheWrite,omitempty"`
	} `json:"price"`
	ContextWindow int            `json:"contextWindow"`
	Scores        map[string]any `json:"scores"`
	Perf          map[string]any `json:"perf"`
	Badges        []string       `json:"badges"`
	Scenes        []string       `json:"scenes"`
	Summary       string         `json:"summary"`
	Reasoning     bool           `json:"reasoning"`
	IsFree        bool           `json:"isFree"`
}

type LeaderboardData struct {
	Dimension string      `json:"dimension"`
	UpdatedAt string      `json:"updatedAt"`
	Total     int         `json:"total"`
	Items     []ModelItem `json:"items"`
}

type LeaderboardResponse struct {
	Code    int              `json:"code"`
	Message string           `json:"message"`
	Data    *LeaderboardData `json:"data"`
}

type GatewayItem struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Domain             string   `json:"domain"`
	URL                string   `json:"url"`
	BaseURL            string   `json:"baseUrl"`
	IsSelfOperated     bool     `json:"isSelfOperated"`
	CanOneClickConnect bool     `json:"canOneClickConnect"`
	Badge              string   `json:"badge,omitempty"`
	BadgeTone          string   `json:"badgeTone,omitempty"`
	SavingsPercent     int      `json:"savingsPercent"`
	PriceLabel         string   `json:"priceLabel"`
	OfficialPriceCNY   float64  `json:"officialPriceCNY"`
	LatencyMs          int      `json:"latencyMs"`
	Uptime             string   `json:"uptime"`
	Models             []string `json:"models"`
	RecommendedModels  []string `json:"recommendedModels"`
	Features           []string `json:"features"`
	Caveat             string   `json:"caveat"`
	AuthURL            string   `json:"authUrl,omitempty"`
}

type GatewaysData struct {
	Total int           `json:"total"`
	Items []GatewayItem `json:"items"`
}

type GatewaysResponse struct {
	Code    int           `json:"code"`
	Message string        `json:"message"`
	Data    *GatewaysData `json:"data"`
}

type ModelPricingItem struct {
	Slug             string   `json:"slug"`
	Name             string   `json:"name"`
	Vendor           string   `json:"vendor"`
	APIName          string   `json:"apiName,omitempty"`
	GatewayModelName string   `json:"gatewayModelName,omitempty"`
	AASlug           string   `json:"aaSlug,omitempty"`
	Price            struct {
		Currency   string  `json:"currency"`
		Input      float64 `json:"input"`
		Output     float64 `json:"output"`
		CacheRead  float64 `json:"cacheRead,omitempty"`
		CacheWrite float64 `json:"cacheWrite,omitempty"`
	} `json:"price"`
	ContextWindow   int      `json:"contextWindow"`
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	Modalities      []string `json:"modalities,omitempty"`
	Reasoning       bool     `json:"reasoning"`
	IsFree          bool     `json:"isFree"`
	UpdatedAt       string   `json:"updatedAt,omitempty"`
}

type ModelPricingData struct {
	UpdatedAt string             `json:"updatedAt"`
	Total     int                `json:"total"`
	Items     []ModelPricingItem `json:"items"`
}

type ModelPricingResponse struct {
	Code    int               `json:"code"`
	Message string            `json:"message"`
	Data    *ModelPricingData `json:"data"`
}

type FreeTierGuideItem struct {
	ID                string   `json:"id"`
	ProviderID        string   `json:"providerID"`
	Name              string   `json:"name"`
	Badge             string   `json:"badge"`
	QuotaLabel        string   `json:"quotaLabel"`
	Steps             []string `json:"steps"`
	SignupURL         string   `json:"signupUrl"`
	BaseURL           string   `json:"baseUrl"`
	CustomMode        bool     `json:"customMode,omitempty"`
	RecommendedModels []string `json:"recommendedModels"`
}

type FreeTiersData struct {
	UpdatedAt string              `json:"updatedAt"`
	Total     int                 `json:"total"`
	Items     []FreeTierGuideItem `json:"items"`
}

type FreeTiersResponse struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    *FreeTiersData `json:"data"`
}
