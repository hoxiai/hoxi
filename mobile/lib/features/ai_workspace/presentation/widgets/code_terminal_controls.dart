import 'package:flutter/material.dart';
import '../../../../core/l10n/l10n.dart';


class CodeTerminalProjectLabel extends StatelessWidget {
  const CodeTerminalProjectLabel({super.key, required this.projectName});

  final String projectName;

  @override
  Widget build(BuildContext context) {
    if (projectName.isEmpty) return const SizedBox.shrink();
    return Tooltip(
      message: projectName,
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 72),
        child: Text(
          projectName,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          textAlign: TextAlign.right,
          style: const TextStyle(fontSize: 11, color: Color(0xFF94A3B8)),
        ),
      ),
    );
  }
}

class CodeTerminalConnectionDot extends StatelessWidget {
  const CodeTerminalConnectionDot({
    super.key,
    required this.connected,
    required this.reconnecting,
  });

  final bool connected;
  final bool reconnecting;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 6),
      child: Tooltip(
        message: connected
            ? context.l10n.codeWsTerminalConnected
            : reconnecting
            ? context.l10n.codeWsTerminalReconnecting
            : context.l10n.codeWsTerminalDisconnected,
        child: Container(
          width: 8,
          height: 8,
          decoration: BoxDecoration(
            shape: BoxShape.circle,
            color: connected
                ? const Color(0xFF4ADE80)
                : reconnecting
                ? const Color(0xFFFBBF24)
                : const Color(0xFF64748B),
          ),
        ),
      ),
    );
  }
}

class CodeTerminalStatusBar extends StatelessWidget {
  const CodeTerminalStatusBar({
    super.key,
    required this.connecting,
    required this.reconnecting,
    required this.connectionFailed,
    required this.serverClosed,
    required this.onReconnect,
    this.relayUnsupported = false,
  });

  final bool connecting;
  final bool reconnecting;
  final bool connectionFailed;
  final bool serverClosed;
  final VoidCallback onReconnect;

  /// 经 GoPanel 隧道中继时网关不支持终端 WebSocket（501），重试没有意义。
  final bool relayUnsupported;

  @override
  Widget build(BuildContext context) {
    if (!connecting && !connectionFailed) return const SizedBox.shrink();
    final failed = connectionFailed && !connecting;
    final color = relayUnsupported
        ? const Color(0xFF93C5FD)
        : failed
        ? const Color(0xFFFBBF24)
        : const Color(0xFF60A5FA);
    final label = relayUnsupported
        ? context.l10n.codeWsTerminalRelayUnsupported
        : serverClosed
        ? context.l10n.codeWsTerminalSessionClosed
        : failed
        ? context.l10n.codeWsTerminalConnectionFailed
        : reconnecting
        ? context.l10n.codeWsTerminalReconnecting
        : context.l10n.codeWsTerminalConnecting;
    return Semantics(
      liveRegion: true,
      child: Container(
        constraints: const BoxConstraints(minHeight: 38),
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
        decoration: const BoxDecoration(
          color: Color(0xFF0F172A),
          border: Border(top: BorderSide(color: Color(0xFF1E293B))),
        ),
        child: Row(
          children: [
            if (!failed) ...[
              SizedBox(
                width: 14,
                height: 14,
                child: CircularProgressIndicator(strokeWidth: 2, color: color),
              ),
              const SizedBox(width: 9),
            ] else ...[
              Icon(
                relayUnsupported
                    ? Icons.vpn_lock_outlined
                    : serverClosed
                    ? Icons.stop_circle_outlined
                    : Icons.cloud_off_outlined,
                size: 17,
                color: color,
              ),
              const SizedBox(width: 8),
            ],
            Expanded(
              child: Text(label, style: TextStyle(color: color, fontSize: 12)),
            ),
            if (failed && !serverClosed && !relayUnsupported)
              TextButton.icon(
                onPressed: onReconnect,
                style: TextButton.styleFrom(
                  foregroundColor: const Color(0xFF93C5FD),
                  visualDensity: VisualDensity.compact,
                  padding: const EdgeInsets.symmetric(horizontal: 8),
                ),
                icon: const Icon(Icons.refresh_rounded, size: 17),
                label: Text(context.l10n.codeWsTerminalReconnect),
              ),
          ],
        ),
      ),
    );
  }
}
