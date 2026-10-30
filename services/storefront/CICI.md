# Cici 本地对接

按用户提供的 CCAI API v1.1（2026-10-12）实现。Go 后端是本功能的运行端，Cloudflare Worker 不提供这些路由。后台新增「Cici 供货采购」，并设为登录后的默认页面。

## 本期范围

- 连接与余额查询、分页商品/SKU/库存、商品详情、精确报价。
- 管理员先保存采购报价，再确认从 Cici 钱包采购，不依赖顾客支付或邮件发货开关。
- 持久化业务号和原始请求体；超时恢复使用原业务号和原参数；已知上游 ID 只查单。
- 显示核验后的交付文本；支持查询取消结果和退款累计金额。
- 只采购 `fulfillment_type=auto` 的商品。人工资料表单、部分交付自动分发、顾客收款与顾客邮件发货不在本期范围。
- 回调暂不启用，使用主动查单。业务 Worker 每分钟检查待处理订单；终态订单每六小时复核售后变化。后台也可立即查询。

## 第一步：无需数据库即可验证真实连接

在 `services/storefront` 目录下，将自己的 Key、Secret 分别放到以下纯文本文件（UTF-8，不带 BOM，只填值）：

```text
.secrets/cici-api-key
.secrets/cici-api-secret
```

`.secrets/` 已由 Git 忽略。不要把真实凭据写入示例、源码或命令行参数。确认本机实际公网出口已登记到 Cici 白名单；本机局域网 IP 不适用，代理、IPv6 与 NAT 会影响出口。

```powershell
go run ./cmd/cici ping
go run ./cmd/cici products 1
go run ./cmd/cici product 真实商品ID
go run ./cmd/cici quote 真实SKU_ID 1
```

这些命令不扣余额、不创建采购单，不需要管理员登录、PostgreSQL 或邮件配置。页码必须遍历到 `total/page_size` 给出的末页，不能遇到空页就停止。

## 第二步：Go 后端与采购工作台

数据库、管理员登录及邮件验证沿用 [后端运行说明](README.md)。使用独立本地 PostgreSQL，执行 `migrate` 后再次执行 `grant-runtime`，为迁移 `034_cici_procurement.sql` 新增表授权。

原生运行设置环境变量（程序不会自动读取 `.env`）：

```powershell
$env:CICI_ENABLED = 'true'
$env:CICI_API_KEY_FILE = (Resolve-Path '.secrets/cici-api-key').Path
$env:CICI_API_SECRET_FILE = (Resolve-Path '.secrets/cici-api-secret').Path
$env:CICI_PURCHASES_ENABLED = 'false'
```

Compose 可在原有文件之后叠加 `-f compose.cici.yaml`，在本地 `.env` 设置 `CICI_ENABLED=true`。API 和 Worker 必须使用同一个数据库和同一个 Cici API Key。Cici 凭据只在服务端加载，后台页面不会返回密钥。

连接成功后进入管理后台的「Cici 供货采购」：查看商品 → 获取采购报价 → 查看采购详情。报价只创建本地草稿，不扣款。准备实际采购时再设置 `CICI_PURCHASES_ENABLED=true` 并重启 API；在采购详情勾选确认金额后提交。

## 协议和恢复约束

- 固定地址 `https://aicici888.com/api/v1/upstream`；禁止携带认证头跟随重定向。
- HMAC-SHA256 原文是六行，用 LF 分隔，末尾没有 LF。签名和发送共用最终 URL 查询串和请求体字节。
- 金额始终保留十进制字符串，比较使用精确有理数，不经浮点换算。
- 同笔采购的 `downstream_order_no` 为 `cici_` 加本地 UUID。生成草稿后不能修改 SKU、数量、报价或请求体。
- 支付/对账错误、金额变化或未知响应会标记「需要核对」，不会自动换业务号补购。仅网络结果不明、文档允许的 503 和进行中状态可以按原单恢复；恢复操作需管理员再次确认。
- `paid` 不代表发货。核对订单号、金额、币种、SKU、数量、退款及 `fulfillment.status` 后才展示交付内容。部分交付、部分退款与未知状态进入核对状态。
- 取消不等于退款；Cici 没有自动申请退款接口。退款后不再展示旧交付内容为可发货内容。
- 本期采购记录独立于顾客订单，不伪造支付，不触发旧供应商的自动采购或邮件投递。

## 验证

```powershell
go test ./internal/cici ./internal/config ./internal/httpapi
# 专用本地测试库，名称必须以 _test 结尾；切勿使用业务库。
$env:STOREFRONT_TEST_DATABASE_URL = 'postgres://测试用户:测试密码@127.0.0.1:端口/daichong_test?sslmode=disable'
go test ./integration -run Cici -count=1
```

测试通过不代表真实凭据、白名单或钱包余额可用。真实联调先运行只读命令，采购接口会真实扣除 Cici 钱包余额。

### 本次验收记录（2026-10-29）

- Go 全量测试：519 个测试及子测试通过，0 失败、0 跳过；集成测试使用独立本地 PostgreSQL。
- 前端及原有 Worker 回归：391 项通过；前端构建与 TypeScript 检查通过。
- `go vet ./...`、新增界面 lint、Compose 配置和源码敏感信息扫描通过。
- 对抗式检查覆盖并发重试、进程恢复、审计写入失败、订单号漂移、金额和交付不符、退款后隐藏交付，以及管理员鉴权与日志脱敏。
- 尚未配置真实 Cici 凭据，因此真实连通性、白名单和真实采购尚未验证。
