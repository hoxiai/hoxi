import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:web_socket_channel/web_socket_channel.dart';
import 'package:xterm/xterm.dart';

import '../../../../core/storage/storage_service.dart';
import 'code_terminal_socket.dart';

class CodeTerminalClient extends ChangeNotifier {
  CodeTerminalClient({
    required this.terminal,
    required this.sessionId,
    required this.nativeProtocol,
    this.mode = CodeTerminalMode.aiSession,
  }) {
    terminal.onOutput = sendInput;
    terminal.onResize = _handleResize;
  }

  final Terminal terminal;
  final int sessionId;
  final bool nativeProtocol;
  final CodeTerminalMode mode;

  WebSocketChannel? _channel;
  StreamSubscription<dynamic>? _subscription;
  Timer? _reconnectTimer;
  Timer? _pingTimer;
  DateTime? _connectionOpenedAt;
  bool _connecting = false;
  bool _connected = false;
  bool _reconnecting = false;
  bool _connectionFailed = false;
  bool _serverClosed = false;
  bool _hasControl = false;
  bool _closing = false;
  bool _disposed = false;
  bool _forceStartOnConnect = false;
  bool _takeControlOnConnect = false;
  int _reconnectAttempts = 0;
  int _lastSequence = 0;
  int _resyncRequest = 0;
  int _cols = 80;
  int _rows = 24;
  String _pendingResyncId = '';

  bool get connecting => _connecting;
  bool get connected => _connected;
  bool get reconnecting => _reconnecting;
  bool get connectionFailed => _connectionFailed;
  bool get serverClosed => _serverClosed;
  bool get hasControl => _hasControl;
  bool get canInput => _connected && _hasControl;

  Uri? _terminalUri() {
    final server = StorageService.activeServerUrl;
    final token = StorageService.activeServerToken;
    if (server == null || server.isEmpty || token == null || token.isEmpty) {
      return null;
    }
    return buildCodeTerminalUri(
      server: server,
      token: token,
      sessionId: sessionId,
      mode: mode,
      cols: _cols,
      rows: _rows,
      entrance: StorageService.activeServerEntrance ?? '',
      lastSequence: _lastSequence,
      attachOnly: mode == CodeTerminalMode.aiSession && !_forceStartOnConnect,
      takeControl: mode == CodeTerminalMode.aiSession && _takeControlOnConnect,
      readOnly: mode == CodeTerminalMode.aiSession && !_takeControlOnConnect,
    );
  }

  Future<void> connect() async {
    if (_disposed || _closing) return;
    _connecting = true;
    _connectionFailed = false;
    _serverClosed = false;
    _notify();
    final uri = _terminalUri();
    if (uri == null) {
      _closing = true;
      _connecting = false;
      _connectionFailed = true;
      _writeSystem('缺少服务器连接信息，无法打开终端。', error: true);
      _notify();
      return;
    }

    final previousChannel = _channel;
    _channel = null;
    await _subscription?.cancel();
    _subscription = null;
    previousChannel?.sink.close();

    final channel = connectCodeTerminalSocket(
      uri,
      token: StorageService.activeServerToken!,
      cookie: StorageService.activeServerCookie,
    );
    _channel = channel;
    _subscription = channel.stream.listen(
      (event) => _handleMessage(channel, event),
      onError: (Object _) {
        if (_channel != channel) return;
        _handleDisconnect(channel);
      },
      onDone: () => _handleDisconnect(channel),
      cancelOnError: true,
    );
    try {
      await channel.ready;
      if (_disposed || _channel != channel) return;
      _connecting = false;
      _connected = true;
      _reconnecting = false;
      _connectionOpenedAt = DateTime.now();
      if (!nativeProtocol) _hasControl = true;
      _startPing();
      _sendResize();
      _notify();
    } catch (_) {
      if (_channel != channel) return;
      _handleDisconnect(channel);
    }
  }

  void reconnect() {
    if (_disposed) return;
    _closing = false;
    _connecting = true;
    _connectionFailed = false;
    _serverClosed = false;
    _reconnectAttempts = 0;
    _connectionOpenedAt = null;
    _forceStartOnConnect = mode == CodeTerminalMode.aiSession;
    _takeControlOnConnect = mode == CodeTerminalMode.aiSession;
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
    _reconnecting = true;
    _notify();
    connect();
  }

