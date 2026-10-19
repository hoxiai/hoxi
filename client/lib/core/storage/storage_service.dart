import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../shared/models/server_connection.dart';

class StorageService {
  static late SharedPreferences _prefs;
  static final FlutterSecureStorage _secureStorage = FlutterSecureStorage(
    mOptions: buildMacOsSecureStorageOptions(releaseMode: kReleaseMode),
  );
  static final Map<String, String> _serverTokens = {};
  static final Map<String, String> _serverEntrances = {};
  static String? _activeServerToken;
  static String? _activeServerCookie;
  static String? _activeServerEntrance;
  static String _activeServerNodeType = 'gopanel';
  static bool _prefsInitialized = false;

  static const String _keyServerList = 'server_list';
  static const String _keyActiveServerUrl = 'active_server_url';
  static const String _keyActiveServerNodeType = 'active_server_node_type';
  static const String _keyActiveServerToken = 'active_server_token';
  static const String _keyActiveServerCookie = 'active_server_cookie';
  static const String _secureActiveToken = 'secure_active_server_token';
  static const String _secureActiveCookie = 'secure_active_server_cookie';
  static const String _secureActiveEntrance = 'secure_active_server_entrance';
  static const String _keyUpgradeRecovery = 'settings_upgrade_recovery';
  static const int _maxCodeDraftLength = 8000;

  static Future<void> init() async {
    _prefs = await SharedPreferences.getInstance();
    _prefsInitialized = true;
    await _migrateActiveCredentials();
    _activeServerToken = await _secureStorage.read(key: _secureActiveToken);
    _activeServerCookie = await _secureStorage.read(key: _secureActiveCookie);
    _activeServerEntrance = await _secureStorage.read(
      key: _secureActiveEntrance,
    );
    _activeServerNodeType =
        _prefs.getString(_keyActiveServerNodeType) ?? 'gopanel';
    await _migrateActiveServerAddress();
    await _loadServerCredentials();
    await _restoreActiveServerEntrance();
  }

  static List<ServerConnection> getServerList() {
    final entries = _readServerEntries();
    return entries.map((entry) {
      final server = ServerConnection.fromJson(entry);
      return ServerConnection(
        id: server.id,
        name: server.name,
        url: server.url,
        entrance: _serverEntrances[server.id] ?? server.entrance,
        token: _serverTokens[server.id] ?? '',
        nodeType: server.nodeType,
        lastConnectedAt: server.lastConnectedAt,
      );
    }).toList();
  }

  static Future<bool> saveServerConnection(ServerConnection server) async {
    final list = getServerList();
    final index = list.indexWhere((item) => item.url == server.url);
    final saved = index >= 0
        ? ServerConnection(
            id: list[index].id,
            name: server.name,
            url: server.url,
            entrance: server.entrance,
            token: server.token,
            nodeType: server.nodeType,
            lastConnectedAt: server.lastConnectedAt,
          )
        : server;
    await _secureStorage.write(
      key: _serverTokenKey(saved.id),
      value: saved.token,
    );
    if (saved.entrance.isEmpty) {
      await _secureStorage.delete(key: _serverEntranceKey(saved.id));
    } else {
      await _secureStorage.write(
        key: _serverEntranceKey(saved.id),
        value: saved.entrance,
      );
    }
    _serverTokens[saved.id] = saved.token;
    if (saved.entrance.isEmpty) {
      _serverEntrances.remove(saved.id);
    } else {
      _serverEntrances[saved.id] = saved.entrance;
    }
    if (index >= 0) {
      list[index] = saved;
    } else {
      list.add(saved);
    }
    return _writeServerList(list);
  }

  static Future<bool> removeServerConnection(String url) async {
    final list = getServerList();
    final removed = list.where((item) => item.url == url).toList();
    list.removeWhere((item) => item.url == url);
    for (final server in removed) {
      await _secureStorage.delete(key: _serverTokenKey(server.id));
      await _secureStorage.delete(key: _serverEntranceKey(server.id));
      _serverTokens.remove(server.id);
      _serverEntrances.remove(server.id);
    }
    return _writeServerList(list);
  }

