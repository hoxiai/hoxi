import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'account_scope_binding.dart';
import 'active_node_context.dart';
import 'connection_list_signal.dart';
import 'node_type_spec.dart';
import 'server_connection.dart';
import 'storage_address_cookie_helpers.dart';

export 'storage_address_cookie_helpers.dart';

class StorageService {
  static late SharedPreferences _prefs;
  static final FlutterSecureStorage _secureStorage = FlutterSecureStorage(
    mOptions: buildMacOsSecureStorageOptions(releaseMode: kReleaseMode),
  );
  static Future<void> _deleteSecure(String key) =>
      safeSecureStorageDelete(_secureStorage, key);
  static Future<void> _writeOrDeleteSecure(String key, String value) =>
      value.isEmpty ? _deleteSecure(key) : _secureStorage.write(key: key, value: value);
  static final Map<String, String> _serverTokens = {};
  static final Map<String, String> _serverRelayTokens = {};
  static final Map<String, String> _serverE2eeKeys = {};
  static final Map<String, String> _serverEntrances = {};
  static List<ServerConnection>? _cachedServerList;
  static String? _activeServerToken;
  static String? _activeServerE2eeKey;
  static String? _activeServerCookie;
  static String? _activeServerEntrance;
  static String _activeServerNodeType = 'gopanel';
  static bool _prefsInitialized = false;
  static String? _activeServerUrl;
  static final ValueNotifier<int> nodeGeneration = ValueNotifier(0);
  static final SerializedWriteQueue _connectionWrites = SerializedWriteQueue();

  static ActiveNodeContext get activeNodeContext => ActiveNodeContext(
    url: _activeServerUrl,
    nodeType: _activeServerNodeType,
    token: _activeServerToken,
    e2eeKey: _activeServerE2eeKey,
    entrance: _activeServerEntrance,
    cookie: _activeServerCookie,
    generation: nodeGeneration.value,
  );

  static bool isCurrentNode(ActiveNodeContext node) =>
      node.generation == nodeGeneration.value && node.url == _activeServerUrl;

  /// Serialize activation and publish a complete connection only after storage
  /// succeeds. Requests continue to see the previous connection during writes.
  static Future<void> activateServer(
    ServerConnection server, {
    ActiveNodeContext? expectedNode,
  }) => _connectionWrites.run(() async {
    if (expectedNode != null && !isCurrentNode(expectedNode)) {
      throw StateError('节点已切换');
    }
    final previous = activeNodeContext;
    final activeToken = server.tokenForUrl(server.url);
    final activeE2ee = server.e2eeKey.trim();
    try {
      await _secureStorage.write(key: _secureActiveToken, value: activeToken);
      await _writeOrDeleteSecure(_secureActiveE2eeKey, activeE2ee);
      await _secureStorage.write(key: _secureActiveEntrance, value: normalizeServerEntrance(server.entrance));
      await _deleteSecure(_secureActiveCookie);
      if (!await _prefs.setString(_keyActiveServerUrl, server.url) ||
          !await _prefs.setString(_keyActiveServerNodeType, server.nodeType)) {
        throw StateError('无法保存节点连接');
      }
    } catch (_) {
      await _secureStorage.write(key: _secureActiveToken, value: previous.token);
      await _secureStorage.write(key: _secureActiveE2eeKey, value: previous.e2eeKey);
      await _secureStorage.write(key: _secureActiveEntrance, value: previous.entrance);
      await _secureStorage.write(key: _secureActiveCookie, value: previous.cookie);
      if (previous.url == null) {
        await _prefs.remove(_keyActiveServerUrl);
      } else {
        await _prefs.setString(_keyActiveServerUrl, previous.url!);
      }
      await _prefs.setString(_keyActiveServerNodeType, previous.nodeType);
      rethrow;
    }
    _activeServerUrl = server.url;
    _activeServerNodeType = server.nodeType;
    _activeServerToken = activeToken;
    _activeServerE2eeKey = activeE2ee.isEmpty ? null : activeE2ee;
    _activeServerEntrance = normalizeServerEntrance(server.entrance);
    _activeServerCookie = null;
    nodeGeneration.value++;
  });

