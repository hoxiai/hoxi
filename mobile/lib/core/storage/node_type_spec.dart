/// 受控节点与外部服务的协议能力描述规范。
///
/// 将底层存储与网络拦截器从具体商业服务名中解耦：核心底座只关心节点是否属于
/// 研发受控节点（`hoxi` / `gopanel`）、是否使用 Bearer 鉴权、是否返回裸 JSON。
class NodeTypeSpec {
  final String id;
  final bool isDevNode;
  final bool usesBearerAuth;
  final bool usesRawJson;
  final bool preservesTopLevelCodeMap;

  const NodeTypeSpec({
    required this.id,
    this.isDevNode = false,
    this.usesBearerAuth = true,
    this.usesRawJson = true,
    this.preservesTopLevelCodeMap = false,
  });

  bool get isAuxService => !isDevNode;

  static const NodeTypeSpec hoxi = NodeTypeSpec(
    id: 'hoxi',
    isDevNode: true,
    usesBearerAuth: true,
    usesRawJson: true,
  );

  static const NodeTypeSpec gopanel = NodeTypeSpec(
    id: 'gopanel',
    isDevNode: true,
    usesBearerAuth: false,
    usesRawJson: false,
  );

  static final Map<String, NodeTypeSpec> _registered = <String, NodeTypeSpec>{
    hoxi.id: hoxi,
    gopanel.id: gopanel,
  };

  static void register(NodeTypeSpec spec) {
    _registered[spec.id.trim().toLowerCase()] = spec;
  }

  static NodeTypeSpec resolve(String rawNodeType) {
    final normalized = rawNodeType.trim().toLowerCase();
    final matched = _registered[normalized];
    if (matched != null) return matched;
    if (normalized.isEmpty) return gopanel;
    return NodeTypeSpec(
      id: normalized,
      isDevNode: false,
      usesBearerAuth: true,
      usesRawJson: true,
    );
  }

  static String normalizeNodeType(String rawNodeType) {
    final normalized = rawNodeType.trim().toLowerCase();
    if (_registered.containsKey(normalized)) return normalized;
    if (RegExp(r'^[a-z][a-z0-9_-]{1,23}$').hasMatch(normalized) &&
        normalized != 'lan_device' &&
        normalized != 'unknown') {
      return normalized;
    }
    return gopanel.id;
  }
}
