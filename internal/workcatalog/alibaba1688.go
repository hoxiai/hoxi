package workcatalog

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var credentialPrefixPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// Alibaba1688 calls only the seller-authorized product APIs. The endpoint is
// fixed, tokens are POST body parameters, redirects are disabled, and raw
// platform diagnostics (which can echo credentials) are never exposed to tools.
type Alibaba1688 struct {
	appKey, appSecret, accessToken string
	client                         *http.Client
	endpoint                       string
	proxy                          *url.URL
}

type alibabaProxyKey struct{}

var alibabaHTTP = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{Proxy: func(r *http.Request) (*url.URL, error) {
		proxy, _ := r.Context().Value(alibabaProxyKey{}).(*url.URL)
		return proxy, nil
	}, MaxIdleConns: 100, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second},
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func ValidCredentialPrefix(prefix string) bool { return credentialPrefixPattern.MatchString(prefix) }

func NewAlibaba1688FromEnv(prefix, proxyURL string) (*Alibaba1688, error) {
	if !ValidCredentialPrefix(prefix) {
		return nil, errors.New("configure alibaba1688CredentialEnv with an uppercase environment-variable prefix")
	}
	key := os.Getenv(prefix + "_APP_KEY")
	secret := os.Getenv(prefix + "_APP_SECRET")
	token := os.Getenv(prefix + "_ACCESS_TOKEN")
	return NewAlibaba1688(Credentials{AppKey: key, AppSecret: secret, AccessToken: token}, proxyURL)
}

func NewAlibaba1688(credentials Credentials, proxyURL string) (*Alibaba1688, error) {
	if !productIDPattern.MatchString(credentials.AppKey) || credentials.AppSecret == "" || credentials.AccessToken == "" {
		return nil, errors.New("1688 authorization is missing; configure APP_KEY, APP_SECRET and ACCESS_TOKEN")
	}
	var proxy *url.URL
	if proxyURL != "" {
		var err error
		proxy, err = url.Parse(proxyURL)
		if err != nil || proxy.Hostname() == "" || proxy.Port() == "" || proxy.RawQuery != "" || proxy.Fragment != "" ||
			(proxy.Scheme != "http" && proxy.Scheme != "https" && proxy.Scheme != "socks5" && proxy.Scheme != "socks5h") {
			return nil, errors.New("invalid configured store proxy")
		}
	}
	return &Alibaba1688{appKey: credentials.AppKey, appSecret: credentials.AppSecret, accessToken: credentials.AccessToken, client: alibabaHTTP,
		endpoint: "https://gw.open.1688.com/openapi/", proxy: proxy}, nil
}

func (a *Alibaba1688) List(ctx context.Context, page int, since, until time.Time) (Page, error) {
	params := map[string]any{"pageNo": page, "pageSize": 20, "needDetail": true, "needFreight": true,
		"needUserCategoryInfo": true, "orderByCondition": "ID", "orderByType": "ASC",
		"statusList": []string{"published", "expired", "TBD", "deleted", "new", "modified", "member expired"}}
	if !since.IsZero() {
		params["startModifyTime"] = alibabaTime(since)
		params["endModifyTime"] = alibabaTime(until)
	}
	res, err := a.call(ctx, "alibaba.product.list.get", params)
	if err != nil {
		return Page{}, err
	}
	if !isTrue(res["success"]) {
		return Page{}, errors.New("1688 product listing was not successful; check API permissions and seller authorization")
	}
	result, _ := res["result"].(map[string]any)
	pageResult, _ := result["pageResult"].(map[string]any)
	total, err := strconv.Atoi(scalarID(pageResult["totalRecords"]))
	if err != nil || total < 0 {
		return Page{}, errors.New("1688 listing response is missing a valid totalRecords")
	}
	items, ok := pageResult["resultList"].([]any)
	if !ok && !(total == 0 && pageResult["resultList"] == nil) {
		return Page{}, errors.New("1688 listing response is missing result.pageResult.resultList")
	}
	out := Page{Products: []Product{}, Total: total}
	for _, item := range items {
		data, ok := item.(map[string]any)
		if !ok {
			return Page{}, errors.New("invalid 1688 product record")
		}
		out.Products = append(out.Products, platformProduct(data))
	}
	return out, nil
}