  static Future<void> updateActiveServerAddress(String url, {String? entrance}) async {
    final node = activeNodeContext;
    if (!node.isConnected) throw StateError('未连接节点');
    final saved = getServerList()
        .where((s) => s.url == node.url || s.transports.any((t) => t.url == node.url || t.url == url))
        .firstOrNull;
    final base = saved ?? ServerConnection(
      id: DateTime.now().microsecondsSinceEpoch.toString(),
      name: '当前设备',
      url: url,
      token: node.token ?? '',
      e2eeKey: node.e2eeKey ?? '',
      entrance: entrance ?? node.entrance ?? '',
      nodeType: node.nodeType,
      lastConnectedAt: DateTime.now(),
    );
    final updated = buildAddressSwitchedServerConnection(base, nextUrl: url, entrance: entrance ?? node.entrance ?? '');
    await replaceServerConnection(node.url!, updated);
    await activateServer(updated, expectedNode: node);
  }

  static const String _keyServerList = 'server_list';
  static const String _keyActiveServerUrl = 'active_server_url';
  static const String _keyActiveServerNodeType = 'active_server_node_type';
  static const String _keyActiveServerToken = 'active_server_token';
  static const String _keyActiveServerCookie = 'active_server_cookie';
  static const String _secureActiveToken = 'secure_active_server_token';
  static const String _secureActiveE2eeKey = 'secure_active_server_e2ee_key';
  static const String _secureActiveCookie = 'secure_active_server_cookie';
  static const String _secureActiveEntrance = 'secure_active_server_entrance';
  static const String _keyUpgradeRecovery = 'settings_upgrade_recovery';
  static const int _maxCodeDraftLength = 8000;

  static Future<void> init() async {
    _connectionWrites.reset();
    _prefs = await SharedPreferences.getInstance();
    _prefsInitialized = true;
    _serverTokens.clear();
    _serverRelayTokens.clear();
    _serverE2eeKeys.clear();
    _serverEntrances.clear();
    _cachedServerList = null;
    _activeServerUrl = _prefs.getString(_keyActiveServerUrl);
    await _migrateActiveCredentials();
    final activeCreds = await Future.wait([
      _secureStorage.read(key: _secureActiveToken),
      _secureStorage.read(key: _secureActiveCookie),
      _secureStorage.read(key: _secureActiveEntrance),
      _secureStorage.read(key: _secureActiveE2eeKey),
    ]);
    _activeServerToken = activeCreds[0];
    _activeServerCookie = activeCreds[1];
    _activeServerEntrance = activeCreds[2];
    _activeServerE2eeKey = activeCreds[3];
    _activeServerNodeType = _prefs.getString(_keyActiveServerNodeType) ?? 'gopanel';
    await _migrateActiveServerAddress();
    await _loadServerCredentials();
    await _restoreActiveServerEntrance();
    nodeGeneration.value++;
  }

  static List<ServerConnection> getServerList({bool allAccounts = false}) {
    final currentScope = AccountScopeBinding.accountScope;
    final cached = _cachedServerList;
    if (cached != null) return cached.where((s) => allAccounts || s.accountScope == null || s.accountScope == currentScope).toList();
    final servers = <ServerConnection>[];
    for (final entry in _readServerEntries()) {
      try {
        final server = ServerConnection.fromJson(entry);
        servers.add(server.copyWith(
          entrance: _serverEntrances[server.id] ?? server.entrance,
          token: _serverTokens[server.id] ?? server.token,
          relayToken: _serverRelayTokens[server.id] ?? server.relayToken,
          e2eeKey: _serverE2eeKeys[server.id] ?? server.e2eeKey,
        ));
      } catch (_) {}
    }
    if (_prefsInitialized) _cachedServerList = List<ServerConnection>.unmodifiable(servers);
    return servers.where((s) => allAccounts || s.accountScope == null || s.accountScope == currentScope).toList();
  }

  static bool _matchesSavedServer(ServerConnection item, ServerConnection target) =>
      (item.accountScope == target.accountScope || (target.accountScope == null && item.accountScope == AccountScopeBinding.accountScope)) &&
      matchesSavedServerConnection(item, target);

