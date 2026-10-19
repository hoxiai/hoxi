import '../../../core/network/api_client.dart';
import '../../../core/storage/storage_service.dart';
import '../models/monitor_info.dart';
import '../models/security_risk.dart';
import '../models/system_info.dart';

const securityRiskSummaryPath = '/api/security-monitoring/summary';

/// 仪表盘与系统资源仓库
/// 负责处理 GoPanel 获取主机信息、CPU、内存等资源状态的请求，同时兼容 Hoxi 研发节点概览
class DashboardRepository {
  final ApiClient _apiClient;

  DashboardRepository(this._apiClient);

  /// 获取基础操作系统信息
  /// GoPanel: GET /api/dashboard/base/os
  /// Hoxi: GET /api/hoxi/system-info
  Future<OsInfo> getOsInfo() async {
    if (StorageService.isActiveHoxi) {
      final response = await _apiClient.get<Map<String, dynamic>>(
        '/api/hoxi/system-info',
      );
      final data = response.data ?? const <String, dynamic>{};
      final appVer = (data['appVersion'] ?? 'Hoxi').toString();
      final ocVer = (data['openCodeVersion'] ?? '').toString();
      final os = (data['os'] ?? '').toString();
      final arch = (data['arch'] ?? '').toString();
      final rootDir = (data['rootDir'] ?? '').toString();
      final host = rootDir.isNotEmpty
          ? rootDir.split(RegExp(r'[/\\]')).where((s) => s.isNotEmpty).last
          : 'Hoxi Workspace';
      return OsInfo(
        version: 'Hoxi $appVer',
        kernelVersion: ocVer.isNotEmpty ? 'OpenCode $ocVer' : 'Go Runtime',
        platform: [os, arch].where((s) => s.isNotEmpty).join('/'),
        hostname: host,
      );
    }
    final response = await _apiClient.get<Map<String, dynamic>>(
      '/api/dashboard/base/os',
    );
    return OsInfo.fromJson(response.data ?? {});
  }

  /// 获取实时资源数据（CPU使用率、内存、磁盘进度）
  /// GoPanel: POST /api/dashboard/current
  /// Hoxi: GET /api/hoxi/dev-pulse
  Future<SystemCurrentInfo> getCurrentInfo() async {
    if (StorageService.isActiveHoxi) {
      final response = await _apiClient.get<Map<String, dynamic>>(
        '/api/hoxi/dev-pulse',
      );
      final data = response.data ?? const <String, dynamic>{};
      final sessions = (data['dbSessionsCount'] as num?)?.toInt() ?? 0;
      final turns = (data['dbTurnsCount'] as num?)?.toInt() ?? 0;
      return SystemCurrentInfo(
        uptime: 0,
        procs: sessions,
        percent: (turns.clamp(0, 100)).toDouble(),
        load1: ((data['todayCommits'] as num?)?.toDouble() ?? 0.0),
        load5: ((data['committedFiles'] as num?)?.toDouble() ?? 0.0),
        load15: ((data['uncommittedFiles'] as num?)?.toDouble() ?? 0.0),
        loadUsagePercent: 0.0,
        memoryTotal: 0,
        memoryAvailable: 0,
        memoryUsed: 0,
        diskData: const [],
      );
    }
    final response = await _apiClient.post<Map<String, dynamic>>(
      '/api/dashboard/current',
    );
    return SystemCurrentInfo.fromJson(response.data ?? {});
  }

  /// 获取 IO / 网络累计数据（用于计算速率与绘制趋势）
  /// POST /api/dashboard/current {scope: ioNet}
  Future<IoNetInfo> getIoNetInfo({
    String ioOption = 'all',
    String netOption = 'all',
  }) async {
    if (StorageService.isActiveHoxi) {
      return IoNetInfo.fromJson(const {});
    }
    final response = await _apiClient.post<Map<String, dynamic>>(
      '/api/dashboard/current',
      data: {'scope': 'ioNet', 'ioOption': ioOption, 'netOption': netOption},
    );
    return IoNetInfo.fromJson(response.data ?? {});
  }

  Future<List<SecurityRisk>> getSecurityRisks() async {
    if (StorageService.isActiveHoxi) {
      return const [];
    }
    final response = await _apiClient.get<Map<String, dynamic>>(
      securityRiskSummaryPath,
    );
    final items = response.data?['items'];
    if (items is! List) return const [];
    return items
        .whereType<Map>()
        .map(
          (item) => SecurityRisk.fromJson(
            item.map((key, value) => MapEntry(key.toString(), value)),
          ),
        )
        .toList();
  }
}
