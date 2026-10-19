class CodeQualityCheck {
  final String id;
  final String kind;
  final String label;
  final String command;
  final String workDir;
  final CodeQualityCheckResult? lastResult;

  const CodeQualityCheck({
    required this.id,
    required this.kind,
    required this.label,
    required this.command,
    required this.workDir,
    this.lastResult,
  });

  factory CodeQualityCheck.fromJson(Map<String, dynamic> json) {
    return CodeQualityCheck(
      id: json['id'] as String? ?? '',
      kind: json['kind'] as String? ?? '',
      label: json['label'] as String? ?? '',
      command: json['command'] as String? ?? '',
      workDir: json['workDir'] as String? ?? '',
      lastResult: json['lastResult'] is Map<String, dynamic>
          ? CodeQualityCheckResult.fromJson(
              json['lastResult'] as Map<String, dynamic>,
            )
          : null,
    );
  }

  CodeQualityCheck copyWith({
    String? id,
    String? kind,
    String? label,
    String? command,
    String? workDir,
    CodeQualityCheckResult? lastResult,
  }) {
    return CodeQualityCheck(
      id: id ?? this.id,
      kind: kind ?? this.kind,
      label: label ?? this.label,
      command: command ?? this.command,
      workDir: workDir ?? this.workDir,
      lastResult: lastResult ?? this.lastResult,
    );
  }
}

class CodeQualityCheckResult {
  final String checkId;
  final String status;
  final int exitCode;
  final int durationMs;
  final String output;
  final bool outputTruncated;
  final DateTime? startedAt;
  final DateTime? completedAt;
  final String revision;
  final bool current;

  const CodeQualityCheckResult({
    required this.checkId,
    required this.status,
    required this.exitCode,
    required this.durationMs,
    required this.output,
    required this.outputTruncated,
    this.startedAt,
    this.completedAt,
    required this.revision,
    required this.current,
  });

  factory CodeQualityCheckResult.fromJson(Map<String, dynamic> json) {
    return CodeQualityCheckResult(
      checkId: json['checkId'] as String? ?? '',
      status: json['status'] as String? ?? '',
      exitCode: json['exitCode'] as int? ?? 0,
      durationMs: json['durationMs'] as int? ?? 0,
      output: json['output'] as String? ?? '',
      outputTruncated: json['outputTruncated'] as bool? ?? false,
      startedAt: json['startedAt'] != null
          ? DateTime.tryParse(json['startedAt'].toString())
          : null,
      completedAt: json['completedAt'] != null
          ? DateTime.tryParse(json['completedAt'].toString())
          : null,
      revision: json['revision'] as String? ?? '',
      current: json['current'] as bool? ?? true,
    );
  }

  bool get isPassed => status == 'passed' || (exitCode == 0 && status != 'running' && status != 'failed');
  bool get isFailed => status == 'failed' || (status != 'running' && exitCode != 0);
  bool get isRunning => status == 'running';
}
