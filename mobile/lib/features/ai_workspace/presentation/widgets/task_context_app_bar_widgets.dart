import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../../../../core/l10n/l10n.dart';
import '../../../../core/theme/app_theme.dart';

/// Circular back button aligned with the modern compact task/session header.
class TaskHeaderBackButton extends StatelessWidget {
  const TaskHeaderBackButton({super.key, this.onPressed});

  final VoidCallback? onPressed;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.only(left: 10, right: 4),
        child: Material(
          color: AppTheme.isDark ? AppTheme.surfaceMuted : AppTheme.surface,
          shape: CircleBorder(
            side: BorderSide(color: AppTheme.border, width: 1),
          ),
          clipBehavior: Clip.antiAlias,
          child: InkWell(
            key: const ValueKey('task_header_back_btn'),
            customBorder: const CircleBorder(),
            onTap: onPressed ?? () => Navigator.of(context).maybePop(),
            child: SizedBox(
              width: 36,
              height: 36,
              child: Icon(
                Icons.chevron_left_rounded,
                size: 22,
                color: AppTheme.textPrimary,
              ),
            ),
          ),
        ),
      ),
    );
  }
}

/// Two-line header block showing the task title and `项目名 · 节点名` subtitle.
class TaskHeaderTitleBlock extends StatelessWidget {
  const TaskHeaderTitleBlock({
    super.key,
    required this.title,
    this.subtitle = '',
    this.subtitleKey,
  });

  final String title;
  final String subtitle;
  final Key? subtitleKey;

  @override
  Widget build(BuildContext context) {
    final trimmedSub = subtitle.trim();
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          title,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: TextStyle(
            color: AppTheme.textPrimary,
            fontSize: 15,
            fontWeight: FontWeight.w700,
            height: 1.2,
          ),
        ),
        if (trimmedSub.isNotEmpty) ...[
          const SizedBox(height: 2),
          Text(
            trimmedSub,
            key: subtitleKey,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 11.5,
              fontWeight: FontWeight.w500,
              height: 1.2,
            ),
          ),
        ],
      ],
    );
  }
}

/// Compact pill-shaped action bar on the right of [AiChatScreen] header:
/// `[ 新建/切换会话 | ··· 更多操作 ]`
class AiChatHeaderActionCapsule extends StatelessWidget {
  const AiChatHeaderActionCapsule({
    super.key,
    required this.hasSession,
    required this.searchOpen,
    required this.onOpenSessionSheet,
    required this.onMenuSelected,
  });

  final bool hasSession;
  final bool searchOpen;
  final VoidCallback onOpenSessionSheet;
  final ValueChanged<String> onMenuSelected;

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    return Center(
      child: Container(
        height: 36,
        margin: const EdgeInsets.only(left: 6, right: 12),
        padding: const EdgeInsets.symmetric(horizontal: 2),
        decoration: BoxDecoration(
          color: AppTheme.isDark
              ? AppTheme.surfaceMuted
              : AppTheme.surfaceMuted.withValues(alpha: 0.72),
          borderRadius: BorderRadius.circular(999),
          border: Border.all(color: AppTheme.border, width: 1),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            IconButton(
              key: const ValueKey('ai_chat_session_sheet_btn'),
              visualDensity: VisualDensity.compact,
              padding: const EdgeInsets.symmetric(horizontal: 8),
              constraints: const BoxConstraints(minWidth: 34, minHeight: 32),
              tooltip: l10n.codeWsChatChooseSession,
              onPressed: onOpenSessionSheet,
              icon: Icon(
                CupertinoIcons.square_pencil,
                size: 18,
                color: AppTheme.textPrimary,
              ),
            ),
            Container(
              width: 1,
              height: 15,
              color: AppTheme.border.withValues(alpha: 0.9),
            ),
            PopupMenuButton<String>(
              key: const ValueKey('ai_chat_more_menu_btn'),
              padding: const EdgeInsets.symmetric(horizontal: 8),
              constraints: const BoxConstraints(minWidth: 34, minHeight: 32),
              icon: Icon(
                Icons.more_horiz_rounded,
                size: 20,
                color: AppTheme.textPrimary,
              ),
              onSelected: onMenuSelected,
              itemBuilder: (menuContext) {
                final menuL10n = menuContext.l10n;
                final isZh = menuContext.isChineseLocale;
                return [
                  if (hasSession) ...[
                    _buildMenuItem(
                      'search',
                      searchOpen
                          ? Icons.search_off_rounded
                          : Icons.search_rounded,
                      isZh ? '搜索会话内容' : 'Search session',
                    ),
                    _buildMenuItem(
                      'git_review',
                      Icons.source_outlined,
                      menuL10n.codeWsChatGitReview,
                    ),
                    _buildMenuItem(
                      'terminal',
                      Icons.terminal_rounded,
                      menuL10n.codeWsTerminalProjectTitle,
                    ),
                    const PopupMenuDivider(height: 8),
                    _buildMenuItem(
                      'task_detail',
                      Icons.receipt_long_outlined,
                      isZh ? '任务详情' : 'Task details',
                    ),
                    _buildMenuItem(
                      'files',
                      Icons.folder_open_rounded,
                      menuL10n.codeWsFilesTitle,
                    ),
                    _buildMenuItem(
                      'settings',
                      Icons.tune_rounded,
                      menuL10n.codeWsSessionSettingsTitle,
                    ),
                    _buildMenuItem(
                      'recovery',
                      Icons.history_rounded,
                      menuL10n.codeWsRecoveryTitle,
                    ),
                  ],
                  _buildMenuItem(
                    'refresh',
                    Icons.refresh_rounded,
                    menuL10n.codeWsChatRefresh,
                  ),
                ];
              },
            ),
          ],
        ),
      ),
    );
  }

  PopupMenuItem<String> _buildMenuItem(
    String value,
    IconData icon,
    String label,
  ) {
    return PopupMenuItem<String>(
      value: value,
      child: Row(
        children: [
          Icon(icon, size: 18),
          const SizedBox(width: 10),
          Text(label),
        ],
      ),
    );
  }
}
