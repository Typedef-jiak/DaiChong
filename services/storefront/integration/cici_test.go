package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"gptibo/storefront/internal/cici"
	"gptibo/storefront/internal/fault"
	"gptibo/storefront/internal/httpapi"
	"gptibo/storefront/internal/security"
)

type ciciRemote struct {
	mu          sync.Mutex
	creates     int
	body, nonce []string
	timeoutOnce bool
	createCode  string
	status      string
	quantity    int
	amount      string
	number      string
}

func (f *ciciRemote) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, _ := io.ReadAll(r.Body)
	if r.Header.Get("Dujiao-Next-Signature") != cici.Signature("fixture-secret", r.Method, r.URL.RequestURI(), r.Header.Get("Dujiao-Next-Timestamp"), r.Header.Get("Dujiao-Next-Nonce"), b) {
		return nil, errors.New("bad signature")
	}
	status, body := 200, ""
	switch r.Method + " " + r.URL.Path {
	case "POST /api/v1/upstream/ping":
		body = `{"ok":true,"protocol_version":"1.1","currency":"CNY","balance":"100.00"}`
	case "GET /api/v1/upstream/products/101":
		body = `{"ok":true,"product":{"id":101,"title":{"zh-CN":"测试自动商品"},"fulfillment_type":"auto","is_active":true,"skus":[{"id":501,"is_active":true,"stock_status":"in_stock","stock_quantity":100}]}}`
	case "POST /api/v1/upstream/quote":
		body = `{"ok":true,"sku_id":501,"quantity":1,"amount":"1.00","currency":"CNY"}`
	case "POST /api/v1/upstream/orders":
		f.creates++
		f.body = append(f.body, string(b))
		f.nonce = append(f.nonce, r.Header.Get("Dujiao-Next-Nonce"))
		if f.timeoutOnce {
			f.timeoutOnce = false
			return nil, errors.New("timeout after charge")
		}
		if f.createCode != "" {
			status = 409
			body = `{"ok":false,"error_code":"` + f.createCode + `"}`
		} else {
			body = `{"ok":true,"order_id":9001,"order_no":"fixture-cici-1","status":"paid","amount":"1.00","currency":"CNY"}`
		}
	case "GET /api/v1/upstream/orders/9001":
		state := f.status
		if state == "" {
			state = "completed"
		}
		quantity := f.quantity
		if quantity == 0 {
			quantity = 1
		}
		amount := f.amount
		if amount == "" {
			amount = "1.00"
		}
		refund := "0.00"
		if state == "refunded" {
			refund = amount
		}
		number := f.number
		if number == "" {
			number = "fixture-cici-1"
		}
		out := map[string]any{"ok": true, "order_id": 9001, "order_no": number, "status": state, "amount": amount, "currency": "CNY", "refunded_amount": refund, "items": []any{map[string]any{"product_id": 101, "sku_id": 501, "quantity": quantity}}, "fulfillment": map[string]any{"status": "delivered", "payload": "PRIVATE-FIXTURE-DELIVERY"}}
		data, _ := json.Marshal(out)
		body = string(data)
	case "POST /api/v1/upstream/orders/9001/cancel":
		body = `{"ok":true,"order_id":9001,"order_no":"fixture-cici-1","status":"canceled"}`
		f.status = "canceled"
	default:
		return nil, errors.New("unexpected Cici call")
	}
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func TestCiciDurableTimeoutRecoveryAndRefunds(t *testing.T) {
	owner, app := fixture(t, true)
	remote := &ciciRemote{timeoutOnce: true}
	client, _ := cici.NewClient("fixture-key", "fixture-secret")
	client.HTTP.Transport = remote
	s := &cici.Service{Store: app, Client: client, PurchasesEnabled: true}
	ctx := context.Background()
	id := security.ID()
	in := cici.DraftInput{ID: id, Product: 101, SKU: 501, Quantity: 1}
	p, err := s.Draft(ctx, in, "admin")
	if err != nil || p.State != "draft" || p.Amount != "1.00" || remote.creates != 0 {
		t.Fatal("quote made purchase", err)
	}
	if _, err = s.Draft(ctx, in, "admin"); err != nil {
		t.Fatal("same quote not idempotent", err)
	}
	changed := in
	changed.Quantity = 2
	if _, err = s.Draft(ctx, changed, "admin"); fault.Status(err) != 409 {
		t.Fatal("mutated frozen quote accepted", err)
	}
	if _, err = s.Submit(ctx, id, "2.00", "admin"); fault.Status(err) != 409 || remote.creates != 0 {
		t.Fatal("unconfirmed amount submitted", err)
	}
	p, err = s.Submit(ctx, id, "1.00", "admin")
	if err != nil || p.State != "unknown" || remote.creates != 1 {
		t.Fatal("ambiguous order lost", err)
	}
	// Reconstruct the service as if the process restarted, retaining only the database.
	s = &cici.Service{Store: app, Client: client, PurchasesEnabled: true}
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.Submit(ctx, id, "1.00", "admin")
			if e != nil && fault.Status(e) != 409 {
				failures <- e
			}
		}()
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		t.Fatal(e)
	}
	if remote.creates != 2 || remote.body[0] != remote.body[1] || remote.nonce[0] == remote.nonce[1] {
		t.Fatal("retry changed business payload, reused nonce or duplicated known purchase")
	}
	p, err = s.Refresh(ctx, id, "admin")
	if err != nil || p.State != "delivered" {
		t.Fatal("delivery not verified", err, p.State)
	}
	d, err := s.Detail(ctx, id)
	if err != nil || d.Order == nil || d.Order.Fulfillment == nil {
		t.Fatal("missing verified payload", err)
	}
	list, _, err := s.List(ctx, 1)
	data, _ := json.Marshal(list)
	if err != nil || strings.Contains(string(data), "PRIVATE-FIXTURE") || strings.Contains(string(data), "request_body") {
		t.Fatal("list leaked delivery/request")
	}
	remote.mu.Lock()
	remote.status = "refunded"
	remote.mu.Unlock()
	execSQL(t, owner, `UPDATE cici_purchases SET next_check_at=0 WHERE id=$1`, id)
	if err = s.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	d, err = s.Detail(ctx, id)
	if err != nil || d.Purchase.State != "refunded" || d.Order.Fulfillment != nil {
		t.Fatal("refunded purchase still exposes delivery", err)
	}
	if _, err = s.Submit(ctx, id, "1.00", "admin"); err != nil || remote.creates != 2 {
		t.Fatal("known purchase recreated", err)
	}
	var count int
	if err = app.DB.QueryRow(ctx, `SELECT count(*) FROM cici_purchase_events WHERE purchase_id=$1`, id).Scan(&count); err != nil || count < 5 {
		t.Fatal("audit events missing", err)
	}
}

