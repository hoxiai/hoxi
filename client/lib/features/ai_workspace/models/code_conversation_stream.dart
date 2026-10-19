import 'dart:convert';

class CodeConversationStreamEvent {
  const CodeConversationStreamEvent({
    required this.type,
    required this.runId,
    required this.content,
    required this.status,
    required this.activityKind,
    required this.activity,
    required this.message,
  });

  final String type;
  final int runId;
  final String content;
  final String status;
  final String activityKind;
  final String activity;
  final String message;

  factory CodeConversationStreamEvent.fromJson(Map<String, dynamic> json) {
    return CodeConversationStreamEvent(
      type: (json['type'] ?? '').toString(),
      runId: (json['runId'] as num?)?.toInt() ?? 0,
      content: (json['content'] ?? '').toString(),
      status: (json['status'] ?? '').toString(),
      activityKind: (json['activityKind'] ?? '').toString(),
      activity: (json['activity'] ?? '').toString(),
      message: (json['message'] ?? '').toString(),
    );
  }
}

class CodeConversationStreamParser {
  String _buffer = '';

  List<CodeConversationStreamEvent> add(String chunk) {
    _buffer += chunk.replaceAll('\r\n', '\n');
    final events = <CodeConversationStreamEvent>[];
    var boundary = _buffer.indexOf('\n\n');
    while (boundary >= 0) {
      final frame = _buffer.substring(0, boundary);
      _buffer = _buffer.substring(boundary + 2);
      final event = _parseFrame(frame);
      if (event != null) events.add(event);
      boundary = _buffer.indexOf('\n\n');
    }
    return events;
  }

  CodeConversationStreamEvent? _parseFrame(String frame) {
    var eventType = 'message';
    final data = <String>[];
    for (final line in frame.split('\n')) {
      if (line.startsWith(':')) continue;
      if (line.startsWith('event:')) eventType = line.substring(6).trim();
      if (line.startsWith('data:')) data.add(line.substring(5).trimLeft());
    }
    if (data.isEmpty) return null;
    try {
      final decoded = jsonDecode(data.join('\n'));
      if (decoded is! Map) return null;
      final json = decoded.cast<String, dynamic>();
      final resolvedType =
          (eventType == 'message' ? json['type'] : eventType)?.toString() ?? '';

      // 兼容 Hoxi / OpenCode 标准 SSE 事件格式: {type: '...', properties: {...}}
      if (json['properties'] is Map) {
        final props = (json['properties'] as Map).cast<String, dynamic>();
        if (resolvedType == 'message.part.delta') {
          final delta = (props['delta'] ?? '').toString();
          return CodeConversationStreamEvent(
            type: 'delta',
            runId: 0,
            content: delta,
            status: 'running',
            activityKind: 'writing',
            activity: '正在生成回复...',
            message: '',
          );
        }
        if (resolvedType == 'message.part.updated') {
          final part = props['part'] is Map
              ? (props['part'] as Map).cast<String, dynamic>()
              : const <String, dynamic>{};
          final partType = (part['type'] ?? '').toString();
          if (partType == 'tool') {
            final tool = (part['tool'] ?? 'tool').toString();
            return CodeConversationStreamEvent(
              type: 'activity',
              runId: 0,
              content: '',
              status: 'running',
              activityKind: 'tool',
              activity: '正在执行 $tool',
              message: '',
            );
          }
        }
        if (resolvedType == 'permission.updated' ||
            resolvedType == 'permission.asked') {
          return const CodeConversationStreamEvent(
            type: 'status',
            runId: 0,
            content: '',
            status: 'awaiting_approval',
            activityKind: 'approval_waiting',
            activity: '等待人工审批',
            message: '',
          );
        }
        if (resolvedType == 'session.idle' ||
            (resolvedType == 'session.status' &&
                props['status'] is Map &&
                props['status']['type'] == 'idle')) {
          return const CodeConversationStreamEvent(
            type: 'done',
            runId: 0,
            content: '',
            status: 'completed',
            activityKind: '',
            activity: '',
            message: '',
          );
        }
        if (resolvedType == 'session.error') {
          final err = (props['error'] ?? '执行出错').toString();
          return CodeConversationStreamEvent(
            type: 'error',
            runId: 0,
            content: '',
            status: 'failed',
            activityKind: '',
            activity: '',
            message: err,
          );
        }
      }

      return CodeConversationStreamEvent.fromJson({
        ...json,
        'type': resolvedType,
      });
    } catch (_) {
      return null;
    }
  }
}
