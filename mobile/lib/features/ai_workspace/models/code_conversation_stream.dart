import 'dart:convert';

import 'code_tool_step_summary.dart';

final RegExp _closedThinkBlockRe = RegExp(r'<think(?:ing)?>[\s\S]*?<\/think(?:ing)?>', caseSensitive: false);
final RegExp _unclosedThinkBlockRe = RegExp(r'<think(?:ing)?>[\s\S]*$', caseSensitive: false);
final RegExp _thinkTagRe = RegExp(r'<\/?think(?:ing)?>', caseSensitive: false);
final RegExp _partialClosingThinkSuffixRe = RegExp(r'<\/(?:t(?:h(?:i(?:n(?:k(?:i(?:n(?:g)?)?)?)?)?)?)?)?$', caseSensitive: false);
final RegExp _partialOpeningThinkPrefixRe = RegExp(r'^<(?:t(?:h(?:i(?:n(?:k(?:i(?:n(?:g)?)?)?)?)?)?)?)?$', caseSensitive: false);
final RegExp _openingThinkPrefixRe = RegExp(r'^<think(?:ing)?>', caseSensitive: false);
final RegExp _closingThinkRe = RegExp(r'<\/think(?:ing)?>', caseSensitive: false);
final RegExp _adjacentBoldHeadingsRe = RegExp(r'([^\s*])\*\*[ \t]*\*\*(?=[^\s*])');

/// Strips `<think>...</think>` and `<thinking>...</thinking>` blocks (including
/// unclosed trailing blocks) from assistant text so raw thinking tags never
/// leak into mobile chat bubbles or TTS playback.
String stripInlineThinking(String raw) {
  if (raw.isEmpty) return '';
  final withoutClosed = raw.replaceAll(_closedThinkBlockRe, '');
  final withoutUnclosed = withoutClosed.replaceAll(_unclosedThinkBlockRe, '');
  return withoutUnclosed.trimLeft();
}

/// Normalizes reasoning text by stripping stray `<think>` / `<thinking>` tags
/// and separating concatenated Codex bold headings (`**A****B**`).
String normalizeReasoningText(String raw) {
  if (raw.isEmpty) return '';
  return raw
      .replaceAll('[REDACTED]', '')
      .replaceAll(_thinkTagRe, '')
      .replaceAll(_partialClosingThinkSuffixRe, '')
      .replaceAllMapped(_adjacentBoldHeadingsRe, (m) => '${m[1]}**\n\n**');
}

class InlineThinkingDeltaSplit {
  const InlineThinkingDeltaSplit({
    this.textDelta = '',
    this.reasoningDelta = '',
  });

  final String textDelta;
  final String reasoningDelta;
}

/// Stateful streaming filter that splits leading `<think>` / `<thinking>` blocks
/// out of incremental text deltas, routing thinking tokens to `reasoningDelta`
/// and visible reply tokens to `textDelta`.
class InlineThinkingStreamFilter {
  String _buffer = '';
  bool _decided = false;
  bool _inThinking = false;
  bool _stripLeadingVisibleWhitespace = false;
  bool _reasoningEndsWithDoubleStar = false;

  String _formatReasoningChunk(String chunk) {
    if (chunk.isEmpty) return '';
    var normalized = normalizeReasoningText(chunk);
    if (_reasoningEndsWithDoubleStar &&
        normalized.startsWith('**') &&
        normalized.length > 2 &&
        normalized[2] != '*') {
      normalized = '\n\n$normalized';
    }
    _reasoningEndsWithDoubleStar = normalized.endsWith('**');
    return normalized;
  }

