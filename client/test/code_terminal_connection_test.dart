import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:gopanel/features/ai_workspace/presentation/controllers/code_terminal_socket.dart';

void main() {
  test('terminal handshake reuses JWT and security entrance cookie', () {
    final headers = buildCodeTerminalHeaders(
      token: 'jwt-token',
      cookie: 'Entrance=secure-entry; session=active',
    );

    expect(headers['X-Auth'], 'jwt-token');
    expect(headers['Cookie'], 'Entrance=secure-entry; session=active');
  });

  test('terminal handshake omits an empty cookie', () {
    final headers = buildCodeTerminalHeaders(token: 'jwt-token', cookie: '  ');

    expect(headers, {'X-Auth': 'jwt-token'});
  });

  test('builds AI session terminal websocket URL', () {
    final uri = buildCodeTerminalUri(
      server: 'https://panel.example.com/entrance',
      token: 'jwt-token',
      sessionId: 12,
      mode: CodeTerminalMode.aiSession,
      cols: 100,
      rows: 30,
      entrance: 'private-entry',
      lastSequence: 8,
      attachOnly: true,
      takeControl: true,
    );

    expect(uri.scheme, 'wss');
    expect(uri.path, '/api/code/terminal');
    expect(uri.queryParameters['session_id'], '12');
    expect(uri.queryParameters['after_sequence'], '8');
    expect(uri.queryParameters['attach_only'], '1');
    expect(uri.queryParameters['take_control'], '1');
    expect(uri.queryParameters.containsKey('read_only'), isFalse);
    expect(uri.queryParameters['entrance'], 'cHJpdmF0ZS1lbnRyeQ==');
  });

  test('builds read-only AI attachment without taking control', () {
    final uri = buildCodeTerminalUri(
      server: 'https://panel.example.com',
      token: 'jwt-token',
      sessionId: 12,
      mode: CodeTerminalMode.aiSession,
      cols: 80,
      rows: 24,
      attachOnly: true,
      readOnly: true,
    );

    expect(uri.queryParameters['attach_only'], '1');
    expect(uri.queryParameters['read_only'], '1');
    expect(uri.queryParameters.containsKey('take_control'), isFalse);
  });

  test('builds project terminal websocket URL', () {
    final uri = buildCodeTerminalUri(
      server: 'http://panel.example.com',
      token: 'jwt-token',
      sessionId: 27,
      mode: CodeTerminalMode.projectTerminal,
      cols: 80,
      rows: 24,
    );

    expect(uri.scheme, 'ws');
    expect(uri.path, '/api/code/project-terminal/27/ws');
    expect(uri.queryParameters, {'token': 'jwt-token'});
    expect(uri.queryParameters.containsKey('attach_only'), isFalse);
    expect(uri.queryParameters.containsKey('take_control'), isFalse);
    expect(uri.queryParameters.containsKey('read_only'), isFalse);
  });

  test('validates authoritative terminal dimensions', () {
    final size = codeTerminalAuthoritativeSize('120', 36);

    expect(size?.cols, 120);
    expect(size?.rows, 36);
    expect(codeTerminalAuthoritativeSize(80.5, 24), isNull);
    expect(codeTerminalAuthoritativeSize(double.nan, 24), isNull);
    expect(codeTerminalAuthoritativeSize(0, 24), isNull);
  });

  test('bounds reconnect attempts by terminal protocol', () {
    expect(shouldRetryCodeTerminal(nativeProtocol: false, attempts: 2), isTrue);
    expect(
      shouldRetryCodeTerminal(nativeProtocol: false, attempts: 3),
      isFalse,
    );
    expect(shouldRetryCodeTerminal(nativeProtocol: true, attempts: 19), isTrue);
    expect(
      shouldRetryCodeTerminal(nativeProtocol: true, attempts: 20),
      isFalse,
    );
  });

  test('take-control message includes current terminal dimensions', () {
    final message = jsonDecode(buildCodeTerminalControlMessage(96, 28));
    final size = jsonDecode(message['data'] as String);

    expect(message['type'], 'take_control');
    expect(size, {'cols': 96, 'rows': 28});
  });
}
