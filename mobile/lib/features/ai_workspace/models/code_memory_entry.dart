class CodeMemoryEntry {
  final int id;
  final int projectId;
  final String moduleKey;
  final String content;
  final String status;
  final DateTime? createdAt;
  final DateTime? updatedAt;

  const CodeMemoryEntry({
    required this.id,
    required this.projectId,
    required this.moduleKey,
    required this.content,
    required this.status,
    this.createdAt,
    this.updatedAt,
  });

  factory CodeMemoryEntry.fromJson(Map<String, dynamic> json) {
    return CodeMemoryEntry(
      id: json['ID'] as int? ?? json['id'] as int? ?? 0,
      projectId: json['project_id'] as int? ?? json['projectId'] as int? ?? 0,
      moduleKey: json['module_key'] as String? ?? json['moduleKey'] as String? ?? '',
      content: json['content'] as String? ?? '',
      status: json['status'] as String? ?? 'active',
      createdAt: json['CreatedAt'] != null || json['createdAt'] != null
          ? DateTime.tryParse(
              (json['CreatedAt'] ?? json['createdAt']).toString(),
            )
          : null,
      updatedAt: json['UpdatedAt'] != null || json['updatedAt'] != null
          ? DateTime.tryParse(
              (json['UpdatedAt'] ?? json['updatedAt']).toString(),
            )
          : null,
    );
  }
}

class CodeMemoryExtractionStatus {
  final int sessionId;
  final String status;
  final String lastError;
  final DateTime? extractedAt;

  const CodeMemoryExtractionStatus({
    required this.sessionId,
    required this.status,
    this.lastError = '',
    this.extractedAt,
  });

  factory CodeMemoryExtractionStatus.fromJson(Map<String, dynamic> json) {
    return CodeMemoryExtractionStatus(
      sessionId: json['session_id'] as int? ?? json['sessionId'] as int? ?? 0,
      status: json['status'] as String? ?? 'idle',
      lastError: json['last_error'] as String? ?? json['lastError'] as String? ?? '',
      extractedAt: json['extracted_at'] != null || json['extractedAt'] != null
          ? DateTime.tryParse(
              (json['extracted_at'] ?? json['extractedAt']).toString(),
            )
          : null,
    );
  }

  bool get isExtracting => status == 'extracting' || status == 'running';
  bool get isCompleted => status == 'completed' || status == 'success';
}
