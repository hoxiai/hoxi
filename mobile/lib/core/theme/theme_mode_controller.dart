import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'app_theme.dart';

/// 明暗模式偏好：跟随系统 / 常亮 / 常暗，持久化到本地。
final themeModeControllerProvider =
    NotifierProvider<ThemeModeController, ThemeMode>(ThemeModeController.new);

class ThemeModeController extends Notifier<ThemeMode> {
  static const String _prefKey = 'hoxi.theme_mode_v1';

  bool _chosenByUser = false;

  @override
  ThemeMode build() {
    Future.microtask(_load);
    return ThemeMode.light;
  }

  Future<void> _load() async {
    final prefs = await SharedPreferences.getInstance();
    final stored = prefs.getString(_prefKey);
    final mode = switch (stored) {
      'dark' => ThemeMode.dark,
      'system' => ThemeMode.system,
      'light' => ThemeMode.light,
      _ => ThemeMode.light,
    };
    // 用户已经手动选择过就不要被稍后返回的读取结果覆盖。
    if (ref.mounted && !_chosenByUser) state = mode;
  }

  Future<void> setMode(ThemeMode mode) async {
    _chosenByUser = true;
    state = mode;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_prefKey, switch (mode) {
      ThemeMode.dark => 'dark',
      ThemeMode.light => 'light',
      ThemeMode.system => 'system',
    });
  }

  Future<void> toggle() async {
    final next = switch (state) {
      ThemeMode.dark => ThemeMode.light,
      _ => ThemeMode.dark,
    };
    await setMode(next);
  }
}

/// 把 [ThemeMode] 解析成当前生效的亮度，并同步 [AppTheme] 语义色。
Brightness resolveBrightness(BuildContext context, ThemeMode mode) {
  return switch (mode) {
    ThemeMode.dark => Brightness.dark,
    ThemeMode.light => Brightness.light,
    ThemeMode.system => MediaQuery.platformBrightnessOf(context),
  };
}
