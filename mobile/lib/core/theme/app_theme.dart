import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

/// 全局主题配置
/// 遵循 PROMPT.md 视觉约束：
/// - GoPanel 主色调为蓝色系
/// - 简约、清晰、扁平、低噪声
/// - 白底卡片、浅蓝强调、柔和阴影、弱边框
///
/// 同时提供亮色与暗色调色板。业务代码统一读取 [AppTheme] 上的语义色
/// （如 `AppTheme.textPrimary`），由当前生效的调色板决定实际取值，
/// 这样切换明暗模式时不需要在每个页面重复判断 `isDark`。
class AppTheme {
  static const Color primaryBlue = Color(0xFF2563EB); // 类似 Tailwind blue-600

  // 状态色
  static const Color success = Color(0xFF10B981);
  static const Color warning = Color(0xFFF59E0B);
  static const Color error = Color(0xFFF43F5E); // rose-500

  static const _Palette _lightPalette = _Palette(
    accent: primaryBlue,
    accentSoft: Color(0xFFEFF6FF),
    accentBorder: Color(0xFFDBEAFE),
    accentText: Color(0xFF1D4ED8),
    textPrimary: Color(0xFF0F172A), // slate-900
    textSecondary: Color(0xFF64748B), // slate-500
    textLight: Color(0xFF94A3B8), // slate-400
    background: Color(0xFFF4F7FB),
    surface: Colors.white,
    surfaceMuted: Color(0xFFF8FAFC),
    border: Color(0xFFE2E8F0), // slate-200
    successSoft: Color(0xFFECFDF5),
    successText: Color(0xFF047857),
    warningSoft: Color(0xFFFFF7ED),
    warningBorder: Color(0xFFFDE68A),
    warningText: Color(0xFFB45309),
    errorSoft: Color(0xFFFFF1F2),
    brightness: Brightness.light,
  );

  static const _Palette _darkPalette = _Palette(
    accent: Color(0xFF3B82F6),
    accentSoft: Color(0xFF17233A),
    accentBorder: Color(0xFF1E3A5F),
    accentText: Color(0xFF93C5FD),
    textPrimary: Color(0xFFE2E8F0),
    textSecondary: Color(0xFF94A3B8),
    textLight: Color(0xFF64748B),
    background: Color(0xFF0B1220),
    surface: Color(0xFF141C2B),
    surfaceMuted: Color(0xFF1B2436),
    border: Color(0xFF243044),
    successSoft: Color(0xFF10281E),
    successText: Color(0xFF34D399),
    warningSoft: Color(0xFF2A2317),
    warningBorder: Color(0xFF523C18),
    warningText: Color(0xFFFBBF24),
    errorSoft: Color(0xFF2A1720),
    brightness: Brightness.dark,
  );

  static _Palette _active = _lightPalette;

  /// 当前生效的明暗模式。页面重建前由根组件同步。
  static Brightness get brightness => _active.brightness;
  static bool get isDark => _active.brightness == Brightness.dark;

  static void applyBrightness(Brightness brightness) {
    _active = brightness == Brightness.dark ? _darkPalette : _lightPalette;
  }

  static Color get primary => _active.accent;
  static Color get primaryBlueLight => _active.accentSoft;
  static Color get primaryBlueBorder => _active.accentBorder;
  static Color get primaryBlueText => _active.accentText;

  static Color get textPrimary => _active.textPrimary;
  static Color get textSecondary => _active.textSecondary;
  static Color get textLight => _active.textLight;

  static Color get background => _active.background;
  static Color get surface => _active.surface;
  static Color get surfaceMuted => _active.surfaceMuted;
  static Color get border => _active.border;

  /// 状态色的浅色底与对比文字色（成功 / 警告 / 错误），随明暗模式自动调整。
  static Color get successSoft => _active.successSoft;
  static Color get successText => _active.successText;
  static Color get warningSoft => _active.warningSoft;
  static Color get warningBorder => _active.warningBorder;
  static Color get warningText => _active.warningText;
  static Color get errorSoft => _active.errorSoft;

  /// 全局状态栏与系统导航栏样式（供无 AppBar 的页面统一同步）。
  static SystemUiOverlayStyle get systemUiOverlayStyle =>
      _systemOverlayFor(_active);

  static SystemUiOverlayStyle _systemOverlayFor(_Palette palette) {
    final dark = palette.brightness == Brightness.dark;
    return (dark ? SystemUiOverlayStyle.light : SystemUiOverlayStyle.dark)
        .copyWith(
          statusBarColor: Colors.transparent,
          statusBarIconBrightness: dark ? Brightness.light : Brightness.dark,
          statusBarBrightness: dark ? Brightness.dark : Brightness.light,
          systemNavigationBarColor: palette.surface,
          systemNavigationBarIconBrightness: dark
              ? Brightness.light
              : Brightness.dark,
        );
  }

  /// 沟通首页的天空渐变：顶部淡蓝，向下融入页面底色。
  /// 只用于首页背景，不铺到其他页，避免整站发蓝。
  static LinearGradient get homeSkyGradient => LinearGradient(
    begin: Alignment.topCenter,
    end: Alignment.bottomCenter,
    colors: isDark
        ? const [Color(0xFF12203A), Color(0xFF0B1220)]
        : const [Color(0xFFDFEDFF), Color(0xFFF4F7FB)],
    stops: const [0.0, 0.62],
  );

  /// 亮色主题
  static ThemeData get lightTheme => _build(_lightPalette);

  /// 暗色主题
  static ThemeData get darkTheme => _build(_darkPalette);

