package workcatalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAlibaba1688SellerAPIContractsAndFieldLevelWrite(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		method := strings.Split(r.URL.Path, "/")[5]
		calls = append(calls, method)
		signature := r.PostForm.Get("_aop_signature")
		r.PostForm.Del("_aop_signature")
		if r.Method != "POST" || r.URL.RawQuery != "" || r.PostForm.Get("access_token") != "secret-token" ||
			signature != alibabaSignature(strings.TrimPrefix(r.URL.Path, "/openapi/"), r.PostForm, "app-secret") {
			t.Error("invalid signed POST", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "alibaba.product.list.get":
			if r.PostForm.Get("pageSize") != "20" || r.PostForm.Get("orderByCondition") != "ID" || r.PostForm.Get("needDetail") != "true" || r.PostForm.Get("endModifyTime") != "" || !strings.Contains(r.PostForm.Get("statusList"), "expired") {
				t.Error("list contract mismatch", r.PostForm)
			}
			_, _ = w.Write([]byte(`{"success":true,"result":{"pageResult":{"totalRecords":1,"resultList":[{"productID":9223372036854775806,"sellerLoginId":"factory-a","subject":"Original"}]}}}`))
		case "alibaba.product.get":
			if r.PostForm.Get("webSite") != "1688" || r.PostForm.Get("scene") != "1688" {
				t.Error("get contract mismatch")
			}
			_, _ = w.Write([]byte(`{"productInfo":{"productID":9223372036854775806,"sellerLoginId":"factory-a","subject":"Original"}}`))
		case "alibaba.product.incrementModify":
			if len(r.PostForm) != 4 || r.PostForm.Get("subject") != "Improved" || r.PostForm.Has("saleInfo") || r.PostForm.Has("skuInfos") {
				t.Error("write included unmodified fields", r.PostForm)
			}
			_, _ = w.Write([]byte(`{"success":"true","result":{"productID":9223372036854775806}}`))
		default:
			t.Error("unexpected method", method)
		}
	}))
	defer srv.Close()
	a := &Alibaba1688{appKey: "123", appSecret: "app-secret", accessToken: "secret-token", client: srv.Client(), endpoint: srv.URL + "/openapi/"}
	page, err := a.List(context.Background(), 1, time.Time{}, time.Now())
	if err != nil || len(page.Products) != 1 || page.Products[0].ID != "9223372036854775806" {
		t.Fatal(page, err)
	}
	live, err := a.Get(context.Background(), page.Products[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	subject := "Improved"
	if err := a.Update(context.Background(), live, Patch{Subject: &subject}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatal(calls)
	}
	if got := alibabaTime(time.Date(2026, 11, 13, 1, 2, 3, 123000000, time.UTC)); got != "20261113090203123+0800" {
		t.Fatal("invalid platform timestamp", got)
	}
	// Fixed vector independently computed with Node's crypto.createHmac; catches
	// signing raw values vs URL-encoded values and incorrect parameter ordering.
	form := url.Values{"webSite": {"1688"}, "scene": {"1688"}, "productID": {"9223372036854775806"}, "access_token": {"secret-token"}}
	if got := alibabaSignature("param2/1/com.alibaba.product/alibaba.product.get/123", form, "app-secret"); got != "6AB463377B0C4EB1882CC55102019B1401A6A5D8" {
		t.Fatal("invalid request signature", got)
	}
}

func TestAlibaba1688RejectsErrorsWithoutLeakingTokens(t *testing.T) {
	for _, body := range []string{
		`{"error_code":"401","error_message":"secret-token"}`,
		`{"success":false,"message":"secret-token"}`,
		`{"success":true,"result":{}}`,
		`not-json secret-token`,
		`{"success":true} {"extra":true}`,
	} {
		t.Run(body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer srv.Close()
			a := &Alibaba1688{appKey: "123", appSecret: "secret", accessToken: "secret-token", client: srv.Client(), endpoint: srv.URL + "/"}
			if _, err := a.List(context.Background(), 1, time.Time{}, time.Now()); err == nil || strings.Contains(err.Error(), "secret-token") {
				t.Fatal("unsafe API error", err)
			}
		})
	}
	if equalField("images", []string{"https://cbu01.alicdn.com/img/ibank/a.jpg"}, []string{"img/ibank/a.jpg"}) != true {
		t.Fatal("equivalent platform image forms did not match")
	}
	if validAlibabaImage("https://alicdn.com.evil.example/img/ibank/a.jpg") || validAlibabaImage("img/ibank/../secret") {
		t.Fatal("non-platform image accepted")
	}
	var patch Patch
	if err := json.Unmarshal([]byte(`{"subject":""}`), &patch); err != nil || validatePatch(patch) == nil {
		t.Fatal("empty no-op title accepted")
	}
	longTitle := strings.Repeat("厂", 31)
	if validatePatch(Patch{Subject: &longTitle}) == nil {
		t.Fatal("overlong Chinese title accepted")
	}
}
