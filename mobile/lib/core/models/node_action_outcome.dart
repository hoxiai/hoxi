/// 聊天里的节点动作与工作台会话 / 交付状态共用的结果口径。
/// 断网或超时不能写成成功或失败。
enum NodeActionOutcome {
  submitted,
  accepted,
  running,
  awaitingApproval,
  completed,
  failed,
  cancelled,
  unknown,
}

NodeActionOutcome nodeActionOutcomeFromError(Object error) {
  final text = error.toString().toLowerCase();
  if (text.contains('timeout') ||
      text.contains('超时') ||
      text.contains('socket') ||
      text.contains('connection') ||
      text.contains('网络') ||
      text.contains('outcome_unknown') ||
      text.contains('状态待确认') ||
      text.contains('结果待确认') ||
      text.contains('结果未知')) {
    return NodeActionOutcome.unknown;
  }
  return NodeActionOutcome.failed;
}

NodeActionOutcome nodeActionOutcomeFromSessionStatus(String status) {
  switch (status) {
    case 'awaiting_approval':
      return NodeActionOutcome.awaitingApproval;
    case 'running':
    case 'busy':
    case 'delivering':
      return NodeActionOutcome.running;
    case 'completed':
    case 'success':
    case 'idle':
      return NodeActionOutcome.accepted;
    case 'failed':
    case 'error':
      return NodeActionOutcome.failed;
    case 'cancelled':
    case 'canceled':
    case 'stopped':
      return NodeActionOutcome.cancelled;
    default:
      return status.isEmpty
          ? NodeActionOutcome.submitted
          : NodeActionOutcome.unknown;
  }
}

/// 节点真实返回的指令编号。Hoxi 适配器用本地时间戳冒充 id，不能拿来对账。
String? nodeInstructionReference({
  required int instructionId,
  required bool nodeAssigned,
}) {
  if (!nodeAssigned || instructionId <= 0) return null;
  return '工作台会话指令 #$instructionId';
}

String nodeActionOutcomeLabel(NodeActionOutcome outcome) {
  switch (outcome) {
    case NodeActionOutcome.submitted:
      return '已提交，尚未确认设备收到';
    case NodeActionOutcome.accepted:
      return '设备已接收';
    case NodeActionOutcome.running:
      return '执行中';
    case NodeActionOutcome.awaitingApproval:
      return '待审批';
    case NodeActionOutcome.completed:
      return '已完成';
    case NodeActionOutcome.failed:
      return '失败';
    case NodeActionOutcome.cancelled:
      return '已取消';
    case NodeActionOutcome.unknown:
      return '结果未知，未自动重试';
  }
}

/// 节点修改类动作（容器、证书、站点流水线、数据库同步等）的幂等回执与对账凭证。
class NodeOperationReceipt {
  final String clientRequestId;
  final String operationId;
  final NodeActionOutcome outcome;
  final bool deduplicated;

  const NodeOperationReceipt({
    this.clientRequestId = '',
    this.operationId = '',
    this.outcome = NodeActionOutcome.accepted,
    this.deduplicated = false,
  });

  bool get hasOperationId => operationId.isNotEmpty && operationId != '0';

  String? get referenceLabel {
    if (!hasOperationId) return null;
    return '节点操作回执 #$operationId';
  }

  factory NodeOperationReceipt.fromJson(
    Map<String, dynamic>? json, {
    String clientRequestId = '',
    NodeActionOutcome defaultOutcome = NodeActionOutcome.accepted,
  }) {
    if (json == null) {
      return NodeOperationReceipt(
        clientRequestId: clientRequestId,
        outcome: defaultOutcome,
      );
    }
    final rawId =
        json['operationId'] ??
        json['taskId'] ??
        json['recordId'] ??
        json['instructionId'] ??
        '';
    final opId = rawId.toString().trim();
    final rawStatus = (json['status'] ?? '').toString().trim();
    final outcome = rawStatus.isEmpty
        ? defaultOutcome
        : nodeActionOutcomeFromSessionStatus(rawStatus);
    final deduplicated = json['deduplicated'] == true;
    return NodeOperationReceipt(
      clientRequestId:
          (json['clientRequestId']?.toString().trim().isNotEmpty ?? false)
          ? json['clientRequestId'].toString().trim()
          : clientRequestId,
      operationId: opId == '0' ? '' : opId,
      outcome: outcome == NodeActionOutcome.unknown ? defaultOutcome : outcome,
      deduplicated: deduplicated,
    );
  }
}