  void resumeConnection() {
    if (_disposed ||
        _connected ||
        _connecting ||
        _reconnecting ||
        _serverClosed) {
      return;
    }
    _closing = false;
    _connectionFailed = false;
    _reconnectAttempts = 0;
    _forceStartOnConnect = false;
    _takeControlOnConnect = false;
    connect();
  }

  void suspend() {
    if (_disposed) return;
    _closing = true;
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
    _pingTimer?.cancel();
    _pingTimer = null;
    final channel = _channel;
    _channel = null;
    _subscription?.cancel();
    _subscription = null;
    if (_connected && _hasControl) {
      channel?.sink.add(jsonEncode({'type': 'release_control', 'data': ''}));
    }
    channel?.sink.close();
    _connecting = false;
    _connected = false;
    _reconnecting = false;
    _hasControl = false;
    _connectionOpenedAt = null;
    _pendingResyncId = '';
    _takeControlOnConnect = false;
    _notify();
  }

  void _handleMessage(WebSocketChannel channel, dynamic event) {
    if (_channel != channel || _disposed) return;
    if (!_connected) {
      _connecting = false;
      _connected = true;
      _reconnecting = false;
      _connectionOpenedAt ??= DateTime.now();
      _notify();
    }
    final text = event is List<int> ? utf8.decode(event) : event.toString();
    if (text.contains('启动原生') && text.contains('会话失败')) {
      _pingTimer?.cancel();
      _connecting = false;
      _connected = false;
      _connectionFailed = true;
      _reconnecting = false;
      _updateControl(false);
      terminal.write('$text\r\n');
      _notify();
      return;
    }
    try {
      final message = jsonDecode(text) as Map<String, dynamic>;
      switch ((message['type'] ?? '').toString()) {
        case 'baseline':
          _handleBaseline(message);
        case 'output':
          _handleOutput(message);
        case 'control':
          _applyAuthoritativeSize(message);
          final gainedControl = message['hasControl'] == true;
          _updateControl(gainedControl);
          final reason = (message['controlReason'] ?? message['data'] ?? '')
              .toString();
          if (reason.isNotEmpty) _writeSystem(reason);
        case 'resync_required':
          _requestResync();
        case 'closed':
          _closing = true;
          _connecting = false;
          _connected = false;
          _connectionFailed = true;
          _serverClosed = true;
          _updateControl(false);
          _writeSystem('会话已结束。');
          _notify();
        case 'error':
          _closing = true;
          _connecting = false;
          _connected = false;
          _connectionFailed = true;
          _updateControl(false);
          _writeSystem((message['data'] ?? '终端发生错误。').toString(), error: true);
          _notify();
        case 'inactive':
          _closing = true;
          _connecting = false;
          _connected = false;
          _connectionFailed = true;
          _serverClosed = true;
          _updateControl(false);
          _writeSystem('会话已结束。');
          _notify();
        case 'cmd':
          terminal.write((message['data'] ?? '').toString());
          if (!nativeProtocol) _updateControl(true);
        case 'meta':
        case 'pong':
          break;
        default:
          terminal.write(text);
      }
    } catch (_) {
      terminal.write(text);
    }
  }

  void _handleBaseline(Map<String, dynamic> message) {
    _connectionFailed = false;
    _forceStartOnConnect = false;
    final requestId = (message['requestId'] ?? '').toString();
    if (_pendingResyncId.isNotEmpty &&
        mode == CodeTerminalMode.aiSession &&
        requestId != _pendingResyncId) {
      return;
    }
    _applyAuthoritativeSize(message);
    final chunkIndex = (message['chunkIndex'] as num?)?.toInt() ?? 0;
    final chunkCount = (message['chunkCount'] as num?)?.toInt() ?? 1;
    if ((mode == CodeTerminalMode.projectTerminal ||
            message['truncated'] == true) &&
        chunkIndex == 0) {
      terminal.buffer.clear();
      terminal.buffer.setCursor(0, 0);
      if (message['truncated'] == true) {
        _writeSystem('较早的终端输出已截断。');
      }
    }
    terminal.write((message['data'] ?? '').toString());
    if (chunkIndex != chunkCount - 1) return;
    _lastSequence = (message['sequence'] as num?)?.toInt() ?? 0;
    _pendingResyncId = '';
    final gainedControl = message['hasControl'] == true;
    _updateControl(gainedControl);
    _send('ack', _lastSequence.toString());
  }