  static Future<void> _persistServerSecrets(ServerConnection s) async {
    await _secureStorage.write(key: _serverTokenKey(s.id), value: s.token);
    _serverTokens[s.id] = s.token;
    await _writeOrDeleteSecure(_serverRelayTokenKey(s.id), s.relayToken);
    s.relayToken.isEmpty ? _serverRelayTokens.remove(s.id) : _serverRelayTokens[s.id] = s.relayToken;
    await _writeOrDeleteSecure(_serverE2eeKey(s.id), s.e2eeKey);
    s.e2eeKey.isEmpty ? _serverE2eeKeys.remove(s.id) : _serverE2eeKeys[s.id] = s.e2eeKey;
    await _writeOrDeleteSecure(_serverEntranceKey(s.id), s.entrance);
    s.entrance.isEmpty ? _serverEntrances.remove(s.id) : _serverEntrances[s.id] = s.entrance;
  }

  static Future<bool> saveServerConnection(
    ServerConnection server, {
    bool preserveExistingName = false,
    bool matchByIdOnly = false,
    int? expectedIdentityEpoch,
    int? expectedListGeneration,
  }) => serializeConnectionListWrite(() => _saveServerConnection(
    server,
    preserveExistingName: preserveExistingName,
    matchByIdOnly: matchByIdOnly,
    expectedIdentityEpoch: expectedIdentityEpoch,
    expectedListGeneration: expectedListGeneration,
  ));

  static Future<bool> _saveServerConnection(
    ServerConnection server, {
    bool preserveExistingName = false,
    bool matchByIdOnly = false,
    int? expectedIdentityEpoch,
    int? expectedListGeneration,
  }) async {
    if (expectedIdentityEpoch != null && expectedIdentityEpoch != AccountScopeBinding.identityEpoch) return false;
    if (expectedListGeneration != null && expectedListGeneration != connectionListGeneration.value) return false;
    final list = getServerList(allAccounts: true);
    final index = list.indexWhere((item) => matchByIdOnly ? item.id == server.id : _matchesSavedServer(item, server));
    final previous = index < 0 ? null : list[index];
    final saved = buildSavedServerConnection(previous, server, preserveExistingName: preserveExistingName, replace: matchByIdOnly);
    await _persistServerSecrets(saved);
    index >= 0 ? list[index] = saved : list.add(saved);
    if (saved.isGoPanel && saved.token.isNotEmpty) {
      await _syncHoxiRelayCredentials(saved, list);
    }
    if (expectedIdentityEpoch != null && expectedIdentityEpoch != AccountScopeBinding.identityEpoch) {
      if (previous != null && (previous.accountScope == null || previous.accountScope == AccountScopeBinding.accountScope)) {
        await _saveServerConnection(previous, matchByIdOnly: true);
      } else if (saved.accountScope != null) {
        await _deleteConnectionCredentials(saved.id);
      }
      return false;
    }
    return _writeServerList(list);
  }

  static Future<void> _syncHoxiRelayCredentials(ServerConnection gopanel, List<ServerConnection> list) async {
    for (var i = 0; i < list.length; i++) {
      final relay = list[i];
      if (!relay.isHoxi || (relay.accountScope != null && relay.accountScope != AccountScopeBinding.accountScope)) continue;
      final matchesDirectUrl = hoxiRelayControllerUrl(relay.url) == gopanel.url;
      final matchesTransport = relay.transports.any((t) => hoxiRelayControllerUrl(t.url) == gopanel.url);
      if (!matchesDirectUrl && !matchesTransport) continue;
      final hasLanCredential = relay.transports.any((t) => t.isLan) || !isHoxiRelayUrl(relay.url) || relay.relayToken.isNotEmpty;
      final next = hasLanCredential
          ? relay.copyWith(entrance: gopanel.entrance, relayToken: gopanel.token)
          : relay.copyWith(entrance: gopanel.entrance, token: gopanel.token);
      await _persistServerSecrets(next);
      list[i] = next;
      if (_activeServerUrl != null && hoxiRelayControllerUrl(_activeServerUrl!) == gopanel.url) {
        final normalizedEntrance = normalizeServerEntrance(gopanel.entrance);
        await _secureStorage.write(key: _secureActiveToken, value: gopanel.token);
        await _secureStorage.write(key: _secureActiveEntrance, value: normalizedEntrance);
        _activeServerToken = gopanel.token;
        _activeServerEntrance = normalizedEntrance;
      }
    }
  }

  static Future<bool> removeServerConnection(String url) async {
    var removed = false;
    for (final server in getServerList().where((s) => s.url == url)) {
      removed = await removeServerConnectionById(server.id) || removed;
    }
    return removed;
  }

