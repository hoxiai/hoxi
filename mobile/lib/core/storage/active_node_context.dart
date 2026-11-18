import 'node_type_spec.dart';

/// A request keeps the connection it started with, including its generation.
class ActiveNodeContext {
  final String? url;
  final String nodeType;
  final String? token;
  final String? e2eeKey;
  final String? entrance;
  final String? cookie;
  final int generation;

  const ActiveNodeContext({
    this.url,
    this.nodeType = 'gopanel',
    this.token,
    this.e2eeKey,
    this.entrance,
    this.cookie,
    this.generation = 0,
  });

  NodeTypeSpec get spec => NodeTypeSpec.resolve(nodeType);
  bool get isHoxi => spec.id == 'hoxi';
  bool get isGoPanel => spec.id == 'gopanel';
  bool get isDevNode => spec.isDevNode;
  bool get isAuxService => spec.isAuxService;
  bool get usesBearerAuth => spec.usesBearerAuth;
  bool get usesRawJson => spec.usesRawJson;
  bool get preservesTopLevelCodeMap => spec.preservesTopLevelCodeMap;
  bool get isConnected => url != null && url!.isNotEmpty;
}
