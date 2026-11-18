import 'dart:ui';

/// 品牌命名的唯一来源：中文「可喜」，英文「Hoxi」。
///
/// - 界面文案按系统语言取 [localizedName]，中文界面显示「可喜」，英文界面显示「Hoxi」；
/// - 代码标识、包名、域名与接口路径（`hoxi.ai`、`/api/hoxi/...`、`ai.hoxi.app`）一律使用
///   [nameEn]，不要翻译；
/// - 不要在业务代码里再硬编码品牌名，统一引用这里的常量或本地化文案 `appName`。
abstract final class Brand {
  static const String nameZh = 'Hoxi';
  static const String nameEn = 'Hoxi';

  /// 产品域名，代码与文档中的品牌标识沿用英文名。
  static const String domain = 'hoxi.ai';

  static const String taglineZh = '让复杂，变简单';
  static const String taglineEn = 'Make complex simple';

  /// 头像、图标等只放一个字时的缩写。
  static const String initialZh = '可';
  static const String initialEn = 'H';

  /// 手机端语音助手专属名称：中文「小乐」，英文「Xiaole」。
  static const String voiceAssistantNameZh = '小乐';
  static const String voiceAssistantNameEn = 'Xiaole';

  static bool isChinese(Locale locale) =>
      locale.languageCode.toLowerCase() == 'zh';

  /// 中文界面显示「可喜」，其他语言显示「Hoxi」。
  static String localizedName(Locale locale) =>
      isChinese(locale) ? nameZh : nameEn;

  /// 中文界面显示语音助手名「小乐」，其他语言显示「Xiaole」。
  static String localizedVoiceAssistantName(Locale locale) =>
      isChinese(locale) ? voiceAssistantNameZh : voiceAssistantNameEn;

  static String localizedTagline(Locale locale) =>
      isChinese(locale) ? taglineZh : taglineEn;

  static String localizedInitial(Locale locale) =>
      isChinese(locale) ? initialZh : initialEn;

  /// 节点名称、工作区描述等拼接场景：中文用「可喜 · xxx」，英文用「Hoxi · xxx」。
  static String withSuffix(Locale locale, String suffix) {
    final trimmed = suffix.trim();
    if (trimmed.isEmpty) return localizedName(locale);
    return '${localizedName(locale)} · $trimmed';
  }
}