  static Future<bool> renameServerConnection(String id, String name) => serializeConnectionListWrite(() async {
    final trimmed = name.trim();
    if (trimmed.isEmpty) return false;
    final list = getServerList(allAccounts: true);
    final index = list.indexWhere((item) => item.id == id);
    if (index < 0) return false;
    list[index] = list[index].copyWith(name: trimmed);
    return _writeServerList(list);
  });

  static bool isServerActive(ServerConnection server, [String? activeUrl]) {
    final url = activeUrl ?? _activeServerUrl;
    if (!doesServerMatchUrl(server, url)) return false;
    return url != _activeServerUrl || (_activeServerToken ?? '').isEmpty || server.tokenForUrl(url!) == _activeServerToken;
  }

  static Future<bool> updateServerDetails({
    required String id,
    required String name,
    required String url,
    String? entrance,
    String? token,
  }) => serializeConnectionListWrite(() async {
    final trimmedName = name.trim();
    final trimmedUrl = url.trim();
    if (trimmedName.isEmpty || trimmedUrl.isEmpty) return false;
    final list = getServerList(allAccounts: true);
    final index = list.indexWhere((item) => item.id == id);
    if (index < 0) return false;
    final previous = list[index];
    final wasActive = isServerActive(previous);
    final updated = buildUpdatedServerDetails(
      previous: previous,
      trimmedName: trimmedName,
      trimmedUrl: trimmedUrl,
      entrance: entrance,
      token: token,
    );
    await _persistServerSecrets(updated);
    list[index] = updated;
    if (wasActive) {
      await _connectionWrites.run(() async {
        _activeServerUrl = trimmedUrl;
        _activeServerEntrance = normalizeServerEntrance(updated.entrance);
        _activeServerToken = updated.tokenForUrl(trimmedUrl);
        await _prefs.setString(_keyActiveServerUrl, trimmedUrl);
        await _secureStorage.write(key: _secureActiveToken, value: _activeServerToken);
        await _secureStorage.write(key: _secureActiveEntrance, value: _activeServerEntrance);
        nodeGeneration.value++;
      });
    }
    return _writeServerList(list);
  });

  static Future<bool> removeServerConnectionById(String id) => serializeConnectionListWrite(() async {
    final list = getServerList(allAccounts: true);
    final removed = list.where((item) => item.id == id).toList();
    if (removed.isEmpty) return false;
    list.removeWhere((item) => item.id == id);
    for (final server in removed) {
      await _deleteConnectionCredentials(server.id);
    }
    return _writeServerList(list);
  });

  static Future<void> _deleteConnectionCredentials(String id) async {
    await Future.wait([
      _deleteSecure(_serverTokenKey(id)),
      _deleteSecure(_serverRelayTokenKey(id)),
      _deleteSecure(_serverE2eeKey(id)),
      _deleteSecure(_serverEntranceKey(id)),
    ]);
    _serverTokens.remove(id);
    _serverRelayTokens.remove(id);
    _serverE2eeKeys.remove(id);
    _serverEntrances.remove(id);
  }

  static Future<bool> replaceServerConnection(String previousUrl, ServerConnection server) => serializeConnectionListWrite(() async {
    final list = getServerList(allAccounts: true);
    final index = list.indexWhere(
      (item) => item.url == previousUrl || _matchesSavedServer(item, server) || item.transports.any((t) => t.url == previousUrl),
    );
    if (index < 0) return _saveServerConnection(server);
    final previous = list[index];
    final replacement = previous.copyWith(
      url: server.url,
      entrance: server.entrance,
      token: server.token,
      relayToken: server.relayToken.isNotEmpty ? server.relayToken : previous.relayToken,
      e2eeKey: server.e2eeKey.isNotEmpty ? server.e2eeKey : previous.e2eeKey,
      nodeType: server.nodeType,
      lastConnectedAt: server.lastConnectedAt,
      transports: server.transports.isEmpty ? previous.transports : server.transports,
    );
    await _persistServerSecrets(replacement);
    list[index] = replacement;
    return _writeServerList(list);
  });

  static String? get activeServerUrl => _activeServerUrl;

  static Future<bool> setActiveServerUrl(String url) async {
    final saved = await _prefs.setString(_keyActiveServerUrl, url);
    if (saved && _activeServerUrl != url) {
      _activeServerUrl = url;
      nodeGeneration.value++;
    }
    return saved;
  }

