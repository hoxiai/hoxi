/// Hoxi 远程中继控制器默认配置（开源构建默认置空，支持编译期 `--dart-define` 注入自建中继）。
const String defaultHoxiRelayControllerUrl = String.fromEnvironment(
  'HOXI_RELAY_CONTROLLER_URL',
);

const List<String> defaultHoxiRelayHosts = <String>[];