  void _handleOutput(Map<String, dynamic> message) {
    _connectionFailed = false;
    if (_pendingResyncId.isNotEmpty) return;
    final sequence = (message['sequence'] as num?)?.toInt() ?? 0;
    if (_lastSequence > 0 && sequence != _lastSequence + 1) {
      _requestResync();
      return;
    }
    terminal.write((message['data'] ?? '').toString());
    _lastSequence = sequence;
    _send('ack', sequence.toString());
  }

  void _handleDisconnect(WebSocketChannel channel) {
    if (_channel != channel) return;
    _pingTimer?.cancel();
    _channel = null;
    _subscription = null;
    _connecting = false;
    _connected = false;
    _hasControl = false;
    _pendingResyncId = '';
    _notify();
    if (_closing || _disposed || _connectionFailed || _reconnectTimer != null) {
      return;
    }
    final openedAt = _connectionOpenedAt;
    if (openedAt != null &&
        DateTime.now().difference(openedAt) >= codeTerminalStableConnection) {
      _reconnectAttempts = 0;
    }
    _connectionOpenedAt = null;
    if (!shouldRetryCodeTerminal(
      nativeProtocol: nativeProtocol,
      attempts: _reconnectAttempts,
    )) {
      _reconnecting = false;
      _connectionFailed = true;
      _notify();
      return;
    }
    _reconnectAttempts++;
    _connecting = true;
    _reconnecting = true;
    _notify();
    _reconnectTimer = Timer(codeTerminalReconnectDelay, () {
      _reconnectTimer = null;
      connect();
    });
  }

  void _startPing() {
    _pingTimer?.cancel();
    _pingTimer = Timer.periodic(
      const Duration(seconds: 15),
      (_) => _send('ping', ''),
    );
  }

  void _requestResync() {
    if (!_connected || _pendingResyncId.isNotEmpty) return;
    _pendingResyncId =
        '${DateTime.now().millisecondsSinceEpoch}-${++_resyncRequest}';
    _send(
      'resync',
      jsonEncode({'sequence': _lastSequence, 'requestId': _pendingResyncId}),
    );
  }

  void _send(String type, String data) {
    if (!_connected) return;
    _channel?.sink.add(jsonEncode({'type': type, 'data': data}));
  }

  void sendInput(String data) {
    if (!canInput || data.isEmpty) return;
    _send('cmd', data);
  }

  void takeControl() {
    if (!_connected) {
      reconnect();
      return;
    }
    _takeControlOnConnect = true;
    _channel?.sink.add(buildCodeTerminalControlMessage(_cols, _rows));
  }

  void releaseControl() {
    _takeControlOnConnect = false;
    _send('release_control', '');
  }

  void _handleResize(int cols, int rows, int pixelWidth, int pixelHeight) {
    _cols = cols;
    _rows = rows;
    _sendResize();
  }

  void _sendResize() {
    if (!canInput) return;
    _send('resize', jsonEncode({'cols': _cols, 'rows': _rows}));
  }

  void _updateControl(bool value) {
    if (_hasControl == value) return;
    _hasControl = value;
    if (value) _sendResize();
    _notify();
  }

  void _applyAuthoritativeSize(Map<String, dynamic> message) {
    if (!nativeProtocol || mode != CodeTerminalMode.aiSession) return;
    final size = codeTerminalAuthoritativeSize(
      message['cols'],
      message['rows'],
    );
    if (size == null ||
        (terminal.viewWidth == size.cols && terminal.viewHeight == size.rows)) {
      return;
    }
    terminal.resize(size.cols, size.rows);
  }

  void _writeSystem(String message, {bool error = false}) {
    final color = error ? '31' : '33';
    terminal.write('\r\n\x1b[${color}m[GoPanel] $message\x1b[0m\r\n');
  }

  void _notify() {
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    suspend();
    _disposed = true;
    terminal.onOutput = null;
    terminal.onResize = null;
    super.dispose();
  }
}