  static String get activeServerNodeType => _activeServerNodeType;
  static NodeTypeSpec get activeServerSpec => NodeTypeSpec.resolve(_activeServerNodeType);
  static bool get isActiveHoxi => activeServerSpec.id == 'hoxi';
  static bool get isActiveGoPanel => activeServerSpec.id == 'gopanel';
  static bool get isActiveBusinessService => activeServerSpec.isAuxService;

  static Future<bool> setActiveServerNodeType(String nodeType) async {
    final normalized = NodeTypeSpec.normalizeNodeType(nodeType);
    _activeServerNodeType = normalized;
    nodeGeneration.value++;
    return _prefsInitialized ? _prefs.setString(_keyActiveServerNodeType, normalized) : true;
  }

  static String? get activeServerToken => _activeServerToken;
  static String? get activeServerE2eeKey => _activeServerE2eeKey;

  static Future<bool> setActiveServerToken(String token) async {
    await _secureStorage.write(key: _secureActiveToken, value: token);
    _activeServerToken = token;
    nodeGeneration.value++;
    return true;
  }

  static String? get activeServerCookie => _activeServerCookie;

  static Future<bool> setActiveServerCookie(String cookie, {ActiveNodeContext? expectedNode, bool merge = false}) {
    final node = expectedNode ?? activeNodeContext;
    return _connectionWrites.run(() async {
      if (!isCurrentNode(node)) return false;
      final value = merge ? mergeCookieStrings(_activeServerCookie, cookie) : cookie;
      await _secureStorage.write(key: _secureActiveCookie, value: value);
      _activeServerCookie = value;
      return true;
    });
  }

  static Future<void> clearActiveServerCookie() async {
    await _deleteSecure(_secureActiveCookie);
    _activeServerCookie = null;
  }

  static String? get activeServerEntrance => _activeServerEntrance;

  static Future<bool> setActiveServerEntrance(String entrance) async {
    final normalized = normalizeServerEntrance(entrance);
    if (normalized.isEmpty) {
      await _deleteSecure(_secureActiveEntrance);
      _activeServerEntrance = null;
      return true;
    }
    await _secureStorage.write(key: _secureActiveEntrance, value: normalized);
    _activeServerEntrance = normalized;
    return true;
  }

  static Future<void> clearActiveServer() => _connectionWrites.run(_clearActiveServer);

  static Future<void> _clearActiveServer() async {
    if (_prefsInitialized) {
      await _prefs.remove(_keyActiveServerUrl);
      await _prefs.remove(_keyActiveServerNodeType);
    }
    await Future.wait([
      _deleteSecure(_secureActiveToken),
      _deleteSecure(_secureActiveE2eeKey),
      _deleteSecure(_secureActiveCookie),
      _deleteSecure(_secureActiveEntrance),
    ]);
    _activeServerToken = null;
    _activeServerE2eeKey = null;
    _activeServerCookie = null;
    _activeServerEntrance = null;
    _activeServerNodeType = 'gopanel';
    _activeServerUrl = null;
    nodeGeneration.value++;
  }

  static String getCodeInstructionDraft(int sessionId) =>
      _prefs.getString(buildCodeInstructionDraftKey(activeServerUrl, sessionId)) ?? '';

