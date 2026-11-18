import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../core/navigation/navigation_guard.dart';
import '../../core/storage/server_connection.dart';
import '../models/task_entity.dart';

typedef GopanelInstalledCredentials = ({
  String host,
  String panelUrl,
  String entrance,
  String adminEmail,
  String adminPassword,
});

typedef ConnectionSyncToggleBuilder =
    Widget Function({
      String? connectionId,
      required bool value,
      required ValueChanged<bool> onChanged,
    });

typedef ConnectionSyncSaveHook =
    Future<void> Function({
      required WidgetRef ref,
      required BuildContext context,
      required ServerConnection? activeServer,
      required bool syncToAccount,
      required String? syncScope,
      required bool showFailureSnackBar,
    });

/// 跨模块页面跳转与可选商业插槽注册表：让底层模块（如 `auth`、`ai_workspace`）无需反向导入
/// 上层页面或商业同步模块即可安全发起跳转或挂载可选插槽；开源薄壳未注册时自动静默降级。
abstract final class FeatureRouteRegistry {
  static const String workbenchPath = '/workbench';
  static const String taskDetailPath = '/tasks/detail';

  static Widget Function(TaskEntity task)? taskDetailBuilder;
  static Future<void> Function(BuildContext context)? workbenchOpener;
  static Future<GopanelInstalledCredentials?> Function(BuildContext context)?
  gopanelInstallerOpener;
  static Future<void> Function(BuildContext context)? portalSiteBuilderOpener;
  static Future<void> Function(BuildContext context)? storeSiteBuilderOpener;
  static Future<void> Function(BuildContext context)? accountSignInOpener;
  static Future<void> Function(BuildContext context)? connectionSyncOpener;
  static Future<void> Function(BuildContext context, ServerConnection server)?
  connectionAuthorizationOpener;

  /// 可选的连接云同步开关插槽（商业版注入，开源薄壳为空）
  static ConnectionSyncToggleBuilder? connectionSyncToggleBuilder;

  /// 可选的连接保存后云同步回调（商业版注入，开源薄壳为空）
  static ConnectionSyncSaveHook? onConnectionSavedSyncHook;

  /// 可选的中继节点连接后远程推送登记回调（商业版注入，开源薄壳为空）
  static void Function(WidgetRef ref)? onRelayConnectedPushHook;

  static Widget buildConnectionSyncToggle({
    String? connectionId,
    required bool value,
    required ValueChanged<bool> onChanged,
  }) {
    final builder = connectionSyncToggleBuilder;
    if (builder == null) return const SizedBox.shrink();
    return builder(
      connectionId: connectionId,
      value: value,
      onChanged: onChanged,
    );
  }

  static Future<void> openTaskDetail(
    BuildContext context,
    TaskEntity task,
  ) async {
    final builder = taskDetailBuilder;
    if (builder != null) {
      await AppNavigationGuard.push<void>(
        context,
        MaterialPageRoute<void>(builder: (_) => builder(task)),
      );
      return;
    }
    if (GoRouter.maybeOf(context) != null) {
      await AppNavigationGuard.pushNamedLocation<void>(
        context,
        taskDetailPath,
        extra: task,
      );
    }
  }

  static Future<void> openWorkbench(BuildContext context) async {
    final opener = workbenchOpener;
    if (opener != null) {
      await opener(context);
      return;
    }
    if (GoRouter.maybeOf(context) != null) {
      await AppNavigationGuard.pushNamedLocation<void>(context, workbenchPath);
    }
  }

  static Future<GopanelInstalledCredentials?> openGopanelInstaller(
    BuildContext context,
  ) async {
    final opener = gopanelInstallerOpener;
    if (opener != null) {
      return opener(context);
    }
    return null;
  }

  static Future<void> openPortalSiteBuilder(BuildContext context) async {
    final opener = portalSiteBuilderOpener;
    if (opener != null) {
      await opener(context);
    }
  }

  static Future<void> openStoreSiteBuilder(BuildContext context) async {
    final opener = storeSiteBuilderOpener;
    if (opener != null) {
      await opener(context);
    }
  }
}
