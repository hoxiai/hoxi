class ServerConnection {
  final String id;
  final String name; // 别名，比如 "香港测试服务器" 或 "Hoxi 本地工作台"
  final String url;
  final String entrance;
  final String token;
  final String nodeType; // 'gopanel' | 'hoxi'
  final DateTime lastConnectedAt;

  ServerConnection({
    required this.id,
    required this.name,
    required this.url,
    this.entrance = '',
    required this.token,
    this.nodeType = 'gopanel',
    required this.lastConnectedAt,
  });

  bool get isHoxi => nodeType.trim().toLowerCase() == 'hoxi';
  bool get isGoPanel => !isHoxi;

  Map<String, dynamic> toJson() {
    return {
      'id': id,
      'name': name,
      'url': url,
      'nodeType': nodeType,
      'hasEntrance': entrance.isNotEmpty,
      'lastConnectedAt': lastConnectedAt.toIso8601String(),
    };
  }

  factory ServerConnection.fromJson(Map<String, dynamic> json) {
    final rawNodeType = (json['nodeType'] as String? ?? '').trim().toLowerCase();
    return ServerConnection(
      id: json['id'] as String,
      name: json['name'] as String,
      url: json['url'] as String,
      entrance: json['entrance'] as String? ?? '',
      token: json['token'] as String? ?? '',
      nodeType: rawNodeType == 'hoxi' ? 'hoxi' : 'gopanel',
      lastConnectedAt: DateTime.parse(json['lastConnectedAt'] as String),
    );
  }
}

String replaceServerUrlPort(String serverUrl, int port) {
  final uri = Uri.parse(serverUrl);
  return uri.replace(port: port).toString().replaceFirst(RegExp(r'/$'), '');
}
