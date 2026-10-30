package cici

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"gptibo/storefront/internal/fault"
	"gptibo/storefront/internal/security"
	"gptibo/storefront/internal/store"
)

type Service struct {
	Store            *store.Store
	Client           *Client
	PurchasesEnabled bool
}

type Purchase struct {
	ID                   string `json:"id"`
	Product              int64  `json:"product_id"`
	SKU                  int64  `json:"sku_id"`
	Quantity             int    `json:"quantity"`
	Amount               string `json:"expected_amount"`
	Title                string `json:"title"`
	State                string `json:"state"`
	SupplierID           *int64 `json:"supplier_id"`
	SupplierNumber       string `json:"supplier_number"`
	SupplierStatus       string `json:"supplier_status"`
	Error                string `json:"error_code"`
	Created              int64  `json:"created_at"`
	Updated              int64  `json:"updated_at"`
	account, body, lease string
	result               []byte
}

const columns = `id,product_id,sku_id,quantity,expected_amount,title,state,supplier_id,supplier_number,supplier_status,error_code,created_at,updated_at,account_hash,request_body,lease_token,result`

func scan(row pgx.Row) (Purchase, error) {
	var p Purchase
	err := row.Scan(&p.ID, &p.Product, &p.SKU, &p.Quantity, &p.Amount, &p.Title, &p.State, &p.SupplierID, &p.SupplierNumber, &p.SupplierStatus, &p.Error, &p.Created, &p.Updated, &p.account, &p.body, &p.lease, &p.result)
	if errors.Is(err, pgx.ErrNoRows) {
		err = fault.New(404, "采购单不存在。")
	}
	return p, err
}
func (s *Service) Get(ctx context.Context, id string) (Purchase, error) {
	return scan(s.Store.DB.QueryRow(ctx, `SELECT `+columns+` FROM cici_purchases WHERE id=$1`, id))
}
func (s *Service) List(ctx context.Context, page int) ([]Purchase, int, error) {
	var count int
	if err := s.Store.DB.QueryRow(ctx, `SELECT count(*) FROM cici_purchases`).Scan(&count); err != nil {
		return nil, 0, err
	}
	rows, err := s.Store.DB.Query(ctx, `SELECT `+columns+` FROM cici_purchases ORDER BY created_at DESC,id LIMIT 25 OFFSET $1`, (page-1)*25)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Purchase{}
	for rows.Next() {
		p, e := scan(rows)
		if e != nil {
			return nil, 0, e
		}
		out = append(out, p)
	}
	return out, count, rows.Err()
}
func (s *Service) Ready() error {
	if s.Client == nil {
		return fault.New(409, "请先在 Go 后端配置 Cici 凭据。")
	}
	return nil
}

type DraftInput struct {
	ID       string `json:"id"`
	Product  int64  `json:"product_id"`
	SKU      int64  `json:"sku_id"`
	Quantity int    `json:"quantity"`
}

