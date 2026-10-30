# GPTibo

当前开发以 Cici 供货 API 为主：Go 后端新增商品查询、报价、管理员钱包采购及交付核验，详见 [Cici 本地对接](services/storefront/CICI.md)。顾客支付暂不接入。下文的匿名化说明适用于原有供应商示例，Cici 使用正式公开服务地址，真实凭据仍不随源码提供。

可自托管的商城前台、管理后台、代理工作台及主站后端。前端使用 React、TypeScript 和 Vite；主后端使用 Go、Gin 与 PostgreSQL，同时保留 Cloudflare Workers + D1 实现。

这是经过脱敏的独立开源版本，使用 MIT 许可证。上游供应商和聚合支付通道已匿名化，接入地址使用保留域名 `example.invalid`，商户身份、密钥、授权商品映射和生产部署资料不随源码提供。接口适配器保留为开发参考，需要自行实现或核对协议；**不是填入原商户密钥即可上线的生产副本**。支付宝官方公开协议示例保留，真实支付、采购及通知默认关闭。

## 包含内容

- 商品展示、订单查询、售后、邮件模板和多语言界面。
- 管理员及代理工作台、商品定价、订单、客户、退款和结算管理。
- Go API、业务 Worker、数据库迁移、鉴权、交易状态机及回归测试。
- Cloudflare Worker / D1 备用实现与本地开发配置。

不包含生产数据、生产配置、历史 Git 提交、运营截图、独立供货平台和内部运维工具。数据库结构、配置名和部分路由已匿名化：仅使用新建的开发数据库，不可直接升级已有生产库。示例金额和商品仅用于演示，不构成报价。

## 前端开发

需要 Node.js 22.18+（推荐 24）、npm 和 Python 3。

```sh
npm ci
npm run dev
npm run build
npm test
npm run lint
npm run security:scan
```

默认 Vite 地址为 `http://127.0.0.1:5173`，API 代理至本地 Worker 8787。接 Go 后端时改用：

```sh
npx vite --config services/storefront/vite.config.ts
```

此时前端在 `http://127.0.0.1:5175`，API 代理至 Go 的 8093 端口。管理员入口 `/admin`，代理入口 `/partner`。空库和未配置身份时，相关功能不可用。

## Go 后端

见 [后端运行说明](services/storefront/README.md)。需要 Go 1.26+ 和独立 PostgreSQL 数据库，或使用 Docker Compose。默认不启用邮件和任何真实交易渠道。

```sh
cd services/storefront
go test ./...
go vet ./...
```

未设置 `STOREFRONT_TEST_DATABASE_URL` 时，PostgreSQL 集成测试会跳过；完整验收必须设置本机、库名以 `_test` 结尾的专用数据库连接。

## Cloudflare 本地运行

```sh
cp .dev.vars.example .dev.vars
npx wrangler d1 migrations apply gptibo-local --local
npm run preview:worker
```

`.dev.vars` 仅保存在本机。管理员需要自行设置 `ADMIN_USERNAME`、`ADMIN_EMAIL` 和独立的 Base32 `ADMIN_TOTP_SECRET`；邮件验证还需要邮件服务凭据和认证密钥，空配置不会启用登录。示例中的认证秘密不能复用测试向量。

仓库没有一键生产部署命令。`wrangler.jsonc` 不绑定任何生产账号/域名，数据库 ID 是全零占位值。若自行部署，先创建自己的资源、注入独立秘密、完成迁移并验证隔离和交易流程。

## 接入与安全

- `recharge`：匿名充值适配器。
- `account_supplier`、`delivery_supplier`：匿名采购适配器。
- `gatewaya`、`gatewayb`：匿名聚合支付协议示例。
- `xgift`：商城侧赠送接口适配器，不包含供应商执行系统。

所有回调、金额、签名、商户归属和幂等校验仍然保留。匿名地址和演示映射不对应可用服务；需要按自己的服务重新适配。前端变量不得存放服务器秘密。

发布检查及报告见 [SECURITY.md](SECURITY.md) 和 [OPEN_SOURCE_REVIEW.md](OPEN_SOURCE_REVIEW.md)。第三方图标、字体和样式遵循各自许可证；MIT 授权不转授第三方商标和美术作品权利，详见 `public/brands/` 和 `src/styles/vendor/`。
