import 'package:dio/dio.dart';
import 'package:flutter/material.dart';

import 'api_exception.dart';

/// 工作台顶栏的连接标记。保存了地址不等于在线。
enum NodeLinkPhase { none, selected, connecting, online, unreachable, credentialExpired }

class NodeLinkStatus {
  final NodeLinkPhase phase;

  const NodeLinkStatus(this.phase);

  static const none = NodeLinkStatus(NodeLinkPhase.none);

  bool get isOnline => phase == NodeLinkPhase.online;

  Color get color {
    switch (phase) {
      case NodeLinkPhase.online:
        return const Color(0xFF10B981);
      case NodeLinkPhase.connecting:
        return const Color(0xFFF59E0B);
      case NodeLinkPhase.credentialExpired:
        return const Color(0xFFEF4444);
      case NodeLinkPhase.unreachable:
        return const Color(0xFF94A3B8);
      case NodeLinkPhase.selected:
      case NodeLinkPhase.none:
        return const Color(0xFF94A3B8);
    }
  }

  static NodeLinkPhase phaseFromProbe({
    required bool hasSavedUrl,
    required bool probing,
    required Object? error,
    required bool reachable,
  }) {
    if (!hasSavedUrl) return NodeLinkPhase.none;
    if (probing) return NodeLinkPhase.connecting;
    if (isCredentialFailure(error)) return NodeLinkPhase.credentialExpired;
    if (reachable) return NodeLinkPhase.online;
    if (error != null) return NodeLinkPhase.unreachable;
    return NodeLinkPhase.selected;
  }

  static bool isCredentialFailure(Object? error) {
    if (error is DioException) {
      final code = error.response?.statusCode;
      return code == 401 || code == 403;
    }
    if (error is ApiException) {
      return error.code == 401 || error.code == 403;
    }
    return false;
  }
}