  static Future<bool> saveCodeInstructionDraft(int sessionId, String content) {
    final key = buildCodeInstructionDraftKey(activeServerUrl, sessionId);
    if (content.isEmpty) return _prefs.remove(key);
    final value = content.length > _maxCodeDraftLength ? content.substring(content.length - _maxCodeDraftLength) : content;
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
  }) => _prefs.setString(_keyUpgradeRecovery, jsonEncode({
    'logName': logName,
    'currentVersion': currentVersion,
    'targetVersion': targetVersion,
    'startedAt': DateTime.now().toIso8601String(),
  }));

  static Future<bool> clearUpgradeRecovery() => _prefs.remove(_keyUpgradeRecovery);

  static Future<void> _migrateActiveCredentials() async {
    await Future.wait([
      _migratePreference(_keyActiveServerToken, _secureActiveToken),
      _migratePreference(_keyActiveServerCookie, _secureActiveCookie),
    ]);
  }

  static Future<void> _migrateActiveServerAddress() async {
    final url = activeServerUrl;
    if (url == null || url.isEmpty) return;
    try {
      final normalized = normalizeServerAddress(url, _activeServerEntrance ?? '');
      if (normalized.baseUrl != url) await setActiveServerUrl(normalized.baseUrl);
      if ((_activeServerEntrance ?? '').isEmpty && normalized.entrance.isNotEmpty) {
        await setActiveServerEntrance(normalized.entrance);
      }
    } on FormatException {
      return;
    }
  }

  static Future<void> _migratePreference(String preferenceKey, String secureKey) async {
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
      if (isServerActive(server, url) && server.entrance.isNotEmpty) {
        await setActiveServerEntrance(server.entrance);
        return;
      }
    }
  }

  static Future<void> _loadServerCredentials() async {
    final entries = _readServerEntries();
    var needsRewrite = false;
    await Future.wait(entries.map((entry) async {
      final id = (entry['id'] ?? '').toString().trim();
      final rawUrl = (entry['url'] ?? '').toString().trim();
      if (id.isEmpty || rawUrl.isEmpty) return;
      final legacyToken = entry['token']?.toString();
      var legacyEntrance = entry['entrance']?.toString();
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
        // Keep the raw legacy URL when URI parsing fails.
      }
      final storedCreds = await Future.wait([
        _secureStorage.read(key: _serverTokenKey(id)),
        _secureStorage.read(key: _serverRelayTokenKey(id)),
        _secureStorage.read(key: _serverEntranceKey(id)),
        _secureStorage.read(key: _serverE2eeKey(id)),
      ]);
      var token = storedCreds[0];
      final relayToken = storedCreds[1];
      var entrance = storedCreds[2];
      final e2eeKey = storedCreds[3];
      if ((token == null || token.isEmpty) && legacyToken != null && legacyToken.isNotEmpty) {
        await _secureStorage.write(key: _serverTokenKey(id), value: legacyToken);
        token = legacyToken;
      }
      if (token != null && token.isNotEmpty) _serverTokens[id] = token;
      if (relayToken != null && relayToken.isNotEmpty) _serverRelayTokens[id] = relayToken;
      if (e2eeKey != null && e2eeKey.isNotEmpty) _serverE2eeKeys[id] = e2eeKey;
      if ((entrance == null || entrance.isEmpty) && legacyEntrance != null && legacyEntrance.isNotEmpty) {
        entrance = normalizeServerEntrance(legacyEntrance);
        await _secureStorage.write(key: _serverEntranceKey(id), value: entrance);
      }
      if (entrance != null && entrance.isNotEmpty) _serverEntrances[id] = entrance;
      for (final sensitive in const ['token', 'e2eeKey']) {
        if (entry.containsKey(sensitive)) {
          entry.remove(sensitive);
          needsRewrite = true;
        }
      }
      if (entry.containsKey('entrance')) {
        entry.remove('entrance');
        entry['hasEntrance'] = entrance != null && entrance.isNotEmpty;
        needsRewrite = true;
      }
    }));
    _cachedServerList = null;
    if (needsRewrite) await _prefs.setString(_keyServerList, jsonEncode(entries));
  }

  static List<Map<String, dynamic>> _readServerEntries() {
    if (!_prefsInitialized) return [];
    final jsonString = _prefs.getString(_keyServerList);
    if (jsonString == null || jsonString.isEmpty) return [];
    try {
      return (jsonDecode(jsonString) as List<dynamic>)
          .whereType<Map<String, dynamic>>()
          .map((value) => Map<String, dynamic>.from(value))
          .toList();
    } catch (_) {
      return [];
    }
  }

  static Future<bool> _writeServerList(List<ServerConnection> servers) async {
    final saved = await _prefs.setString(_keyServerList, jsonEncode(servers.map((s) => s.toJson()).toList()));
    if (saved) {
      _cachedServerList = List<ServerConnection>.unmodifiable(servers);
      connectionListGeneration.value++;
    }
    return saved;
  }

  static String _serverTokenKey(String id) => 'secure_server_token_$id';
  static String _serverRelayTokenKey(String id) => 'secure_server_relay_token_$id';
  static String _serverE2eeKey(String id) => 'secure_server_e2ee_key_$id';
  static String _serverEntranceKey(String id) => 'secure_server_entrance_$id';
}
