# Hoxi 移动端指挥台 (`client/`)

[English](README.md) | [简体中文](README.zh-CN.md)

**Hoxi Mobile** 是配套 Hoxi 桌面端与 GoPanel（`gopanel_v2`）节点的 iOS / Android 移动开发指挥台。

它的定位**不是**在手机小屏幕上抠代码的 IDE，也不是卡顿的远程桌面，而是只解决一个核心痛点：**让长耗时 Agent 任务在后台狂奔时，你能放心离开工位。**

---

## 解决的核心痛点

1. **离开电脑也不卡审批（待我处理 Attention 中心）**：Agent 遇到高危命令（`rm`、`sudo`、`git reset --hard`）或抛出追问选择题时，手机端实时聚合提醒，一键「仅本次放行 / 始终允许 / 拒绝并附原因」，不用为了点一下回车跑回电脑前。
2. **随时随地纠偏止损（Steer / Stop）**：实时查看对话流、思考链与工具执行耗时；发现 Agent 思路跑偏，直接在手机上发指令中途纠偏（Steer）或中止任务。
3. **躺着审完 Diff 再合并**：在手机上清晰审阅 Git Worktree 改动文件、Hunk Diff、8 维记忆沉淀与产物预览链接。
4. **Hoxi + GoPanel 双节点原生兼容**：自动探测节点类型（`nodeType: 'hoxi' | 'gopanel'`），既能免密/扫码直连本机 **Hoxi 桌面工作台**（`/api/hoxi/*`），也能连接远程 **GoPanel 服务器节点**（`/api/v1/*` 与 `/api/code/*`），统一管理远程 AI 编码会话与容器/站点运维。

---

## 技术栈与校验

- **技术栈**：Flutter + Riverpod + GoRouter + Dio
- **静态分析与测试**：
  ```bash
  flutter analyze
  flutter test
  ```