  static Future<bool> replaceServerConnection(
    String previousUrl,
    ServerConnection server,
  ) async {
    final list = getServerList();
    final index = list.indexWhere((item) => item.url == previousUrl);
    if (index < 0) return saveServerConnection(server);
    final previous = list[index];
    final replacement = ServerConnection(
      id: previous.id,
      name: previous.name,
      url: server.url,
      entrance: server.entrance,
      token: server.token,
      nodeType: server.nodeType,
      lastConnectedAt: server.lastConnectedAt,
    );
    await _secureStorage.write(
      key: _serverTokenKey(previous.id),
      value: replacement.token,
    );
    if (replacement.entrance.isEmpty) {
      await _secureStorage.delete(key: _serverEntranceKey(previous.id));
      _serverEntrances.remove(previous.id);
    } else {
      await _secureStorage.write(
        key: _serverEntranceKey(previous.id),
        value: replacement.entrance,
      );
      _serverEntrances[previous.id] = replacement.entrance;
    }
    _serverTokens[previous.id] = replacement.token;
    list[index] = replacement;
    return _writeServerList(list);
  }

  static String? get activeServerUrl =>
      _prefsInitialized ? _prefs.getString(_keyActiveServerUrl) : null;

  static Future<bool> setActiveServerUrl(String url) {
    return _prefs.setString(_keyActiveServerUrl, url);
  }

  static String get activeServerNodeType => _activeServerNodeType;

  static bool get isActiveHoxi => _activeServerNodeType == 'hoxi';

  static Future<bool> setActiveServerNodeType(String nodeType) async {
    final normalized = nodeType.trim().toLowerCase() == 'hoxi'
        ? 'hoxi'
        : 'gopanel';
    _activeServerNodeType = normalized;
    if (_prefsInitialized) {
      return _prefs.setString(_keyActiveServerNodeType, normalized);
    }
    return true;
  }

  static String? get activeServerToken => _activeServerToken;

  static Future<bool> setActiveServerToken(String token) async {
    await _secureStorage.write(key: _secureActiveToken, value: token);
    _activeServerToken = token;
    return true;
  }

  static String? get activeServerCookie => _activeServerCookie;

  static Future<bool> setActiveServerCookie(String cookie) async {
    await _secureStorage.write(key: _secureActiveCookie, value: cookie);
    _activeServerCookie = cookie;
    return true;
  }

  static Future<void> clearActiveServerCookie() async {
    await _secureStorage.delete(key: _secureActiveCookie);
    _activeServerCookie = null;
  }

  static String? get activeServerEntrance => _activeServerEntrance;

  static Future<bool> setActiveServerEntrance(String entrance) async {
    final normalized = normalizeServerEntrance(entrance);
    if (normalized.isEmpty) {
      await _secureStorage.delete(key: _secureActiveEntrance);
      _activeServerEntrance = null;
      return true;
    }
    await _secureStorage.write(key: _secureActiveEntrance, value: normalized);
    _activeServerEntrance = normalized;
    return true;
  }

  static Future<void> clearActiveServer() async {
    if (_prefsInitialized) {
      await _prefs.remove(_keyActiveServerUrl);
      await _prefs.remove(_keyActiveServerNodeType);
    }
    await _secureStorage.delete(key: _secureActiveToken);
    await _secureStorage.delete(key: _secureActiveCookie);
    await _secureStorage.delete(key: _secureActiveEntrance);
    _activeServerToken = null;
    _activeServerCookie = null;
    _activeServerEntrance = null;
    _activeServerNodeType = 'gopanel';
  }

  static String getCodeInstructionDraft(int sessionId) {
    return _prefs.getString(
          buildCodeInstructionDraftKey(activeServerUrl, sessionId),
        ) ??
        '';
  }

