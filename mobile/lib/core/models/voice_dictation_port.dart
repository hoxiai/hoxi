import 'package:flutter_riverpod/flutter_riverpod.dart';

/// 编码会话输入框的语音听写抽象端口。
///
/// 让 `ai_workspace` 保持纯研发领域独立，不反向或跨层拉入商业语音管线、
/// 模型网关外发告知或业务生态连接器；完整版由 `main.dart` 注入实现，
/// 开源薄壳默认不挂载或按需注入系统级听写。
class VoiceDictationSnapshot {
  final bool available;
  final bool isRecording;
  final String liveTranscript;

  const VoiceDictationSnapshot({
    this.available = false,
    this.isRecording = false,
    this.liveTranscript = '',
  });
}

abstract interface class VoiceDictationPort {
  Future<void> startRecording();
  Future<String> stopRecording();
}

final voiceDictationSnapshotProvider = Provider<VoiceDictationSnapshot>(
  (ref) => const VoiceDictationSnapshot(),
);

final voiceDictationPortProvider = Provider<VoiceDictationPort?>(
  (ref) => null,
);
