import '../../../core/network/api_client.dart';
import '../../../core/network/hoxi_node_probe.dart';
import '../../../core/storage/node_transport.dart';
import '../../../core/storage/server_connection.dart';

export '../../../core/storage/server_connection.dart'
    show defaultHoxiRelayControllerUrl;

/// 已与 GoPanel 配对、经反向隧道连入的 Hoxi 桌面电脑。
class HoxiRelayNode {
  final int nodeId;
  final String name;
  final String hostname;
  final String version;
  final bool online;
  final DateTime? lastSeenAt;

  const HoxiRelayNode({
    required this.nodeId,
    required this.name,
    this.hostname = '',
    this.version = '',
    this.online = false,
    this.lastSeenAt,
  });

  String get displayName {
    if (name.trim().isNotEmpty) return name.trim();
    if (hostname.trim().isNotEmpty) return hostname.trim();
    return 'Hoxi #$nodeId';
  }

  static HoxiRelayNode? fromJson(Map<String, dynamic> json) {
    final id = json['nodeId'];
    final nodeId = id is int ? id : int.tryParse('$id');
    if (nodeId == null || nodeId <= 0) return null;
    return HoxiRelayNode(
      nodeId: nodeId,
      name: '${json['name'] ?? ''}',
      hostname: '${json['hostname'] ?? ''}',
      version: '${json['version'] ?? ''}',
      online: json['status'] == 'online',
      lastSeenAt: DateTime.tryParse('${json['lastSeenAt'] ?? ''}'),
    );
  }
}

/// 读取某台 GoPanel 下已配对的 Hoxi 桌面电脑与远程隧道节点列表。
class HoxiRelayRepository {
  final ApiClient _apiClient;

  HoxiRelayRepository(this._apiClient);

  Future<List<HoxiRelayNode>> fetchNodes(ServerConnection gopanel) =>
      fetchNodesByUrl(
        controllerUrl: gopanel.url,
        entrance: gopanel.entrance,
        token: gopanel.token,
      );

  Future<List<HoxiRelayNode>> fetchNodesByUrl({
    required String controllerUrl,
    String entrance = '',
    String token = '',
    Duration timeout = const Duration(seconds: 4),
  }) async {
    final cleanBase = controllerUrl.trim().replaceAll(RegExp(r'/+$'), '');
    final response = await _apiClient
        .get<Map<String, dynamic>>(
          '$cleanBase/api/tunnel/hoxi-nodes',
          options: buildConnectionOptions(
            entrance: entrance,
            token: token,
            connectTimeout: timeout,
            receiveTimeout: timeout,
          ),
        )
        .timeout(timeout);
    return parseHoxiRelayNodes(response.data);
  }

  /// Queries saved GoPanel controllers and the configured default relay controller
  /// (when non-empty) to discover tunnel proxy URLs matching [conn].
  Future<List<String>> discoverRemoteRelayNodeUrls(
    ServerConnection conn,
    List<ServerConnection> savedServers,
  ) async {
    final discovered = <String>[];
    final defaultCtrl = defaultHoxiRelayControllerUrl
        .trim()
        .replaceAll(RegExp(r'/+$'), '');
    final controllers = <({String url, String entrance, String token})>[
      for (final s in savedServers)
        if (s.isGoPanel && s.token.trim().isNotEmpty)
          (
            url: s.url.trim().replaceAll(RegExp(r'/+$'), ''),
            entrance: s.entrance,
            token: s.token.trim(),
          ),
      if (defaultCtrl.isNotEmpty &&
          (conn.relayToken.trim().isNotEmpty ||
              ((isHoxiRelayUrl(conn.url) ||
                      conn.transports.any((t) => t.isRelay)) &&
                  conn.token.trim().isNotEmpty)))
        (
          url: defaultCtrl,
          entrance: conn.entrance,
          token: conn.relayToken.trim().isNotEmpty
              ? conn.relayToken.trim()
              : conn.token.trim(),
        ),
    ];
    final seen = <String>{};
    for (final ctrl in controllers) {
      if (!seen.add('${ctrl.url}|${ctrl.token}')) continue;
      try {
        final nodes = await fetchNodesByUrl(
          controllerUrl: ctrl.url,
          entrance: ctrl.entrance,
          token: ctrl.token,
          timeout: const Duration(seconds: 3),
        );
        final matched = matchHoxiRelayNode(nodes, nodeName: conn.name);
        if (matched != null) {
          discovered.add(buildHoxiRelayUrl(ctrl.url, matched.nodeId));
        }
      } catch (_) {}
    }
    return discovered;
  }
}

List<HoxiRelayNode> parseHoxiRelayNodes(Map<String, dynamic>? data) {
  final items = data?['items'];
  if (items is! List) return const [];
  return items
      .whereType<Map>()
      .map((item) => HoxiRelayNode.fromJson(item.cast<String, dynamic>()))
      .whereType<HoxiRelayNode>()
      .toList();
}

