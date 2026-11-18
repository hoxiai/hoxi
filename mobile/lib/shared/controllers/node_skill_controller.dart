import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/network/network_providers.dart';
import '../services/node_skill_repository.dart';
import '../../core/models/node_skill.dart';

final nodeSkillRepositoryProvider = Provider<NodeSkillRepository>((ref) {
  return NodeSkillRepository(ref.watch(nodeApiClientProvider));
});

class NodeSkillState {
  final NodeSkillCatalog catalog;
  final bool isLoading;

  /// 上一次请求的节点类型 + 目录 + 会话，用来避免每次 build 重复拉取。
  final bool requestedAsHoxi;
  final String requestedDirectory;
  final String requestedSessionId;

  const NodeSkillState({
    this.catalog = const NodeSkillCatalog.idle(),
    this.isLoading = false,
    this.requestedAsHoxi = false,
    this.requestedDirectory = '',
    this.requestedSessionId = '',
  });

  NodeSkillState copyWith({
    NodeSkillCatalog? catalog,
    bool? isLoading,
    bool? requestedAsHoxi,
    String? requestedDirectory,
    String? requestedSessionId,
  }) {
    return NodeSkillState(
      catalog: catalog ?? this.catalog,
      isLoading: isLoading ?? this.isLoading,
      requestedAsHoxi: requestedAsHoxi ?? this.requestedAsHoxi,
      requestedDirectory: requestedDirectory ?? this.requestedDirectory,
      requestedSessionId: requestedSessionId ?? this.requestedSessionId,
    );
  }

  bool matches({
    required bool isHoxi,
    required String directory,
    required String sessionId,
  }) =>
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
    if (state.matches(
      isHoxi: isHoxi,
      directory: directory,
      sessionId: sessionId,
    )) {
      return;
    }
    final request = NodeSkillState(
      isLoading: isHoxi,
      requestedAsHoxi: isHoxi,
      requestedDirectory: directory.trim(),
      requestedSessionId: sessionId,
      catalog: isHoxi
          ? state.catalog
          : NodeSkillCatalog.unavailable('Skill 目录只在 Hoxi 桌面端提供'),
    );
    state = request;
    if (!isHoxi) return;
    final catalog = await _repository.list(
      directory: directory,
      sessionId: sessionId,
    );
    if (!ref.mounted) return;
    if (!state.matches(
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
