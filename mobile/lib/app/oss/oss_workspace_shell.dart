import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../core/l10n/l10n.dart';
import '../../core/navigation/navigation_guard.dart';
import '../../core/storage/active_node_provider.dart';
import '../../core/storage/connection_list_signal.dart';
import '../../core/storage/server_connection.dart';
import '../../core/storage/storage_service.dart';
import '../../core/theme/app_theme.dart';
import '../../core/theme/theme_mode_controller.dart';
import '../../features/ai_workspace/models/ai_dev_session.dart';
import '../../features/ai_workspace/presentation/controllers/ai_workspace_controller.dart';
import '../../features/ai_workspace/presentation/controllers/code_attention_controller.dart';
import '../../features/ai_workspace/presentation/screens/ai_chat_screen.dart';
import '../../features/ai_workspace/presentation/screens/code_hub_screen.dart';
import '../../features/ai_workspace/presentation/screens/code_session_sheet.dart';
import '../../features/auth/models/node_link_kind.dart';
import '../../features/auth/presentation/controllers/auth_controller.dart';
import '../../features/auth/presentation/screens/node_link_kind_screen.dart';
import '../../features/auth/presentation/screens/qr_scanner_screen.dart';
import '../../features/auth/presentation/screens/server_login_screen.dart';
import '../../shared/models/task_entity.dart';
import '../../shared/navigation/feature_route_registry.dart';

/// 开源移动控制端路由注册与声明式路由表（仅聚焦 Hoxi 桌面端与 GoPanel 节点编码协同）。
abstract final class OssAppRouter {
  static const String homePath = '/';
  static const String dashboardPath = '/dashboard';
  static const String workbenchPath = '/workbench';
  static const String addNodePath = '/add_node';
  static const String loginPath = '/login';
  static const String qrScannerPath = '/qr_scanner';

  static void ensureRoutesRegistered() {
    FeatureRouteRegistry.connectionAuthorizationOpener = (context, server) =>
        ServerLoginScreen.open(
          context,
          kind: NodeLinkKind.resolve(server.nodeType) ?? NodeLinkKind.hoxi,
          initialUrl: server.url,
          replacingConnectionId: server.id,
        );
    FeatureRouteRegistry.workbenchOpener = (context) async {
      if (GoRouter.maybeOf(context) != null) {
        context.go(dashboardPath);
      }
    };
    FeatureRouteRegistry.taskDetailBuilder = (task) =>
        OssTaskSummaryScreen(task: task);
  }

  static final GoRouter router = (() {
    ensureRoutesRegistered();
    return AppNavigationGuard.bindRouter(
      GoRouter(
        initialLocation: homePath,
        observers: <NavigatorObserver>[AppNavigationGuard.observer],
        routes: <RouteBase>[
          GoRoute(
            path: homePath,
            builder: (context, state) => const OssWorkspaceShellScreen(),
          ),
          GoRoute(
            path: dashboardPath,
            builder: (context, state) => const OssWorkspaceShellScreen(),
          ),
          GoRoute(
            path: workbenchPath,
            builder: (context, state) => const OssWorkspaceShellScreen(),
          ),
          GoRoute(
            path: addNodePath,
            builder: (context, state) => const NodeLinkKindScreen(),
          ),
          GoRoute(
            path: loginPath,
            builder: (context, state) => ServerLoginScreen(
              kind:
                  NodeLinkKind.resolve(state.uri.queryParameters['kind']) ??
                  NodeLinkKind.hoxi,
              initialUrl: state.uri.queryParameters['url'],
              replacingConnectionId: state.uri.queryParameters['connectionId'],
            ),
          ),
          GoRoute(
            path: qrScannerPath,
            builder: (context, state) => QRScannerScreen(
              kind: NodeLinkKind.resolve(state.uri.queryParameters['kind']),
              replacingConnectionId: state.uri.queryParameters['connectionId'],
            ),
          ),
          GoRoute(
            path: FeatureRouteRegistry.taskDetailPath,
            builder: (context, state) {
              final task = state.extra;
              if (task is TaskEntity) {
                return OssTaskSummaryScreen(task: task);
              }
              return const OssWorkspaceShellScreen();
            },
          ),
        ],
      ),
    );
  })();
}