/// Matches a saved Hoxi node against a controller's `/api/tunnel/hoxi-nodes` list.
HoxiRelayNode? matchHoxiRelayNode(
  List<HoxiRelayNode> nodes, {
  String nodeName = '',
}) {
  if (nodes.isEmpty) return null;
  final online = nodes.where((n) => n.online).toList();
  final pool = online.isNotEmpty ? online : nodes;
  final cleanTarget = nodeName
      .trim()
      .replaceFirst(
        RegExp(
          r'^Hoxi(?:\s*(?:Work|Desktop|桌面端))?\s*·\s*',
          caseSensitive: false,
        ),
        '',
      )
      .trim()
      .toLowerCase();
  if (cleanTarget.isNotEmpty) {
    for (final node in pool) {
      final name = node.name.trim().toLowerCase();
      final host = node.hostname.trim().toLowerCase();
      if (name == cleanTarget ||
          host == cleanTarget ||
          (name.isNotEmpty && cleanTarget.contains(name)) ||
          (host.isNotEmpty && cleanTarget.contains(host))) {
        return node;
      }
    }
  }
  return pool.first;
}

/// Builds fallback relay URLs for pairing or reconnecting when configured.
List<String> buildPairingFallbackRelayUrls({
  required String primaryUrl,
  required String relayUrl,
  required Iterable<NodeTransport> transports,
}) {
  final tempConn = ServerConnection(
    id: 'temp',
    name: '',
    url: primaryUrl,
    token: '',
    nodeType: 'hoxi',
    lastConnectedAt: DateTime.fromMillisecondsSinceEpoch(0, isUtc: true),
    transports: [
      ...transports,
      if (relayUrl.trim().isNotEmpty)
        NodeTransport(url: relayUrl.trim(), kind: 'relay'),
    ],
  );
  return [for (final t in buildHoxiFallbackRelayTransports(tempConn)) t.url];
}

/// Finds a saved GoPanel token matching the controller origin of [url].
String resolveSavedGoPanelRelayToken(
  String url,
  List<ServerConnection> savedServers, {
  String fallbackToken = '',
}) {
  final controllerOrigin = hoxiRelayControllerUrl(url);
  if (controllerOrigin != null) {
    for (final server in savedServers) {
      if (server.isGoPanel &&
          server.url.trim().replaceAll(RegExp(r'/+$'), '') ==
              controllerOrigin &&
          server.token.trim().isNotEmpty) {
        return server.token.trim();
      }
    }
  }
  return fallbackToken;
}

/// Merges `/api/hoxi/bootstrap` relay metadata, direct peers, E2EE keys, and saved GoPanel tokens.
({
  List<NodeTransport> transports,
  String relayToken,
  String relayDeviceId,
  String e2eeKey,
})
mergeHoxiBootstrapRelay({
  required Map<String, dynamic> bootstrap,
  required Iterable<NodeTransport> baseTransports,
  required String currentRelayToken,
  String currentRelayDeviceId = '',
  String currentE2eeKey = '',
  required List<ServerConnection> savedServers,
}) {
  final extracted = extractHoxiBootstrapRelay(bootstrap);
  var relayToken = currentRelayToken.trim().isNotEmpty
      ? currentRelayToken.trim()
      : extracted.relayToken;
  final relayDeviceId = extracted.relayUrl.isNotEmpty
      ? extracted.relayDeviceId.trim()
      : currentRelayDeviceId.trim();
  final e2eeKey = extracted.e2eeKey.trim().isNotEmpty
      ? extracted.e2eeKey.trim()
      : currentE2eeKey.trim();
  final freshController = extracted.relayUrl.isNotEmpty
      ? hoxiRelayControllerUrl(extracted.relayUrl)
      : null;
  final freshNodeId = extracted.relayUrl.isNotEmpty
      ? extractHoxiRelayNodeId(extracted.relayUrl)
      : null;
  final filteredBase = <NodeTransport>[
    for (final t in baseTransports)
      if (!isRetiredHoxiRelayUrl(t.url) &&
          !(freshController != null &&
              freshNodeId != null &&
              hoxiRelayControllerUrl(t.url) == freshController &&
              extractHoxiRelayNodeId(t.url) != null &&
              extractHoxiRelayNodeId(t.url) != freshNodeId))
        t,
  ];
  final transports = NodeTransport.ordered([
    for (final p in extracted.peerUrls)
      NodeTransport(url: p, kind: isHoxiRelayUrl(p) ? 'relay' : 'lan'),
    if (extracted.relayUrl.isNotEmpty)
      NodeTransport(url: extracted.relayUrl, kind: 'relay'),
    ...filteredBase,
  ]);
  if (relayToken.isEmpty) {
    for (final t in transports.where((t) => t.isRelay)) {
      final inherited = resolveSavedGoPanelRelayToken(t.url, savedServers);
      if (inherited.isNotEmpty) {
        relayToken = inherited;
        break;
      }
    }
  }
  return (
    transports: transports,
    relayToken: relayToken,
    relayDeviceId: relayDeviceId,
    e2eeKey: e2eeKey,
  );
}
