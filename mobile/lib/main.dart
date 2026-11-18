import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'app/oss/oss_workspace_shell.dart';
import 'core/l10n/l10n.dart';
import 'core/navigation/navigation_guard.dart';
import 'core/storage/storage_service.dart';
import 'core/theme/app_theme.dart';
import 'core/theme/theme_mode_controller.dart';
import 'features/auth/models/node_link_kind.dart';

/// Hoxi Mobile 开源版轻量入口（仅包含 Hoxi 桌面端 / GoPanel 节点连接与 AI 编码工作台）。
void main() {
  WidgetsFlutterBinding.ensureInitialized();
  OssAppRouter.ensureRoutesRegistered();

  final bootFuture = _initializeOssServices();
  runApp(
    ProviderScope(
      overrides: [
        supportedNodeLinkKindsProvider.overrideWithValue(const [
          NodeLinkKind.hoxi,
          NodeLinkKind.gopanel,
        ]),
      ],
      child: HoxiOssApp(bootFuture: bootFuture),
    ),
  );
}

Future<void> _initializeOssServices() async {
  try {
    await StorageService.init().timeout(const Duration(seconds: 4));
  } catch (_) {
    // 存储初始化超时或异常时不阻塞首屏渲染。
  }
}

class HoxiOssApp extends ConsumerWidget {
  final Future<void>? bootFuture;

  const HoxiOssApp({super.key, this.bootFuture});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final themeMode = ref.watch(themeModeControllerProvider);
    return NavigationPointerGate(
      child: MaterialApp.router(
        title: 'Hoxi',
        debugShowCheckedModeBanner: false,
        theme: AppTheme.lightTheme,
        darkTheme: AppTheme.darkTheme,
        themeMode: themeMode,
        themeAnimationDuration: Duration.zero,
        localizationsDelegates: AppLocalizations.localizationsDelegates,
        supportedLocales: AppLocalizations.supportedLocales,
        routerConfig: OssAppRouter.router,
        builder: (context, child) => OssThemeSync(
          themeMode: themeMode,
          child: bootFuture == null
              ? (child ?? const SizedBox.shrink())
              : FutureBuilder<void>(
                  future: bootFuture,
                  builder: (context, snapshot) {
                    if (snapshot.connectionState != ConnectionState.done) {
                      return const Scaffold(
                        body: Center(child: CircularProgressIndicator()),
                      );
                    }
                    return child ?? const SizedBox.shrink();
                  },
                ),
        ),
      ),
    );
  }
}

/// 同步开源壳层 [AppTheme] 明暗语义色与状态栏样式（不依赖商业站点会话客户端）。
class OssThemeSync extends StatefulWidget {
  final ThemeMode themeMode;
  final Widget child;

  const OssThemeSync({super.key, required this.themeMode, required this.child});

  @override
  State<OssThemeSync> createState() => _OssThemeSyncState();
}

class _OssThemeSyncState extends State<OssThemeSync> {
  Brightness? _lastBrightness;

  void _rebuildDescendants(Element root) {
    void visit(Element child) {
      child.markNeedsBuild();
      child.visitChildren(visit);
    }

    root.visitChildren(visit);
  }

  @override
  Widget build(BuildContext context) {
    final brightness = resolveBrightness(context, widget.themeMode);
    final changed = _lastBrightness != null && _lastBrightness != brightness;
    _lastBrightness = brightness;
    AppTheme.applyBrightness(brightness);
    final overlayStyle = AppTheme.systemUiOverlayStyle;
    SystemChrome.setSystemUIOverlayStyle(overlayStyle);
    if (changed && context is Element) {
      _rebuildDescendants(context);
    }
    return AnnotatedRegion<SystemUiOverlayStyle>(
      value: overlayStyle,
      child: widget.child,
    );
  }
}