  InlineThinkingDeltaSplit consume(String delta) {
    if (delta.isEmpty) return const InlineThinkingDeltaSplit();
    if (_decided && !_inThinking) {
      if (_stripLeadingVisibleWhitespace) {
        final trimmed = delta.replaceFirst(RegExp(r'^\s+'), '');
        if (trimmed.isEmpty) return const InlineThinkingDeltaSplit();
        _stripLeadingVisibleWhitespace = false;
        return InlineThinkingDeltaSplit(textDelta: trimmed);
      }
      return InlineThinkingDeltaSplit(textDelta: delta);
    }

    _buffer += delta;
    final textOut = StringBuffer();
    final reasoningOut = StringBuffer();

    while (_buffer.isNotEmpty) {
      if (!_decided) {
        final trimmed = _buffer.trimLeft();
        if (trimmed.isEmpty) {
          if (_stripLeadingVisibleWhitespace) {
            _buffer = '';
          }
          break;
        }
        if (!trimmed.startsWith('<')) {
          _decided = true;
          _inThinking = false;
          final out = _stripLeadingVisibleWhitespace
              ? _buffer.replaceFirst(RegExp(r'^\s+'), '')
              : _buffer;
          _stripLeadingVisibleWhitespace = false;
          _buffer = '';
          textOut.write(out);
          break;
        }
        final openMatch = _openingThinkPrefixRe.firstMatch(trimmed);
        if (openMatch != null) {
          _decided = true;
          _inThinking = true;
          _buffer = trimmed.substring(openMatch.end);
          continue;
        }
        if (_partialOpeningThinkPrefixRe.hasMatch(trimmed)) {
          _buffer = trimmed;
          break;
        }
        _decided = true;
        _inThinking = false;
        final out = _stripLeadingVisibleWhitespace
            ? _buffer.replaceFirst(RegExp(r'^\s+'), '')
            : _buffer;
        _stripLeadingVisibleWhitespace = false;
        _buffer = '';
        textOut.write(out);
        break;
      }

      if (_inThinking) {
        final closeMatch = _closingThinkRe.firstMatch(_buffer);
        if (closeMatch != null) {
          final before = _buffer.substring(0, closeMatch.start);
          reasoningOut.write(_formatReasoningChunk(before));
          _buffer = _buffer.substring(closeMatch.end);
          _inThinking = false;
          _decided = false;
          _stripLeadingVisibleWhitespace = true;
          continue;
        }
        final partialSuffix = _partialClosingThinkSuffixRe.firstMatch(_buffer);
        if (partialSuffix != null) {
          final safe = _buffer.substring(0, partialSuffix.start);
          reasoningOut.write(_formatReasoningChunk(safe));
          _buffer = _buffer.substring(partialSuffix.start);
          break;
        }
        reasoningOut.write(_formatReasoningChunk(_buffer));
        _buffer = '';
        break;
      }
    }

    return InlineThinkingDeltaSplit(
      textDelta: textOut.toString(),
      reasoningDelta: reasoningOut.toString(),
    );
  }
}

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
  CodeConversationStreamParser({
    this.targetSessionId,
    this.targetSessionIntId,
    this.lastSeq = 0,
  });

  final String? targetSessionId;
  final int? targetSessionIntId;
  int lastSeq;
  String _buffer = '';
  final Set<String> _reasoningPartIds = <String>{};
  final Set<String> _userMessageIds = <String>{};
  final Set<String> _userPartIds = <String>{};
  final Map<String, StringBuffer> _emittedTextByMessageId =
      <String, StringBuffer>{};
  final Map<String, InlineThinkingStreamFilter> _inlineFiltersByPartId =
      <String, InlineThinkingStreamFilter>{};
  final Map<String, int> _partConsumedLengthById = <String, int>{};
  String _lastEmittedTextPartId = '';
  String _lastEmittedTextMessageId = '';
  String _lastEmittedReasoningPartId = '';

  static String formatStepNote(String raw) =>
      CodeToolStepSummary.formatStepNote(raw);

  static bool isReadTool(String lower) =>
      CodeToolStepSummary.isReadTool(lower);

  static bool isEditTool(String lower) =>
      CodeToolStepSummary.isEditTool(lower);

  static String extractToolStepSummary(
    String tool,
    Map<String, dynamic> state,
  ) => CodeToolStepSummary.extract(tool, state);

  static bool _isLikelyReasoningPartId(String partId) {
    if (partId.isEmpty) return false;
    return partId.contains('_reason_') ||
        partId.contains('_r_') ||
        partId.startsWith('prt_r');
  }

  static int _stablePositiveIntId(String raw) {
    var hash = 0x811c9dc5;
    for (final unit in utf8.encode(raw)) {
      hash ^= unit;
      hash = (hash * 0x01000193) & 0x7fffffff;
    }
    return hash == 0 ? 1 : hash;
  }

  static String _extractEventSessionId(Map<String, dynamic> props) {
    final direct = (props['sessionID'] ?? props['sessionId'] ?? '')
        .toString()
        .trim();
    if (direct.isNotEmpty) return direct;
    final part = props['part'];
    if (part is Map) {
      final partSid = (part['sessionID'] ?? part['sessionId'] ?? '')
          .toString()
          .trim();
      if (partSid.isNotEmpty) return partSid;
    }
    final info = props['info'];
    if (info is Map) {
      final infoSid = (info['sessionID'] ?? info['sessionId'] ?? '')
          .toString()
          .trim();
      if (infoSid.isNotEmpty) return infoSid;
      final rawId = (info['id'] ?? '').toString().trim();
      if (rawId.startsWith('ses_')) return rawId;
    }
    return '';
  }

  bool _matchesTargetSession(Map<String, dynamic> props) {
    final eventSid = _extractEventSessionId(props);
    if (eventSid.isEmpty) return true;
    final expectedRaw = targetSessionId?.trim() ?? '';
    if (expectedRaw.isNotEmpty) {
      return eventSid == expectedRaw;
    }
    final expectedInt = targetSessionIntId;
    if (expectedInt != null && expectedInt > 0) {
      return _stablePositiveIntId(eventSid) == expectedInt ||
          int.tryParse(eventSid) == expectedInt;
    }
    return true;
  }

  static String _extractErrorText(Object? rawError) {
    if (rawError is String && rawError.trim().isNotEmpty) {
      return rawError.trim();
    }
    if (rawError is Map) {
      final map = rawError.cast<String, dynamic>();
      final data = map['data'];
      if (data is Map && (data['message'] ?? '').toString().trim().isNotEmpty) {
        return data['message'].toString().trim();
      }
      final msg = (map['message'] ?? map['name'] ?? '').toString().trim();
      if (msg.isNotEmpty) return msg;
    }
    return rawError?.toString() ?? '执行出错';
  }

  List<CodeConversationStreamEvent> add(String chunk) {
    _buffer += chunk.replaceAll('\r\n', '\n');
    final events = <CodeConversationStreamEvent>[];
    var start = 0;
    var boundary = _buffer.indexOf('\n\n', start);
    while (boundary >= 0) {
      final frame = _buffer.substring(start, boundary);
      start = boundary + 2;
      final event = _parseFrame(frame);
      if (event != null) events.add(event);
      boundary = _buffer.indexOf('\n\n', start);
    }
    if (start > 0) {
      _buffer = start >= _buffer.length ? '' : _buffer.substring(start);
    }
    return events;
  }

  CodeConversationStreamEvent? _parseFrame(String frame) {
    var eventType = 'message';
    final data = <String>[];
    for (final line in frame.split('\n')) {
      if (line.startsWith(':')) continue;
      if (line.startsWith('id:')) {
        final parsedId = int.tryParse(line.substring(3).trim());
        if (parsedId != null && parsedId > lastSeq) {
          lastSeq = parsedId;
        }
        continue;
      }
      if (line.startsWith('event:')) eventType = line.substring(6).trim();
      if (line.startsWith('data:')) data.add(line.substring(5).trimLeft());
    }
    if (data.isEmpty) return null;
    try {
      final decoded = jsonDecode(data.join('\n'));
      if (decoded is! Map) return null;
      final rawJson = decoded.cast<String, dynamic>();
      final rawSeq = rawJson['seq'];
      if (rawSeq is num && rawSeq.toInt() > lastSeq) {
        lastSeq = rawSeq.toInt();
      }
      final json = rawJson['payload'] is Map
          ? (rawJson['payload'] as Map).cast<String, dynamic>()
          : rawJson;
      final innerSeq = json['seq'];
      if (innerSeq is num && innerSeq.toInt() > lastSeq) {
        lastSeq = innerSeq.toInt();
      }
      final resolvedType =
          (eventType == 'message' ? json['type'] : eventType)?.toString() ?? '';

      // Hoxi 全局状态快照与心跳只服务于桌面侧跨项目状态，不能当成会话事件：
      // 否则会把正在生成的对话误判为完成/出错。
      if (resolvedType == 'hoxi.status.snapshot' ||
          resolvedType == 'server.heartbeat') {
        return null;
      }
      if (resolvedType == 'hoxi.stream.resync') {
        _partConsumedLengthById.clear();
        _inlineFiltersByPartId.clear();
        return const CodeConversationStreamEvent(
          type: 'resync',
          runId: 0,
          content: '',
          status: 'running',
          activityKind: 'resync',
          activity: '',
          message: '',
        );
      }

      // 兼容 Hoxi / OpenCode 标准 SSE 事件格式: {type: '...', properties: {...}}
      if (json['properties'] is Map) {
        final props = (json['properties'] as Map).cast<String, dynamic>();
        if (!_matchesTargetSession(props)) {
          return null;
        }
        if (resolvedType == 'message.updated') {
          final info = props['info'] is Map
              ? (props['info'] as Map).cast<String, dynamic>()
              : const <String, dynamic>{};
          final msgId = (info['id'] ?? info['messageID'] ?? '')
              .toString()
              .trim();
          final role = (info['role'] ?? '').toString().trim().toLowerCase();
          if (msgId.isNotEmpty && role == 'user') {
            _userMessageIds.add(msgId);
            if (_lastEmittedTextMessageId == msgId) {
              _lastEmittedTextPartId = '';
              _lastEmittedTextMessageId = '';
            }
            final leaked = _emittedTextByMessageId.remove(msgId)?.toString();
            if (leaked != null && leaked.isNotEmpty) {
              return CodeConversationStreamEvent(
                type: 'strip_user_echo',
                runId: 0,
                content: leaked,
                status: 'running',
                activityKind: '',
                activity: '',
                message: '',
              );
            }
          }
          return null;
        }
        if (resolvedType == 'message.part.delta') {
          final partId = (props['partID'] ?? props['id'] ?? '').toString();
          final messageId = (props['messageID'] ?? props['messageId'] ?? '')
              .toString()
              .trim();
          final role = (props['role'] ?? '').toString().trim().toLowerCase();
          if (role == 'user' ||
              (messageId.isNotEmpty && _userMessageIds.contains(messageId)) ||
              (partId.isNotEmpty && _userPartIds.contains(partId))) {
            if (partId.isNotEmpty) _userPartIds.add(partId);
            return null;
          }
          final field = (props['field'] ?? '').toString().trim();
          if (field.isNotEmpty && field != 'text' && field != 'reasoning') {
            return null;
          }
          final delta = (props['delta'] ?? '').toString();
          if (partId.isNotEmpty) {
            _partConsumedLengthById[partId] =
                (_partConsumedLengthById[partId] ?? 0) + delta.length;
          }
          final isReasoning =
              field == 'reasoning' ||
              (partId.isNotEmpty &&
                  (_reasoningPartIds.contains(partId) ||
                      _isLikelyReasoningPartId(partId)));
          if (isReasoning) {
            if (partId.isNotEmpty) _reasoningPartIds.add(partId);
            var outReasoning = normalizeReasoningText(delta);
            if (outReasoning.isNotEmpty && partId.isNotEmpty) {
              if (_lastEmittedReasoningPartId.isNotEmpty &&
                  _lastEmittedReasoningPartId != partId &&
                  !outReasoning.startsWith('\n')) {
                outReasoning = '\n\n$outReasoning';
              }
              _lastEmittedReasoningPartId = partId;
            }
            return CodeConversationStreamEvent(
              type: 'activity', runId: 0, content: outReasoning, status: 'running',
              activityKind: 'thinking', activity: '正在深度思考...', message: '',
            );
          }
          final filter = _inlineFiltersByPartId.putIfAbsent(
            partId.isEmpty ? '_default' : partId,
            InlineThinkingStreamFilter.new,
          );
          final split = filter.consume(delta);
          if (split.textDelta.isNotEmpty) {
            var outText = split.textDelta;
            if (partId.isNotEmpty &&
                _lastEmittedTextPartId.isNotEmpty &&
                _lastEmittedTextPartId != partId &&
                !outText.startsWith('\n')) {
              outText = '\n\n$outText';
            }
            if (partId.isNotEmpty) {
              _lastEmittedTextPartId = partId;
              _lastEmittedTextMessageId = messageId;
            }
            if (messageId.isNotEmpty) {
              _emittedTextByMessageId
                  .putIfAbsent(messageId, StringBuffer.new)
                  .write(outText);
            }
            return CodeConversationStreamEvent(
              type: 'delta', runId: 0, content: outText, status: 'running',
              activityKind: 'writing', activity: '正在生成回复...', message: '',
            );
          }
          if (split.reasoningDelta.isNotEmpty) {
            return CodeConversationStreamEvent(
              type: 'activity', runId: 0, content: split.reasoningDelta, status: 'running',
              activityKind: 'thinking', activity: '正在深度思考...', message: '',
            );
          }
          return null;
        }
        if (resolvedType == 'message.part.updated') {
          final part = props['part'] is Map
              ? (props['part'] as Map).cast<String, dynamic>()
              : const <String, dynamic>{};
          final partId = (part['id'] ?? '').toString();
          final messageId = (part['messageID'] ?? part['messageId'] ??
                  props['messageID'] ?? props['messageId'] ?? '').toString().trim();
          final role = (part['role'] ?? props['role'] ?? '').toString().trim().toLowerCase();
          if (role == 'user' ||
              (messageId.isNotEmpty && _userMessageIds.contains(messageId)) ||
              (partId.isNotEmpty && _userPartIds.contains(partId))) {
            if (partId.isNotEmpty) _userPartIds.add(partId);
            return null;
          }
          final partType = (part['type'] ?? '').toString();
          if (partType == 'reasoning') {
            var reasoningDelta = '';
            if (partId.isNotEmpty) {
              _reasoningPartIds.add(partId);
              final fullReasoning = (part['text'] ?? '').toString();
              final consumed = _partConsumedLengthById[partId] ?? 0;
              if (fullReasoning.length < consumed) {
                _partConsumedLengthById[partId] = fullReasoning.length;
              } else if (fullReasoning.length > consumed) {
                reasoningDelta = normalizeReasoningText(
                  fullReasoning.substring(consumed),
                );
                _partConsumedLengthById[partId] = fullReasoning.length;
                if (reasoningDelta.isNotEmpty) {
                  if (_lastEmittedReasoningPartId.isNotEmpty &&
                      _lastEmittedReasoningPartId != partId &&
                      !reasoningDelta.startsWith('\n')) {
                    reasoningDelta = '\n\n$reasoningDelta';
                  }
                  _lastEmittedReasoningPartId = partId;
                }
              }
            }
            return CodeConversationStreamEvent(
              type: 'activity', runId: 0, content: reasoningDelta, status: 'running',
              activityKind: 'thinking', activity: '正在深度思考...', message: '',
            );
          }
          if (partType == 'text' && partId.isNotEmpty) {
            final fullText = (part['text'] ?? '').toString();
            final consumed = _partConsumedLengthById[partId] ?? 0;
            if (fullText.length < consumed) {
              _partConsumedLengthById[partId] = fullText.length;
              _inlineFiltersByPartId.remove(partId);
              if (fullText.isEmpty) {
                return const CodeConversationStreamEvent(
                  type: 'resync', runId: 0, content: '', status: 'running',
                  activityKind: 'resync', activity: '', message: '',
                );
              }
            }
            if (fullText.length > consumed) {
              final nextDelta = fullText.substring(consumed);
              _partConsumedLengthById[partId] = fullText.length;
              final filter = _inlineFiltersByPartId.putIfAbsent(
                partId,
                InlineThinkingStreamFilter.new,
              );
              final split = filter.consume(nextDelta);
              if (split.textDelta.isNotEmpty) {
                var outText = split.textDelta;
                if (_lastEmittedTextPartId.isNotEmpty &&
                    _lastEmittedTextPartId != partId &&
                    !outText.startsWith('\n')) {
                  outText = '\n\n$outText';
                }
                _lastEmittedTextPartId = partId;
                _lastEmittedTextMessageId = messageId;
                if (messageId.isNotEmpty) {
                  _emittedTextByMessageId
                      .putIfAbsent(messageId, StringBuffer.new)
                      .write(outText);
                }
                return CodeConversationStreamEvent(
                  type: 'delta', runId: 0, content: outText, status: 'running',
                  activityKind: 'writing', activity: '正在生成回复...', message: '',
                );
              }
              if (split.reasoningDelta.isNotEmpty) {
                return CodeConversationStreamEvent(
                  type: 'activity', runId: 0, content: split.reasoningDelta, status: 'running',
                  activityKind: 'thinking', activity: '正在深度思考...', message: '',
                );
              }
            }
          }
          if (partType == 'tool') {
            final tool = (part['tool'] ?? 'tool').toString();
            final state = part['state'] is Map
                ? (part['state'] as Map).cast<String, dynamic>()
                : const <String, dynamic>{};
            final status = (state['status'] ?? '').toString();
            // 原生 skill 工具调用是「Skill 已加载」的唯一证据；只读名称，
            // 不把模型口头声明或选择标签当成已加载。
            if (tool == 'skill') {
              final input = state['input'] is Map
                  ? (state['input'] as Map).cast<String, dynamic>()
                  : const <String, dynamic>{};
              final skillName = (input['name'] ?? '').toString().trim();
              final failed = status == 'error';
              final loaded = status == 'completed';
              return CodeConversationStreamEvent(
                type: loaded ? 'skill_loaded' : (failed ? 'skill_failed' : 'activity'),
                runId: 0, content: skillName,
                status: failed ? 'failed' : (loaded ? 'completed' : 'running'),
                activityKind: 'skill',
                activity: failed
                    ? 'Skill $skillName 加载失败'
                    : (loaded ? '已加载 Skill $skillName' : '正在加载 Skill $skillName'),
                message: (state['error'] ?? '').toString(),
              );
            }
            return CodeConversationStreamEvent(
              type: 'activity', runId: 0,
              content: extractToolStepSummary(tool, state),
              status: status == 'error' ? 'failed' : (status == 'completed' ? 'completed' : 'running'),
              activityKind: 'tool', activity: '正在执行 $tool',
              message: (state['error'] ?? '').toString(),
            );
          }
        }
        if (resolvedType == 'permission.updated' ||
            resolvedType == 'permission.asked') {
          return const CodeConversationStreamEvent(
            type: 'status', runId: 0, content: '', status: 'awaiting_approval',
            activityKind: 'approval_waiting', activity: '等待人工审批', message: '',
          );
        }
        if (resolvedType == 'permission.replied') {
          return const CodeConversationStreamEvent(
            type: 'status', runId: 0, content: '', status: 'running',
            activityKind: 'approval_resolved', activity: '审批已处理，继续执行', message: '',
          );
        }
        if (resolvedType == 'question.asked' || resolvedType == 'question.v2.asked') {
          final firstQ = (props['questions'] is List)
              ? (props['questions'] as List).whereType<Map>().firstOrNull
              : null;
          final qText = (firstQ?['question'] ?? firstQ?['header'] ?? '').toString().trim();
          return CodeConversationStreamEvent(
            type: 'status', runId: 0, content: qText, status: 'running',
            activityKind: 'question_waiting',
            activity: qText.isNotEmpty ? '等待回答：$qText' : '等待用户回答问题',
            message: '',
          );
        }
        if (resolvedType == 'question.replied' ||
            resolvedType == 'question.v2.replied' ||
            resolvedType == 'question.rejected' ||
            resolvedType == 'question.v2.rejected') {
          return const CodeConversationStreamEvent(
            type: 'status', runId: 0, content: '', status: 'running',
            activityKind: 'question_resolved', activity: '问题已回答，继续执行', message: '',
          );
        }
        if (resolvedType == 'session.status') {
          final rawStatus = props['status'];
          final statusType = (rawStatus is Map
                  ? (rawStatus['type'] ?? rawStatus['status'] ?? '')
                  : (rawStatus ?? ''))
              .toString()
              .toLowerCase()
              .trim();
          if (statusType == 'busy' ||
              statusType == 'running' ||
              statusType == 'executing') {
            return const CodeConversationStreamEvent(
              type: 'status', runId: 0, content: '', status: 'running',
              activityKind: 'running', activity: '正在生成回复...', message: '',
            );
          }
          if (statusType == 'idle' ||
              statusType == 'completed' ||
              statusType == 'stopped' ||
              statusType == 'aborted' ||
              statusType == 'cancelled') {
            _lastEmittedTextPartId = '';
            _lastEmittedTextMessageId = '';
            _lastEmittedReasoningPartId = '';
            return const CodeConversationStreamEvent(
              type: 'done', runId: 0, content: '', status: 'completed',
              activityKind: '', activity: '', message: '',
            );
          }
        }
        if (resolvedType == 'session.idle') {
          _lastEmittedTextPartId = '';
          _lastEmittedTextMessageId = '';
          _lastEmittedReasoningPartId = '';
          return const CodeConversationStreamEvent(
            type: 'done', runId: 0, content: '', status: 'completed',
            activityKind: '', activity: '', message: '',
          );
        }
        if (resolvedType == 'session.error') {
          final err = _extractErrorText(props['error']);
          return CodeConversationStreamEvent(
            type: 'error', runId: 0, content: '', status: 'failed',
            activityKind: '', activity: '', message: err,
          );
        }
        if (resolvedType == 'session.updated') {
          final info = props['info'] is Map
              ? (props['info'] as Map).cast<String, dynamic>()
              : props;
          final nextTitle = (info['title'] ?? '').toString().trim();
          if (nextTitle.isEmpty) return null;
          return CodeConversationStreamEvent(
            type: 'session_updated', runId: 0, content: nextTitle, status: '',
            activityKind: 'session_updated', activity: '', message: '',
          );
        }
        return null;
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
