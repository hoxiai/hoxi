import 'hoxi_relay_defaults.dart';
import 'node_transport.dart';
import 'node_type_spec.dart';

export 'hoxi_relay_defaults.dart'
    show
        defaultHoxiRelayControllerUrl,
        isRetiredHoxiRelayUrl,
        migrateRetiredHoxiRelayUrl;

class ServerConnection {
  final String id;

  /// Non-null only for account-restored copies. Hidden outside this site/user.
  final String? accountScope;
  final String name;
  final String url;
  final String entrance;
  final String token;

  /// Optional distinct token for relay transports (`https://<relay>`), kept
  /// separate from the cleartext LAN pairing [token] so a LAN leak cannot
  /// authorize requests over the public relay ingress.
  final String relayToken;

  /// Optional stable per-device relay identifier sent via `X-Hoxi-Device-Id`
  /// when multiple desktops share a single account relay hostname.
  final String relayDeviceId;

  /// Optional 256-bit hex master key for end-to-end payload encryption (`hoxi-e2ee-v1`).
  /// Persisted in secure storage only; never written to SharedPreferences JSON.
  final String e2eeKey;
  final String nodeType;
  final DateTime lastConnectedAt;

  /// Ways to reach this node, most-preferred first. Empty for GoPanel nodes and
  /// for Hoxi nodes saved before transports existed (the resolver falls back to
  /// a single transport derived from [url]).
  final List<NodeTransport> transports;

  ServerConnection({
    required this.id,
    this.accountScope,
    required this.name,
    required this.url,
    this.entrance = '',
    required this.token,
    this.relayToken = '',
    this.relayDeviceId = '',
    this.e2eeKey = '',
    this.nodeType = 'gopanel',
    required this.lastConnectedAt,
    this.transports = const [],
  });

  NodeTypeSpec get spec => NodeTypeSpec.resolve(nodeType);
  bool get isHoxi => spec.id == 'hoxi';
  bool get isGoPanel => spec.id == 'gopanel';
  bool get isDevNode => spec.isDevNode;
  bool get isAuxService => spec.isAuxService;

  /// Whether the node's currently active [url] is a relay transport.
  bool get isUsingRelay =>
      isHoxi &&
      (isHoxiRelayUrl(url) ||
          transports.any((t) => t.url == url && t.isRelay));

  /// Returns the credential that authenticates [targetUrl] for this node.
  /// Uses [relayToken] on relay transports when present, falling back to [token].
  String tokenForUrl(String targetUrl) {
    final trimmedRelay = relayToken.trim();
    if (trimmedRelay.isEmpty) return token;
    final normalized = targetUrl.trim();
    final isRelay =
        transports.any((t) => t.url == normalized && t.isRelay) ||
        isHoxiRelayUrl(normalized);
    return isRelay ? trimmedRelay : token;
  }

  /// A copy with the given fields replaced; [transports], [relayToken],
  /// [relayDeviceId], and [e2eeKey] are preserved unless explicitly overridden,
  /// so callers never silently drop them.
  ServerConnection copyWith({
    String? id,
    String? accountScope,
    bool clearAccountScope = false,
    String? name,
    String? url,
    String? entrance,
    String? token,
    String? relayToken,
    String? relayDeviceId,
    String? e2eeKey,
    String? nodeType,
    DateTime? lastConnectedAt,
    List<NodeTransport>? transports,
  }) {
    return ServerConnection(
      id: id ?? this.id,
      accountScope: clearAccountScope
          ? null
          : accountScope ?? this.accountScope,
      name: name ?? this.name,
      url: url ?? this.url,
      entrance: entrance ?? this.entrance,
      token: token ?? this.token,
      relayToken: relayToken ?? this.relayToken,
      relayDeviceId: relayDeviceId ?? this.relayDeviceId,
      e2eeKey: e2eeKey ?? this.e2eeKey,
      nodeType: nodeType ?? this.nodeType,
      lastConnectedAt: lastConnectedAt ?? this.lastConnectedAt,
      transports: transports ?? this.transports,
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'id': id,
      if (accountScope != null) 'accountScope': accountScope,
      'name': name,
      'url': url,
      'nodeType': nodeType,
      'hasEntrance': entrance.isNotEmpty,
      if (relayDeviceId.trim().isNotEmpty)
        'relayDeviceId': relayDeviceId.trim(),
      'lastConnectedAt': lastConnectedAt.toIso8601String(),
      if (transports.isNotEmpty)
        'transports': transports.map((t) => t.toJson()).toList(),
    };
  }