/// 开源移动控制端主壳层：未连接时提供扫码/局域网配对引导，已连接时直通 `CodeHubScreen`。
class OssWorkspaceShellScreen extends ConsumerStatefulWidget {
  const OssWorkspaceShellScreen({super.key});

  @override
  ConsumerState<OssWorkspaceShellScreen> createState() =>
      _OssWorkspaceShellScreenState();
}

class _OssWorkspaceShellScreenState
    extends ConsumerState<OssWorkspaceShellScreen> {
  Future<void> _openSessionCreator() async {
    final session = await showModalBottomSheet<AiDevSession>(
      context: context,
      isScrollControlled: true,
      showDragHandle: false,
      builder: (_) => const CodeSessionSheet(),
    );
    if (!mounted || session == null) return;
    await ref
        .read(aiWorkspaceControllerProvider.notifier)
        .selectSession(session);
    if (!mounted) return;
    await AppNavigationGuard.push<void>(
      context,
      MaterialPageRoute<void>(builder: (_) => const AiChatScreen()),
    );
    if (mounted) {
      unawaited(ref.read(aiWorkspaceControllerProvider.notifier).loadWorkspace());
    }
  }

  Future<void> _refreshWorkspace() async {
    await Future.wait([
      ref.read(aiWorkspaceControllerProvider.notifier).loadWorkspace(),
      ref.read(codeAttentionControllerProvider.notifier).load(),
    ]);
  }

  Future<void> _switchToServer(ServerConnection server) async {
    final ok = await ref
        .read(authControllerProvider.notifier)
        .switchToSavedServer(server);
    if (!mounted) return;
    setState(() {});
    if (!ok) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(context.l10n.serverListUnreachable),
          backgroundColor: AppTheme.error,
        ),
      );
    }
  }

  Future<void> _showNodeSwitcherSheet(List<ServerConnection> servers) async {
    final isZh = Localizations.localeOf(context).languageCode.startsWith('zh');
    await showModalBottomSheet<void>(
      context: context,
      backgroundColor: AppTheme.surface,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (sheetCtx) {
        return SafeArea(
          child: Padding(
            padding: const EdgeInsets.fromLTRB(16, 12, 16, 16),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Text(
                  isZh ? '受控研发节点' : 'Connected Dev Nodes',
                  style: TextStyle(
                    fontSize: 16,
                    fontWeight: FontWeight.w800,
                    color: AppTheme.textPrimary,
                  ),
                ),
                const SizedBox(height: 8),
                for (final server in servers)
                  ListTile(
                    contentPadding: const EdgeInsets.symmetric(horizontal: 4),
                    leading: Icon(
                      server.isHoxi
                          ? Icons.laptop_mac_outlined
                          : Icons.dns_outlined,
                      color: StorageService.isServerActive(server)
                          ? AppTheme.primaryBlue
                          : AppTheme.textSecondary,
                    ),
                    title: Text(
                      server.name,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        fontWeight: StorageService.isServerActive(server)
                            ? FontWeight.w800
                            : FontWeight.w600,
                      ),
                    ),
                    subtitle: Text(
                      server.url,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(fontSize: 12),
                    ),
                    trailing: Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        if (StorageService.isServerActive(server))
                          const Icon(
                            Icons.check_circle_rounded,
                            color: AppTheme.success,
                            size: 18,
                          ),
                        IconButton(
                          visualDensity: VisualDensity.compact,
                          icon: Icon(
                            Icons.delete_outline_rounded,
                            size: 19,
                            color: AppTheme.textSecondary,
                          ),
                          onPressed: () async {
                            final wasActive = StorageService.isServerActive(
                              server,
                            );
                            await StorageService.removeServerConnectionById(
                              server.id,
                            );
                            if (wasActive) {
                              await StorageService.clearActiveServer();
                            }
                            if (sheetCtx.mounted) Navigator.of(sheetCtx).pop();
                            if (mounted) setState(() {});
                          },
                        ),
                      ],
                    ),
                    onTap: () {
                      Navigator.of(sheetCtx).pop();
                      unawaited(_switchToServer(server));
                    },
                  ),
                const Divider(height: 16),
                Row(
                  children: [
                    Expanded(
                      child: OutlinedButton.icon(
                        onPressed: () {
                          Navigator.of(sheetCtx).pop();
                          context.push('/qr_scanner?kind=hoxi');
                        },
                        icon: const Icon(Icons.crop_free_rounded, size: 18),
                        label: Text(context.l10n.loginScanHoxiQr),
                      ),
                    ),
                    const SizedBox(width: 10),
                    Expanded(
                      child: OutlinedButton.icon(
                        onPressed: () {
                          Navigator.of(sheetCtx).pop();
                          context.push('/add_node');
                        },
                        icon: const Icon(Icons.add_rounded, size: 18),
                        label: Text(context.l10n.engineeringAddNode),
                      ),
                    ),
                  ],
                ),
                if ((StorageService.activeServerUrl ?? '').isNotEmpty) ...[
                  const SizedBox(height: 8),
                  TextButton(
                    onPressed: () async {
                      Navigator.of(sheetCtx).pop();
                      await ref.read(authControllerProvider.notifier).logout();
                      if (mounted) setState(() {});
                    },
                    child: Text(
                      isZh ? '断开当前连接' : 'Disconnect Current Node',
                      style: const TextStyle(color: AppTheme.error),
                    ),
                  ),
                ],
              ],
            ),
          ),
        );
      },
    );
  }

  @override
  Widget build(BuildContext context) {
    ref.watch(activeNodeGenerationProvider);
    final themeMode = ref.watch(themeModeControllerProvider);

    return ValueListenableBuilder<int>(
      valueListenable: connectionListGeneration,
      builder: (context, _, _) {
        final l10n = context.l10n;
        final isZh = Localizations.localeOf(
          context,
        ).languageCode.startsWith('zh');
        final servers = StorageService.getServerList()
            .where((s) => s.isDevNode)
            .toList();
        final activeUrl = StorageService.activeServerUrl;
        final hasActiveNode = activeUrl != null && activeUrl.isNotEmpty;
        final activeServer = servers
            .where((s) => StorageService.isServerActive(s, activeUrl))
            .firstOrNull;

        if (!hasActiveNode) {
          return _buildOnboardingView(context, l10n, isZh, themeMode, servers);
        }

        final nodeTitle =
            activeServer?.name ?? (Uri.tryParse(activeUrl)?.host ?? activeUrl);
        final badge = StorageService.isActiveHoxi ? 'Hoxi' : 'GoPanel';

        return Scaffold(
          backgroundColor: AppTheme.background,
          body: Column(
            children: [
              SafeArea(
                bottom: false,
                child: Container(
                  padding: const EdgeInsets.fromLTRB(14, 10, 10, 10),
                  decoration: BoxDecoration(
                    color: AppTheme.surface,
                    border: Border(
                      bottom: BorderSide(
                        color: AppTheme.border.withValues(alpha: 0.8),
                      ),
                    ),
                  ),
                  child: Row(
                    children: [
                      Expanded(
                        child: InkWell(
                          key: const ValueKey('oss_active_node_pill'),
                          borderRadius: BorderRadius.circular(999),
                          onTap: () => _showNodeSwitcherSheet(servers),
                          child: Container(
                            padding: const EdgeInsets.symmetric(
                              horizontal: 12,
                              vertical: 8,
                            ),
                            decoration: BoxDecoration(
                              color: AppTheme.surfaceMuted,
                              borderRadius: BorderRadius.circular(999),
                            ),
                            child: Row(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                const Icon(
                                  Icons.circle,
                                  size: 8,
                                  color: AppTheme.success,
                                ),
                                const SizedBox(width: 8),
                                Flexible(
                                  child: Text(
                                    nodeTitle,
                                    maxLines: 1,
                                    overflow: TextOverflow.ellipsis,
                                    style: TextStyle(
                                      color: AppTheme.textPrimary,
                                      fontSize: 13,
                                      fontWeight: FontWeight.w700,
                                    ),
                                  ),
                                ),
                                const SizedBox(width: 6),
                                Container(
                                  padding: const EdgeInsets.symmetric(
                                    horizontal: 6,
                                    vertical: 1.5,
                                  ),
                                  decoration: BoxDecoration(
                                    color: AppTheme.primaryBlueLight,
                                    borderRadius: BorderRadius.circular(6),
                                  ),
                                  child: Text(
                                    badge,
                                    style: const TextStyle(
                                      color: AppTheme.primaryBlue,
                                      fontSize: 10,
                                      fontWeight: FontWeight.w800,
                                    ),
                                  ),
                                ),
                                const SizedBox(width: 4),
                                Icon(
                                  Icons.unfold_more_rounded,
                                  size: 16,
                                  color: AppTheme.textSecondary,
                                ),
                              ],
                            ),
                          ),
                        ),
                      ),
                      const SizedBox(width: 4),
                      IconButton(
                        visualDensity: VisualDensity.compact,
                        tooltip: l10n.codeWsHubCreate,
                        onPressed: _openSessionCreator,
                        icon: const Icon(Icons.add_rounded, size: 22),
                      ),
                      IconButton(
                        visualDensity: VisualDensity.compact,
                        tooltip: isZh ? '切换明暗主题' : 'Toggle Theme',
                        onPressed: () =>
                            ref.read(themeModeControllerProvider.notifier).toggle(),
                        icon: Icon(
                          themeMode == ThemeMode.dark
                              ? Icons.light_mode_outlined
                              : Icons.dark_mode_outlined,
                          size: 19,
                        ),
                      ),
                      IconButton(
                        visualDensity: VisualDensity.compact,
                        tooltip: l10n.codeWsActionRefresh,
                        onPressed: _refreshWorkspace,
                        icon: const Icon(Icons.refresh_rounded, size: 20),
                      ),
                    ],
                  ),
                ),
              ),
              const Expanded(child: CodeHubScreen(showAppBar: false)),
            ],
          ),
        );
      },
    );
  }

  Widget _buildOnboardingView(
    BuildContext context,
    AppLocalizations l10n,
    bool isZh,
    ThemeMode themeMode,
    List<ServerConnection> savedServers,
  ) {
    return Scaffold(
      backgroundColor: AppTheme.background,
      appBar: AppBar(
        title: const Text('Hoxi'),
        actions: [
          IconButton(
            tooltip: isZh ? '切换明暗主题' : 'Toggle Theme',
            onPressed: () =>
                ref.read(themeModeControllerProvider.notifier).toggle(),
            icon: Icon(
              themeMode == ThemeMode.dark
                  ? Icons.light_mode_outlined
                  : Icons.dark_mode_outlined,
            ),
          ),
        ],
      ),
      body: SafeArea(
        child: ListView(
          padding: const EdgeInsets.fromLTRB(24, 24, 24, 32),
          children: [
            Center(
              child: ClipRRect(
                borderRadius: BorderRadius.circular(20),
                child: Image.asset(
                  'web/icons/Icon-192.png',
                  width: 72,
                  height: 72,
                ),
              ),
            ),
            const SizedBox(height: 16),
            Text(
              l10n.addNodeHoxiTitle,
              textAlign: TextAlign.center,
              style: TextStyle(
                fontSize: 24,
                fontWeight: FontWeight.w800,
                color: AppTheme.textPrimary,
              ),
            ),
            const SizedBox(height: 8),
            Text(
              l10n.addNodeHoxiSubtitle,
              textAlign: TextAlign.center,
              style: TextStyle(
                fontSize: 14,
                height: 1.45,
                color: AppTheme.textSecondary,
              ),
            ),
            const SizedBox(height: 28),
            ElevatedButton.icon(
              key: const ValueKey('oss_scan_qr_button'),
              onPressed: () => context.push('/qr_scanner?kind=hoxi'),
              icon: const Icon(Icons.crop_free_rounded),
              label: Text(l10n.loginScanHoxiQr),
            ),
            const SizedBox(height: 12),
            OutlinedButton.icon(
              key: const ValueKey('oss_connect_hoxi_button'),
              onPressed: () => context.push('/login?kind=hoxi'),
              icon: const Icon(Icons.laptop_mac_outlined),
              label: Text(l10n.loginHoxiTitle),
              style: OutlinedButton.styleFrom(
                padding: const EdgeInsets.symmetric(vertical: 14),
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(12),
                ),
              ),
            ),
            const SizedBox(height: 12),
            TextButton.icon(
              key: const ValueKey('oss_more_nodes_button'),
              onPressed: () => context.push('/add_node'),
              icon: const Icon(Icons.dns_outlined, size: 18),
              label: Text(l10n.engineeringAddNode),
            ),
            if (savedServers.isNotEmpty) ...[
              const SizedBox(height: 24),
              Text(
                isZh ? '已保存的设备' : 'Saved Devices',
                style: TextStyle(
                  fontSize: 13.5,
                  fontWeight: FontWeight.w800,
                  color: AppTheme.textSecondary,
                ),
              ),
              const SizedBox(height: 8),
              for (final s in savedServers)
                Card(
                  margin: const EdgeInsets.only(bottom: 8),
                  child: ListTile(
                    leading: Icon(
                      s.isHoxi ? Icons.laptop_mac_outlined : Icons.dns_outlined,
                      color: AppTheme.primaryBlue,
                    ),
                    title: Text(
                      s.name,
                      style: const TextStyle(fontWeight: FontWeight.w700),
                    ),
                    subtitle: Text(
                      s.url,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                    trailing: const Icon(Icons.chevron_right_rounded),
                    onTap: () => _switchToServer(s),
                  ),
                ),
            ],
          ],
        ),
      ),
    );
  }
}

