/// Hoxi 远程中继控制器默认配置（开源构建默认置空，支持编译期 `--dart-define` 注入自建中继）。
const String _envDefaultRelayUrl = String.fromEnvironment(
  'HOXI_DEFAULT_RELAY_URL',
);
const String _envLegacyRelayControllerUrl = String.fromEnvironment(
  'HOXI_RELAY_CONTROLLER_URL',
);

String defaultHoxiRelayControllerUrl = _envDefaultRelayUrl.isNotEmpty
    ? _envDefaultRelayUrl
    : _envLegacyRelayControllerUrl;

const String _envPairingRelayUrl = String.fromEnvironment(
  'HOXI_PAIRING_RELAY_URL',
);

String get defaultPairingRelayUrl => _envPairingRelayUrl.isNotEmpty
    ? _envPairingRelayUrl
    : defaultHoxiRelayControllerUrl;

String resolveInitialPairingRelayUrl(String saved) {
  final trimmed = saved.trim().replaceAll(RegExp(r'/+$'), '');
  return trimmed.isNotEmpty
      ? trimmed
      : defaultPairingRelayUrl.trim().replaceAll(RegExp(r'/+$'), '');
}

bool isRetiredHoxiRelayUrl(String rawUrl) => false;

String? migrateRetiredHoxiRelayUrl(String rawUrl) => null;

Set<String> defaultHoxiRelayHosts = const <String>{};
