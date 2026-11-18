import '../../core/network/api_client.dart';
import '../../core/storage/storage_service.dart';
import '../../core/models/node_skill.dart';

const nodeSkillListPath = '/api/hoxi/skills/list';

/// 只读的节点 Skill 目录。`directory` 必须是节点上已注册的工作区或会话工作树，
/// 由调用方从当前节点真实数据里取，不能用手机本地路径。
class NodeSkillRepository {
  final ApiClient _apiClient;

  NodeSkillRepository(this._apiClient);

  Future<NodeSkillCatalog> list({
    required String directory,
    String sessionId = '',
  }) async {
    if (!StorageService.isActiveHoxi) {
      return NodeSkillCatalog.unavailable('Skill 目录只在 Hoxi 桌面端提供');
    }
    if (directory.trim().isEmpty) {
      return NodeSkillCatalog.unavailable('尚未选择工作区，无法读取设备 Skill 目录');
    }
    try {
      final res = await _apiClient.get<dynamic>(
        nodeSkillListPath,
        queryParameters: {
          'directory': directory,
          if (sessionId.trim().isNotEmpty) 'sessionID': sessionId,
        },
      );
      return parseNodeSkillCatalog(res.data);
    } catch (e) {
      return NodeSkillCatalog.unavailable('读取设备 Skill 目录失败：$e');
    }
  }
}
