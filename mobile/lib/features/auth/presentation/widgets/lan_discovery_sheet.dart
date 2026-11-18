import 'package:flutter/material.dart';

import '../../../../core/theme/app_theme.dart';
import '../../data/lan_node_scanner.dart';
import '../../models/node_link_kind.dart';
import '../auth_ui_extension_registry.dart';

/// 弹出同局域网设备扫描面板，选中后返回节点地址与类型。
Future<DiscoveredLanNode?> showLanDiscoverySheet({
  required BuildContext context,
  NodeLinkKind? filterKind,
}) {
  return showModalBottomSheet<DiscoveredLanNode>(
    context: context,
    isScrollControlled: true,
    builder: (sheetContext) => _LanDiscoverySheet(filterKind: filterKind),
  );
}

class _LanDiscoverySheet extends StatefulWidget {
  const _LanDiscoverySheet({this.filterKind});

  final NodeLinkKind? filterKind;

  @override
  State<_LanDiscoverySheet> createState() => _LanDiscoverySheetState();
}

class _LanDiscoverySheetState extends State<_LanDiscoverySheet> {
  bool _scanning = true;
  List<String> _localIps = const [];
  List<DiscoveredLanNode> _nodes = const [];

  @override
  void initState() {
    super.initState();
    _runScan();
  }

  Future<void> _runScan() async {
    setState(() {
      _scanning = true;
    });
    final ips = await LanNodeScanner.localPrivateIPv4s();
    final nodes = await LanNodeScanner.scan(filterKind: widget.filterKind);
    if (!mounted) return;
    setState(() {
      _localIps = ips;
      _nodes = nodes;
      _scanning = false;
    });
  }

  IconData _iconFor(NodeLinkKind kind) {
    if (kind == NodeLinkKind.hoxi) return Icons.laptop_mac_outlined;
    if (kind == NodeLinkKind.gopanel) return Icons.dns_outlined;
    return AuthUiExtensionRegistry.kindIconResolver?.call(kind) ??
        Icons.cloud_outlined;
  }

  @override
  Widget build(BuildContext context) {
    final isZh = Localizations.localeOf(context).languageCode.startsWith('zh');
    final subnetHint = _localIps.isNotEmpty
        ? (isZh ? '本机局域网 IP：${_localIps.first}' : 'Local IP: ${_localIps.first}')
        : (isZh ? '未检测到局域网 Wi-Fi 连接' : 'No local Wi-Fi IPv4 detected');

    return SafeArea(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(20, 16, 20, 24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                Icon(Icons.radar_outlined, color: AppTheme.primaryBlue),
                const SizedBox(width: 10),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        isZh ? '局域网设备扫描（零公网直连）' : 'Local Network Discovery',
                        style: const TextStyle(
                          fontSize: 16,
                          fontWeight: FontWeight.bold,
                        ),
                      ),
                      const SizedBox(height: 2),
                      Text(
                        subnetHint,
                        style: TextStyle(
                          fontSize: 12,
                          color: AppTheme.textSecondary,
                        ),
                      ),
                    ],
                  ),
                ),
                IconButton(
                  onPressed: _scanning ? null : _runScan,
                  icon: const Icon(Icons.refresh_rounded),
                  tooltip: isZh ? '重新扫描' : 'Rescan',
                ),
              ],
            ),
            const SizedBox(height: 16),
            if (_scanning)
              Padding(
                padding: const EdgeInsets.symmetric(vertical: 32),
                child: Column(
                  children: [
                    const CircularProgressIndicator(),
                    const SizedBox(height: 14),
                    Text(
                      isZh
                          ? '正在并发探测当前 Wi-Fi 子网内的 Hoxi / GoPanel 受控节点...'
                          : 'Scanning local Wi-Fi subnet for Hoxi / GoPanel nodes...',
                      textAlign: TextAlign.center,
                      style: TextStyle(
                        fontSize: 13,
                        color: AppTheme.textSecondary,
                      ),
                    ),
                  ],
                ),
              )
            else if (_nodes.isEmpty)
              Padding(
                padding: const EdgeInsets.symmetric(vertical: 28),
                child: Column(
                  children: [
                    Icon(
                      Icons.wifi_find_rounded,
                      size: 40,
                      color: AppTheme.textSecondary,
                    ),
                    const SizedBox(height: 10),
                    Text(
                      isZh
                          ? '当前 Wi-Fi 子网未发现开放默认端口（5417 / 15470）的节点，请确认手机与设备连在同一局域网或直接使用扫码连接。'
                          : 'No nodes found on standard ports (5417 / 15470) in this Wi-Fi subnet.',
                      textAlign: TextAlign.center,
                      style: TextStyle(
                        fontSize: 13,
                        color: AppTheme.textSecondary,
                      ),
                    ),
                  ],
                ),
              )
            else
              ConstrainedBox(
                constraints: const BoxConstraints(maxHeight: 320),
                child: ListView.separated(
                  shrinkWrap: true,
                  itemCount: _nodes.length,
                  separatorBuilder: (_, _) => const SizedBox(height: 8),
                  itemBuilder: (context, index) {
                    final item = _nodes[index];
                    return ListTile(
                      shape: RoundedRectangleBorder(
                        borderRadius: BorderRadius.circular(12),
                        side: BorderSide(
                          color: AppTheme.primaryBlue.withValues(alpha: 0.25),
                        ),
                      ),
                      leading: CircleAvatar(
                        backgroundColor: AppTheme.primaryBlue.withValues(
                          alpha: 0.12,
                        ),
                        child: Icon(
                          _iconFor(item.kind),
                          color: AppTheme.primaryBlue,
                        ),
                      ),
                      title: Text(
                        item.title,
                        style: const TextStyle(fontWeight: FontWeight.w600),
                      ),
                      subtitle: Text(
                        item.subtitle,
                        style: TextStyle(
                          fontSize: 12,
                          color: AppTheme.textSecondary,
                        ),
                      ),
                      trailing: const Icon(
                        Icons.arrow_forward_ios_rounded,
                        size: 15,
                      ),
                      onTap: () => Navigator.of(context).pop(item),
                    );
                  },
                ),
              ),
          ],
        ),
      ),
    );
  }
}
