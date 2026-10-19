import '../../../core/network/api_client.dart';

class SettingsRepository {
  final ApiClient _apiClient;

  SettingsRepository(this._apiClient);

  Future<Map<String, dynamic>> getInfo() async {
    final responses = await Future.wait([
      _apiClient.post<Map<String, dynamic>>('/api/setting/system/info'),
      _apiClient.post<Map<String, dynamic>>('/api/setting/system/config'),
    ]);
    final info = Map<String, dynamic>.from(responses[0].data ?? {});
    final config = responses[1].data ?? {};
    final system = config['System'] ?? config['system'];
    if (system is Map) {
      info['baseDir'] = system['BaseDir'] ?? system['base_dir'] ?? '';
      info['logPath'] = system['LogPath'] ?? system['log_path'] ?? '';
      info['port'] = system['Port'] ?? system['port'] ?? info['port'] ?? '';
      info['securityEntrance'] =
          system['Entrance'] ??
          system['entrance'] ??
          info['securityEntrance'] ??
          '';
    }
    return info;
  }

  Future<void> updatePassword({
    required String password,
    required String newPassword,
  }) async {
    await _apiClient.post<Map<String, dynamic>>(
      '/api/user/editPassword',
      data: {'password': password, 'newPassword': newPassword},
    );
  }

  Future<void> updatePort(int port) async {
    await _apiClient.post<Map<String, dynamic>>(
      '/api/setting/system/port',
      data: {'serverPort': port},
    );
  }

  Future<void> updateEntrance(String entrance) async {
    await _apiClient.post<Map<String, dynamic>>(
      '/api/setting/system/entrance',
      data: {'entrance': entrance},
    );
  }

  Future<Map<String, dynamic>> getVersion() async {
    final response = await _apiClient.get<Map<String, dynamic>>(
      '/api/setting/system/version',
    );
    return response.data ?? {};
  }

  Future<Map<String, dynamic>> checkUpdate() async {
    final response = await _apiClient.get<Map<String, dynamic>>(
      '/api/setting/system/check',
      queryParameters: {'lang': 'zh', 'appBrand': 'GoPanel'},
    );
    return response.data ?? {};
  }

  Future<String> startUpgrade({
    required String currentVersion,
    required String targetVersion,
    String lang = 'zh',
  }) async {
    final response = await _apiClient.post<Map<String, dynamic>>(
      '/api/setting/system/upgrade',
      data: {
        'containerName': 'gopanel',
        'currentVersion': currentVersion,
        'targetVersion': targetVersion,
        'lang': lang,
      },
    );
    final log = response.data?['log']?.toString() ?? '';
    if (log.isEmpty) throw Exception('升级任务未返回日志标识');
    return log;
  }

  Future<List<String>> getUpgradeLogs(String logName) {
    return _apiClient.getSseDataLines(
      '/api/setting/system/upgrade/logs',
      queryParameters: {'log': logName},
      maxWait: const Duration(seconds: 2),
      timeout: const Duration(seconds: 5),
      maxLines: 2000,
    );
  }

  Future<Map<String, dynamic>> getUpgradeStatus(String logName) async {
    final response = await _apiClient.get<Map<String, dynamic>>(
      '/api/setting/system/upgrade/status',
      queryParameters: {'log': logName},
    );
    return response.data ?? {};
  }

  Future<void> restart(String operation) async {
    await _apiClient.post<Map<String, dynamic>>(
      '/api/setting/system/restart/$operation',
    );
  }

  Future<Map<String, dynamic>> clear(String key) async {
    final response = await _apiClient.post<Map<String, dynamic>>(
      '/api/setting/system/clear',
      data: {'key': key},
    );
    return response.data ?? {};
  }
}
