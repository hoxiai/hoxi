---
name: shoply-ops
description: Manage Shoply independent e-commerce stores, SaaS tenant licenses, APay partner contracts, and GoPanel production operations (runshoply.com / iqingpu.com / shoply.page). 管理 Shoply 独立站店铺、SaaS 租户订阅权益、APay 合作方接口契约与线上容器运维。当用户要求查询独立站店铺大盘、查看商家店铺与订单 GMV、创建新独立站、生成店铺后台免密登录链接、对账同步客户订阅权益、跟进建站咨询线索、检查四项接入自检或排查 Shoply 线上容器与 PHP 进程池时使用。Use when the user asks to inspect or create Shoply stores, generate store admin SSO URLs, sync or reconcile SaaS licenses, check consulting leads, run partner smoke tests, or diagnose Shoply Go/PHP/Podman services.
compatibility: 需要访问 Shoply 或 APay 工作区、对应合作方环境变量或受控 GoPanel 节点
metadata:
  author: "Hoxi"
  version: "1.0.0"
---

# Shoply 独立站店铺管理与 SaaS 运维技能 (`shoply-ops`)

## 适用场景

- **商家独立站管理**：查询名下独立站列表、剩余可开店配额（`quota`）、14 天试用状态、单店与汇总 30 天订单量 / GMV / UV，创建新独立站，或签发一次性免密进入店铺后台的 SSO 链接。
- **SaaS 平台主理人运营**：查询 `runshoply.com` / `iqingpu.com` 全平台店铺大盘（总店数、活跃/试用/7 天内即将到期 `expiringStores`、出单店数、时区/币种/套餐/类目分布），按店名/域名/客户邮箱检索店铺。
- **客户订阅权益对账与老商家绑定**：触发全量客户订阅权益游标对账（`reconcile`）或单客强制重推（`license/sync`），处理老商家三步验证码绑定（`bind/challenge` → `verify` → `confirm`）。
- **官网咨询线索与四项接入自检**：查看并跟进官网建站咨询线索（`leads`），校验合作方 HMAC 连接、每 5 分钟补推定时任务、线索收件箱与六档合格套餐（`readiness`）。
- **双节点服务诊断与零停机发布**：诊断 Shoply Go/PHP 进程池健康度、执行契约冒烟测试（`partner-smoke`）或通过 GoPanel 安全重启/热切容器。

## 输入

- 用户的自然语音或文字指令（如“看看今天 Shoply 开了几家店”“给客户同步一下店铺权益”“巡检 Shoply 线上状态”）。
- 当前所在工作区（Shoply 引擎仓或 APay 官网仓）。

## 步骤

### 1. 判定操作归属面

| 操作面 | 权威契约与入口 |
| --- | --- |
| **Shoply 引擎侧合作方接口** | `plugins/app/Saas/Partner/Apay.php`、`plugins/app/Saas/PARTNER_APAY.md`、`docs/ai/domains/app-saas.md` |
| **Shoply 引擎侧诊断与进程池** | `./shoply doctor --config ./config/conf.yaml`、`cd plugins && php shoply app:health`（详见 `docs/ai/runtime/runbook.md`） |
| **APay 官网与 SaaS 运营扩展** | `app/themes/shoply/AGENTS.md`、`app/extensions/shoply/server/client.ts`、`app/themes/shoply/scripts/remote-ops.mjs` |

### 2. 独立站店铺与权益操作规范

1. **查询店铺大盘或商家店铺（只读安全操作，可直接执行）**：
   - 全平台大盘调用 `store/overview`（或 APay `/api/admin/plugins/shoply/stores/list`），重点汇报：`totalStores`、`activeStores`、`trialStores`、`expiringStores`（7 天到期预警）、`orderingStores`、`orders30d`、`revenue30d` 及地区/套餐分布。
   - 单商家查询调用 `store/list`（或 `/api/shoply/stores`），汇报 `quota`（已开/上限/可用）与各店域名、套餐版本 `edition`、到期时间 `expireAt`、30 天订单与营收。
2. **创建新独立站（`store/create`）**：
   - 必须生成标准 UUID `requestId` 保证幂等。
   - 校验必填项：`name`、`categoryId`、`currency`、`timezone`、`lang`，可选二级域名前缀 `domain`（3–32 位小写字母/数字/单连字符，避开保留字，最终域名为 `<prefix>.<assignDomain>`）。
3. **签发店铺后台免密链接（`store/admin-url`）**：
   - 每次请求实时现签，禁止缓存；仅对归属人且非平台禁用（`status != 30`）的店铺签发。
4. **订阅权益同步与对账（`license/sync` / `reconcile`）**：
   - 规范套餐键固定六档：`free / showcase / starter / professional / premium / enterprise`。
   - 同步时必须携带单调递增 `version` 与 `licenseSource=apay`。
   - 若存在待恢复建店（`pendingRequests > 0`），必须保留在失败重试队列，严禁误报为全部完成。

### 3. 线上双节点运维与诊断 SOP

1. **双节点快速巡检（在 APay 仓执行）**：
   - 官网与网关巡检：`node app/themes/shoply/scripts/remote-ops.mjs status`
   - Shoply SaaS 节点巡检：`node app/themes/shoply/scripts/remote-ops.mjs partner-status`
   - 线上合作方 8 项契约冒烟测试：`node app/themes/shoply/scripts/remote-ops.mjs partner-smoke`
2. **Shoply 引擎本机/容器内健康检查（在 Shoply 仓执行）**：
   - 配置与环境诊断：`./shoply doctor --config ./config/conf.yaml`
   - PHP 应用池健康检查：`cd plugins && php shoply app:health`（若某应用因临时 Fatal/OOM 被冻结在 `runtime/application-failures/`，修复后运行 `php shoply app:health --retry <App>`）。

## 输出

- 面向语音播报或移动端展示时，先用 **1–3 句精炼结论**汇报核心数字（如活跃店数、30 天 GMV、到期预警、自检是否全绿），再列出关键明细。
- 涉及创建店铺、修改客户套餐或重启容器时，明确汇报目标店铺域名/ID、变更前后状态及探活结果。

## 边界与安全红线

- **【致命】Rootless Podman 重启红线**：在 Shoply SaaS 生产节点（`gopanel` 用户 Rootless Podman）通过 SSH 启动或重启容器时，**必须**使用 `systemd-run --machine=gopanel@.host --user --property=KillMode=none --pipe`（或直接调用 `node app/themes/shoply/scripts/remote-ops.mjs partner-restart`），严禁裸跑 `runuser -u gopanel`，否则会话退出会杀死 `rootlessport` 导致 `5490` 端口断联 502。
- **【致命】凭据保密**：`SHOPLY_PARTNER_SECRET`、`SHOPLY_PLATFORM_ADMIN_SECRET`、`CRON_SECRET` 与 RSA 私钥严禁打印到终端日志、写入公开 `settings` 表或下发到客户端。
- **【致命】老商家绑定防枚举与降级确认**：绝不按邮箱自动合并账号；`bind/confirm` 当 `downgrade=true` 时必须先向用户展示每家店的前后套餐对比并获得显式确认（`acknowledgeDowngrade=true`）。
- **发布后必探活**：任何容器重启或流水线切流后，必须验证站点 HTTP 200 且 `partner-smoke` 8/8 通过。
