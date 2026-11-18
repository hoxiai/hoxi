import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../core/l10n/l10n.dart';
import '../../../../core/theme/app_theme.dart';

import '../../../../core/network/network_providers.dart';
import '../../../../core/storage/server_connection.dart';
import '../../data/hoxi_relay_repository.dart';

/// 列出某台 GoPanel 下已配对的 Hoxi 桌面电脑，选中后由调用方保存为中继连接。
/// 离线电脑不可选：中继连接需要隧道在线才能完成首次探测。
class HoxiRelayNodesSheet extends ConsumerStatefulWidget {
  final ServerConnection gopanel;
  final ValueChanged<HoxiRelayNode> onSelect;
  final HoxiRelayRepository? repository;

  const HoxiRelayNodesSheet({
    super.key,
    required this.gopanel,
    required this.onSelect,
    this.repository,
  });

  @override
  ConsumerState<HoxiRelayNodesSheet> createState() =>
      _HoxiRelayNodesSheetState();
}

class _HoxiRelayNodesSheetState extends ConsumerState<HoxiRelayNodesSheet> {
  late final HoxiRelayRepository _repository;
  List<HoxiRelayNode>? _nodes;
  String? _error;

  @override
  void initState() {
    super.initState();
    _repository =
        widget.repository ??
        HoxiRelayRepository(ref.read(nodeApiClientProvider));
    _load();
  }

  Future<void> _load() async {
    setState(() {
      _nodes = null;
      _error = null;
    });
    try {
      final nodes = await _repository.fetchNodes(widget.gopanel);
      if (mounted) setState(() => _nodes = nodes);
    } catch (error) {
      if (mounted) setState(() => _error = '$error');
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    return SafeArea(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(20, 16, 20, 20),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(
                    l10n.relayNodesTitle,
                    style: const TextStyle(
                      fontSize: 17,
                      fontWeight: FontWeight.bold,
                    ),
                  ),
                ),
                IconButton(
                  icon: const Icon(Icons.refresh_rounded),
                  tooltip: l10n.commonRefresh,
                  onPressed: _load,
                ),
              ],
            ),
            const SizedBox(height: 4),
            Text(
              l10n.relayNodesSubtitle,
              style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
            ),
            const SizedBox(height: 12),
            _buildBody(l10n),
          ],
        ),
      ),
    );
  }

  Widget _buildBody(AppLocalizations l10n) {
    if (_error != null) {
      return Text(
        l10n.relayNodesLoadFailed(_error!),
        style: const TextStyle(color: Colors.red),
      );
    }
    final nodes = _nodes;
    if (nodes == null) {
      return const Padding(
        padding: EdgeInsets.symmetric(vertical: 24),
        child: Center(child: CircularProgressIndicator()),
      );
    }
    if (nodes.isEmpty) {
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 24),
        child: Center(
          child: Text(
            l10n.relayNodesEmpty,
            style: TextStyle(color: AppTheme.textSecondary),
          ),
        ),
      );
    }
    return Column(
      children: [
        for (final node in nodes)
          ListTile(
            contentPadding: EdgeInsets.zero,
            enabled: node.online,
            leading: Icon(
              Icons.laptop_mac_rounded,
              color: node.online ? AppTheme.primaryBlue : AppTheme.textLight,
            ),
            title: Text(node.displayName),
            subtitle: Text(
              [
                node.online ? l10n.relayNodeOnline : l10n.relayNodeOffline,
                if (node.hostname.isNotEmpty) node.hostname,
                if (node.version.isNotEmpty) node.version,
              ].join(' · '),
            ),
            trailing: node.online ? const Icon(Icons.add_link_rounded) : null,
            onTap: node.online ? () => widget.onSelect(node) : null,
          ),
      ],
    );
  }
}
