import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../core/storage/storage_service.dart';
import '../../../../core/theme/app_theme.dart';
import '../../../../shared/models/server_connection.dart';
import '../controllers/settings_controller.dart';
import '../../models/settings_info.dart';
import 'upgrade_progress_sheet.dart';

class PanelSettingsActions {
  static Future<void> showPanelSettings(
    BuildContext context,
    WidgetRef ref,
    SettingsInfo? info,
  ) async {
    final portController = TextEditingController(
      text: _portValue(info?.panelPort),
    );
    final entranceController = TextEditingController(
      text: info?.securityEntrance ?? '',
    );
    final formKey = GlobalKey<FormState>();
    try {
      await showDialog<void>(
        context: context,
        builder: (dialogContext) => AlertDialog(
          title: const Text('面板设置'),
          content: Form(
            key: formKey,
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                TextFormField(
                  controller: portController,
                  keyboardType: TextInputType.number,
                  decoration: const InputDecoration(
                    labelText: '面板端口',
                    hintText: '5470',
                  ),
                  validator: (value) {
                    final port = int.tryParse(value ?? '');
                    return port == null || port < 1 || port > 65535
                        ? '请输入 1-65535 的端口'
                        : null;
                  },
                ),
                TextFormField(
                  controller: entranceController,
                  decoration: const InputDecoration(
                    labelText: '安全入口',
                    hintText: '留空表示当前未设置',
                  ),
                  validator: (value) {
                    if ((value ?? '').trim().isEmpty) return '安全入口不能为空';
                    if (!isValidServerEntrance(value ?? '')) {
                      return '只能使用字母、数字、下划线或短横线';
                    }
                    return null;
                  },
                ),
              ],
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(dialogContext),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () async {
                if (!(formKey.currentState?.validate() ?? false)) return;
                final oldPort = _portValue(info?.panelPort);
                final newPort = int.parse(portController.text);
                final oldEntrance = (info?.securityEntrance ?? '').trim();
                final newEntrance = entranceController.text.trim();
                try {
                  final controller = ref.read(
                    settingsControllerProvider.notifier,
                  );
                  if (newEntrance != oldEntrance) {
                    await controller.updateEntrance(newEntrance);
                    final currentUrl = StorageService.activeServerUrl;
                    final token = StorageService.activeServerToken ?? '';
                    if (currentUrl != null) {
                      final server = normalizeServerAddress(
                        currentUrl,
                        newEntrance,
                      );
                      final saved = StorageService.getServerList()
                          .where((item) => item.url == currentUrl)
                          .firstOrNull;
                      await StorageService.replaceServerConnection(
                        currentUrl,
                        ServerConnection(
                          id: DateTime.now().millisecondsSinceEpoch.toString(),
                          name: saved?.name ?? '当前服务器',
                          url: server.baseUrl,
                          entrance: server.entrance,
                          token: token,
                          lastConnectedAt: DateTime.now(),
                        ),
                      );
                      await StorageService.setActiveServerUrl(server.baseUrl);
                      await StorageService.setActiveServerEntrance(
                        server.entrance,
                      );
                    }
                  }
                  if (newPort.toString() != oldPort) {
                    await controller.updatePort(newPort);
                    final currentUrl = StorageService.activeServerUrl;
                    if (currentUrl != null) {
                      final nextUrl = replaceServerUrlPort(currentUrl, newPort);
                      final saved = StorageService.getServerList()
                          .where((item) => item.url == currentUrl)
                          .firstOrNull;
                      await StorageService.replaceServerConnection(
                        currentUrl,
                        ServerConnection(
                          id:
                              saved?.id ??
                              DateTime.now().millisecondsSinceEpoch.toString(),
                          name: saved?.name ?? '当前服务器',
                          url: nextUrl,
                          entrance: newEntrance,
                          token: StorageService.activeServerToken ?? '',
                          lastConnectedAt: DateTime.now(),
                        ),
                      );
                      await StorageService.setActiveServerUrl(nextUrl);
                    }
                  }
                  if (!dialogContext.mounted) return;
                  Navigator.pop(dialogContext);
                  if (context.mounted) {
                    ScaffoldMessenger.of(context).showSnackBar(
                      const SnackBar(
                        content: Text('面板设置已保存'),
                        backgroundColor: AppTheme.success,
                      ),
                    );
                  }
                } catch (_) {
                  if (!dialogContext.mounted) return;
                  ScaffoldMessenger.of(dialogContext).showSnackBar(
                    const SnackBar(
                      content: Text('面板设置保存失败'),
                      backgroundColor: AppTheme.error,
                    ),
                  );
                }
              },
              child: const Text('保存'),
            ),
          ],
        ),
      );
    } finally {
      portController.dispose();
      entranceController.dispose();
    }
  }

  static Future<void> showVersionCenter(
    BuildContext context,
    WidgetRef ref, {
    UpgradeRecovery? recovery,
  }) async {
    if (recovery != null && recovery.logName.isNotEmpty) {
      await showModalBottomSheet<void>(
        context: context,
        isScrollControlled: true,
        showDragHandle: true,
        builder: (_) => UpgradeProgressSheet(
          logName: recovery.logName,
          ref: ref,
          currentVersion: recovery.currentVersion,
          targetVersion: recovery.targetVersion,
        ),
      );
      return;
    }
    await ref.read(settingsControllerProvider.notifier).loadVersion();
    await ref.read(settingsControllerProvider.notifier).checkUpdate();
    if (!context.mounted) return;
    final state = ref.read(settingsControllerProvider);
    await showDialog<void>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: const Text('版本更新'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            _versionLine('当前版本', state.version?.versionName),
            _versionLine('构建时间', state.version?.buildTime),
            const SizedBox(height: 12),
            const Text('更新状态', style: TextStyle(fontWeight: FontWeight.w800)),
            const SizedBox(height: 6),
            Text(
              state.update == null
                  ? '暂时无法获取更新状态'
                  : _updateSummary(state.update!),
            ),
            if (state.update?.description.isNotEmpty == true) ...[
              const SizedBox(height: 10),
              Text(
                state.update!.description,
                style: const TextStyle(color: AppTheme.textSecondary),
              ),
            ],
          ],
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(dialogContext),
            child: const Text('关闭'),
          ),
          FilledButton(
            onPressed: () async {
              final update = state.update;
              final version = state.version;
              if (update?.needUpdate != true || version == null) {
                await ref
                    .read(settingsControllerProvider.notifier)
                    .checkUpdate();
                if (!dialogContext.mounted) return;
                Navigator.pop(dialogContext);
                showVersionCenter(context, ref);
                return;
              }
              final targetVersion = update?.latestVersionName ?? '';
              final confirmed = await showDialog<bool>(
                context: dialogContext,
                builder: (confirmContext) => AlertDialog(
                  title: const Text('确认更新'),
                  content: Text(
                    '将 GoPanel 从 ${version.versionName} 更新到 $targetVersion，现在开始吗？',
                  ),
                  actions: [
                    TextButton(
                      onPressed: () => Navigator.pop(confirmContext, false),
                      child: const Text('取消'),
                    ),
                    FilledButton(
                      onPressed: () => Navigator.pop(confirmContext, true),
                      child: const Text('立即更新'),
                    ),
                  ],
                ),
              );
              if (confirmed != true || !dialogContext.mounted) return;
              Navigator.pop(dialogContext);
              try {
                final log = await ref
                    .read(settingsControllerProvider.notifier)
                    .startUpgrade(
                      currentVersion: version.versionName,
                      targetVersion: targetVersion,
                    );
                if (context.mounted) {
                  await showModalBottomSheet<void>(
                    context: context,
                    isScrollControlled: true,
                    showDragHandle: true,
                    builder: (_) => UpgradeProgressSheet(
                      logName: log,
                      ref: ref,
                      currentVersion: version.versionName,
                      targetVersion: targetVersion,
                    ),
                  );
                }
              } catch (_) {
                if (!context.mounted) return;
                ScaffoldMessenger.of(context).showSnackBar(
                  const SnackBar(
                    content: Text('升级任务启动失败'),
                    backgroundColor: AppTheme.error,
                  ),
                );
              }
            },
            child: Text(state.update?.needUpdate == true ? '立即更新' : '重新检查'),
          ),
        ],
      ),
    );
  }

  static String _portValue(String? value) {
    final raw = value ?? '';
    final port = raw.replaceFirst(':', '');
    return port.isEmpty ? '5470' : port;
  }

  static Widget _versionLine(String label, String? value) => Padding(
    padding: const EdgeInsets.only(bottom: 8),
    child: Row(
      children: [
        Text(label, style: const TextStyle(color: AppTheme.textSecondary)),
        const Spacer(),
        Text(
          value?.isNotEmpty == true ? value! : '-',
          style: const TextStyle(fontWeight: FontWeight.w700),
        ),
      ],
    ),
  );

  static String _updateSummary(SettingsVersion info) {
    if (!info.needUpdate) return '当前已经是最新版本';
    return info.latestVersionName.isEmpty
        ? '发现新版本'
        : '发现新版本 ${info.latestVersionName}';
  }
}
