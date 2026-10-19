import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../../core/storage/storage_service.dart';
import '../../../../core/theme/app_theme.dart';
import '../../../../shared/models/server_connection.dart';
import '../../../auth/presentation/controllers/auth_controller.dart';
import '../../../website/presentation/screens/dns_account_list_screen.dart';
import '../controllers/settings_controller.dart';
import '../widgets/settings_actions.dart';
import '../widgets/panel_settings_actions.dart';

class SettingsScreen extends ConsumerWidget {
  const SettingsScreen({super.key});

  Future<void> _handleDisconnect(BuildContext context, WidgetRef ref) async {
    await ref.read(authControllerProvider.notifier).logout();
    if (context.mounted) context.go('/');
  }

  Future<void> _handleDeleteServer(BuildContext context, WidgetRef ref) async {
    final currentUrl = StorageService.activeServerUrl;
    if (currentUrl == null) return;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('移除服务器'),
        content: const Text('确定要从本地列表中移除当前服务器吗？移除后需重新登录或扫码。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('取消'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: AppTheme.error),
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('移除'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    await StorageService.removeServerConnection(currentUrl);
    await ref.read(authControllerProvider.notifier).logout();
    if (context.mounted) context.go('/');
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(settingsControllerProvider);
    final currentUrl = StorageService.activeServerUrl ?? '未知地址';
    ref.listen(settingsControllerProvider, (previous, next) {
      if (next.errorMessage != null &&
          next.errorMessage != previous?.errorMessage &&
          context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(next.errorMessage!),
            backgroundColor: AppTheme.error,
          ),
        );
      }
    });
    return Scaffold(
      body: SafeArea(
        bottom: false,
        child: ListView(
          padding: const EdgeInsets.fromLTRB(20, 22, 20, 28),
          children: [
            _pageHeader(),
            const SizedBox(height: 20),
            _serverCard(
              currentUrl,
              onTap: () => _showServerPicker(context, ref),
            ),
            const SizedBox(height: 28),
            _sectionLabel('系统设置', '与 PC 端「系统设置」保持一致'),
            const SizedBox(height: 10),
            _settingsCard([
              _SettingEntry(
                Icons.tune_rounded,
                AppTheme.primaryBlue,
                '面板设置',
                '端口、安全入口、目录与基础运行配置',
                status: '可用',
                onTap: () => PanelSettingsActions.showPanelSettings(
                  context,
                  ref,
                  state.info,
                ),
              ),
              _SettingEntry(
                Icons.manage_accounts_outlined,
                const Color(0xFF7C3AED),
                '账号与安全',
                '当前账号、二次验证状态与登录密码',
                status: '可用',
                onTap: () => SettingsActions.showAccountSecurity(
                  context,
                  ref,
                  state.info,
                ),
              ),
              _SettingEntry(
                Icons.admin_panel_settings_outlined,
                const Color(0xFF475569),
                '管理员设置',
                '管理员与子账号、访问权限管理',
                status: 'PC 端',
              ),
              _SettingEntry(
                Icons.cloud_outlined,
                const Color(0xFF0891B2),
                '云账号授权',
                '云厂商账号与自动化能力授权',
                status: 'PC 端',
              ),
              _SettingEntry(
                Icons.auto_awesome_outlined,
                const Color(0xFFDB2777),
                'AI 账号',
                '模型供应商、密钥与默认推理策略',
                status: 'PC 端',
              ),
              _SettingEntry(
                Icons.mail_outline_rounded,
                const Color(0xFFD97706),
                '邮件通知',
                'SMTP、告警事件与通知接收人',
                status: 'PC 端',
              ),
              _SettingEntry(
                Icons.system_update_outlined,
                AppTheme.success,
                '版本更新',
                state.recovery == null ? '查看当前版本与系统更新状态' : '检测到未结束的升级任务，可恢复进度',
                status: state.recovery == null ? '可用' : '进行中',
                onTap: () => PanelSettingsActions.showVersionCenter(
                  context,
                  ref,
                  recovery: state.recovery,
                ),
              ),
            ]),
            const SizedBox(height: 28),
            _sectionLabel('面板操作', '高风险操作需要确认'),
            const SizedBox(height: 10),
            _settingsCard([
              _SettingEntry(
                Icons.restart_alt_rounded,
                AppTheme.warning,
                '重启面板',
                '应用配置变更，短时间内将无法访问面板',
                onTap: () =>
                    SettingsActions.confirmPanelAction(context, ref, 'panel'),
              ),
              _SettingEntry(
                Icons.power_settings_new_rounded,
                AppTheme.error,
                '重启服务器',
                '重启当前服务器上的操作系统',
                destructive: true,
                onTap: () =>
                    SettingsActions.confirmPanelAction(context, ref, 'server'),
              ),
              _SettingEntry(
                Icons.cleaning_services_outlined,
                AppTheme.textSecondary,
                '清理运行文件',
                '清理日志、临时文件或缓存目录中的文件',
                onTap: () => SettingsActions.showCleanupPicker(context, ref),
              ),
            ]),
            const SizedBox(height: 28),
            _sectionLabel('证书与域名', '当前移动端已支持'),
            const SizedBox(height: 10),
            _settingsCard([
              _SettingEntry(
                Icons.dns_rounded,
                AppTheme.primaryBlue,
                'DNS 账户授权',
                '用于自动申请与续期 Let\'s Encrypt 证书',
                onTap: () => Navigator.push(
                  context,
                  MaterialPageRoute(
                    builder: (_) => const DnsAccountListScreen(),
                  ),
                ),
              ),
            ]),
            const SizedBox(height: 28),
            _sectionLabel('连接管理', '本地客户端操作'),
            const SizedBox(height: 10),
            _settingsCard([
              _SettingEntry(
                Icons.logout_rounded,
                AppTheme.warning,
                '断开连接',
                '返回服务器选择列表，不删除本地记录',
                onTap: () => _handleDisconnect(context, ref),
              ),
              _SettingEntry(
                Icons.delete_outline_rounded,
                AppTheme.error,
                '移除服务器',
                '从本地服务器列表中彻底删除',
                destructive: true,
                onTap: () => _handleDeleteServer(context, ref),
              ),
            ]),
          ],
        ),
      ),
    );
  }