/// 轻量编码任务摘要页（实现 `FeatureRouteRegistry.taskDetailBuilder`，无商业依赖）。
class OssTaskSummaryScreen extends ConsumerWidget {
  final TaskEntity task;

  const OssTaskSummaryScreen({super.key, required this.task});

  Future<void> _openSession(BuildContext context, WidgetRef ref) async {
    final sessionId = int.tryParse(task.meta['sessionId'] ?? '');
    if (sessionId == null || sessionId <= 0) return;
    final sessions = ref.read(aiWorkspaceControllerProvider).sessions;
    final matched = sessions.where((s) => s.id == sessionId).firstOrNull;
    if (matched != null) {
      await ref
          .read(aiWorkspaceControllerProvider.notifier)
          .selectSession(matched);
      if (!context.mounted) return;
      await AppNavigationGuard.push<void>(
        context,
        MaterialPageRoute<void>(builder: (_) => const AiChatScreen()),
      );
    }
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isZh = Localizations.localeOf(context).languageCode.startsWith('zh');
    final workDir = (task.meta['workDir'] ?? '').trim();
    final agent = (task.meta['agentName'] ?? '').trim();
    final model = (task.meta['providerModel'] ?? '').trim();
    final hasSession = (int.tryParse(task.meta['sessionId'] ?? '') ?? 0) > 0;

    return Scaffold(
      backgroundColor: AppTheme.background,
      appBar: AppBar(title: Text(isZh ? '任务详情' : 'Task Detail')),
      body: ListView(
        padding: const EdgeInsets.all(20),
        children: [
          Card(
            child: Padding(
              padding: const EdgeInsets.all(18),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    task.title,
                    style: TextStyle(
                      fontSize: 17,
                      fontWeight: FontWeight.w800,
                      color: AppTheme.textPrimary,
                    ),
                  ),
                  const SizedBox(height: 10),
                  if (workDir.isNotEmpty)
                    Text(
                      workDir,
                      style: TextStyle(
                        fontSize: 12.5,
                        fontFamily: 'monospace',
                        color: AppTheme.textSecondary,
                      ),
                    ),
                  if (agent.isNotEmpty || model.isNotEmpty) ...[
                    const SizedBox(height: 6),
                    Text(
                      [if (agent.isNotEmpty) agent, if (model.isNotEmpty) model]
                          .join(' · '),
                      style: TextStyle(
                        fontSize: 12.5,
                        color: AppTheme.textSecondary,
                      ),
                    ),
                  ],
                  if ((task.summary ?? '').trim().isNotEmpty) ...[
                    const SizedBox(height: 12),
                    Text(
                      task.summary!.trim(),
                      style: TextStyle(
                        fontSize: 14,
                        color: AppTheme.textPrimary,
                      ),
                    ),
                  ],
                  if ((task.error ?? '').trim().isNotEmpty) ...[
                    const SizedBox(height: 12),
                    Text(
                      task.error!.trim(),
                      style: const TextStyle(
                        fontSize: 13,
                        color: AppTheme.error,
                      ),
                    ),
                  ],
                ],
              ),
            ),
          ),
          if (hasSession) ...[
            const SizedBox(height: 16),
            ElevatedButton.icon(
              onPressed: () => _openSession(context, ref),
              icon: const Icon(Icons.terminal_rounded),
              label: Text(isZh ? '进入编码会话' : 'Open Coding Session'),
            ),
          ],
        ],
      ),
    );
  }
}
