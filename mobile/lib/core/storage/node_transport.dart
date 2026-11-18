/// One way to reach a Hoxi node.
///
/// A node can be reachable over several transports at once: the local network
/// (`lan`, direct and cheap) and a relay (`relay`, for when the phone is off the
/// LAN). The client prefers LAN and falls back to a relay, keeping the node
/// identity separate from its transport address.
class NodeTransport {
  final String url;
  final String kind; // 'lan' | 'relay'

  const NodeTransport({required this.url, required this.kind});

  bool get isLan => kind == 'lan';
  bool get isRelay => kind == 'relay';

  Map<String, dynamic> toJson() => {'url': url, 'kind': kind};

  static NodeTransport? fromJson(Object? json) {
    if (json is! Map) return null;
    final url = (json['url'] ?? '').toString().trim();
    if (url.isEmpty) return null;
    final kind = (json['kind'] ?? '').toString().trim().toLowerCase();
    return NodeTransport(url: url, kind: kind == 'relay' ? 'relay' : 'lan');
  }

  /// Orders transports for selection: LAN candidates before relays, preserving
  /// insertion order within each group and dropping duplicate urls.
  static List<NodeTransport> ordered(Iterable<NodeTransport> transports) {
    final seen = <String>{};
    final lan = <NodeTransport>[];
    final relay = <NodeTransport>[];
    for (final transport in transports) {
      if (transport.url.isEmpty || !seen.add(transport.url)) continue;
      (transport.isRelay ? relay : lan).add(transport);
    }
    return [...lan, ...relay];
  }

  /// The transports for a single saved url, used for connections imported before
  /// transports existed or built from a GoPanel relay node.
  static List<NodeTransport> single(String url, {required bool relay}) =>
      url.trim().isEmpty
      ? const []
      : [NodeTransport(url: url.trim(), kind: relay ? 'relay' : 'lan')];
}