  static ThemeData _build(_Palette palette) {
    final isDark = palette.brightness == Brightness.dark;
    return ThemeData(
      useMaterial3: true,
      brightness: palette.brightness,
      colorScheme: ColorScheme.fromSeed(
        seedColor: primaryBlue,
        brightness: palette.brightness,
        primary: palette.accent,
        surface: palette.surface,
        onSurface: palette.textPrimary,
        error: error,
      ),
      scaffoldBackgroundColor: palette.background,

      // AppBar 样式（扁平、克制）
      appBarTheme: AppBarTheme(
        backgroundColor: palette.background,
        foregroundColor: palette.textPrimary,
        elevation: 0,
        scrolledUnderElevation: 0,
        centerTitle: false,
        toolbarHeight: 68,
        titleSpacing: 20,
        iconTheme: IconThemeData(color: palette.textPrimary),
        titleTextStyle: TextStyle(
          color: palette.textPrimary,
          fontSize: 20,
          fontWeight: FontWeight.w700,
        ),
        systemOverlayStyle: _systemOverlayFor(palette),
      ),

      // 卡片样式（无边框、柔和阴影、圆角）：降噪，靠留白与阴影区分层次
      cardTheme: CardThemeData(
        color: palette.surface,
        elevation: 0,
        shadowColor: (isDark ? Colors.black : palette.textPrimary).withValues(
          alpha: isDark ? 0.4 : 0.06,
        ),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(24),
        ),
        margin: EdgeInsets.zero,
      ),

      // 按钮样式
      elevatedButtonTheme: ElevatedButtonThemeData(
        style: ElevatedButton.styleFrom(
          backgroundColor: palette.accent,
          foregroundColor: Colors.white,
          elevation: 0,
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(16),
          ),
          padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 16),
          textStyle: const TextStyle(fontSize: 16, fontWeight: FontWeight.w600),
        ),
      ),

      textButtonTheme: TextButtonThemeData(
        style: TextButton.styleFrom(
          foregroundColor: palette.accent,
          textStyle: const TextStyle(fontWeight: FontWeight.w600),
        ),
      ),

      // 输入框样式
      inputDecorationTheme: InputDecorationTheme(
        filled: true,
        fillColor: palette.surface,
        contentPadding: const EdgeInsets.symmetric(
          horizontal: 16,
          vertical: 16,
        ),
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(16),
          borderSide: BorderSide(color: palette.border),
        ),
        enabledBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(16),
          borderSide: BorderSide(color: palette.border),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(16),
          borderSide: BorderSide(color: palette.accent, width: 2),
        ),
        errorBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(16),
          borderSide: const BorderSide(color: error),
        ),
        hintStyle: TextStyle(color: palette.textLight),
      ),
      dividerColor: palette.border,
      dividerTheme: DividerThemeData(
        color: palette.border.withValues(alpha: 0.75),
        space: 1,
      ),
      bottomSheetTheme: BottomSheetThemeData(
        backgroundColor: palette.surface,
        surfaceTintColor: Colors.transparent,
        modalBackgroundColor: palette.surface,
        dragHandleColor: palette.border,
        shape: const RoundedRectangleBorder(
          borderRadius: BorderRadius.vertical(top: Radius.circular(28)),
        ),
      ),
      dialogTheme: DialogThemeData(
        backgroundColor: palette.surface,
        surfaceTintColor: Colors.transparent,
      ),
      popupMenuTheme: PopupMenuThemeData(
        color: palette.surface,
        surfaceTintColor: Colors.transparent,
        textStyle: TextStyle(color: palette.textPrimary),
      ),
      snackBarTheme: SnackBarThemeData(
        backgroundColor: isDark ? palette.surfaceMuted : const Color(0xFF0F172A),
        contentTextStyle: TextStyle(
          color: isDark ? palette.textPrimary : Colors.white,
          fontSize: 13.5,
          fontWeight: FontWeight.w500,
        ),
      ),
      floatingActionButtonTheme: FloatingActionButtonThemeData(
        backgroundColor: palette.accent,
        foregroundColor: Colors.white,
        elevation: 4,
        shape: const StadiumBorder(),
      ),
      listTileTheme: ListTileThemeData(
        textColor: palette.textPrimary,
        iconColor: palette.textSecondary,
      ),
      iconTheme: IconThemeData(color: palette.textPrimary),
      textTheme: _buildTextTheme(palette),
    );
  }

  static TextTheme _buildTextTheme(_Palette palette) {
    final typography = Typography.material2021(
      platform: TargetPlatform.iOS,
      colorScheme: ColorScheme.fromSeed(
        seedColor: primaryBlue,
        brightness: palette.brightness,
      ),
    );
    final base = palette.brightness == Brightness.dark
        ? typography.white
        : typography.black;
    return base.apply(
      bodyColor: palette.textPrimary,
      displayColor: palette.textPrimary,
    );
  }
}

class _Palette {
  final Color accent;
  final Color accentSoft;
  final Color accentBorder;
  final Color accentText;
  final Color textPrimary;
  final Color textSecondary;
  final Color textLight;
  final Color background;
  final Color surface;
  final Color surfaceMuted;
  final Color border;
  final Color successSoft;
  final Color successText;
  final Color warningSoft;
  final Color warningBorder;
  final Color warningText;
  final Color errorSoft;
  final Brightness brightness;

  const _Palette({
    required this.accent,
    required this.accentSoft,
    required this.accentBorder,
    required this.accentText,
    required this.textPrimary,
    required this.textSecondary,
    required this.textLight,
    required this.background,
    required this.surface,
    required this.surfaceMuted,
    required this.border,
    required this.successSoft,
    required this.successText,
    required this.warningSoft,
    required this.warningBorder,
    required this.warningText,
    required this.errorSoft,
    required this.brightness,
  });
}
