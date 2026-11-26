import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/network/network_providers.dart';
import '../../core/storage/node_request_guard.dart';
import '../services/node_skill_repository.dart';
import '../../core/models/node_skill.dart';

final nodeSkillRepositoryProvider = Provider<NodeSkillRepository>((ref) {
  return NodeSkillRepository(ref.watch(nodeApiClientProvider));
});

class NodeSkillState {
  final NodeSkillCatalog catalog;
  final bool isLoading;

  /// 上一次请求的节点代次 + 节点类型 + 目录 + 会话，避免跨节点命中缓存或迟到响应互串。
  final int nodeGeneration;
  final bool requestedAsHoxi;
  final String requestedDirectory;
  final String requestedSessionId;

  const NodeSkillState({
    this.catalog = const NodeSkillCatalog.idle(),
    this.isLoading = false,
    this.nodeGeneration = -1,
    this.requestedAsHoxi = false,
    this.requestedDirectory = '',
    this.requestedSessionId = '',
  });

  NodeSkillState copyWith({
    NodeSkillCatalog? catalog,
    bool? isLoading,
    int? nodeGeneration,
    bool? requestedAsHoxi,
    String? requestedDirectory,
    String? requestedSessionId,
  }) {
    return NodeSkillState(
      catalog: catalog ?? this.catalog,
      isLoading: isLoading ?? this.isLoading,
      nodeGeneration: nodeGeneration ?? this.nodeGeneration,
      requestedAsHoxi: requestedAsHoxi ?? this.requestedAsHoxi,
      requestedDirectory: requestedDirectory ?? this.requestedDirectory,
      requestedSessionId: requestedSessionId ?? this.requestedSessionId,
    );
  }

  bool matches({
    required int currentGeneration,
    required bool isHoxi,
    required String directory,
    required String sessionId,
  }) =>
      nodeGeneration == currentGeneration &&
      requestedAsHoxi == isHoxi &&
      requestedDirectory == directory.trim() &&
      requestedSessionId == sessionId;

  /// 节点真实 Skill 名称，用于聊天里的技能挂载；读取失败时为空。
  List<String> get skillNames => catalog.loaded
      ? catalog.skills.map((skill) => skill.name).toList()
      : const [];
}

final nodeSkillControllerProvider =
    NotifierProvider<NodeSkillController, NodeSkillState>(
      NodeSkillController.new,
    );

class NodeSkillController extends Notifier<NodeSkillState> {
  late NodeSkillRepository _repository;

  @override
  NodeSkillState build() {
    _repository = ref.watch(nodeSkillRepositoryProvider);
    return const NodeSkillState();
  }

  /// 只有节点是 Hoxi 桌面时才存在 Skill 目录；GoPanel 服务器直接标记为不可用，
  /// 不要把「不是 Hoxi」说成「没有安装 Skill」。
  Future<void> load({
    required String directory,
    required bool isHoxi,
    String sessionId = '',
  }) async {
    final nodeRequest = NodeRequestGuard(_repository);
    if (!ref.mounted || !nodeRequest.isCurrent(_repository)) {
      return;
    }
    final generation = nodeRequest.node.generation;
    if (state.matches(
      currentGeneration: generation,
      isHoxi: isHoxi,
      directory: directory,
      sessionId: sessionId,
    )) {
      return;
    }
    final preservePrevious = isHoxi && state.nodeGeneration == generation;
    state = NodeSkillState(
      isLoading: isHoxi,
      nodeGeneration: generation,
      requestedAsHoxi: isHoxi,
      requestedDirectory: directory.trim(),
      requestedSessionId: sessionId,
      catalog: isHoxi
          ? (preservePrevious ? state.catalog : const NodeSkillCatalog.idle())
          : NodeSkillCatalog.unavailable('Skill 目录只在 Hoxi 桌面端提供'),
    );
    if (!isHoxi) return;
    final catalog = await nodeRequest.repository.list(
      directory: directory,
      sessionId: sessionId,
    );
    if (!ref.mounted || !nodeRequest.isCurrent(_repository)) return;
    if (!state.matches(
      currentGeneration: generation,
      isHoxi: isHoxi,
      directory: directory,
      sessionId: sessionId,
    )) {
      return;
    }
    state = state.copyWith(catalog: catalog, isLoading: false);
  }

  Future<void> reload() async {
    if (state.requestedDirectory.isEmpty) return;
    final isHoxi = state.requestedAsHoxi;
    final directory = state.requestedDirectory;
    final sessionId = state.requestedSessionId;
    state = const NodeSkillState();
    await load(isHoxi: isHoxi, directory: directory, sessionId: sessionId);
  }
}
