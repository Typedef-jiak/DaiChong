import { useRef, useState } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { CopyButton } from '@/components/copy-button'
import { Feedback } from '@/components/workspace-loading'
import { api, errorMessage } from '@/lib/api'
import { useResource } from '@/lib/use-resource'
import './cici.css'

type Config = { configured: boolean; purchasesEnabled: boolean; protocol: string }
type Ping = { site_name: string; balance: string; currency: string }
type SKU = { id: number; sku_code: string; spec_values: Record<string, string>; price_amount: string; stock_status: string; stock_quantity: number; is_active: boolean }
type Product = { id: number; title: Record<string, string>; description: Record<string, string>; fulfillment_type: string; is_active: boolean; skus: SKU[] }
type Purchase = { id: string; product_id: number; sku_id: number; quantity: number; expected_amount: string; title: string; state: string; supplier_id: number | null; supplier_number: string; supplier_status: string; error_code: string; updated_at: number }
type Detail = { purchase: Purchase; order?: { refunded_amount: string; fulfillment?: { payload: string } } }
const title = (v: Record<string, string> | null | undefined) => v?.['zh-CN'] || v?.['en-US'] || Object.values(v ?? {})[0] || '未命名商品'
const states: Record<string, string> = { draft: '已报价，未扣款', submitting: '采购提交中', unknown: '结果不明，保留原单', pending: '等待交付 / 核对', delivered: '交付已核验', review: '需要核对', canceled: '已取消', refunded: '已退款' }

export default function CiciPage({ revision }: { revision: number }) {
  const config = useResource<Config>('/api/admin/cici', revision)
  const [localRevision, setLocalRevision] = useState(0)
  const [selected, setSelected] = useState<string>()
  const refresh = revision + localRevision
  return <div className="cici-workspace">
    <section className="admin-panel">
      <div className="admin-panel-title"><h2>Cici 供货采购</h2><span>商品、报价、钱包采购与交付</span></div>
      <Feedback error={config.error} loading={config.loading} data={config.data} shape="compact" />
      {config.data && <p className="admin-subtle">连接：{config.data.configured ? '凭据已配置' : '等待配置'} · 钱包采购：{config.data.purchasesEnabled ? '已启用' : '未启用'} · 协议 {config.data.protocol}</p>}
      {!config.data?.configured && config.data && <p>请在 Go 后端配置 CICI_API_KEY_FILE 和 CICI_API_SECRET_FILE，并启用 CICI_ENABLED。密钥只保存在服务器端。</p>}
      <p className="admin-panel-footnote">此处是向 Cici 采购商品的工作台。报价不扣款，提交采购会扣除你的 Cici 钱包余额；顾客支付尚未接入。</p>
      {config.data?.configured && <Connection />}
    </section>
    {config.data?.configured && <Products revision={refresh} onDraft={id => { setSelected(id); setLocalRevision(v => v + 1) }} />}
    <Purchases revision={refresh} onSelect={setSelected} />
    {selected && <PurchaseDialog key={selected} id={selected} enabled={Boolean(config.data?.purchasesEnabled)} onClose={() => setSelected(undefined)} onChanged={() => setLocalRevision(v => v + 1)} />}
  </div>
}

function Connection() {
  const [ping, setPing] = useState<Ping>(), [busy, setBusy] = useState(false), [error, setError] = useState('')
  async function check() {
    setBusy(true); setError(''); setPing(undefined)
    try { setPing(await api<Ping>('/api/admin/cici/ping')) } catch (e) { setError(errorMessage(e)) } finally { setBusy(false) }
  }
  return <div className="cici-connection"><Button variant="outline" disabled={busy} onClick={() => void check()}>{busy ? '检查中…' : '测试连接与查询余额'}</Button><Feedback error={error} />{ping && <p role="status">{ping.site_name} · 钱包余额 {ping.currency} {ping.balance}</p>}</div>
}