func (s *Service) Draft(ctx context.Context, in DraftInput, actor string) (Purchase, error) {
	if err := s.Ready(); err != nil {
		return Purchase{}, err
	}
	if !security.ValidID(in.ID) || in.Product <= 0 || in.SKU <= 0 || in.Quantity < 1 || in.Quantity > 10000 {
		return Purchase{}, fault.New(400, "商品、数量或本地采购编号无效。")
	}
	// Repeated UI requests return the same frozen quote; they never mint a new business key.
	if old, err := s.Get(ctx, in.ID); err == nil {
		if old.account != s.Client.Account() || old.Product != in.Product || old.SKU != in.SKU || old.Quantity != in.Quantity {
			return Purchase{}, fault.New(409, "该采购编号已绑定其他商品或凭据。")
		}
		return old, nil
	} else if fault.Status(err) != 404 {
		return Purchase{}, err
	}
	product, err := s.Client.Product(ctx, in.Product)
	if err != nil {
		return Purchase{}, err
	}
	if !product.Active || product.Type != "auto" {
		return Purchase{}, fault.New(409, "本期只支持在售的自动交付商品。")
	}
	available := false
	for _, sku := range product.SKUs {
		if sku.ID == in.SKU && sku.Active && (sku.Stock == "in_stock" || sku.Stock == "low_stock") && (sku.Quantity == -1 || sku.Quantity >= int64(in.Quantity)) {
			available = true
		}
	}
	if !available {
		return Purchase{}, fault.New(409, "规格已下架、库存不足或不属于该商品。")
	}
	quote, err := s.Client.Quote(ctx, in.SKU, in.Quantity)
	if err != nil {
		return Purchase{}, err
	}
	body, _ := json.Marshal(CreateInput{SKU: in.SKU, Quantity: in.Quantity, Number: "cici_" + in.ID, Amount: quote.Amount})
	title := product.Title["zh-CN"]
	if title == "" {
		title = product.Title["en-US"]
	}
	if title == "" {
		title = "Cici 商品"
	}
	tx, err := s.Store.DB.Begin(ctx)
	if err != nil {
		return Purchase{}, err
	}
	defer tx.Rollback(ctx)
	now := time.Now().UnixMilli()
	tag, err := tx.Exec(ctx, `INSERT INTO cici_purchases(id,account_hash,product_id,sku_id,quantity,expected_amount,title,request_body,created_by,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10) ON CONFLICT(id) DO NOTHING`, in.ID, s.Client.Account(), in.Product, in.SKU, in.Quantity, quote.Amount, title, string(body), actor, now)
	if err != nil {
		return Purchase{}, err
	}
	if tag.RowsAffected() == 1 {
		if err = event(ctx, tx, in.ID, actor, "quote", "draft", ""); err != nil {
			return Purchase{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Purchase{}, err
	}
	p, err := s.Get(ctx, in.ID)
	if err == nil && (p.account != s.Client.Account() || p.Product != in.Product || p.SKU != in.SKU || p.Quantity != in.Quantity) {
		err = fault.New(409, "该采购编号已绑定其他商品或凭据。")
	}
	return p, err
}

func event(ctx context.Context, tx pgx.Tx, id, actor, action, state, code string) error {
	_, err := tx.Exec(ctx, `INSERT INTO cici_purchase_events(id,purchase_id,actor,action,state,error_code,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, security.ID(), id, actor, action, state, code, time.Now().UnixMilli())
	return err
}

func (s *Service) lock(ctx context.Context, id string) (Purchase, error) {
	if err := s.Ready(); err != nil {
		return Purchase{}, err
	}
	lease := security.ID()
	now := time.Now().UnixMilli()
	p, err := scan(s.Store.DB.QueryRow(ctx, `UPDATE cici_purchases SET lease_token=$2,lease_until=$3 WHERE id=$1 AND account_hash=$4 AND lease_until<=$5 RETURNING `+columns, id, lease, now+120000, s.Client.Account(), now))
	if fault.Status(err) == 404 {
		return p, fault.New(409, "采购正在处理、记录不存在或 API Key 已变化，请核对后重试。")
	}
	return p, err
}
func (s *Service) unlock(p Purchase) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = s.Store.DB.Exec(ctx, `UPDATE cici_purchases SET lease_until=0 WHERE id=$1 AND lease_token=$2`, p.ID, p.lease)
}

// Persist outcomes even when the browser disconnects after the upstream accepted a purchase.
func (s *Service) save(p Purchase, state, code, actor, action string, order *Order) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := s.Store.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var result any
	id, number, status := p.SupplierID, p.SupplierNumber, p.SupplierStatus
	if order != nil {
		if order.ID > 0 {
			id = &order.ID
		}
		if order.Number != "" && (number == "" || number == order.Number) {
			number = order.Number
		}
		status = order.Status
		if action == "query" {
			b, _ := json.Marshal(order)
			result = string(b)
		}
	}
	now := time.Now().UnixMilli()
	next := now + 60000
	if state == "delivered" || state == "refunded" || state == "canceled" {
		next = now + int64((6*time.Hour)/time.Millisecond)
	}
	tag, err := tx.Exec(ctx, `UPDATE cici_purchases SET state=$3,error_code=$4,supplier_id=$5,supplier_number=$6,supplier_status=$7,result=COALESCE($8::jsonb,result),updated_at=$9,next_check_at=$10 WHERE id=$1 AND lease_token=$2 AND lease_until>$9 AND (supplier_id IS NULL OR supplier_id=$5)`, p.ID, p.lease, state, code, id, number, status, result, now, next)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fault.New(409, "采购状态已变化，请重新查询。")
	}
	if err = event(ctx, tx, p.ID, actor, action, state, code); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func errorState(err error) (string, string, *Order) {
	var api *APIError
	if !errors.As(err, &api) {
		return "review", "invalid_response", nil
	}
	var order *Order
	if api.OrderID > 0 {
		order = &Order{ID: api.OrderID, Status: api.Status}
	}
	switch api.Code {
	case "transport_unknown":
		return "unknown", api.Code, order
	case "order_in_progress":
		return "pending", api.Code, order
	case "quote_unavailable", "idempotency_lookup_failed", "order_lookup_failed":
		if api.HTTP == 503 {
			return "unknown", api.Code, order
		}
	}
	// Includes price changes, canceled keys, payment failure and reconciliation errors.
	// No automatic key replacement or purchase retry is permitted for these cases.
	return "review", api.Code, order
}

func (s *Service) Submit(ctx context.Context, id, amount, actor string) (Purchase, error) {
	if !s.PurchasesEnabled {
		return Purchase{}, fault.New(409, "Cici 钱包采购尚未启用，商品查询和报价不受影响。")
	}
	p, err := s.lock(ctx, id)
	if err != nil {
		return p, err
	}
	defer s.unlock(p)
	if amount != p.Amount {
		return p, fault.New(409, "确认金额与采购单不一致，请重新查看报价。")
	}
	if p.SupplierID != nil {
		return s.query(ctx, p, actor)
	}
	if p.State != "draft" && p.State != "unknown" && p.State != "submitting" && p.State != "pending" {
		return p, fault.New(409, "该采购需要人工核对，禁止重新购买。")
	}
	if err = s.save(p, "submitting", "", actor, "submit", nil); err != nil {
		return p, err
	}
	order, callErr := s.Client.Create(ctx, []byte(p.body))
	if callErr != nil {
		state, code, partial := errorState(callErr)
		if err = s.save(p, state, code, actor, "submit-result", partial); err != nil {
			return p, err
		}
		return s.Get(ctx, id)
	}
	state, code := "pending", ""
	if order.Currency != "CNY" || !sameAmount(order.Amount, p.Amount) {
		state, code = "review", "amount_mismatch"
	}
	if err = s.save(p, state, code, actor, "submit-result", &order); err != nil {
		return p, err
	}
	// Never equate paid with delivered; querying is a separate, recoverable operation.
	return s.Get(ctx, id)
}

func classify(p Purchase, o Order) (string, string) {
	if p.SupplierID == nil || o.ID != *p.SupplierID || (p.SupplierNumber != "" && o.Number != p.SupplierNumber) {
		return "review", "order_identity_mismatch"
	}
	if o.Currency != "CNY" || !sameAmount(o.Amount, p.Amount) {
		return "review", "amount_mismatch"
	}
	refund, ok := money(o.Refunded)
	if !ok {
		return "review", "invalid_refund_amount"
	}
	if o.Status == "canceled" {
		return "canceled", ""
	}
	if o.Status == "refunded" {
		return "refunded", ""
	}
	if refund.Sign() > 0 || o.Status == "partially_refunded" || o.Status == "partially_delivered" {
		return "review", "partial_order"
	}
	if len(o.Items) != 1 || o.Items[0].Product != p.Product || o.Items[0].SKU != p.SKU || o.Items[0].Quantity != p.Quantity {
		return "review", "item_mismatch"
	}
	switch o.Status {
	case "pending_payment", "paid", "fulfilling":
		return "pending", ""
	case "completed", "delivered":
		if o.Fulfillment != nil && o.Fulfillment.Status == "delivered" && strings.TrimSpace(o.Fulfillment.Payload) != "" {
			return "delivered", ""
		}
		return "pending", "awaiting_delivery"
	default:
		return "review", "unknown_status"
	}
}
func (s *Service) query(ctx context.Context, p Purchase, actor string) (Purchase, error) {
	if p.SupplierID == nil {
		return p, fault.New(409, "尚未取得 Cici 订单 ID；结果不明时只能用原采购单、原参数恢复，不能另建采购。")
	}
	o, err := s.Client.Order(ctx, *p.SupplierID)
	if err != nil {
		_, code, _ := errorState(err)
		// Preserve a known terminal state during a temporary query failure.
		if e := s.save(p, p.State, code, actor, "query-error", nil); e != nil {
			return p, e
		}
		return s.Get(ctx, p.ID)
	}
	state, code := classify(p, o)
	if err = s.save(p, state, code, actor, "query", &o); err != nil {
		return p, err
	}
	return s.Get(ctx, p.ID)
}
func (s *Service) Refresh(ctx context.Context, id, actor string) (Purchase, error) {
	p, err := s.lock(ctx, id)
	if err != nil {
		return p, err
	}
	defer s.unlock(p)
	return s.query(ctx, p, actor)
}
func (s *Service) Cancel(ctx context.Context, id, actor string) (Purchase, error) {
	if !s.PurchasesEnabled {
		return Purchase{}, fault.New(409, "Cici 钱包操作尚未启用。")
	}
	p, err := s.lock(ctx, id)
	if err != nil {
		return p, err
	}
	defer s.unlock(p)
	if p.SupplierID == nil {
		return p, fault.New(409, "没有 Cici 订单 ID，请先核对采购结果。")
	}
	if err = s.save(p, "review", "cancel_pending", actor, "cancel", nil); err != nil {
		return p, err
	}
	err = s.Client.Cancel(ctx, *p.SupplierID)
	if err != nil {
		_, code, _ := errorState(err)
		if e := s.save(p, "review", code, actor, "cancel-result", nil); e != nil {
			return p, e
		}
		return s.Get(ctx, id)
	}
	if err = s.save(p, "canceled", "", actor, "cancel-result", &Order{ID: *p.SupplierID, Number: p.SupplierNumber, Status: "canceled"}); err != nil {
		return p, err
	}
	return s.Get(ctx, id)
}

type Detail struct {
	Purchase Purchase `json:"purchase"`
	Order    *Order   `json:"order,omitempty"`
}

func (s *Service) Detail(ctx context.Context, id string) (Detail, error) {
	p, err := s.Get(ctx, id)
	out := Detail{Purchase: p}
	if err != nil {
		return out, err
	}
	if len(p.result) > 0 {
		var o Order
		if json.Unmarshal(p.result, &o) != nil {
			return out, errors.New("invalid stored Cici result")
		}
		// A refund/review must never keep exposing old delivery as ready to dispatch.
		if p.State != "delivered" {
			o.Fulfillment = nil
		}
		out.Order = &o
	}
	return out, nil
}

// Background reconciliation only queries known IDs; it never creates purchases.
func (s *Service) Reconcile(ctx context.Context) error {
	if s.Client == nil {
		return nil
	}
	rows, err := s.Store.DB.Query(ctx, `SELECT id FROM cici_purchases WHERE account_hash=$1 AND supplier_id IS NOT NULL AND lease_until<=$2 AND next_check_at<=$2 ORDER BY next_check_at LIMIT 10`, s.Client.Account(), time.Now().UnixMilli())
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var first error
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, err = s.Refresh(ctx, id, "system"); err != nil && first == nil {
			first = err
		}
	}
	return first
}
