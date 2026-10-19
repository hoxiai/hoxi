import 'dart:ui';

import 'package:flutter/widgets.dart';

abstract final class AuthText {
  static const _values = <String, Map<String, String>>{
    'zh': {
      'entranceLabel': '专属安全入口（可选）',
      'entranceHint': '例如：your-entrance',
      'entranceHelper': '也可以直接粘贴包含入口路径的完整面板地址',
      'entranceInvalid': '只能包含字母、数字、下划线和短横线',
      'adminEmail': '管理员邮箱',
      'adminEmailHint': '请输入管理员邮箱',
      'serverEntranceConfigured': '{url} · 已配置安全入口',
      'serverAddressHttpRequired': '服务器地址必须使用 http 或 https',
      'serverAddressInvalid': '服务器地址格式不正确',
      'serverEntranceMismatch': '服务器地址中的入口与专属安全入口不一致',
      'operationTimeout': '操作超时，请检查网络或系统钥匙串权限',
    },
    'en': {
      'entranceLabel': 'Security entrance (optional)',
      'entranceHint': 'For example: your-entrance',
      'entranceHelper': 'You can also paste the full panel entrance URL',
      'entranceInvalid': 'Use only letters, numbers, underscores, and hyphens',
      'adminEmail': 'Administrator email',
      'adminEmailHint': 'Enter the administrator email',
      'serverEntranceConfigured': '{url} · Security entrance configured',
      'serverAddressHttpRequired': 'The server URL must use HTTP or HTTPS',
      'serverAddressInvalid': 'The server URL is invalid',
      'serverEntranceMismatch':
          'The URL path and security entrance do not match',
      'operationTimeout':
          'The operation timed out. Check the network or keychain access.',
    },
  };

  static String t(BuildContext context, String key) {
    final language = View.of(context).platformDispatcher.locale.languageCode;
    return _value(language, key);
  }

  static String current(String key) {
    return _value(PlatformDispatcher.instance.locale.languageCode, key);
  }

  static String format(BuildContext context, String key, String url) {
    return t(context, key).replaceAll('{url}', url);
  }

  static String _value(String language, String key) {
    return _values[language]?[key] ?? _values['en']![key] ?? key;
  }
}