function Products({ revision, onDraft }: { revision: number; onDraft: (id: string) => void }) {
  const [page, setPage] = useState(1), [selection, setSelection] = useState<{ product: Product; sku: SKU }>()
  const data = useResource<{ items: Product[]; total: number; page_size: number }>(`/api/admin/cici/products?page=${page}`, revision)
  const pages = Math.max(1, Math.ceil((data.data?.total ?? 0) / 50))
  return <section className="admin-panel">
    <div className="admin-panel-title"><h2>Cici 商品目录</h2><span>当前账户供货价 · 每页最多 50 件</span></div>
    <Feedback error={data.error} loading={data.loading} data={data.data} />
    {data.data && <><div className="admin-table-scroll"><table className="admin-table"><thead><tr><th>商品 / SKU</th><th>供货单价</th><th>库存</th><th>交付方式</th><th>操作</th></tr></thead><tbody>
      {data.data.items.flatMap(product => (product.skus ?? []).map(sku => {
        const available = product.is_active && sku.is_active && ['in_stock', 'low_stock'].includes(sku.stock_status)
        return <tr key={`${product.id}-${sku.id}`}><td>{title(product.title)}<small>{Object.values(sku.spec_values ?? {}).join(' / ') || sku.sku_code} · SKU {sku.id}</small></td><td>CNY {sku.price_amount}</td><td>{!available ? '已下架或缺货' : sku.stock_quantity === -1 ? '有货，数量隐藏' : sku.stock_quantity}</td><td>{product.fulfillment_type === 'auto' ? '自动交付' : '本期暂不支持'}</td><td><Button variant="outline" disabled={!available || product.fulfillment_type !== 'auto'} onClick={() => setSelection({ product, sku })}>获取采购报价</Button></td></tr>
      }))}
    </tbody></table></div>{!data.data.items.length && <p className="admin-subtle">本页没有 API 开放商品；后续页面仍可能有商品。</p>}
    <div className="cici-pagination"><Button variant="outline" disabled={page === 1 || data.loading} onClick={() => setPage(v => v - 1)}>上一页</Button><span>第 {page} / {pages} 页</span><Button variant="outline" disabled={page >= pages || data.loading} onClick={() => setPage(v => v + 1)}>下一页</Button></div></>}
    {selection && <DraftDialog product={selection.product} sku={selection.sku} onClose={() => setSelection(undefined)} onDraft={id => { setSelection(undefined); onDraft(id) }} />}
  </section>
}

function DraftDialog({ product, sku, onClose, onDraft }: { product: Product; sku: SKU; onClose: () => void; onDraft: (id: string) => void }) {
  const [quantity, setQuantity] = useState('1'), [busy, setBusy] = useState(false), [error, setError] = useState('')
  const [hasRequest, setHasRequest] = useState(false)
  const request = useRef<{ id: string; quantity: number } | undefined>(undefined), lock = useRef(false)
  async function quote() {
    const count = Number(quantity)
    if (lock.current || !Number.isInteger(count) || count < 1 || count > 10000) return
    lock.current = true; setBusy(true); setError('')
    request.current ??= { id: crypto.randomUUID(), quantity: count }
    setHasRequest(true)
    try {
      const result = await api<{ purchase: Purchase }>('/api/admin/cici/orders', { id: request.current.id, product_id: product.id, sku_id: sku.id, quantity: request.current.quantity })
      onDraft(result.purchase.id)
    } catch (e) { setError(errorMessage(e)) } finally { lock.current = false; setBusy(false) }
  }
  return <Dialog open onOpenChange={open => { if (!open && !busy) onClose() }}><DialogContent className="admin-edit"><DialogHeader><DialogTitle>获取采购报价</DialogTitle><DialogDescription>{title(product.title)} · SKU {sku.id}。只保存报价，不扣钱包余额。</DialogDescription></DialogHeader>
    <form className="admin-edit-form" onSubmit={e => { e.preventDefault(); void quote() }}><label htmlFor="cici-quantity">采购数量</label><Input id="cici-quantity" type="number" min={1} max={10000} step={1} required value={quantity} disabled={busy || hasRequest} onChange={e => setQuantity(e.target.value)} /><p className="admin-subtle">最终总额以 Cici 报价为准，报价不锁定库存。</p><Feedback error={error} /><Button type="submit" disabled={busy}>{busy ? '获取报价…' : hasRequest ? '重试获取原报价' : '保存采购报价'}</Button></form>
  </DialogContent></Dialog>
}

function Purchases({ revision, onSelect }: { revision: number; onSelect: (id: string) => void }) {
  const [page, setPage] = useState(1)
  const data = useResource<{ items: Purchase[]; total: number }>(`/api/admin/cici/orders?page=${page}`, revision)
  return <section className="admin-panel"><div className="admin-panel-title"><h2>采购记录</h2><span>报价、提交与交付分别记录</span></div><Feedback error={data.error} loading={data.loading} data={data.data} />
    {data.data && <><div className="admin-table-scroll"><table className="admin-table"><thead><tr><th>商品</th><th>数量 / 总额</th><th>状态</th><th>Cici 单号</th><th>操作</th></tr></thead><tbody>{data.data.items.map(p => <tr key={p.id}><td>{p.title}<small>{p.id}</small></td><td>{p.quantity} 件<small>CNY {p.expected_amount}</small></td><td>{states[p.state] ?? p.state}<small>{p.error_code}</small></td><td>{p.supplier_number || '尚未取得'}</td><td><Button variant="outline" onClick={() => onSelect(p.id)}>查看采购</Button></td></tr>)}</tbody></table></div>{!data.data.items.length && <p className="admin-subtle">暂无采购。从上方目录获取报价即可开始。</p>}
    <div className="cici-pagination"><Button variant="outline" disabled={page === 1 || data.loading} onClick={() => setPage(v => v - 1)}>上一页</Button><span>第 {page} 页 · 共 {data.data.total} 笔</span><Button variant="outline" disabled={page * 25 >= data.data.total || data.loading} onClick={() => setPage(v => v + 1)}>下一页</Button></div></>}
  </section>
}

