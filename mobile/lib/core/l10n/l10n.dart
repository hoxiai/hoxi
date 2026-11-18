import 'package:flutter/widgets.dart';

import '../../l10n/gen/app_localizations.dart';

export '../../l10n/gen/app_localizations.dart';

/// 统一入口：`context.l10n.chatSend`。
///
/// 页面不要直接 `AppLocalizations.of(context)`，也不要自建 `*Text` 类拼文案；
/// 新增文案一律进 ARB，中文与英文同步维护。
extension AppLocalizationsX on BuildContext {
  AppLocalizations get l10n => AppLocalizations.of(this);

  /// 当前界面语言是否为中文。
  bool get isChineseLocale => Localizations.localeOf(this).languageCode == 'zh';

  /// 当前界面语言的品牌名：中文「可喜」，其他语言「Hoxi」。
  String get brandName => l10n.appName;
}