func TestCiciBlockedErrorsAndCredentialChange(t *testing.T) {
	_, app := fixture(t, true)
	remote := &ciciRemote{createCode: "payment_reconciliation_required"}
	client, _ := cici.NewClient("fixture-key", "fixture-secret")
	client.HTTP.Transport = remote
	s := &cici.Service{Store: app, Client: client, PurchasesEnabled: true}
	ctx := context.Background()
	in := cici.DraftInput{ID: security.ID(), Product: 101, SKU: 501, Quantity: 1}
	if _, err := s.Draft(ctx, in, "admin"); err != nil {
		t.Fatal(err)
	}
	p, err := s.Submit(ctx, in.ID, "1.00", "admin")
	if err != nil || p.State != "review" {
		t.Fatal(err, p.State)
	}
	if _, err = s.Submit(ctx, in.ID, "1.00", "admin"); fault.Status(err) != 409 || remote.creates != 1 {
		t.Fatal("reconciliation error retried", err)
	}
	s.PurchasesEnabled = false
	if _, err = s.Submit(ctx, in.ID, "1.00", "admin"); fault.Status(err) != 409 {
		t.Fatal("purchase switch ignored")
	}
	s.PurchasesEnabled = true
	other, _ := cici.NewClient("other-key", "fixture-secret")
	other.HTTP.Transport = remote
	s.Client = other
	if _, err = s.Refresh(ctx, in.ID, "admin"); fault.Status(err) != 409 {
		t.Fatal("changed credential used for old order")
	}
}

