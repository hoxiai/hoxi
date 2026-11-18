import 'package:flutter/foundation.dart';

class ChatMessage {
  final String id;
  final String text;
  final bool isUser; // true: 用户发送, false: AI 回复
  final DateTime timestamp;
  final String reasoning;
  final String activity;
  final bool isStreaming;
  final String agentLabel;
  final int durationMs;
  final int totalTokens;
  final List<String> toolSteps;
  final String? imageDataUrl;

  bool get hasImage => imageDataUrl != null && imageDataUrl!.isNotEmpty;

  const ChatMessage({
    required this.id,
    required this.text,
    required this.isUser,
    required this.timestamp,
    this.reasoning = '',
    this.activity = '',
    this.isStreaming = false,
    this.agentLabel = '',
    this.durationMs = 0,
    this.totalTokens = 0,
    this.toolSteps = const [],
    this.imageDataUrl,
  });

  ChatMessage copyWith({
    String? id,
    String? text,
    bool? isUser,
    DateTime? timestamp,
    String? reasoning,
    String? activity,
    bool? isStreaming,
    String? agentLabel,
    int? durationMs,
    int? totalTokens,
    List<String>? toolSteps,
    String? imageDataUrl,
  }) {
    return ChatMessage(
      id: id ?? this.id,
      text: text ?? this.text,
      isUser: isUser ?? this.isUser,
      timestamp: timestamp ?? this.timestamp,
      reasoning: reasoning ?? this.reasoning,
      activity: activity ?? this.activity,
      isStreaming: isStreaming ?? this.isStreaming,
      agentLabel: agentLabel ?? this.agentLabel,
      durationMs: durationMs ?? this.durationMs,
      totalTokens: totalTokens ?? this.totalTokens,
      toolSteps: toolSteps ?? this.toolSteps,
      imageDataUrl: imageDataUrl ?? this.imageDataUrl,
    );
  }

  factory ChatMessage.fromAiMessageJson(Map<String, dynamic> json) {
    final role = (json['role'] ?? '').toString().toLowerCase();
    final rawSteps = json['toolSteps'];
    return ChatMessage(
      id: (json['id'] ?? '').toString(),
      text: (json['content'] ?? '').toString(),
      isUser: role == 'user',
      timestamp:
          DateTime.tryParse((json['createdAt'] ?? '').toString()) ??
          DateTime.now(),
      reasoning: (json['reasoning'] ?? '').toString(),
      durationMs: (json['durationMs'] as num?)?.toInt() ?? 0,
      totalTokens: (json['totalTokens'] as num?)?.toInt() ?? 0,
      toolSteps: rawSteps is List
          ? rawSteps
                .map((e) => e.toString().trim())
                .where((e) => e.isNotEmpty)
                .toList(growable: false)
          : const [],
      imageDataUrl: json['imageDataUrl']?.toString(),
    );
  }

  @override
  bool operator ==(Object other) {
    if (identical(this, other)) return true;
    return other is ChatMessage &&
        other.id == id &&
        other.text == text &&
        other.isUser == isUser &&
        other.timestamp == timestamp &&
        other.reasoning == reasoning &&
        other.activity == activity &&
        other.isStreaming == isStreaming &&
        other.agentLabel == agentLabel &&
        other.durationMs == durationMs &&
        other.totalTokens == totalTokens &&
        listEquals(other.toolSteps, toolSteps) &&
        other.imageDataUrl == imageDataUrl;
  }

  @override
  int get hashCode => Object.hash(
    id,
    text,
    isUser,
    timestamp,
    reasoning,
    activity,
    isStreaming,
    agentLabel,
    durationMs,
    totalTokens,
    Object.hashAll(toolSteps),
    imageDataUrl,
  );
}
