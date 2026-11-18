import 'package:flutter/material.dart';

import '../../../../core/theme/app_theme.dart';
import '../../models/code_git_review.dart';
import 'ai_chat_state_widgets.dart';

/// 任务结束后挂在当前会话回复底部的本轮文件变更卡片（与 PC 端
/// `ChangedFilesSummary` 对齐）：文件名 + 加减行数，点击单文件查看 Diff。
class AiChatChangedFilesCard extends StatelessWidget {
  const AiChatChangedFilesCard({
    super.key,
    required this.changedFiles,
    required this.changedFileStats,
    required this.additions,
    required this.deletions,
    this.onOpenGitReview,
    this.onOpenFileDiff,
  });

  final List<String> changedFiles;
  final List<CodeGitFile> changedFileStats;
  final int additions;
  final int deletions;
  final VoidCallback? onOpenGitReview;
  final ValueChanged<String>? onOpenFileDiff;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(10),
      onTap: onOpenGitReview,
      child: Container(
        width: double.infinity,
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
        decoration: BoxDecoration(
          color: AppTheme.surfaceMuted.withValues(alpha: 0.65),
          borderRadius: BorderRadius.circular(10),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(
                  Icons.difference_outlined,
                  size: 14,
                  color: AppTheme.primaryBlueText,
                ),
                const SizedBox(width: 6),
                Expanded(
                  child: Row(
                    children: [
                      Flexible(
                        child: Text(
                          '变更 ${changedFiles.length} 个文件',
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(
                            color: AppTheme.textPrimary,
                            fontSize: 12,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                      ),
                      const SizedBox(width: 8),
                      AiDiffStatBadges(
                        additions: additions,
                        deletions: deletions,
                        fontSize: 11.5,
                      ),
                    ],
                  ),
                ),
                if (onOpenGitReview != null) ...[
                  Text(
                    '审查变更',
                    style: TextStyle(
                      color: AppTheme.primaryBlueText,
                      fontSize: 11.5,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  Icon(
                    Icons.chevron_right_rounded,
                    size: 15,
                    color: AppTheme.primaryBlueText,
                  ),
                ],
              ],
            ),
            const SizedBox(height: 6),
            Column(
              children: changedFiles
                  .map(
                    (file) => _ChangedFileRow(
                      file: file,
                      stat: changedFileStats
                          .where((item) => item.path == file)
                          .firstOrNull,
                      onTap: onOpenFileDiff != null
                          ? () => onOpenFileDiff!(file)
                          : onOpenGitReview,
                    ),
                  )
                  .toList(),
            ),
          ],
        ),
      ),
    );
  }
}

class _ChangedFileRow extends StatelessWidget {
  const _ChangedFileRow({
    required this.file,
    required this.stat,
    required this.onTap,
  });

  final String file;
  final CodeGitFile? stat;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final normalized = file.replaceAll(r'\', '/');
    final slashIdx = normalized.lastIndexOf('/');
    final name = slashIdx >= 0 ? normalized.substring(slashIdx + 1) : normalized;
    return Tooltip(
      message: file,
      child: InkWell(
        borderRadius: BorderRadius.circular(5),
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 2, vertical: 3),
          child: Row(
            children: [
              Icon(
                Icons.insert_drive_file_outlined,
                size: 13,
                color: AppTheme.textSecondary,
              ),
              const SizedBox(width: 5),
              Expanded(
                child: Text(
                  name,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 11,
                    fontFamily: 'monospace',
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              if (stat != null && (stat!.additions > 0 || stat!.deletions > 0)) ...[
                const SizedBox(width: 6),
                AiDiffStatBadges(
                  additions: stat!.additions,
                  deletions: stat!.deletions,
                  fontSize: 10.5,
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }
}
