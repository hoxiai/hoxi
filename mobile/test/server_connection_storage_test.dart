import 'dart:convert';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/core/storage/storage_service.dart';
import 'package:hoxi/core/storage/server_connection.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  test('server list serialization excludes credentials', () {
    final connection = ServerConnection(
      id: 'server-1',
      name: 'demo',
      url: 'https://example.com',
      token: 'secret-token',
      lastConnectedAt: DateTime.utc(2026),
    );

    expect(connection.toJson().containsKey('token'), isFalse);
    expect(connection.toJson()['hasEntrance'], isFalse);
  });

  test('normalizes a full secure entrance URL', () {
    final result = normalizeServerAddress(
      'https://panel.example.com/private-entry/',
    );

    expect(result.baseUrl, 'https://panel.example.com');
    expect(result.entrance, 'private-entry');
  });

  test('rejects conflicting or unsafe entrance values', () {
    expect(
      () => normalizeServerAddress('https://panel.example.com/first', 'second'),
      throwsFormatException,
    );
    expect(isValidServerEntrance('private_entry-1'), isTrue);
    expect(isValidServerEntrance('private/entry'), isFalse);
  });

  test('replaces a server URL port without changing its path semantics', () {
    expect(
      replaceServerUrlPort('https://panel.example.com', 8443),
      'https://panel.example.com:8443',
    );
  });

  test('legacy server list entries still load for credential migration', () {
    final connection = ServerConnection.fromJson({
      'id': 'server-1',
      'name': 'demo',
      'url': 'https://example.com',
      'token': 'legacy-token',
      'lastConnectedAt': '2026-01-01T00:00:00.000Z',
    });

    expect(connection.token, 'legacy-token');
  });

  test('macOS uses the classic keychain for ad-hoc signed builds', () {
    expect(
      buildMacOsSecureStorageOptions(
        releaseMode: false,
      ).usesDataProtectionKeychain,
      isFalse,
    );
    expect(
      buildMacOsSecureStorageOptions(
        releaseMode: true,
      ).usesDataProtectionKeychain,
      isFalse,
    );
  });

  test('Code instruction drafts are isolated by server and session', () {
    final firstServer = buildCodeInstructionDraftKey(
      'https://one.example.com',
      12,
    );
    final secondServer = buildCodeInstructionDraftKey(
      'https://two.example.com',
      12,
    );

    expect(firstServer, isNot(secondServer));
    expect(firstServer, isNot(buildCodeInstructionDraftKey(null, 12)));
    expect(
      firstServer,
      isNot(buildCodeInstructionDraftKey('https://one.example.com', 13)),
    );
    expect(
      firstServer,
      buildCodeInstructionDraftKey(' HTTPS://ONE.EXAMPLE.COM ', 12),
    );
  });

  test('corrupted server_list entries are skipped without crashing getServerList or init', () async {
    SharedPreferences.setMockInitialValues({
      'server_list': jsonEncode([
        {'id': null, 'name': 'broken-no-id', 'url': 'https://broken.example.com'},
        {'id': 'broken-no-url', 'name': 'no-url'},
        {
          'id': 123,
          'name': 456,
          'url': 'https://coerced.example.com',
          'lastConnectedAt': 'not-a-date',
        },
        {
          'id': 'legacy-lan-tv',
          'name': 'Living Room TV',
          'url': 'http://192.168.1.50:8060',
          'nodeType': 'lan_device',
          'lastConnectedAt': '2026-01-01T00:00:00.000Z',
        },
        {
          'id': 'valid-1',
          'name': 'Studio',
          'url': 'http://192.168.1.20:5417',
          'nodeType': 'hoxi',
          'lastConnectedAt': '2026-01-01T00:00:00.000Z',
        },
      ]),
    });
    FlutterSecureStorage.setMockInitialValues({});
    await StorageService.init();

    final list = StorageService.getServerList();
    expect(list.map((s) => s.id), ['123', 'valid-1']);
    expect(list.first.url, 'https://coerced.example.com');
    expect(list.last.isHoxi, isTrue);
  });

  test('updateServerDetails updates saved device details and active state', () async {
    SharedPreferences.setMockInitialValues({});
    FlutterSecureStorage.setMockInitialValues({});
    await StorageService.init();

    final node = ServerConnection(
      id: 'node-1',
      name: '公司面板',
      url: 'https://panel.example.com',
      token: 'tok-1',
      lastConnectedAt: DateTime.utc(2026),
    );
    await StorageService.saveServerConnection(node);

    final ok = await StorageService.updateServerDetails(
      id: node.id,
      name: '新面板',
      url: 'https://new-panel.example.com',
      entrance: 'safe-entry',
      token: 'new-token',
    );

    expect(ok, isTrue);
    final saved = StorageService.getServerList().single;
    expect(saved.name, '新面板');
    expect(saved.url, 'https://new-panel.example.com');
    expect(saved.entrance, 'safe-entry');
    expect(saved.token, 'new-token');
  });

  test('safeSecureStorageDelete safely deletes keys and handles non-existent keys', () async {
    FlutterSecureStorage.setMockInitialValues({'test_key': 'test_val'});
    const storage = FlutterSecureStorage();

    expect(await storage.read(key: 'test_key'), 'test_val');
    await safeSecureStorageDelete(storage, 'test_key');
    expect(await storage.read(key: 'test_key'), isNull);

    // Deleting a non-existent key does not throw
    await expectLater(
      safeSecureStorageDelete(storage, 'test_key_does_not_exist'),
      completes,
    );
  });
}