function PurchaseDialog({ id, enabled, onClose, onChanged }: { id: string; enabled: boolean; onClose: () => void; onChanged: () => void }) {
  const [revision, setRevision] = useState(0), [busy, setBusy] = useState(false), [error, setError] = useState('')
  const [confirmed, setConfirmed] = useState(false), [cancelConfirmed, setCancelConfirmed] = useState(false), lock = useRef(false)
  const data = useResource<Detail>(`/api/admin/cici/orders/${id}`, revision)
  const p = data.data?.purchase
  async function act(action: 'submit' | 'refresh' | 'cancel') {
    if (lock.current || !p) return
    lock.current = true; setBusy(true); setError('')
    try {
      if (action === 'submit') await api(`/api/admin/cici/orders/${id}/submit`, { confirmed, expected_amount: p.expected_amount })
      else if (action === 'cancel') await api(`/api/admin/cici/orders/${id}/cancel`, { confirmed: cancelConfirmed })
      else await api(`/api/admin/cici/orders/${id}/refresh`, {})
    } catch (e) { setError(errorMessage(e)) }
    finally { setRevision(v => v + 1); onChanged(); setConfirmed(false); setCancelConfirmed(false); lock.current = false; setBusy(false) }
  }
  const canSubmit = p && !p.supplier_id && ['draft', 'unknown', 'submitting', 'pending'].includes(p.state)
  return <Dialog open onOpenChange={open => { if (!open && !busy) onClose() }}><DialogContent className="admin-edit cici-detail"><DialogHeader><DialogTitle>采购详情</DialogTitle><DialogDescription>查看原采购单的金额、状态和交付结果。</DialogDescription></DialogHeader>
    <Feedback error={error || data.error} loading={data.loading} data={data.data} shape="compact" />
    {p && <><h3>{p.title}</h3><p>{p.quantity} 件 · CNY {p.expected_amount} · {states[p.state] ?? p.state}</p><p className="admin-subtle">本地采购号：{p.id}<br />Cici 单号：{p.supplier_number || '尚未取得'}<br />上游状态：{p.supplier_status || '尚未提交'}</p>
      {p.error_code && <p role="alert">核对提示：{p.error_code}。保留本采购号，先核对原单和钱包，勿另建订单重复购买。</p>}
      {canSubmit && <div className="cici-actions"><label className="admin-checkbox"><input type="checkbox" checked={confirmed} onChange={e => setConfirmed(e.target.checked)} disabled={busy || !enabled} />确认按 CNY {p.expected_amount} 从 Cici 钱包采购；重试使用原业务号和原参数。</label><Button disabled={!enabled || !confirmed || busy || data.loading} onClick={() => void act('submit')}>{busy ? '处理中…' : p.state === 'draft' ? '确认钱包采购' : '恢复原采购请求'}</Button>{!enabled && <p className="admin-subtle">钱包采购未启用，当前仅可查询和报价。</p>}</div>}
      {p.supplier_id && <div className="cici-actions"><Button variant="outline" disabled={busy || data.loading} onClick={() => void act('refresh')}>{busy ? '核对中…' : '查询最新状态与交付'}</Button>
        {!['delivered', 'refunded', 'canceled'].includes(p.state) && <details><summary>申请取消原采购</summary><p>是否可取消由 Cici 决定，此操作不等同于退款。</p><label className="admin-checkbox"><input type="checkbox" checked={cancelConfirmed} disabled={busy || !enabled} onChange={e => setCancelConfirmed(e.target.checked)} />确认申请取消这笔采购</label><Button variant="outline" disabled={!enabled || !cancelConfirmed || busy || data.loading} onClick={() => void act('cancel')}>申请取消</Button></details>}
      </div>}
      {data.data?.order?.refunded_amount && <p>累计退款：CNY {data.data.order.refunded_amount}</p>}
      {p.state === 'delivered' && data.data?.order?.fulfillment && <section><h3>已核验交付内容</h3><pre className="cici-payload">{data.data.order.fulfillment.payload}</pre><CopyButton variant="outline" value={data.data.order.fulfillment.payload} label="复制交付内容" /></section>}
      {p.state !== 'delivered' && <p className="admin-panel-footnote">只有订单、金额、SKU、数量及交付状态核验通过后才展示交付内容。已支付不代表已交付。</p>}
    </>}
  </DialogContent></Dialog>
}