  static Future<bool> saveCodeInstructionDraft(int sessionId, String content) {
    final key = buildCodeInstructionDraftKey(activeServerUrl, sessionId);
    if (content.isEmpty) return _prefs.remove(key);
    final value = content.length > _maxCodeDraftLength
        ? content.substring(content.length - _maxCodeDraftLength)
        : content;
    return _prefs.setString(key, value);
  }

  static Map<String, dynamic>? getUpgradeRecovery() {
    final raw = _prefs.getString(_keyUpgradeRecovery);
    if (raw == null || raw.isEmpty) return null;
    try {
      final value = jsonDecode(raw);
      return value is Map ? Map<String, dynamic>.from(value) : null;
    } catch (_) {
      return null;
    }
  }

  static Future<bool> saveUpgradeRecovery({
    required String logName,
    required String currentVersion,
    required String targetVersion,
  }) {
    return _prefs.setString(
      _keyUpgradeRecovery,
      jsonEncode({
        'logName': logName,
        'currentVersion': currentVersion,
        'targetVersion': targetVersion,
        'startedAt': DateTime.now().toIso8601String(),
      }),
    );
  }

  static Future<bool> clearUpgradeRecovery() =>
      _prefs.remove(_keyUpgradeRecovery);

  static Future<void> _migrateActiveCredentials() async {
    await _migratePreference(_keyActiveServerToken, _secureActiveToken);
    await _migratePreference(_keyActiveServerCookie, _secureActiveCookie);
  }

  static Future<void> _migrateActiveServerAddress() async {
    final url = activeServerUrl;
    if (url == null || url.isEmpty) return;
    try {
      final normalized = normalizeServerAddress(
        url,
        _activeServerEntrance ?? '',
      );
      if (normalized.baseUrl != url) {
        await setActiveServerUrl(normalized.baseUrl);
      }
      if ((_activeServerEntrance ?? '').isEmpty &&
          normalized.entrance.isNotEmpty) {
        await setActiveServerEntrance(normalized.entrance);
      }
    } on FormatException {
      return;
    }
  }

  static Future<void> _migratePreference(
    String preferenceKey,
    String secureKey,
  ) async {
    final legacyValue = _prefs.getString(preferenceKey);
    if (legacyValue == null || legacyValue.isEmpty) return;
    final secureValue = await _secureStorage.read(key: secureKey);
    if (secureValue == null || secureValue.isEmpty) {
      await _secureStorage.write(key: secureKey, value: legacyValue);
    }
    await _prefs.remove(preferenceKey);
  }

  static Future<void> _restoreActiveServerEntrance() async {
    if ((_activeServerEntrance ?? '').isNotEmpty) return;
    final url = activeServerUrl;
    if (url == null || url.isEmpty) return;
    for (final server in getServerList()) {
      if (server.url == url && server.entrance.isNotEmpty) {
        await setActiveServerEntrance(server.entrance);
        return;
      }
    }
  }

