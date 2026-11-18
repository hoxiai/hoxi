import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../../core/l10n/l10n.dart';
import '../../../../core/theme/app_theme.dart';
import '../../models/node_link_kind.dart';
import '../auth_ui_extension_registry.dart';
import '../widgets/lan_discovery_sheet.dart';

/// 连接受控设备与扩展服务选择页。
class NodeLinkKindScreen extends ConsumerWidget {
  const NodeLinkKindScreen({super.key});

  Future<void> _handleDiscoverLan(BuildContext context) async {
    final picked = await showLanDiscoverySheet(context: context);
    if (!context.mounted || picked == null) return;
    final encodedUrl = Uri.encodeQueryComponent(picked.url);
    context.push('/login?kind=${picked.kind.id}&url=$encodedUrl');
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = context.l10n;
    final isZh = Localizations.localeOf(context).languageCode.startsWith('zh');
    final kinds = ref.watch(supportedNodeLinkKindsProvider);

    return Scaffold(
      backgroundColor: AppTheme.background,
      appBar: AppBar(
        title: Text(l10n.engineeringAddNode),
        actions: [
          IconButton(
            tooltip: isZh ? '扫描局域网设备' : 'Scan LAN Devices',
            icon: const Icon(Icons.radar_outlined),
            onPressed: () => _handleDiscoverLan(context),
          ),
          IconButton(
            tooltip: l10n.loginScanQr,
            icon: const Icon(Icons.crop_free_rounded),
            onPressed: () => context.push('/qr_scanner'),
          ),
        ],
      ),
      body: SafeArea(
        top: false,
        child: ListView(
          padding: const EdgeInsets.fromLTRB(20, 8, 20, 28),
          children: [
            Padding(
              padding: const EdgeInsets.only(left: 4, bottom: 10),
              child: Text(
                l10n.nodeLinkDevicesSection,
                style: TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 13.5,
                  fontWeight: FontWeight.w800,
                ),
              ),
            ),
            for (final kind in kinds) ...[
              _KindCard(
                key: ValueKey('node_link_kind_${kind.id}'),
                kind: kind,
                title: _title(l10n, kind),
                subtitle: _subtitle(l10n, kind),
                onTap: kind.isConnectable
                    ? () => context.push('/login?kind=${kind.id}')
                    : null,
              ),
              const SizedBox(height: 10),
            ],
          ],
        ),
      ),
    );
  }

  String _title(AppLocalizations l10n, NodeLinkKind kind) {
    if (kind == NodeLinkKind.hoxi) return l10n.addNodeHoxiTitle;
    if (kind == NodeLinkKind.gopanel) return l10n.addNodeGopanelTitle;
    return AuthUiExtensionRegistry.pickerTitleResolver?.call(kind, l10n) ??
        kind.id;
  }

  String _subtitle(AppLocalizations l10n, NodeLinkKind kind) {
    if (kind == NodeLinkKind.hoxi) return l10n.addNodeHoxiSubtitle;
    if (kind == NodeLinkKind.gopanel) return l10n.addNodeGopanelSubtitle;
    return AuthUiExtensionRegistry.pickerSubtitleResolver?.call(kind, l10n) ??
        '';
  }
}

class _KindCard extends StatelessWidget {
  const _KindCard({
    super.key,
    required this.kind,
    required this.title,
    required this.subtitle,
    this.onTap,
  });

  final NodeLinkKind kind;
  final String title;
  final String subtitle;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final enabled = onTap != null;
    return Material(
      color: AppTheme.surface,
      borderRadius: BorderRadius.circular(16),
      child: InkWell(
        borderRadius: BorderRadius.circular(16),
        onTap: onTap,
        child: Container(
          padding: const EdgeInsets.fromLTRB(14, 14, 14, 14),
          decoration: BoxDecoration(
            borderRadius: BorderRadius.circular(16),
          ),
          child: Row(
            children: [
              Container(
                width: 40,
                height: 40,
                decoration: BoxDecoration(
                  color: AppTheme.surfaceMuted,
                  borderRadius: BorderRadius.circular(12),
                ),
                child: Icon(
                  _icon(kind),
                  color: enabled ? AppTheme.textSecondary : AppTheme.textLight,
                  size: 20,
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      title,
                      style: TextStyle(
                        color: enabled
                            ? AppTheme.textPrimary
                            : AppTheme.textSecondary,
                        fontSize: 15,
                        fontWeight: FontWeight.w800,
                      ),
                    ),
                    const SizedBox(height: 3),
                    Text(
                      subtitle,
                      style: TextStyle(
                        color: AppTheme.textSecondary,
                        fontSize: 12.5,
                        height: 1.35,
                      ),
                    ),
                  ],
                ),
              ),
              const SizedBox(width: 8),
              if (enabled)
                Icon(
                  Icons.chevron_right_rounded,
                  color: AppTheme.textSecondary,
                )
              else
                Container(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 8,
                    vertical: 3,
                  ),
                  decoration: BoxDecoration(
                    color: AppTheme.surfaceMuted,
                    borderRadius: BorderRadius.circular(999),
                  ),
                  child: Text(
                    l10n.addNodeComingSoon,
                    style: TextStyle(
                      color: AppTheme.textSecondary,
                      fontSize: 11,
                      fontWeight: FontWeight.w700,
                    ),
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }

  IconData _icon(NodeLinkKind kind) {
    if (kind == NodeLinkKind.hoxi) return Icons.laptop_mac_outlined;
    if (kind == NodeLinkKind.gopanel) return Icons.dns_outlined;
    return AuthUiExtensionRegistry.kindIconResolver?.call(kind) ??
        Icons.cloud_outlined;
  }
}
