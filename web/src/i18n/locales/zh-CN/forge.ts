// web/src/i18n/locales/zh-CN/forge.ts
export const forge = {
  open: "打开 Pull Request #{number}",
  tooltip: "{title}\n{state} · {checks} · {review}",
  states: {
    open: "开放",
    draft: "草稿",
    merged: "已合并",
    closed: "已关闭",
  },
  review: {
    approved: "已批准",
    changesRequested: "要求修改",
    pending: "等待评审",
    none: "暂无评审",
  },
  checks: {
    none: "无检查",
    passed: "{passed}/{total} 项检查通过",
    failed: "{count} 项检查失败",
    pending: "{count} 项检查进行中",
  },
};
