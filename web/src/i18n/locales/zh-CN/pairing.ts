// web/src/i18n/locales/zh-CN/pairing.ts
export const pairing = {
  title: "与手机 App 配对",
  tip: "生成一个二维码，Hoxi App 扫码后可在局域网内直连这台电脑；两端需处于同一 Wi-Fi。二维码内含本机配对令牌，请在可信环境中展示",
  refreshAction: "刷新",
  loading: "正在生成配对二维码",
  scanHint: "打开 Hoxi App，在「关联设备」中选择「可喜AI电脑」，再扫描此码",
  addressTip: "局域网地址：{url}。地址与配对令牌保存在本机，请仅在与手机同一可信网络下展示此码",
  noLanHint: "未找到局域网地址，请先把本机接入 Wi-Fi 或有线网络，再刷新",
  qrAlt: "配对二维码",
  rotateAction: "重新生成令牌",
  rotateConfirm: "确定重新生成配对令牌？当前二维码与已扫码的手机会失去局域网访问，需重新扫描新码",
  rotateError: "无法重新生成配对令牌",
  loadError: "无法生成配对二维码",
};