  static Future<void> _loadServerCredentials() async {
    final entries = _readServerEntries();
    var needsRewrite = false;
    for (final entry in entries) {
      final id = entry['id'] as String;
      final legacyToken = entry['token'] as String?;
      var legacyEntrance = entry['entrance'] as String?;
      final rawUrl = entry['url'] as String? ?? '';
      try {
        final normalized = normalizeServerAddress(rawUrl, legacyEntrance ?? '');
        if (normalized.baseUrl != rawUrl) {
          entry['url'] = normalized.baseUrl;
          needsRewrite = true;
        }
        if ((legacyEntrance ?? '').isEmpty && normalized.entrance.isNotEmpty) {
          legacyEntrance = normalized.entrance;
        }
      } on FormatException {
        // 保留无法规范化的旧数据，供用户手动修正。
      }
      var token = await _secureStorage.read(key: _serverTokenKey(id));
      var entrance = await _secureStorage.read(key: _serverEntranceKey(id));
      if ((token == null || token.isEmpty) &&
          legacyToken != null &&
          legacyToken.isNotEmpty) {
        await _secureStorage.write(
          key: _serverTokenKey(id),
          value: legacyToken,
        );
        token = legacyToken;
      }
      if (token != null && token.isNotEmpty) {
        _serverTokens[id] = token;
      }
      if ((entrance == null || entrance.isEmpty) &&
          legacyEntrance != null &&
          legacyEntrance.isNotEmpty) {
        entrance = normalizeServerEntrance(legacyEntrance);
        await _secureStorage.write(
          key: _serverEntranceKey(id),
          value: entrance,
        );
      }
      if (entrance != null && entrance.isNotEmpty) {
        _serverEntrances[id] = entrance;
      }
      if (entry.containsKey('token')) {
        entry.remove('token');
        needsRewrite = true;
      }
      if (entry.containsKey('entrance')) {
        entry.remove('entrance');
        entry['hasEntrance'] = entrance != null && entrance.isNotEmpty;
        needsRewrite = true;
      }
    }
    if (needsRewrite) {
      await _prefs.setString(_keyServerList, jsonEncode(entries));
    }
  }

  static List<Map<String, dynamic>> _readServerEntries() {
    final jsonString = _prefs.getString(_keyServerList);
    if (jsonString == null || jsonString.isEmpty) return [];
    try {
      final values = jsonDecode(jsonString) as List<dynamic>;
      return values
          .whereType<Map<String, dynamic>>()
          .map((value) => Map<String, dynamic>.from(value))
          .toList();
    } catch (_) {
      return [];
    }
  }

  static Future<bool> _writeServerList(List<ServerConnection> servers) {
    return _prefs.setString(
      _keyServerList,
      jsonEncode(servers.map((server) => server.toJson()).toList()),
    );
  }

  static String _serverTokenKey(String id) => 'secure_server_token_$id';
  static String _serverEntranceKey(String id) => 'secure_server_entrance_$id';
}

String normalizeServerEntrance(String value) {
  return value.trim().replaceAll(RegExp(r'^/+|/+$'), '');
}

bool isValidServerEntrance(String value) {
  final normalized = normalizeServerEntrance(value);
  return normalized.isEmpty || RegExp(r'^[A-Za-z0-9_-]+$').hasMatch(normalized);
}

({String baseUrl, String entrance}) normalizeServerAddress(
  String serverUrl, [
  String entrance = '',
]) {
  final uri = Uri.parse(serverUrl.trim());
  if (uri.scheme != 'http' && uri.scheme != 'https') {
    throw const FormatException('serverAddressHttpRequired');
  }
  if (uri.host.isEmpty || uri.userInfo.isNotEmpty) {
    throw const FormatException('serverAddressInvalid');
  }
  final explicitEntrance = normalizeServerEntrance(entrance);
  final pathEntrance = normalizeServerEntrance(uri.path);
  if (explicitEntrance.isNotEmpty &&
      pathEntrance.isNotEmpty &&
      explicitEntrance != pathEntrance) {
    throw const FormatException('serverEntranceMismatch');
  }
  final baseUrl = uri.replace(path: '', query: null, fragment: null).toString();
  return (
    baseUrl: baseUrl.replaceFirst(RegExp(r'/$'), ''),
    entrance: explicitEntrance.isNotEmpty ? explicitEntrance : pathEntrance,
  );
}

String buildCodeInstructionDraftKey(String? serverUrl, int sessionId) {
  final server = (serverUrl ?? '').trim().toLowerCase();
  final encoded = base64Url.encode(utf8.encode(server)).replaceAll('=', '');
  return 'code_instruction_draft_${encoded.isEmpty ? 'local' : encoded}_$sessionId';
}

MacOsOptions buildMacOsSecureStorageOptions({required bool releaseMode}) {
  return const MacOsOptions(usesDataProtectionKeychain: false);
}
