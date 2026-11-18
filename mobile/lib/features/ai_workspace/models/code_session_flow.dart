/// Code 会话视角的交付状态：项目交付流程、这次交付能否生成预览、最近一次交付执行。
class CodeSessionFlow {
  final bool available;
  final int flowId;
  final String flowName;
  final bool autoStart;
  final bool canStart;

  /// 交付刚完成、后台正在自动生成预览：显示进行中并短时刷新，不给出生成按钮
  final bool autoStartPending;
  final CodeFlowRun? run;

  const CodeSessionFlow({
    required this.available,
    required this.flowId,
    required this.flowName,
    required this.autoStart,
    required this.canStart,
    this.autoStartPending = false,
    required this.run,
  });

  bool get visible =>
      available && (run != null || canStart || autoStartPending);

  factory CodeSessionFlow.fromJson(Map<String, dynamic> json) {
    final run = json['run'];
    return CodeSessionFlow(
      available: json['available'] == true,
      flowId: (json['flowId'] as num?)?.toInt() ?? 0,
      flowName: (json['flowName'] ?? '').toString(),
      autoStart: json['autoStart'] == true,
      canStart: json['canStart'] == true,
      autoStartPending: json['autoStartPending'] == true,
      run: run is Map
          ? CodeFlowRun.fromJson(run.cast<String, dynamic>())
          : null,
    );
  }
}

class CodeFlowRun {
  final int id;
  final String version;
  final String status;
  final String currentStage;
  final String errorSummary;
  final String previewUrl;
  final List<CodeFlowEnvironment> environments;
  final bool deployable;
  final String deployBlocked;

  const CodeFlowRun({
    required this.id,
    required this.version,
    required this.status,
    required this.currentStage,
    required this.errorSummary,
    required this.previewUrl,
    required this.environments,
    required this.deployable,
    required this.deployBlocked,
  });

  bool get isActive => status == 'queued' || status == 'running';

  factory CodeFlowRun.fromJson(Map<String, dynamic> json) {
    return CodeFlowRun(
      id: (json['id'] as num?)?.toInt() ?? 0,
      version: (json['version'] ?? '').toString(),
      status: (json['status'] ?? '').toString(),
      currentStage: (json['currentStage'] ?? '').toString(),
      errorSummary: (json['errorSummary'] ?? '').toString(),
      previewUrl: (json['previewUrl'] ?? '').toString(),
      environments: (json['environments'] as List<dynamic>? ?? const [])
          .whereType<Map>()
          .map(
            (item) =>
                CodeFlowEnvironment.fromJson(item.cast<String, dynamic>()),
          )
          .toList(),
      deployable: json['deployable'] == true,
      deployBlocked: (json['deployBlocked'] ?? '').toString(),
    );
  }
}

class CodeFlowEnvironment {
  final int id;
  final String name;
  final String websiteName;
  final String state;
  final bool current;

  const CodeFlowEnvironment({
    required this.id,
    required this.name,
    required this.websiteName,
    required this.state,
    required this.current,
  });

  bool get isProduction => name == 'production';

  factory CodeFlowEnvironment.fromJson(Map<String, dynamic> json) {
    return CodeFlowEnvironment(
      id: (json['id'] as num?)?.toInt() ?? 0,
      name: (json['name'] ?? '').toString(),
      websiteName: (json['websiteName'] ?? '').toString(),
      state: (json['state'] ?? '').toString(),
      current: json['current'] == true,
    );
  }
}
