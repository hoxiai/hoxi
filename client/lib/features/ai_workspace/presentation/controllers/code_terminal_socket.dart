import 'dart:convert';

import 'package:web_socket_channel/web_socket_channel.dart';

import 'code_terminal_socket_fallback.dart'
    if (dart.library.io) 'code_terminal_socket_io.dart'
    as platform;

enum CodeTerminalMode { aiSession, projectTerminal }

const codeTerminalReconnectDelay = Duration(milliseconds: 1500);
const codeTerminalStableConnection = Duration(seconds: 10);
const codeTerminalInitialReconnectLimit = 3;
const codeTerminalNativeReconnectLimit = 20;

class CodeTerminalSize {
  const CodeTerminalSize(this.cols, this.rows);

  final int cols;
  final int rows;
}

CodeTerminalSize? codeTerminalAuthoritativeSize(dynamic cols, dynamic rows) {
  final width = _codeTerminalDimension(cols);
  final height = _codeTerminalDimension(rows);
  if (width == null || height == null) {
    return null;
  }
  return CodeTerminalSize(width, height);
}

int? _codeTerminalDimension(dynamic value) {
  if (value is int) return value > 0 ? value : null;
  if (value is double) {
    if (!value.isFinite || value != value.truncateToDouble()) return null;
    final dimension = value.toInt();
    return dimension > 0 ? dimension : null;
  }
  final dimension = int.tryParse('${value ?? ''}');
  return dimension != null && dimension > 0 ? dimension : null;
}

bool shouldRetryCodeTerminal({
  required bool nativeProtocol,
  required int attempts,
}) {
  final limit = nativeProtocol
      ? codeTerminalNativeReconnectLimit
      : codeTerminalInitialReconnectLimit;
  return attempts < limit;
}

String buildCodeTerminalControlMessage(int cols, int rows) {
  return jsonEncode({
    'type': 'take_control',
    'data': jsonEncode({'cols': cols, 'rows': rows}),
  });
}

Uri buildCodeTerminalUri({
  required String server,
  required String token,
  required int sessionId,
  required CodeTerminalMode mode,
  required int cols,
  required int rows,
  String entrance = '',
  int lastSequence = 0,
  bool attachOnly = false,
  bool takeControl = false,
  bool readOnly = false,
}) {
  final base = Uri.parse(server);
  final projectTerminal = mode == CodeTerminalMode.projectTerminal;
  return base.replace(
    scheme: base.scheme == 'https' ? 'wss' : 'ws',
    path: projectTerminal
        ? '/api/code/project-terminal/$sessionId/ws'
        : '/api/code/terminal',
    queryParameters: {
      'token': token,
      if (entrance.isNotEmpty) 'entrance': base64Encode(utf8.encode(entrance)),
      if (!projectTerminal) 'session_id': sessionId.toString(),
      if (!projectTerminal) 'cols': cols.toString(),
      if (!projectTerminal) 'rows': rows.toString(),
      if (!projectTerminal && lastSequence > 0)
        'after_sequence': lastSequence.toString(),
      if (!projectTerminal && attachOnly) 'attach_only': '1',
      if (!projectTerminal && takeControl) 'take_control': '1',
      if (!projectTerminal && readOnly) 'read_only': '1',
    },
  );
}

Map<String, dynamic> buildCodeTerminalHeaders({
  required String token,
  String? cookie,
}) {
  final normalizedCookie = cookie?.trim() ?? '';
  return {
    'X-Auth': token,
    if (normalizedCookie.isNotEmpty) 'Cookie': normalizedCookie,
  };
}

WebSocketChannel connectCodeTerminalSocket(
  Uri uri, {
  required String token,
  String? cookie,
}) {
  return platform.connectCodeTerminalSocket(
    uri,
    headers: buildCodeTerminalHeaders(token: token, cookie: cookie),
  );
}
