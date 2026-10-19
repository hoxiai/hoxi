import 'package:flutter_test/flutter_test.dart';
import 'package:hoxi/core/storage/storage_service.dart';
import 'package:hoxi/shared/models/server_connection.dart';

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
}
