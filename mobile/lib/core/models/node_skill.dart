/// 节点端 Skill 目录。手机只读取描述符，不安装、不执行：执行仍由节点端 Agent
/// 与其审批门控负责。目录为空、读取失败与「节点没有 Skill」必须区分开，不能
/// 因为一次请求失败就宣称节点未安装任何 Skill。
class NodeSkill {
  final String key;
  final String name;
  final String description;
  final String runtime;
  final String source;
  final String sourceLabel;

  const NodeSkill({
    required this.key,
    required this.name,
    required this.description,
    this.runtime = '',
    this.source = '',
    this.sourceLabel = '',
  });

  factory NodeSkill.fromJson(Map<String, dynamic> json) {
    final source = (json['source'] ?? '').toString().trim();
    return NodeSkill(
      key: (json['key'] ?? '').toString().trim(),
      name: (json['name'] ?? '').toString().trim(),
      description: (json['description'] ?? '').toString().trim(),
      runtime: (json['runtime'] ?? '').toString().trim(),
      source: source,
      sourceLabel: (json['sourceLabel'] ?? source).toString().trim(),
    );
  }
}

/// 用户在聊天里挂载的节点 Skill。`key` 由节点按运行时与有效目录生成，
/// 手机只原样回传，不能自行拼路径或改名。
class NodeSkillSelection {
  final String key;
  final String name;

  const NodeSkillSelection({required this.key, required this.name});

  /// 只有读到目录并拿到 `skl_` 前缀的 key 才算有效选择。
  static NodeSkillSelection? of({String? key, String? name}) {
    final cleanKey = (key ?? '').trim();
    final cleanName = (name ?? '').trim();
    if (!cleanKey.startsWith('skl_') || cleanName.isEmpty) return null;
    return NodeSkillSelection(key: cleanKey, name: cleanName);
  }

  Map<String, dynamic> toJson() => {'key': key, 'name': name};
}

class NodeSkillCatalog {
  /// 是否成功读取到节点 Skill 目录。
  final bool loaded;

  /// 读取失败或节点不支持时给用户看的原因。
  final String notice;

  final String runtime;
  final List<NodeSkill> skills;

  const NodeSkillCatalog({
    required this.loaded,
    this.notice = '',
    this.runtime = '',
    this.skills = const [],
  });

  const NodeSkillCatalog.idle() : this(loaded: false);

  factory NodeSkillCatalog.unavailable(String notice) =>
      NodeSkillCatalog(loaded: false, notice: notice);

  factory NodeSkillCatalog.fromJson(Map<String, dynamic> json) {
    final skills = (json['skills'] as List<dynamic>? ?? const [])
        .whereType<Map>()
        .map((item) => NodeSkill.fromJson(item.cast<String, dynamic>()))
        .where((skill) => skill.name.isNotEmpty)
        .toList();
    final unsupported = (json['unsupportedReason'] ?? '').toString().trim();
    final supported = json['supported'] != false;
    if (!supported) {
      return NodeSkillCatalog(
        loaded: false,
        notice: unsupported.isEmpty ? '该设备运行时不支持 Skill 目录' : unsupported,
        runtime: (json['runtime'] ?? '').toString(),
      );
    }
    return NodeSkillCatalog(
      loaded: true,
      runtime: (json['runtime'] ?? '').toString(),
      skills: skills,
    );
  }

  bool get isEmpty => loaded && skills.isEmpty;

  /// 按名称解析挂载中的 Skill；同名多条或未读到目录时返回 null，
  /// 不能按列表首项猜一个 key。
  NodeSkillSelection? selectionFor(String name) {
    if (!loaded || name.trim().isEmpty) return null;
    final matches = skills.where((s) => s.name == name.trim()).toList();
    if (matches.length != 1) return null;
    return NodeSkillSelection.of(
      key: matches.single.key,
      name: matches.single.name,
    );
  }

  /// 名称是否已不在当前目录（重命名、删除或来源变化）。
  bool lacks(String name) =>
      loaded &&
      name.trim().isNotEmpty &&
      !skills.any((s) => s.name == name.trim());
}

/// 解析 `GET /api/hoxi/skills/list` 的返回体，字段缺失时按「未读到」处理。
NodeSkillCatalog parseNodeSkillCatalog(dynamic data) {
  if (data is Map) {
    return NodeSkillCatalog.fromJson(data.cast<String, dynamic>());
  }
  return NodeSkillCatalog.unavailable('返回格式无法解析');
}
