import 'package:flutter/material.dart';

import '../../../../core/l10n/l10n.dart';
import '../../../../core/theme/app_theme.dart';
import '../../../../shared/widgets/panel/panel_card.dart';
import '../../models/code_git_review.dart';
import 'ai_chat_state_widgets.dart';

class CodeGitSummaryCard extends StatelessWidget {
  const CodeGitSummaryCard({super.key, required this.status});

  final CodeGitStatus status;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(14),
      ),
      child: Wrap(
        spacing: 8,
        runSpacing: 6,
        crossAxisAlignment: WrapCrossAlignment.center,
        children: [
          Text(
            context.l10n.gitReviewSummary(status.files),
            style: TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 13,
              fontWeight: FontWeight.w800,
            ),
          ),
          _CountBadge(
            label: '+${status.totalAdditions}',
            color: AppTheme.success,
          ),
          _CountBadge(
            label: '-${status.totalDeletions}',
            color: AppTheme.error,
          ),
          if (status.staged > 0)
            _CountBadge(
              label: '${context.l10n.gitReviewStaged} ${status.staged}',
              color: AppTheme.primaryBlue,
            ),
          if (status.changed > 0)
            _CountBadge(
              label: '${context.l10n.gitReviewChanged} ${status.changed}',
              color: Colors.orange,
            ),
          if (status.untracked > 0)
            _CountBadge(
              label: '${context.l10n.gitReviewUntracked} ${status.untracked}',
              color: Colors.purple,
            ),
        ],
      ),
    );
  }
}

class CodeGitRepositoryCard extends StatelessWidget {
  const CodeGitRepositoryCard({
    super.key,
    required this.repository,
    required this.onOpenDiff,
  });

  final CodeGitRepositoryStatus repository;
  final void Function(CodeGitFile file, String kind) onOpenDiff;

  @override
  Widget build(BuildContext context) {
    return PanelCard(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
      title: Text(repository.name),
      trailing: Text(
        repository.branch.isEmpty
            ? context.l10n.gitReviewDetached
            : repository.branch,
        style: TextStyle(color: AppTheme.textSecondary, fontSize: 11.5),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (repository.savedCommits > 0) ...[
            Text(
              context.l10n.gitReviewSavedCommits(repository.savedCommits),
              style: const TextStyle(color: AppTheme.success, fontSize: 12),
            ),
            const SizedBox(height: 8),
          ],
          if (repository.truncated) ...[
            _Warning(message: context.l10n.gitReviewTruncated),
            const SizedBox(height: 8),
          ],
          for (var index = 0; index < repository.files.length; index++) ...[
            _FileItem(file: repository.files[index], onOpenDiff: onOpenDiff),
            if (index != repository.files.length - 1) const SizedBox(height: 10),
          ],
        ],
      ),
    );
  }
}

class _FileItem extends StatelessWidget {
  const _FileItem({required this.file, required this.onOpenDiff});

  final CodeGitFile file;
  final void Function(CodeGitFile file, String kind) onOpenDiff;

  @override
  Widget build(BuildContext context) {
    final hasWorking = file.changed || file.untracked;
    final hasBoth = file.staged && hasWorking;
    final primaryKind = hasWorking ? 'working' : 'staged';

    return InkWell(
      borderRadius: BorderRadius.circular(10),
      onTap: () => onOpenDiff(file, primaryKind),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 4, horizontal: 2),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                _FileStatus(file: file),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    file.path,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(
                      fontSize: 13.5,
                      fontWeight: FontWeight.w700,
                    ),
                  ),
                ),
                if (file.additions > 0 || file.deletions > 0) ...[
                  const SizedBox(width: 8),
                  AiDiffStatBadges(
                    additions: file.additions,
                    deletions: file.deletions,
                    fontSize: 11.5,
                  ),
                ],
                const SizedBox(width: 4),
                Icon(
                  Icons.chevron_right_rounded,
                  size: 18,
                  color: AppTheme.textSecondary,
                ),
              ],
            ),
            if (file.oldPath.isNotEmpty) ...[
              const SizedBox(height: 2),
              Padding(
                padding: const EdgeInsets.only(left: 30),
                child: Text(
                  file.oldPath,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(color: AppTheme.textLight, fontSize: 11),
                ),
              ),
            ],
            if (hasBoth) ...[
              const SizedBox(height: 4),
              Padding(
                padding: const EdgeInsets.only(left: 30),
                child: Wrap(
                  spacing: 8,
                  children: [
                    InkWell(
                      borderRadius: BorderRadius.circular(6),
                      onTap: () => onOpenDiff(file, 'staged'),
                      child: Text(
                        context.l10n.gitReviewStagedDiff,
                        style: const TextStyle(
                          color: AppTheme.primaryBlue,
                          fontSize: 11.5,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    ),
                    InkWell(
                      borderRadius: BorderRadius.circular(6),
                      onTap: () => onOpenDiff(file, 'working'),
                      child: Text(
                        context.l10n.gitReviewWorkingDiff,
                        style: const TextStyle(
                          color: AppTheme.primaryBlue,
                          fontSize: 11.5,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }
}

class _FileStatus extends StatelessWidget {
  const _FileStatus({required this.file});

  final CodeGitFile file;

  @override
  Widget build(BuildContext context) {
    final label = file.untracked
        ? 'U'
        : file.worktreeStatus.trim().isNotEmpty
        ? file.worktreeStatus
        : file.indexStatus;
    final color = file.untracked
        ? Colors.purple
        : file.staged && !file.changed
        ? AppTheme.primaryBlue
        : Colors.orange;
    return _CountBadge(label: label, color: color);
  }
}

class _CountBadge extends StatelessWidget {
  const _CountBadge({required this.label, required this.color});

  final String label;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(999),
      ),
      child: Text(
        label,
        style: TextStyle(
          color: color,
          fontSize: 11,
          fontWeight: FontWeight.w700,
        ),
      ),
    );
  }
}

class _Warning extends StatelessWidget {
  const _Warning({required this.message});

  final String message;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: AppTheme.warningSoft,
        borderRadius: BorderRadius.circular(10),
      ),
      child: Text(message, style: TextStyle(color: AppTheme.warningText)),
    );
  }
}
