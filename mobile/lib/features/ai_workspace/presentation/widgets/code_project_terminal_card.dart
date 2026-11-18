import 'package:flutter/material.dart';

import '../../../../core/l10n/l10n.dart';
import '../../../../core/storage/storage_service.dart';
import '../../../../core/theme/app_theme.dart';

class CodeProjectTerminalCard extends StatelessWidget {
  const CodeProjectTerminalCard({super.key, required this.onTap});

  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final isZh = Localizations.localeOf(context).languageCode.startsWith('zh');
    final activeUrl = StorageService.activeServerUrl;
    final activeServer = StorageService.getServerList()
        .where((s) => StorageService.isServerActive(s, activeUrl))
        .firstOrNull;
    final isRelay = activeServer?.isUsingRelay ?? false;

    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(16),
        child: Ink(
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 11),
          decoration: BoxDecoration(
            color: AppTheme.surface,
            borderRadius: BorderRadius.circular(16),
          ),
          child: Row(
            children: [
              Container(
                width: 34,
                height: 34,
                decoration: BoxDecoration(
                  color: AppTheme.primaryBlueLight,
                  borderRadius: BorderRadius.circular(10),
                ),
                child: const Icon(
                  Icons.terminal_rounded,
                  size: 18,
                  color: AppTheme.primaryBlue,
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Row(
                  children: [
                    Flexible(
                      child: Text(
                        context.l10n.codeWsHubProjectTerminal,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: TextStyle(
                          fontWeight: FontWeight.w800,
                          fontSize: 14,
                          color: AppTheme.textPrimary,
                        ),
                      ),
                    ),
                    if (isRelay) ...[
                      const SizedBox(width: 8),
                      Container(
                        key: const ValueKey('terminal_relay_mode_badge'),
                        padding: const EdgeInsets.symmetric(
                          horizontal: 7,
                          vertical: 2,
                        ),
                        decoration: BoxDecoration(
                          color: AppTheme.warningSoft,
                          borderRadius: BorderRadius.circular(999),
                        ),
                        child: Text(
                          isZh ? '仅局域网直连可用' : 'LAN Direct Only',
                          style: TextStyle(
                            color: AppTheme.warning,
                            fontSize: 10.5,
                            fontWeight: FontWeight.w700,
                          ),
                        ),
                      ),
                    ] else ...[
                      const SizedBox(width: 8),
                      Flexible(
                        child: Text(
                          context.l10n.codeWsHubProjectTerminalHint,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(
                            color: AppTheme.textSecondary,
                            fontSize: 12,
                          ),
                        ),
                      ),
                    ],
                  ],
                ),
              ),
              const SizedBox(width: 6),
              Icon(
                Icons.chevron_right_rounded,
                size: 20,
                color: AppTheme.textSecondary,
              ),
            ],
          ),
        ),
      ),
    );
  }
}
