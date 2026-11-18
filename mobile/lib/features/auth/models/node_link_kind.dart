import 'package:flutter_riverpod/flutter_riverpod.dart';

/// 连接受控设备时可选的目标类型（开源版聚焦 Hoxi 桌面端与自建 GoPanel 节点）。
enum NodeLinkKind {
  hoxi('hoxi'),
  gopanel('gopanel');

  const NodeLinkKind(this.id);

  /// 稳定标识，写进路由参数与二维码 nodeType，不随语言变化。
  final String id;

  /// 是否属于基础研发受控节点（Hoxi 桌面端 / GoPanel 面板）。
  bool get isDevNode =>
      this == NodeLinkKind.hoxi || this == NodeLinkKind.gopanel;

  /// 手机端已实现关联流程的类型。
  bool get isConnectable => true;

  static NodeLinkKind? resolve(String? value) {
    final normalized = (value ?? '').trim().toLowerCase();
    if (normalized.isEmpty) return null;
    for (final kind in NodeLinkKind.values) {
      if (kind.id == normalized) return kind;
    }
    return null;
  }
}

final supportedNodeLinkKindsProvider = Provider<List<NodeLinkKind>>(
  (ref) => NodeLinkKind.values,
);