  factory ServerConnection.fromJson(Map<String, dynamic> json) {
    final id = (json['id'] ?? '').toString().trim();
    final url = (json['url'] ?? '').toString().trim();
    if (id.isEmpty || url.isEmpty) {
      throw const FormatException('Invalid ServerConnection JSON');
    }
    final name = (json['name'] ?? '').toString().trim();
    final rawNodeType = (json['nodeType'] ?? '')
        .toString()
        .trim()
        .toLowerCase();
    if (rawNodeType == 'lan_device') {
      throw const FormatException(
        'Unsupported legacy lan_device ServerConnection',
      );
    }
    final rawTransports = json['transports'];
    final transports = rawTransports is List
        ? rawTransports
              .map(NodeTransport.fromJson)
              .whereType<NodeTransport>()
              .toList()
        : <NodeTransport>[];
    final rawConnectedAt = (json['lastConnectedAt'] ?? '').toString().trim();
    final lastConnectedAt =
        DateTime.tryParse(rawConnectedAt) ??
        DateTime.fromMillisecondsSinceEpoch(0, isUtc: true);
    return ServerConnection(
      id: id,
      accountScope: json['accountScope'] is String
          ? json['accountScope'] as String
          : null,
      name: name.isEmpty ? url : name,
      url: url,
      entrance: (json['entrance'] ?? '').toString(),
      token: (json['token'] ?? '').toString(),
      relayToken: (json['relayToken'] ?? '').toString(),
      relayDeviceId: (json['relayDeviceId'] ?? '').toString().trim(),
      e2eeKey: (json['e2eeKey'] ?? '').toString(),
      nodeType: NodeTypeSpec.normalizeNodeType(rawNodeType),
      lastConnectedAt: lastConnectedAt,
      transports: transports,
    );
  }
}

String replaceServerUrlPort(String serverUrl, int port) {
  final uri = Uri.parse(serverUrl);
  return uri.replace(port: port).toString().replaceFirst(RegExp(r'/$'), '');
}

/// Hoxi 桌面或内网 GoPanel 经公网 GoPanel 反向隧道访问的中继地址：
/// - Hoxi 桌面：`<gopanel>/api/tunnel/hoxi-nodes/<nodeId>/proxy`
/// - 内网 GoPanel：`<gopanel>/api/tunnel/gopanel-nodes/<nodeId>/proxy`
final RegExp _hoxiRelayPath = RegExp(
  r'^/api/tunnel/(?:hoxi|gopanel)-nodes/(\d+)/proxy$',
);

String buildHoxiRelayUrl(String gopanelUrl, int nodeId) =>
    '${gopanelUrl.replaceFirst(RegExp(r'/+$'), '')}/api/tunnel/hoxi-nodes/$nodeId/proxy';

String buildGoPanelRelayUrl(String gopanelUrl, int nodeId) =>
    '${gopanelUrl.replaceFirst(RegExp(r'/+$'), '')}/api/tunnel/gopanel-nodes/$nodeId/proxy';

bool isHoxiRelayHost(String host) {
  final lower = host.trim().toLowerCase();
  if (lower.isEmpty) return false;
  for (final suffix in defaultHoxiRelayHosts) {
    final clean = suffix.trim().toLowerCase();
    if (clean.isEmpty) continue;
    if (lower == clean || lower.endsWith('.$clean')) {
      return true;
    }
  }
  return false;
}

