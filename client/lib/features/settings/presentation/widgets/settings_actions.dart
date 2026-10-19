import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../models/settings_info.dart';
import '../controllers/settings_controller.dart';
import '../../../../core/theme/app_theme.dart';

class SettingsActions {
  static Future<void> showAccountSecurity(
    BuildContext context,
    WidgetRef ref,
    SettingsInfo? info,
  ) async {
    await showModalBottomSheet<void>(
      context: context,
      showDragHandle: true,
      builder: (sheetContext) => SafeArea(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(20, 0, 20, 24),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text(
                '账号与安全',
                style: TextStyle(fontSize: 20, fontWeight: FontWeight.w800),
              ),
              const SizedBox(height: 16),
              _detailLine(
                Icons.person_outline_rounded,
                '账号',
                info?.nickname ?? '加载中',
              ),
              _detailLine(Icons.email_outlined, '邮箱', info?.email ?? '加载中'),
              _detailLine(
                Icons.verified_user_outlined,
                '二次验证',
                _enabledLabel(info?.mfaStatus),
              ),
              const SizedBox(height: 12),
              SizedBox(
                width: double.infinity,
                child: FilledButton.icon(
                  onPressed: () {
                    Navigator.of(sheetContext).pop();
                    showPasswordDialog(context, ref);
                  },
                  icon: const Icon(Icons.lock_reset_rounded),
                  label: const Text('修改登录密码'),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  static Widget _detailLine(IconData icon, String label, String value) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Row(
        children: [
          Icon(icon, size: 19, color: AppTheme.textSecondary),
          const SizedBox(width: 10),
          SizedBox(
            width: 58,
            child: Text(
              label,
              style: const TextStyle(color: AppTheme.textSecondary),
            ),
          ),
          Expanded(
            child: Text(
              value.isEmpty ? '未设置' : value,
              textAlign: TextAlign.right,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(fontWeight: FontWeight.w700),
            ),
          ),
        ],
      ),
    );
  }

  static String _enabledLabel(String? value) {
    final normalized = (value ?? '').toLowerCase();
    return normalized == '1' || normalized == 'true' || normalized == 'enable'
        ? '已开启'
        : '未开启';
  }

  static Future<void> showPasswordDialog(
    BuildContext context,
    WidgetRef ref,
  ) async {
    final formKey = GlobalKey<FormState>();
    final oldController = TextEditingController();
    final newController = TextEditingController();
    final confirmController = TextEditingController();
    try {
      await showDialog<void>(
        context: context,
        builder: (dialogContext) => AlertDialog(
          title: const Text('修改登录密码'),
          content: Form(
            key: formKey,
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                TextFormField(
                  controller: oldController,
                  obscureText: true,
                  decoration: const InputDecoration(labelText: '当前密码'),
                  validator: (value) =>
                      value == null || value.isEmpty ? '请输入当前密码' : null,
                ),
                TextFormField(
                  controller: newController,
                  obscureText: true,
                  decoration: const InputDecoration(labelText: '新密码'),
                  validator: (value) =>
                      value == null || value.length < 8 ? '新密码至少 8 位' : null,
                ),
                TextFormField(
                  controller: confirmController,
                  obscureText: true,
                  decoration: const InputDecoration(labelText: '确认新密码'),
                  validator: (value) =>
                      value != newController.text ? '两次密码不一致' : null,
                ),
              ],
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(dialogContext).pop(),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () async {
                if (!(formKey.currentState?.validate() ?? false)) return;
                try {
                  await ref
                      .read(settingsControllerProvider.notifier)
                      .updatePassword(
                        password: oldController.text,
                        newPassword: newController.text,
                      );
                  if (!dialogContext.mounted) return;
                  Navigator.of(dialogContext).pop();
                  ScaffoldMessenger.of(context).showSnackBar(
                    const SnackBar(
                      content: Text('登录密码已更新'),
                      backgroundColor: AppTheme.success,
                    ),
                  );
                } catch (_) {
                  if (!dialogContext.mounted) return;
                  ScaffoldMessenger.of(dialogContext).showSnackBar(
                    const SnackBar(
                      content: Text('密码更新失败'),
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
      oldController.dispose();
      newController.dispose();
      confirmController.dispose();
    }
  }

  static Future<void> showPanelDetails(
    BuildContext context,
    SettingsInfo? info,
  ) async {
    await showDialog<void>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: const Text('面板设置'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            _detailLine(
              Icons.settings_ethernet_rounded,
              '端口',
              info?.panelPort ?? '加载中',
            ),
            _detailLine(
              Icons.security_rounded,
              '安全入口',
              info?.securityEntrance ?? '未设置',
            ),
            _detailLine(Icons.folder_outlined, '数据目录', info?.baseDir ?? '加载中'),
            _detailLine(Icons.article_outlined, '日志目录', info?.logPath ?? '加载中'),
          ],
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(),
            child: const Text('关闭'),
          ),
        ],
      ),
    );
  }

  static Future<void> confirmPanelAction(
    BuildContext context,
    WidgetRef ref,
    String operation,
  ) async {
    final isServer = operation == 'server';
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: Text(isServer ? '重启服务器' : '重启面板'),
        content: Text(
          isServer ? '服务器重启后，所有服务会短暂中断，确定继续吗？' : '面板会短暂不可访问，确定继续吗？',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(false),
            child: const Text('取消'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(
              backgroundColor: isServer ? AppTheme.error : AppTheme.warning,
            ),
            onPressed: () => Navigator.of(dialogContext).pop(true),
            child: const Text('确认执行'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    try {
      await ref.read(settingsControllerProvider.notifier).restart(operation);
      if (!context.mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(isServer ? '服务器重启已发起' : '面板重启已发起'),
          backgroundColor: AppTheme.success,
        ),
      );
    } catch (_) {
      if (!context.mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('重启操作失败'),
          backgroundColor: AppTheme.error,
        ),
      );
    }
  }

  static Future<void> showCleanupPicker(
    BuildContext context,
    WidgetRef ref,
  ) async {
    final key = await showModalBottomSheet<String>(
      context: context,
      showDragHandle: true,
      builder: (sheetContext) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const ListTile(
              title: Text(
                '选择清理目录',
                style: TextStyle(fontWeight: FontWeight.w800),
              ),
            ),
            for (final item in const [
              ('log', '日志文件', Icons.article_outlined),
              ('tmp', '临时文件', Icons.timer_outlined),
              ('cache', '缓存文件', Icons.cached_rounded),
            ])
              ListTile(
                leading: Icon(item.$3, color: AppTheme.textSecondary),
                title: Text(item.$2),
                trailing: const Icon(Icons.chevron_right_rounded),
                onTap: () => Navigator.of(sheetContext).pop(item.$1),
              ),
            const SizedBox(height: 12),
          ],
        ),
      ),
    );
    if (key == null || !context.mounted) return;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: const Text('确认清理'),
        content: const Text('将删除该目录中的文件，目录本身不会被删除，确定继续吗？'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(dialogContext, false),
            child: const Text('取消'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: AppTheme.warning),
            onPressed: () => Navigator.pop(dialogContext, true),
            child: const Text('清理'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    try {
      final result = await ref
          .read(settingsControllerProvider.notifier)
          .clear(key);
      if (!context.mounted) return;
      final count = result['count'] ?? 0;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('清理完成，共处理 $count 个文件'),
          backgroundColor: AppTheme.success,
        ),
      );
    } catch (_) {
      if (!context.mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('清理操作失败'),
          backgroundColor: AppTheme.error,
        ),
      );
    }
  }
}
