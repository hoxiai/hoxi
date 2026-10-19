class SettingsInfo {
  final String nickname;
  final String email;
  final String systemVersion;
  final String panelPort;
  final String securityEntrance;
  final String baseDir;
  final String logPath;
  final String mfaStatus;
  final String lastCleanTime;
  final String lastCleanSize;

  const SettingsInfo({
    required this.nickname,
    required this.email,
    required this.systemVersion,
    required this.panelPort,
    required this.securityEntrance,
    required this.baseDir,
    required this.logPath,
    required this.mfaStatus,
    required this.lastCleanTime,
    required this.lastCleanSize,
  });

  factory SettingsInfo.fromJson(Map<String, dynamic> json) {
    return SettingsInfo(
      nickname: _string(json['nickName']),
      email: _string(json['email']),
      systemVersion: _string(json['systemVersion']),
      panelPort: _string(json['serverPort']).isNotEmpty
          ? _string(json['serverPort'])
          : _string(json['port']),
      securityEntrance: _string(json['securityEntrance']),
      baseDir: _string(json['baseDir']),
      logPath: _string(json['logPath']),
      mfaStatus: _string(json['mfaStatus']),
      lastCleanTime: _string(json['lastCleanTime']),
      lastCleanSize: _string(json['lastCleanSize']),
    );
  }

  static String _string(dynamic value) => value?.toString() ?? '';
}

class SettingsVersion {
  final String versionName;
  final int versionCode;
  final String buildTime;
  final String installPath;
  final bool needUpdate;
  final String latestVersionName;
  final String title;
  final String description;
  final String content;

  const SettingsVersion({
    required this.versionName,
    required this.versionCode,
    required this.buildTime,
    required this.installPath,
    this.needUpdate = false,
    this.latestVersionName = '',
    this.title = '',
    this.description = '',
    this.content = '',
  });

  factory SettingsVersion.fromJson(Map<String, dynamic> json) {
    return SettingsVersion(
      versionName:
          json['versionName']?.toString() ??
          json['curVersion']?.toString() ??
          '',
      versionCode: int.tryParse(json['versionCode']?.toString() ?? '') ?? 0,
      buildTime: json['buildTime']?.toString() ?? '',
      installPath: json['installPath']?.toString() ?? '',
      needUpdate: json['needUpdate'] == true,
      latestVersionName: json['latestVersionName']?.toString() ?? '',
      title: json['title']?.toString() ?? '',
      description: json['description']?.toString() ?? '',
      content: json['content']?.toString() ?? '',
    );
  }
}

class UpgradeRecovery {
  final String logName;
  final String currentVersion;
  final String targetVersion;
  final DateTime? startedAt;

  const UpgradeRecovery({
    required this.logName,
    required this.currentVersion,
    required this.targetVersion,
    required this.startedAt,
  });

  factory UpgradeRecovery.fromJson(Map<String, dynamic> json) {
    return UpgradeRecovery(
      logName: json['logName']?.toString() ?? '',
      currentVersion: json['currentVersion']?.toString() ?? '',
      targetVersion: json['targetVersion']?.toString() ?? '',
      startedAt: DateTime.tryParse(json['startedAt']?.toString() ?? ''),
    );
  }
}

class UpgradeStatus {
  final String log;
  final String status;
  final String phase;
  final int progress;
  final String error;
  final String currentVersion;
  final String targetVersion;
  final DateTime? startedAt;
  final DateTime? updatedAt;

  const UpgradeStatus({
    required this.log,
    required this.status,
    required this.phase,
    required this.progress,
    required this.error,
    required this.currentVersion,
    required this.targetVersion,
    required this.startedAt,
    required this.updatedAt,
  });

  factory UpgradeStatus.fromJson(Map<String, dynamic> json) {
    return UpgradeStatus(
      log: json['log']?.toString() ?? '',
      status: json['status']?.toString() ?? 'running',
      phase: json['phase']?.toString() ?? 'preparing',
      progress: int.tryParse(json['progress']?.toString() ?? '') ?? 0,
      error: json['error']?.toString() ?? '',
      currentVersion: json['currentVersion']?.toString() ?? '',
      targetVersion: json['targetVersion']?.toString() ?? '',
      startedAt: DateTime.tryParse(json['startedAt']?.toString() ?? ''),
      updatedAt: DateTime.tryParse(json['updatedAt']?.toString() ?? ''),
    );
  }
}