func (a *Alibaba1688) Get(ctx context.Context, id string) (Product, error) {
	if !productIDPattern.MatchString(id) {
		return Product{}, errors.New("invalid 1688 product ID")
	}
	res, err := a.call(ctx, "alibaba.product.get", map[string]any{"productID": id, "webSite": "1688", "scene": "1688"})
	if err != nil {
		return Product{}, err
	}
	data, ok := res["productInfo"].(map[string]any)
	if !ok || scalarID(data["productID"]) != id {
		return Product{}, errors.New("1688 did not return the requested seller-owned product")
	}
	return platformProduct(data), nil
}

func (a *Alibaba1688) Update(ctx context.Context, live Product, patch Patch) error {
	if !productIDPattern.MatchString(live.ID) {
		return errors.New("invalid 1688 product ID")
	}
	if err := validatePatch(patch); err != nil {
		return err
	}
	params := map[string]any{"productID": live.ID, "webSite": "1688"}
	if patch.Subject != nil {
		params["subject"] = *patch.Subject
	}
	if patch.Description != nil {
		params["description"] = *patch.Description
	}
	if patch.Images != nil {
		image := map[string]any{}
		if current, ok := live.Data["image"].(map[string]any); ok {
			for _, key := range []string{"isWatermark", "isWatermarkFrame", "watermarkPosition"} {
				if value, exists := current[key]; exists {
					image[key] = value
				}
			}
		}
		image["images"] = *patch.Images
		params["image"] = image
	}
	res, err := a.call(ctx, "alibaba.product.incrementModify", params)
	if err != nil {
		return err
	}
	if !isTrue(res["success"]) {
		return errors.New("1688 did not acknowledge the incremental update")
	}
	return nil
}

func (a *Alibaba1688) call(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	ctx = context.WithValue(ctx, alibabaProxyKey{}, a.proxy)
	apiPath := "param2/1/com.alibaba.product/" + method + "/" + a.appKey
	form := url.Values{"access_token": {a.accessToken}}
	for key, value := range params {
		if text, ok := value.(string); ok {
			form.Set(key, text)
		} else {
			raw, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			form.Set(key, string(raw))
		}
	}
	form.Set("_aop_signature", alibabaSignature(apiPath, form, a.appSecret))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint+apiPath, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, errors.New("cannot build 1688 API request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, errors.New("1688 API request failed or timed out")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("1688 API returned HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil || len(raw) > 8<<20 {
		return nil, errors.New("1688 response could not be read within its size limit")
	}
	var result map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&result); err != nil || result == nil {
		return nil, errors.New("invalid JSON response from 1688")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return nil, errors.New("trailing JSON data from 1688")
	}
	for _, key := range []string{"error_code", "errorCode", "errCode"} {
		if value := scalarID(result[key]); value != "" && value != "0" {
			return nil, errors.New("1688 API rejected the request; check seller authorization and API permissions")
		}
	}
	if value, exists := result["success"]; exists && !isTrue(value) {
		return nil, errors.New("1688 API reported an unsuccessful request")
	}
	return result, nil
}

func alibabaSignature(path string, form url.Values, secret string) string {
	parts := make([]string, 0, len(form))
	for key := range form {
		parts = append(parts, key+form.Get(key))
	}
	sort.Strings(parts)
	mac := hmac.New(sha1.New, []byte(secret))
	_, _ = mac.Write([]byte(path + strings.Join(parts, "")))
	return strings.ToUpper(hex.EncodeToString(mac.Sum(nil)))
}

func scalarID(value any) string {
	switch v := value.(type) {
	case json.Number:
		return string(v)
	case string:
		return v
	default:
		return ""
	}
}

func isTrue(value any) bool { return value == true || value == "true" }

func alibabaTime(t time.Time) string {
	t = t.In(time.FixedZone("CST", 8*3600))
	return t.Format("20060102150405") + fmt.Sprintf("%03d", t.Nanosecond()/1e6) + t.Format("-0700")
}

func platformProduct(data map[string]any) Product {
	return Product{ID: scalarID(data["productID"]), Data: data, SyncedAt: time.Now().UTC()}
}

func validAlibabaImage(raw string) bool {
	if raw == "" || len(raw) > 2048 || strings.ContainsAny(raw, "\r\n\t ") {
		return false
	}
	if strings.HasPrefix(raw, "img/ibank/") {
		return !strings.Contains(raw, "..") && !strings.ContainsAny(raw, "?#\\")
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.User == nil && u.Port() == "" && u.Fragment == "" &&
		strings.HasSuffix(u.Hostname(), ".alicdn.com") && strings.HasPrefix(u.Path, "/img/ibank/")
}
