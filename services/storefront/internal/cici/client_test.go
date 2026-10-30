package cici

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestSignatureGoldenAndExactBytes(t *testing.T) {
	// Golden computed independently with Node crypto, not this signing implementation.
	body := []byte(`{"sku_id":501,"quantity":1}`)
	sign := func(target string, b []byte) string {
		return Signature("fixture-cici-secret", "POST", target, "1791763200", "0123456789abcdef0123456789abcdef", b)
	}
	want := "40283b2abc0a9eb830e9124ca70f18bda210172eec7d2bda0b6a84e9f708cc28"
	if got := sign(prefix+"/quote", body); got != want {
		t.Fatal("signature differs from independent golden", got)
	}
	if sign(prefix+"/quote", nil) == sign(prefix+"/quote", []byte("{}")) {
		t.Fatal("empty body collapsed into object")
	}
	if sign(prefix+"/quote?a=1&b=2", body) == sign(prefix+"/quote?b=2&a=1", body) {
		t.Fatal("raw query order lost")
	}
	if sign(prefix+"/quote", append(body, '\n')) == want {
		t.Fatal("body whitespace ignored")
	}
}

func TestClientFreshNonceAndFrozenRequest(t *testing.T) {
	c, _ := NewClient("fixture-key", "fixture-secret")
	var nonces []string
	body := []byte(`{"sku_id":501,"quantity":1,"downstream_order_no":"cici_fixture","expected_amount":"1.00"}`)
	c.HTTP.Transport = transport(func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		if r.URL.Host != "aicici888.com" || string(b) != string(body) {
			t.Fatal("request destination or body changed")
		}
		nonce := r.Header.Get("Dujiao-Next-Nonce")
		nonces = append(nonces, nonce)
		if len(nonce) != 32 || r.Header.Get("Dujiao-Next-Signature") != Signature("fixture-secret", r.Method, r.URL.RequestURI(), r.Header.Get("Dujiao-Next-Timestamp"), nonce, b) {
			t.Fatal("invalid wire signature")
		}
		return response(200, `{"ok":true,"order_id":10,"order_no":"one","amount":"1.00","currency":"CNY","status":"paid"}`), nil
	})
	for range 2 {
		if _, err := c.Create(context.Background(), body); err != nil {
			t.Fatal(err)
		}
	}
	if nonces[0] == nonces[1] {
		t.Fatal("reused nonce")
	}
}

func TestClientFailureDoesNotLeakBodies(t *testing.T) {
	for _, tc := range []struct {
		status     int
		body, code string
	}{
		{200, `{"ok":false,"error_code":"payment_failed","error_message":"secret-customer-data","order_id":42,"status":"canceled"}`, "payment_failed"},
		{503, `{"ok":false,"error_code":"order_lookup_failed"}`, "order_lookup_failed"},
		{200, `{"status_code":200,"data":{"ok":true}}`, "invalid_response"},
		{200, `{"ok":false,"error_code":"private info in error"}`, "upstream_error"},
		{200, strings.Repeat("x", (2<<20)+1), "invalid_response"},
	} {
		c, _ := NewClient("fixture-key", "fixture-secret")
		c.HTTP.Transport = transport(func(*http.Request) (*http.Response, error) { return response(tc.status, tc.body), nil })
		_, err := c.Ping(context.Background())
		var got *APIError
		if !errors.As(err, &got) || got.Code != tc.code || strings.Contains(err.Error(), "secret-customer") {
			t.Fatal("unsafe error handling", err)
		}
	}
}

func TestClientRejectsRedirect(t *testing.T) {
	visited := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/other" {
			visited = true
		}
		http.Redirect(w, r, "/other", 302)
	}))
	defer server.Close()
	c, _ := NewClient("fixture-key", "fixture-secret")
	base := server.Client().Transport
	c.HTTP.Transport = transport(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme = "https"
		r.URL.Host = strings.TrimPrefix(server.URL, "https://")
		return base.RoundTrip(r)
	})
	if _, err := c.Ping(context.Background()); err == nil || visited {
		t.Fatal("followed redirect with credentials")
	}
}

func TestProductPageAndQuoteValidation(t *testing.T) {
	c, _ := NewClient("fixture-key", "fixture-secret")
	c.HTTP.Transport = transport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			if r.URL.RawQuery != "include_inactive=true&page=2&page_size=50" {
				t.Fatal("unexpected pagination target")
			}
			return response(200, `{"ok":true,"items":[],"total":151,"page":2,"page_size":50}`), nil
		}
		return response(200, `{"ok":true,"sku_id":501,"quantity":2,"amount":"1.000001","currency":"CNY"}`), nil
	})
	page, err := c.Products(context.Background(), 2)
	if err != nil || page.Total != 151 || len(page.Items) != 0 {
		t.Fatal("empty intermediate page mishandled", err)
	}
	q, err := c.Quote(context.Background(), 501, 2)
	if err != nil || q.Amount != "1.000001" {
		t.Fatal("precision lost", err)
	}
	if _, err = c.Quote(context.Background(), 501, 1); err == nil {
		t.Fatal("accepted wrong quantity")
	}
}

func TestClassifyRequiresExactOrderAndFulfillment(t *testing.T) {
	id := int64(10)
	p := Purchase{Product: 101, SKU: 501, Quantity: 1, Amount: "0.100001", SupplierID: &id, SupplierNumber: "one"}
	base := Order{ID: 10, Number: "one", Status: "completed", Amount: "0.1000010", Currency: "CNY", Refunded: "0.00", Items: []OrderItem{{Product: 101, SKU: 501, Quantity: 1}}, Fulfillment: &Fulfillment{Status: "delivered", Payload: "fixture-delivery"}}
	for _, tc := range []struct {
		name, want string
		change     func(*Order)
	}{
		{"valid", "delivered", func(*Order) {}},
		{"paid-is-not-delivery", "pending", func(o *Order) { o.Status = "paid" }},
		{"missing-delivery", "pending", func(o *Order) { o.Fulfillment = nil }},
		{"tiny-price-difference", "review", func(o *Order) { o.Amount = "0.100002" }},
		{"sku", "review", func(o *Order) { o.Items[0].SKU = 999 }},
		{"quantity", "review", func(o *Order) { o.Items[0].Quantity = 2 }},
		{"identity", "review", func(o *Order) { o.Number = "other" }},
		{"partial", "review", func(o *Order) { o.Status = "partially_delivered" }},
		{"partial-refund", "review", func(o *Order) { o.Refunded = "0.01" }},
		{"unknown", "review", func(o *Order) { o.Status = "future_state" }},
		{"refund", "refunded", func(o *Order) { o.Status = "refunded"; o.Refunded = o.Amount }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := json.Marshal(base)
			var o Order
			_ = json.Unmarshal(b, &o)
			tc.change(&o)
			state, _ := classify(p, o)
			if state != tc.want {
				t.Fatal(state)
			}
		})
	}
}

func TestReconciliationErrorsNeverAutomaticallyRepurchase(t *testing.T) {
	for _, code := range []string{"price_changed", "payment_failed", "idempotency_payload_mismatch", "idempotency_key_canceled", "payment_reconciliation_required", "idempotency_order_unavailable", "idempotency_conflict", "price_reconciliation_required", "internal_error", "invalid_response"} {
		state, _, _ := errorState(&APIError{HTTP: 409, Code: code})
		if state != "review" {
			t.Fatal("unsafe retry", code)
		}
	}
	if sameAmount("1e2", "100") || sameAmount("NaN", "0") || sameAmount("-1", "1") {
		t.Fatal("invalid decimal accepted")
	}
}
