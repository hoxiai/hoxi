<p align="center">
  <a href="https://hoxi.ai"><img src="../docs/assets/hoxi-banner.svg" alt="Hoxi" width="260" /></a>
</p>

<p align="center">
  <b>Hoxi 移动端 (`client/`)</b> —— 装进口袋的 AI 研发指挥台与 <a href="https://github.com/hoxiai/gopanel">GoPanel</a> 手机服务器管家（iOS / Android）。
</p>

<p align="center">
  <kbd><a href="README.md">English</a></kbd>
  <kbd><a href="README.zh-CN.md">中文(简体)</a></kbd>
</p>

---

**Hoxi Mobile** 专为独立开发者（一人公司）与精干研发团队设计。它不在 6 寸手机屏上塞满反人类的代码编辑器，而是精准解决你**离开电脑工位后最刚需的两件事**：
1. **远程指挥正在狂奔的 AI 编程会话**（原生兼容 **Hoxi 桌面端** `nodeType: 'hoxi'` 与 **GoPanel 远端 Code 工作区** `nodeType: 'gopanel'`）。
2. **随时随地运维线上 Linux 服务器**（作为 [**GoPanel (`hoxiai/gopanel`)**](https://github.com/hoxiai/gopanel) 的官方手机端控制面板）。

---

## 两大核心能力域

### 1. 手机里的 AI 开发指挥台（`ai_workspace` & `task_center`）
- **离席不卡审批（待我处理 Attention 中心）**：Agent 触发高危指令拦截（`rm`、`sudo`、`git reset --hard`）或抛出追问选择题时，手机端实时聚合提醒，一键「仅本次放行 / 始终允许 / 拒绝并附原因」，不用为了点一下确认跑回工位。
- **实时盯盘与中途纠偏（Steer / Stop）**：实时查看 SSE 流式输出、思考链与工具执行耗时；发现 Agent 思路跑偏，直接在手机上发指令中途纠偏（Steer）或立即止损。
- **躺着审完 Diff 再合并**：在手机上清晰审阅 Git Worktree 改动文件、Hunk Diff、8 维记忆沉淀与产物预览链接。

### 2. [GoPanel](https://github.com/hoxiai/gopanel) 服务器的掌上运维管家（`server`, `container`, `website`, `database`, `ssl`, `apps`）
当连接至 [GoPanel](https://github.com/hoxiai/gopanel) 服务器节点（支持扫码登录或账号密码接入 `/api/v1/*`）时，手机端即刻化身为随身服务器运维台：
- **主机监控（`server/`）**：实时查看 CPU、内存、磁盘水位与网络吞吐负载。
- **Docker 容器与应用商店（`container/` & `apps/`）**：查看容器状态与日志，一键启停、重启容器或管理应用部署。
- **网站与 SSL 证书（`website/` & `ssl/`）**：管理 Nginx 站点、域名绑定、反向代理及 SSL 证书签发与续期。
- **数据库运维（`database/`）**：随时巡检与管理 MySQL、PostgreSQL、Redis 实例状态。

---

## 技术栈与校验

- **技术栈**：Flutter + Riverpod + GoRouter + Dio
- **静态分析与测试**：
  ```bash
  flutter analyze
  flutter test
  ```