  Widget _pageHeader() => const Row(
    children: [
      Expanded(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              '设置',
              style: TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 28,
                height: 1.1,
                fontWeight: FontWeight.w800,
                letterSpacing: -0.6,
              ),
            ),
            SizedBox(height: 7),
            Text(
              '管理面板配置、账号授权与连接',
              style: TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 13,
                fontWeight: FontWeight.w500,
              ),
            ),
          ],
        ),
      ),
      _HeaderBadge(),
    ],
  );

  Widget _serverCard(String url, {required VoidCallback onTap}) {
    final isHoxi = StorageService.isActiveHoxi;
    return InkWell(
      borderRadius: BorderRadius.circular(24),
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.all(18),
        decoration: BoxDecoration(
          gradient: const LinearGradient(
            colors: [Color(0xFFEFF6FF), Color(0xFFF8FAFC)],
            begin: Alignment.topLeft,
            end: Alignment.bottomRight,
          ),
          borderRadius: BorderRadius.circular(24),
          border: Border.all(color: AppTheme.primaryBlueBorder),
        ),
        child: Row(
          children: [
            Container(
              width: 46,
              height: 46,
              decoration: BoxDecoration(
                color: Colors.white.withValues(alpha: 0.86),
                borderRadius: BorderRadius.circular(15),
              ),
              child: Icon(
                isHoxi ? Icons.terminal_rounded : Icons.dns_rounded,
                color: AppTheme.primaryBlue,
                size: 24,
              ),
            ),
            const SizedBox(width: 13),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    isHoxi ? '当前连接的 Hoxi 节点' : '当前连接的 GoPanel 节点',
                    style: const TextStyle(
                      color: AppTheme.textPrimary,
                      fontSize: 15,
                      fontWeight: FontWeight.w800,
                    ),
                  ),
                  const SizedBox(height: 6),
                  Text(
                    url,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(
                      color: AppTheme.textSecondary,
                      fontSize: 13,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ],
              ),
            ),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 6),
              decoration: BoxDecoration(
                color: const Color(0xFFECFDF5),
                borderRadius: BorderRadius.circular(999),
                border: Border.all(color: const Color(0xFFA7F3D0)),
              ),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  const Icon(Icons.circle, size: 7, color: AppTheme.success),
                  const SizedBox(width: 5),
                  Text(
                    isHoxi ? 'Hoxi 已连接' : '已连接',
                    style: const TextStyle(
                      color: Color(0xFF047857),
                      fontSize: 11,
                      fontWeight: FontWeight.w800,
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  Future<void> _showServerPicker(BuildContext context, WidgetRef ref) async {
    final servers = StorageService.getServerList();
    final selected = await showModalBottomSheet<ServerConnection>(
      context: context,
      showDragHandle: true,
      builder: (sheetContext) => SafeArea(
        child: ListView(
          shrinkWrap: true,
          padding: const EdgeInsets.fromLTRB(16, 0, 16, 18),
          children: [
            Row(
              children: [
                const Padding(
                  padding: EdgeInsets.fromLTRB(4, 0, 4, 10),
                  child: Text(
                    '切换节点 (Hoxi / GoPanel)',
                    style: TextStyle(fontSize: 18, fontWeight: FontWeight.w800),
                  ),
                ),
                const Spacer(),
                TextButton.icon(
                  onPressed: () {
                    Navigator.pop(sheetContext);
                    context.go('/');
                  },
                  icon: const Icon(Icons.add_rounded, size: 18),
                  label: const Text('连接新节点'),
                ),
              ],
            ),
            for (final server in servers)
              ListTile(
                contentPadding: const EdgeInsets.symmetric(horizontal: 4),
                leading: CircleAvatar(
                  backgroundColor: AppTheme.primaryBlueLight,
                  child: Icon(
                    server.isHoxi ? Icons.terminal_rounded : Icons.dns_rounded,
                    color: AppTheme.primaryBlue,
                  ),
                ),
                title: Row(
                  children: [
                    Flexible(
                      child: Text(
                        server.name,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                    const SizedBox(width: 8),
                    Container(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 6,
                        vertical: 2,
                      ),
                      decoration: BoxDecoration(
                        color: AppTheme.primaryBlueLight,
                        borderRadius: BorderRadius.circular(6),
                      ),
                      child: Text(
                        server.isHoxi ? 'Hoxi' : 'GoPanel',
                        style: const TextStyle(
                          fontSize: 10,
                          fontWeight: FontWeight.w700,
                          color: AppTheme.primaryBlue,
                        ),
                      ),
                    ),
                  ],
                ),
                subtitle: Text(server.url),
                trailing: server.url == StorageService.activeServerUrl
                    ? const Icon(
                        Icons.check_circle_rounded,
                        color: AppTheme.success,
                      )
                    : const Icon(Icons.chevron_right_rounded),
                onTap: () => Navigator.pop(sheetContext, server),
              ),
          ],
        ),
      ),
    );
    if (selected == null || selected.url == StorageService.activeServerUrl) {
      return;
    }
    final success = await ref
        .read(authControllerProvider.notifier)
        .switchToSavedServer(selected);
    if (!context.mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(success ? '已切换到 ${selected.name}' : '节点切换失败'),
        backgroundColor: success ? AppTheme.success : AppTheme.error,
      ),
    );
  }

  Widget _sectionLabel(String title, String subtitle) => Padding(
    padding: const EdgeInsets.only(left: 4),
    child: Row(
      crossAxisAlignment: CrossAxisAlignment.end,
      children: [
        Text(
          title,
          style: const TextStyle(
            color: AppTheme.textPrimary,
            fontSize: 15,
            fontWeight: FontWeight.w800,
          ),
        ),
        const SizedBox(width: 8),
        Expanded(
          child: Text(
            subtitle,
            overflow: TextOverflow.ellipsis,
            style: const TextStyle(
              color: AppTheme.textLight,
              fontSize: 11,
              fontWeight: FontWeight.w600,
            ),
          ),
        ),
      ],
    ),
  );

  Widget _settingsCard(List<_SettingEntry> entries) => DecoratedBox(
    decoration: BoxDecoration(
      color: AppTheme.surface,
      borderRadius: BorderRadius.circular(22),
      border: Border.all(color: AppTheme.border.withValues(alpha: 0.85)),
      boxShadow: [
        BoxShadow(
          color: AppTheme.textPrimary.withValues(alpha: 0.035),
          blurRadius: 18,
          offset: const Offset(0, 8),
        ),
      ],
    ),
    child: Column(
      children: [
        for (var i = 0; i < entries.length; i++) ...[
          _settingTile(entries[i]),
          if (i < entries.length - 1)
            const Divider(height: 1, indent: 68, endIndent: 16),
        ],
      ],
    ),
  );

  Widget _settingTile(_SettingEntry entry) => ListTile(
    contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 7),
    leading: Container(
      width: 40,
      height: 40,
      decoration: BoxDecoration(
        color: entry.iconColor.withValues(alpha: 0.1),
        borderRadius: BorderRadius.circular(13),
      ),
      child: Icon(entry.icon, color: entry.iconColor, size: 21),
    ),
    title: Text(
      entry.title,
      style: TextStyle(
        color: entry.destructive ? AppTheme.error : AppTheme.textPrimary,
        fontSize: 14,
        fontWeight: FontWeight.w700,
      ),
    ),
    subtitle: Padding(
      padding: const EdgeInsets.only(top: 4),
      child: Text(
        entry.subtitle,
        style: const TextStyle(
          color: AppTheme.textSecondary,
          fontSize: 12,
          height: 1.3,
        ),
      ),
    ),
    trailing: entry.status == null
        ? const Icon(Icons.chevron_right_rounded, color: AppTheme.textLight)
        : Container(
            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 5),
            decoration: BoxDecoration(
              color: AppTheme.background,
              borderRadius: BorderRadius.circular(999),
            ),
            child: Text(
              entry.status!,
              style: const TextStyle(
                color: AppTheme.textLight,
                fontSize: 10,
                fontWeight: FontWeight.w800,
              ),
            ),
          ),
    onTap: entry.onTap,
  );
}

class _HeaderBadge extends StatelessWidget {
  const _HeaderBadge();
  @override
  Widget build(BuildContext context) => Container(
    padding: const EdgeInsets.all(11),
    decoration: BoxDecoration(
      color: AppTheme.primaryBlueLight,
      borderRadius: BorderRadius.circular(16),
      border: Border.all(color: AppTheme.primaryBlueBorder),
    ),
    child: const Icon(
      Icons.settings_suggest_rounded,
      color: AppTheme.primaryBlue,
      size: 23,
    ),
  );
}

class _SettingEntry {
  final IconData icon;
  final Color iconColor;
  final String title;
  final String subtitle;
  final String? status;
  final bool destructive;
  final VoidCallback? onTap;
  const _SettingEntry(
    this.icon,
    this.iconColor,
    this.title,
    this.subtitle, {
    this.status,
    this.destructive = false,
    this.onTap,
  });
}
