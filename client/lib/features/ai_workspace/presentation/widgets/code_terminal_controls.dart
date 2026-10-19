import 'package:flutter/material.dart';

import '../code_workspace_text.dart';

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
            ? CodeWorkspaceText.t(context, 'terminal.connected')
            : reconnecting
            ? CodeWorkspaceText.t(context, 'terminal.reconnecting')
            : CodeWorkspaceText.t(context, 'terminal.disconnected'),
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
  });

  final bool connecting;
  final bool reconnecting;
  final bool connectionFailed;
  final bool serverClosed;
  final VoidCallback onReconnect;

  @override
  Widget build(BuildContext context) {
    if (!connecting && !connectionFailed) return const SizedBox.shrink();
    final failed = connectionFailed && !connecting;
    final color = failed ? const Color(0xFFFBBF24) : const Color(0xFF60A5FA);
    final label = serverClosed
        ? CodeWorkspaceText.t(context, 'terminal.sessionClosed')
        : failed
        ? CodeWorkspaceText.t(context, 'terminal.connectionFailed')
        : reconnecting
        ? CodeWorkspaceText.t(context, 'terminal.reconnecting')
        : CodeWorkspaceText.t(context, 'terminal.connecting');
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
                serverClosed
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
            if (failed && !serverClosed)
              TextButton.icon(
                onPressed: onReconnect,
                style: TextButton.styleFrom(
                  foregroundColor: const Color(0xFF93C5FD),
                  visualDensity: VisualDensity.compact,
                  padding: const EdgeInsets.symmetric(horizontal: 8),
                ),
                icon: const Icon(Icons.refresh_rounded, size: 17),
                label: Text(CodeWorkspaceText.t(context, 'terminal.reconnect')),
              ),
          ],
        ),
      ),
    );
  }
}

class CodeTerminalShortcutBar extends StatelessWidget {
  const CodeTerminalShortcutBar({
    super.key,
    required this.enabled,
    required this.onShortcut,
    required this.onEnter,
    required this.onKeyboard,
  });

  final bool enabled;
  final ValueChanged<String> onShortcut;
  final VoidCallback onEnter;
  final VoidCallback onKeyboard;

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      top: false,
      child: Container(
        height: 40,
        decoration: const BoxDecoration(
          color: Color(0xFF020617),
          border: Border(top: BorderSide(color: Color(0xFF1E293B))),
        ),
        padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 4),
        child: Row(
          children: [
            Expanded(
              child: ListView(
                scrollDirection: Axis.horizontal,
                children: [
                  for (final shortcut in const [
                    ('Esc', '\x1b'),
                    ('Tab', '\t'),
                    ('^C', '\x03'),
                    ('←', '\x1b[D'),
                    ('↑', '\x1b[A'),
                    ('↓', '\x1b[B'),
                    ('→', '\x1b[C'),
                    ('⌫', '\x7f'),
                  ]) ...[
                    _TerminalKey(
                      label: shortcut.$1,
                      enabled: enabled,
                      danger: shortcut.$1 == '^C',
                      onPressed: () => onShortcut(shortcut.$2),
                    ),
                    const SizedBox(width: 4),
                  ],
                  _TerminalKey(
                    label: '↵',
                    enabled: enabled,
                    accent: true,
                    onPressed: onEnter,
                  ),
                ],
              ),
            ),
            const SizedBox(width: 5),
            _TerminalKey(
              label: '键盘',
              icon: Icons.keyboard_alt_outlined,
              enabled: enabled,
              onPressed: onKeyboard,
            ),
          ],
        ),
      ),
    );
  }
}

class _TerminalKey extends StatelessWidget {
  const _TerminalKey({
    required this.label,
    required this.enabled,
    required this.onPressed,
    this.accent = false,
    this.danger = false,
    this.icon,
  });

  final String label;
  final bool enabled;
  final VoidCallback onPressed;
  final bool accent;
  final bool danger;
  final IconData? icon;

  @override
  Widget build(BuildContext context) {
    final foreground = accent
        ? const Color(0xFF93C5FD)
        : danger
        ? const Color(0xFFFCA5A5)
        : const Color(0xFFCBD5E1);
    return Material(
      color: accent
          ? const Color(0xFF1E3A5F)
          : danger
          ? const Color(0xFF3F1D26)
          : const Color(0xFF111827),
      borderRadius: BorderRadius.circular(7),
      child: InkWell(
        onTap: enabled ? onPressed : null,
        borderRadius: BorderRadius.circular(7),
        child: ConstrainedBox(
          constraints: const BoxConstraints(minWidth: 36),
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 8),
            child: Center(
              child: Tooltip(
                message: label,
                child: icon == null
                    ? Text(
                        label,
                        style: TextStyle(
                          color: enabled
                              ? foreground
                              : foreground.withValues(alpha: 0.3),
                          fontSize: 11,
                          fontWeight: FontWeight.w600,
                        ),
                      )
                    : Icon(
                        icon,
                        size: 17,
                        color: enabled
                            ? foreground
                            : foreground.withValues(alpha: 0.3),
                      ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}