func TestCiciAdminHTTPIsolationAndNoPaymentDependency(t *testing.T) {
	_, app, a, base, mail := identityFixture(t)
	admin := login(t, base, mail, "admin", "")
	remote := &ciciRemote{}
	client, _ := cici.NewClient("fixture-key", "fixture-secret")
	client.HTTP.Transport = remote
	s := &cici.Service{Store: app, Client: client, PurchasesEnabled: true}
	var logs bytes.Buffer
	h := httpapi.Router(app, "fixture", slog.New(slog.NewJSONHandler(&logs, nil)), httpapi.Options{Origin: testOrigin, Auth: a, Cici: s})
	for _, path := range []string{"/api/admin/cici", "/api/admin/cici/ping", "/api/admin/cici/orders"} {
		object(t, request(h, "GET", path, nil, nil), 401)
	}
	object(t, request(h, "GET", "/api/admin/cici/ping", nil, admin), 200)
	id := security.ID()
	out := object(t, request(h, "POST", "/api/admin/cici/orders", map[string]any{"id": id, "product_id": 101, "sku_id": 501, "quantity": 1}, admin), 200)
	if out["purchase"].(map[string]any)["state"] != "draft" {
		t.Fatal("quote missing")
	}
	endpoint := "/api/admin/cici/orders/" + id
	object(t, request(h, "POST", endpoint+"/submit", map[string]any{"confirmed": false, "expected_amount": "1.00"}, admin), 400)
	out = object(t, request(h, "POST", endpoint+"/submit", map[string]any{"confirmed": true, "expected_amount": "1.00"}, admin), 200)
	if out["purchase"].(map[string]any)["state"] != "pending" {
		t.Fatal("paid treated as delivered")
	}
	object(t, request(h, "POST", endpoint+"/refresh", map[string]any{}, admin), 200)
	detail := object(t, request(h, "GET", endpoint, nil, admin), 200)
	if detail["order"].(map[string]any)["fulfillment"].(map[string]any)["payload"] != "PRIVATE-FIXTURE-DELIVERY" {
		t.Fatal("delivery not shown")
	}
	object(t, request(h, "POST", endpoint+"/cancel", map[string]any{"confirmed": true}, admin), 200)
	detail = object(t, request(h, "GET", endpoint, nil, admin), 200)
	if _, exists := detail["order"].(map[string]any)["fulfillment"]; exists {
		t.Fatal("canceled delivery exposed")
	}
	for _, secret := range []string{"fixture-key", "fixture-secret", "PRIVATE-FIXTURE-DELIVERY", "Dujiao-Next-Signature"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("sensitive values logged")
		}
	}
	var count int
	if err := app.DB.QueryRow(context.Background(), `SELECT count(*) FROM orders`).Scan(&count); err != nil || count != 0 {
		t.Fatal("supplier purchase forged a customer payment/order", err)
	}
}

func TestCiciAuditFailurePreventsSpendAndIdentityCannotDrift(t *testing.T) {
	owner, app := fixture(t, true)
	remote := &ciciRemote{}
	client, _ := cici.NewClient("fixture-key", "fixture-secret")
	client.HTTP.Transport = remote
	s := &cici.Service{Store: app, Client: client, PurchasesEnabled: true}
	ctx := context.Background()
	id := security.ID()
	if _, err := s.Draft(ctx, cici.DraftInput{ID: id, Product: 101, SKU: 501, Quantity: 1}, "admin"); err != nil {
		t.Fatal(err)
	}
	execSQL(t, owner, `CREATE FUNCTION reject_cici_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='submit' THEN RAISE EXCEPTION 'fixture audit rejected'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_cici_audit BEFORE INSERT ON cici_purchase_events FOR EACH ROW EXECUTE FUNCTION reject_cici_audit();`)
	if _, err := s.Submit(ctx, id, "1.00", "admin"); err == nil || remote.creates != 0 {
		t.Fatal("spent without durable intent", err)
	}
	p, err := s.Get(ctx, id)
	if err != nil || p.State != "draft" {
		t.Fatal("failed transaction corrupted quote", err)
	}
	execSQL(t, owner, `DROP TRIGGER reject_cici_audit ON cici_purchase_events`)
	if _, err = s.Submit(ctx, id, "1.00", "admin"); err != nil {
		t.Fatal(err)
	}
	remote.number = "wrong-order"
	for range 2 {
		p, err = s.Refresh(ctx, id, "admin")
		if err != nil || p.State != "review" || p.SupplierNumber != "fixture-cici-1" {
			t.Fatal("mismatched response replaced pinned identity", err)
		}
		d, err := s.Detail(ctx, id)
		if err != nil || d.Order.Fulfillment != nil {
			t.Fatal("unverified delivery exposed")
		}
	}
	remote.number = ""
	p, err = s.Refresh(ctx, id, "admin")
	if err != nil || p.State != "delivered" {
		t.Fatal("correct identity did not recover", err)
	}
}
