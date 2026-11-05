# hoxi 架构速查与自进化避坑指南 (ARCH.md)

## 1. 架构定位与事实源
- **工程类型**：`go-wails`
- **母体血缘**：独立自研工程（由 GoPanel 统一部署托管）
- **权威文档入口**：详见项目根目录 `AGENTS.md`（唯一权威架构事实源，必读！）
- **硬性交付门禁**：修改后必须运行 `./verify.sh` 且退出码为 0；上线统一走 `/Users/hugh/code/hoxi/.hq/ship.sh`。

## 2. 核心不可触碰红线
- 严格遵循根工作区 `/Users/hugh/code/hoxi/CLAUDE.md` 与本项目 `AGENTS.md` 的边界约束。
- 若本项目包含内嵌独立 Git 仓（如 `plugins/app/*`、`admin/`、`site/`），操作 Git 前必须用 `git -C <子目录> status` 核实归属。

## Pitfalls
<!-- Agent 在排障或开发中踩到的隐蔽陷阱，请以「一行一条、合并同类项」的格式追加在下方 -->
- [初始化基线] 切勿在未读完项目根 `AGENTS.md` 的情况下跨目录盲改或臆造接口契约。
