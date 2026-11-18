import 'ai_dev_session.dart';

class CodeTask {
  final int id;
  final int sessionId;
  final int projectId;
  final String title;
  final String agentName;
  final String providerModel;
  final int diffAdditions;
  final int diffDeletions;
  final String workDir;
  final String status;
  final DateTime? createdAt;
  final DateTime? updatedAt;

  const CodeTask({
    required this.id,
    required this.sessionId,
    required this.projectId,
    required this.title,
    required this.agentName,
    this.providerModel = '',
    this.diffAdditions = 0,
    this.diffDeletions = 0,
    required this.workDir,
    required this.status,
    required this.createdAt,
    required this.updatedAt,
  });

  CodeTask copyWith({
    int? id,
    int? sessionId,
    int? projectId,
    String? title,
    String? agentName,
    String? providerModel,
    int? diffAdditions,
    int? diffDeletions,
    String? workDir,
    String? status,
    DateTime? createdAt,
    DateTime? updatedAt,
  }) {
    return CodeTask(
      id: id ?? this.id,
      sessionId: sessionId ?? this.sessionId,
      projectId: projectId ?? this.projectId,
      title: title ?? this.title,
      agentName: agentName ?? this.agentName,
      providerModel: providerModel ?? this.providerModel,
      diffAdditions: diffAdditions ?? this.diffAdditions,
      diffDeletions: diffDeletions ?? this.diffDeletions,
      workDir: workDir ?? this.workDir,
      status: status ?? this.status,
      createdAt: createdAt ?? this.createdAt,
      updatedAt: updatedAt ?? this.updatedAt,
    );
  }

  factory CodeTask.fromJson(Map<String, dynamic> json) {
    final diffStats = extractSessionDiffStats(json);
    final agentName = (json['agentName'] ?? '').toString();
    return CodeTask(
      id: (json['id'] as num?)?.toInt() ?? 0,
      sessionId: (json['sessionId'] as num?)?.toInt() ?? 0,
      projectId: (json['projectId'] as num?)?.toInt() ?? 0,
      title: (json['title'] ?? '').toString(),
      agentName: agentName,
      providerModel: extractSessionModelFromMap(json, runtime: agentName),
      diffAdditions: diffStats.additions,
      diffDeletions: diffStats.deletions,
      workDir: (json['workDir'] ?? '').toString(),
      status: (json['status'] ?? '').toString(),
      createdAt: DateTime.tryParse((json['createdAt'] ?? '').toString()),
      updatedAt: DateTime.tryParse((json['updatedAt'] ?? '').toString()),
    );
  }
}