bool isHoxiRelayUrl(String url) {
  final uri = Uri.tryParse(url.trim());
  if (uri == null || !uri.hasAuthority) return false;
  if (_hoxiRelayPath.hasMatch(uri.path)) return true;
  if (isHoxiRelayHost(uri.host)) {
    return uri.path.isEmpty ||
        uri.path == '/' ||
        uri.path.startsWith('/api/tunnel/');
  }
  return false;
}

/// Whether [url] is a shared-host Hoxi relay (not a LAN address and not a
/// `/api/tunnel/.../proxy` path that already encodes a single node ID) that
/// accepts `X-Hoxi-Device-Id` for multi-device routing.
bool supportsHoxiRelayDeviceHeader(String url) =>
    isHoxiRelayUrl(url) && extractHoxiRelayNodeId(url) == null;

int? extractHoxiRelayNodeId(String url) {
  final uri = Uri.tryParse(url.trim());
  if (uri == null) return null;
  final match = _hoxiRelayPath.hasMatch(uri.path)
      ? _hoxiRelayPath.firstMatch(uri.path)
      : null;
  if (match == null) return null;
  final id = int.tryParse(match.group(1) ?? '');
  return (id != null && id > 0) ? id : null;
}

/// 中继地址所属 GoPanel / 中继主控的根地址；非中继地址返回 null。
String? hoxiRelayControllerUrl(String url) {
  final uri = Uri.tryParse(url.trim());
  if (uri == null || !uri.hasAuthority || !isHoxiRelayUrl(url)) {
    return null;
  }
  return uri
      .replace(path: '', query: null, fragment: null)
      .toString()
      .replaceFirst(RegExp(r'/$'), '');
}

/// Builds fallback relay transports for a Hoxi [node] when a default relay controller is configured.
List<NodeTransport> buildHoxiFallbackRelayTransports(
  ServerConnection node, {
  String? controllerUrl,
  Iterable<String> extraRelayUrls = const [],
}) {
  if (!node.isHoxi) return const [];
  final defaultCtrl = defaultHoxiRelayControllerUrl
      .trim()
      .replaceAll(RegExp(r'/+$'), '');
  final cleanController = (controllerUrl ?? '')
      .trim()
      .replaceAll(RegExp(r'/+$'), '');
  final effectiveController = cleanController.isNotEmpty
      ? cleanController
      : defaultCtrl;
  final candidates = <NodeTransport>[];
  final urlsToInspect = <String>{
    node.url,
    for (final t in node.transports) t.url,
  };
  for (final raw in urlsToInspect) {
    final nodeId = extractHoxiRelayNodeId(raw);
    if (nodeId != null && nodeId > 0) {
      if (effectiveController.isNotEmpty) {
        candidates.add(
          NodeTransport(
            url: buildHoxiRelayUrl(effectiveController, nodeId),
            kind: 'relay',
          ),
        );
      }
      if (defaultCtrl.isNotEmpty && effectiveController != defaultCtrl) {
        candidates.add(
          NodeTransport(
            url: buildHoxiRelayUrl(defaultCtrl, nodeId),
            kind: 'relay',
          ),
        );
      }
    }
  }
  for (final extra in extraRelayUrls) {
    final trimmed = extra.trim().replaceAll(RegExp(r'/+$'), '');
    if (trimmed.isNotEmpty && !isRetiredHoxiRelayUrl(trimmed)) {
      candidates.add(NodeTransport(url: trimmed, kind: 'relay'));
    }
  }
  return NodeTransport.ordered(candidates);
}

/// The saved relay connection a push for [nodeId] from [panelUrl] refers to.
ServerConnection? findHoxiRelayServer(
  List<ServerConnection> servers, {
  required String panelUrl,
  required int nodeId,
}) {
  if (nodeId <= 0 || panelUrl.trim().isEmpty) return null;
  final expected = buildHoxiRelayUrl(panelUrl.trim(), nodeId);
  return servers
      .where(
        (s) =>
            s.isHoxi &&
            (s.url == expected || s.transports.any((t) => t.url == expected)),
      )
      .firstOrNull;
}
