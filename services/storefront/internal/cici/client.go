// Package cici implements CCAI's upstream API v1.1, independently of customer payments.
package cici

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const BaseURL = "https://aicici888.com"
const prefix = "/api/v1/upstream"

type Client struct {
	key, secret string
	HTTP        *http.Client
}

func NewClient(key, secret string) (*Client, error) {
	if strings.TrimSpace(key) == "" || strings.TrimSpace(secret) == "" || strings.ContainsAny(key, "\r\n") {
		return nil, errors.New("Cici API Key and Secret are required")
	}
	return &Client{key: key, secret: secret, HTTP: &http.Client{Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// Account identifies the API credential, not the rotatable signing secret.
func (c *Client) Account() string {
	h := sha256.Sum256([]byte(c.key))
	return hex.EncodeToString(h[:])
}

func Signature(secret, method, target, timestamp, nonce string, body []byte) string {
	bodyHash := sha256.Sum256(body)
	text := strings.Join([]string{"v1.1", method, target, timestamp, nonce, hex.EncodeToString(bodyHash[:])}, "\n")
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write([]byte(text))
	return hex.EncodeToString(h.Sum(nil))
}

type APIError struct {
	HTTP    int
	Code    string
	OrderID int64
	Status  string
}

// Never include upstream error_message, response bodies or authentication headers.
func (e *APIError) Error() string { return fmt.Sprintf("Cici HTTP %d: %s", e.HTTP, e.Code) }

type envelope struct {
	OK      *bool  `json:"ok"`
	Code    string `json:"error_code"`
	OrderID int64  `json:"order_id"`
	Status  string `json:"status"`
}

var codePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,79}$`)
var decimalPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,15})(\.[0-9]{1,18})?$`)

func money(value string) (*big.Rat, bool) {
	if !decimalPattern.MatchString(value) {
		return nil, false
	}
	v, ok := new(big.Rat).SetString(value)
	return v, ok
}

func sameAmount(a, b string) bool {
	x, ok := money(a)
	if !ok {
		return false
	}
	y, ok := money(b)
	return ok && x.Cmp(y) == 0
}

func (c *Client) call(ctx context.Context, method, target string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, BaseURL+target, bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid Cici request")
	}
	nonceBytes := make([]byte, 16)
	if _, err = rand.Read(nonceBytes); err != nil {
		return errors.New("cannot generate Cici nonce")
	}
	nonce, stamp := hex.EncodeToString(nonceBytes), strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Dujiao-Next-Api-Key", c.key)
	req.Header.Set("Dujiao-Next-Timestamp", stamp)
	req.Header.Set("Dujiao-Next-Nonce", nonce)
	req.Header.Set("Dujiao-Next-Signature", Signature(c.secret, method, req.URL.RequestURI(), stamp, nonce, body))
	res, err := c.HTTP.Do(req)
	if err != nil {
		return &APIError{Code: "transport_unknown"}
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return &APIError{HTTP: res.StatusCode, Code: "invalid_response"}
	}
	var env envelope
	if json.Unmarshal(data, &env) != nil || env.OK == nil {
		return &APIError{HTTP: res.StatusCode, Code: "invalid_response"}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 || !*env.OK {
		if !codePattern.MatchString(env.Code) {
			env.Code = "upstream_error"
		}
		return &APIError{HTTP: res.StatusCode, Code: env.Code, OrderID: env.OrderID, Status: env.Status}
	}
	if json.Unmarshal(data, out) != nil {
		return &APIError{HTTP: res.StatusCode, Code: "invalid_response"}
	}
	return nil
}

type Localized map[string]string
type SKU struct {
	ID       int64             `json:"id"`
	Code     string            `json:"sku_code"`
	Specs    map[string]string `json:"spec_values"`
	Price    string            `json:"price_amount"`
	Stock    string            `json:"stock_status"`
	Quantity int64             `json:"stock_quantity"`
	Active   bool              `json:"is_active"`
}
type Product struct {
	ID          int64           `json:"id"`
	Title       Localized       `json:"title"`
	Description Localized       `json:"description"`
	Type        string          `json:"fulfillment_type"`
	Active      bool            `json:"is_active"`
	SKUs        []SKU           `json:"skus"`
	Form        json.RawMessage `json:"manual_form_schema"`
}
type Products struct {
	Items    []Product `json:"items"`
	Total    int64     `json:"total"`
	Page     int       `json:"page"`
	PageSize int       `json:"page_size"`
}
type Ping struct {
	OK       bool   `json:"ok"`
	Site     string `json:"site_name"`
	Protocol string `json:"protocol_version"`
	Balance  string `json:"balance"`
	Currency string `json:"currency"`
}
type Quote struct {
	SKU      int64  `json:"sku_id"`
	Quantity int    `json:"quantity"`
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}
type CreateInput struct {
	SKU      int64  `json:"sku_id"`
	Quantity int    `json:"quantity"`
	Number   string `json:"downstream_order_no"`
	Amount   string `json:"expected_amount"`
}
type OrderItem struct {
	Product  int64 `json:"product_id"`
	SKU      int64 `json:"sku_id"`
	Quantity int   `json:"quantity"`
}
type Fulfillment struct {
	Type    string          `json:"type"`
	Status  string          `json:"status"`
	Payload string          `json:"payload"`
	Data    json.RawMessage `json:"delivery_data"`
	At      string          `json:"delivered_at"`
}
type Order struct {
	ID          int64           `json:"order_id"`
	Number      string          `json:"order_no"`
	Status      string          `json:"status"`
	Amount      string          `json:"amount"`
	Currency    string          `json:"currency"`
	Refunded    string          `json:"refunded_amount"`
	Refunds     json.RawMessage `json:"refund_records"`
	Items       []OrderItem     `json:"items"`
	Fulfillment *Fulfillment    `json:"fulfillment,omitempty"`
}

func (c *Client) Ping(ctx context.Context) (Ping, error) {
	var out Ping
	err := c.call(ctx, "POST", prefix+"/ping", []byte("{}"), &out)
	if err == nil && (out.Protocol != "1.1" || out.Currency != "CNY") {
		err = errors.New("unsupported Cici protocol or currency")
	}
	return out, err
}
func (c *Client) Products(ctx context.Context, page int) (Products, error) {
	var out Products
	if page < 1 || page > 100000 {
		return out, errors.New("invalid product page")
	}
	q := url.Values{"page": {strconv.Itoa(page)}, "page_size": {"50"}, "include_inactive": {"true"}}
	err := c.call(ctx, "GET", prefix+"/products?"+q.Encode(), nil, &out)
	if err == nil && (out.Items == nil || out.Page != page || out.PageSize != 50 || out.Total < 0) {
		err = errors.New("invalid Cici product page")
	}
	return out, err
}
func (c *Client) Product(ctx context.Context, id int64) (Product, error) {
	var out struct {
		Product Product `json:"product"`
	}
	if id <= 0 {
		return out.Product, errors.New("invalid Cici product ID")
	}
	err := c.call(ctx, "GET", prefix+"/products/"+strconv.FormatInt(id, 10), nil, &out)
	if err == nil && out.Product.ID != id {
		err = errors.New("Cici product identity mismatch")
	}
	return out.Product, err
}
func (c *Client) Quote(ctx context.Context, sku int64, quantity int) (Quote, error) {
	var out Quote
	if sku <= 0 || quantity < 1 || quantity > 10000 {
		return out, errors.New("invalid Cici quantity or SKU")
	}
	body, _ := json.Marshal(struct {
		SKU      int64 `json:"sku_id"`
		Quantity int   `json:"quantity"`
	}{sku, quantity})
	err := c.call(ctx, "POST", prefix+"/quote", body, &out)
	amount, valid := money(out.Amount)
	if err == nil && (!valid || amount.Sign() <= 0 || out.Currency != "CNY" || out.SKU != sku || out.Quantity != quantity) {
		err = errors.New("invalid Cici quote")
	}
	return out, err
}

// The caller persists these exact bytes before making the first purchase attempt.
func (c *Client) Create(ctx context.Context, body []byte) (Order, error) {
	var out Order
	err := c.call(ctx, "POST", prefix+"/orders", body, &out)
	if err == nil && (out.ID <= 0 || out.Number == "") {
		err = &APIError{Code: "invalid_response"}
	}
	return out, err
}
func (c *Client) Order(ctx context.Context, id int64) (Order, error) {
	var out Order
	if id <= 0 {
		return out, errors.New("invalid Cici order ID")
	}
	err := c.call(ctx, "GET", prefix+"/orders/"+strconv.FormatInt(id, 10), nil, &out)
	if err == nil && (out.ID != id || out.Number == "") {
		err = &APIError{Code: "order_identity_mismatch"}
	}
	return out, err
}
func (c *Client) Cancel(ctx context.Context, id int64) error {
	if id <= 0 {
		return errors.New("invalid Cici order ID")
	}
	var out Order
	err := c.call(ctx, "POST", prefix+"/orders/"+strconv.FormatInt(id, 10)+"/cancel", nil, &out)
	if err == nil && (out.ID != id || out.Status != "canceled") {
		err = &APIError{Code: "invalid_response"}
	}
	return err
}
