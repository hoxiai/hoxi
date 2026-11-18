import 'package:flutter/material.dart';

import '../../../../core/l10n/l10n.dart';
import '../../../../core/theme/app_theme.dart';
import '../../models/ai_dev_session.dart';
import '../../models/ai_session_state_info.dart';
import '../../models/code_git_review.dart';
import 'task_context_app_bar_widgets.dart';

class AiChatEditableTitle extends StatefulWidget {
  const AiChatEditableTitle({
    super.key,
    required this.session,
    required this.taskTitle,
    required this.onSaveTitle,
    this.subtitle = '',
    this.isSaving = false,
  });

  final AiDevSession? session;
  final String? taskTitle;
  final Future<bool> Function(String title) onSaveTitle;
  final String subtitle;
  final bool isSaving;

  @override
  State<AiChatEditableTitle> createState() => _AiChatEditableTitleState();
}

class _AiChatEditableTitleState extends State<AiChatEditableTitle> {
  static const Object _tapRegionGroupId = 'ai_chat_editable_title_group';
  final TextEditingController _controller = TextEditingController();
  final FocusNode _focusNode = FocusNode();
  bool _isEditing = false;
  int? _editingSessionId;

  @override
  void didUpdateWidget(covariant AiChatEditableTitle oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (_isEditing && widget.session?.id != _editingSessionId) {
      _isEditing = false;
      _editingSessionId = null;
    }
  }

  @override
  void dispose() {
    _controller.dispose();
    _focusNode.dispose();
    super.dispose();
  }

  String _displayTitle(BuildContext context) => resolveSessionDisplayTitle(
    session: widget.session,
    taskTitle: widget.taskTitle,
    fallback: context.l10n.codeWsChatTitle,
  );

  void _startEditing(BuildContext context) {
    final session = widget.session;
    if (session == null || widget.isSaving) return;
    final initial = _displayTitle(context).trim();
    setState(() {
      _isEditing = true;
      _editingSessionId = session.id;
      _controller.value = TextEditingValue(
        text: initial,
        selection: TextSelection(baseOffset: 0, extentOffset: initial.length),
      );
    });
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted && _isEditing) {
        _focusNode.requestFocus();
      }
    });
  }

  void _cancelEditing() {
    if (!_isEditing) return;
    setState(() {
      _isEditing = false;
      _editingSessionId = null;
    });
  }

  Future<void> _submitEditing() async {
    final session = widget.session;
    if (!_isEditing || session == null || widget.isSaving) return;
    final nextTitle = _controller.text.trim();
    final currentDisplayTitle = _displayTitle(context).trim();
    final currentRawTitle = session.title.trim();
    setState(() {
      _isEditing = false;
      _editingSessionId = null;
    });
    if (nextTitle.isEmpty ||
        (nextTitle == currentRawTitle && nextTitle == currentDisplayTitle)) {
      return;
    }
    final success = await widget.onSaveTitle(nextTitle);
    if (!mounted) return;
    if (success) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(context.l10n.codeWsSessionSettingsSaved)),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final session = widget.session;
    final displayTitle = _displayTitle(context);
    if (session == null) {
      return TaskHeaderTitleBlock(
        title: displayTitle,
        subtitle: widget.subtitle,
        subtitleKey: const ValueKey('ai_chat_context_subtitle'),
      );
    }
    if (_isEditing) {
      return TapRegion(
        groupId: _tapRegionGroupId,
        onTapOutside: (_) => _submitEditing(),
        child: Row(
          children: [
            Expanded(
              child: TextField(
                key: const ValueKey('ai_chat_title_input'),
                groupId: _tapRegionGroupId,
                controller: _controller,
                focusNode: _focusNode,
                autofocus: true,
                enabled: !widget.isSaving,
                textInputAction: TextInputAction.done,
                onSubmitted: (_) => _submitEditing(),
                style: TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 14,
                  fontWeight: FontWeight.w600,
                ),
                decoration: InputDecoration(
                  isDense: true,
                  hintText: context.l10n.codeWsSessionSettingsName,
                  contentPadding: const EdgeInsets.symmetric(
                    horizontal: 8,
                    vertical: 6,
                  ),
                  border: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(8),
                  ),
                ),
              ),
            ),
            IconButton(
              key: const ValueKey('ai_chat_title_save'),
              visualDensity: VisualDensity.compact,
              padding: EdgeInsets.zero,
              constraints: const BoxConstraints(minWidth: 30, minHeight: 30),
              tooltip: context.l10n.commonSave,
              onPressed: widget.isSaving ? null : _submitEditing,
              icon: const Icon(Icons.check_rounded, size: 18),
            ),
            IconButton(
              key: const ValueKey('ai_chat_title_cancel'),
              visualDensity: VisualDensity.compact,
              padding: EdgeInsets.zero,
              constraints: const BoxConstraints(minWidth: 28, minHeight: 30),
              tooltip: context.l10n.commonCancel,
              onPressed: _cancelEditing,
              icon: const Icon(Icons.close_rounded, size: 17),
            ),
          ],
        ),
      );
    }
    return InkWell(
      key: const ValueKey('ai_chat_title_tap'),
      borderRadius: BorderRadius.circular(6),
      onTap: () => _startEditing(context),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 2, horizontal: 2),
        child: TaskHeaderTitleBlock(
          title: displayTitle,
          subtitle: widget.subtitle,
          subtitleKey: const ValueKey('ai_chat_context_subtitle'),
        ),
      ),
    );
  }
}

class AiChatErrorBanner extends StatelessWidget {
  const AiChatErrorBanner({
    super.key,
    required this.message,
    required this.onRetry,
    required this.onDismiss,
  });

  final String message;
  final VoidCallback onRetry;
  final VoidCallback onDismiss;

  @override
  Widget build(BuildContext context) {
    return MaterialBanner(
      backgroundColor: AppTheme.errorSoft,
      content: Text(message, style: TextStyle(color: AppTheme.textPrimary)),
      leading: const Icon(Icons.error_outline_rounded, color: AppTheme.error),
      actions: [
        TextButton(
          onPressed: onDismiss,
          child: Text(context.l10n.codeWsActionDismiss),
        ),
        TextButton(
          onPressed: onRetry,
          child: Text(context.l10n.codeWsActionRetry),
        ),
      ],
    );
  }
}

/// 实时对话流断开时的提示：数据仍会在重连后补齐，用户知道此刻看到的不是实时输出。
class AiChatReconnectingBanner extends StatelessWidget {
  const AiChatReconnectingBanner({super.key});

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      color: AppTheme.warningSoft,
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 6),
      child: Row(
        children: [
          const Icon(Icons.wifi_off_rounded, size: 16, color: AppTheme.warning),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              context.l10n.codeWsChatStreamReconnecting,
              style: const TextStyle(color: AppTheme.warning, fontSize: 12),
            ),
          ),
        ],
      ),
    );
  }
}

class AiChatEmptyWorkspace extends StatelessWidget {
  const AiChatEmptyWorkspace({super.key, required this.onOpenSessions});

  final VoidCallback onOpenSessions;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              Icons.chat_bubble_outline_rounded,
              color: AppTheme.textSecondary,
              size: 52,
            ),
            const SizedBox(height: 16),
            Text(
              context.l10n.codeWsChatEmptyTitle,
              style: TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 18,
                fontWeight: FontWeight.w700,
              ),
            ),
            const SizedBox(height: 8),
            Text(
              context.l10n.codeWsChatEmptyHint,
              textAlign: TextAlign.center,
              style: TextStyle(color: AppTheme.textSecondary, height: 1.5),
            ),
            const SizedBox(height: 20),
            FilledButton.icon(
              onPressed: onOpenSessions,
              icon: const Icon(Icons.add_rounded),
              label: Text(context.l10n.codeWsChatChooseSession),
            ),
          ],
        ),
      ),
    );
  }
}

Future<bool> showAiChatStopConfirmDialog(BuildContext context) async {
  final confirmed = await showDialog<bool>(
    context: context,
    builder: (dialogContext) => AlertDialog(
      title: Text(dialogContext.l10n.codeWsStopTitle),
      content: Text(dialogContext.l10n.codeWsStopDescription),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(dialogContext).pop(false),
          child: Text(dialogContext.l10n.codeWsStopCancel),
        ),
        FilledButton(
          onPressed: () => Navigator.of(dialogContext).pop(true),
          child: Text(dialogContext.l10n.codeWsStopConfirm),
        ),
      ],
    ),
  );
  return confirmed == true;
}

Future<bool> showAiChatDeliveryConfirmDialog(
  BuildContext context, {
  required int files,
  required int additions,
  required int deletions,
}) async {
  final confirmed = await showDialog<bool>(
    context: context,
    builder: (dialogContext) => AlertDialog(
      title: Text(dialogContext.l10n.codeWsDeliveryConfirmTitle),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(dialogContext.l10n.codeWsDeliveryConfirmDescription),
          const SizedBox(height: 10),
          Text(
            dialogContext.l10n.codeWsDeliveryReviewSummary(
              files,
              additions,
              deletions,
            ),
            style: const TextStyle(fontWeight: FontWeight.w600),
          ),
        ],
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(dialogContext).pop(false),
          child: Text(dialogContext.l10n.codeWsDeliveryCancel),
        ),
        FilledButton(
          onPressed: () => Navigator.of(dialogContext).pop(true),
          child: Text(dialogContext.l10n.codeWsDeliveryConfirm),
        ),
      ],
    ),
  );
  return confirmed == true;
}

class AiDiffStatBadges extends StatelessWidget {
  const AiDiffStatBadges({
    super.key,
    required this.additions,
    required this.deletions,
    this.fontSize = 11.5,
  });

  final int additions;
  final int deletions;
  final double fontSize;

  @override
  Widget build(BuildContext context) {
    if (additions <= 0 && deletions <= 0) return const SizedBox.shrink();
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(
          '+$additions',
          style: TextStyle(
            color: const Color(0xFF10B981),
            fontSize: fontSize,
            fontWeight: FontWeight.w700,
            fontFamily: 'monospace',
          ),
        ),
        const SizedBox(width: 5),
        Text(
          '-$deletions',
          style: TextStyle(
            color: const Color(0xFFF43F5E),
            fontSize: fontSize,
            fontWeight: FontWeight.w700,
            fontFamily: 'monospace',
          ),
        ),
      ],
    );
  }
}

/// 输入框上方的代码变更与任务清单浮窗（与桌面端 ComposerShelf / ChangedFilesSummary / TaskProgressCard 对齐）：
/// 实时展示修改文件数量、总加减行数及 Todo 进度，点击可展开查看单文件 Diff 或任务清单明细。
class AiComposerDiffShelf extends StatefulWidget {
  const AiComposerDiffShelf({
    super.key,
    required this.changedFiles,
    required this.changedFileStats,
    required this.additions,
    required this.deletions,
    required this.onOpenGitReview,
    this.todos = const [],
    this.onOpenFileDiff,
  });

  final List<String> changedFiles;
  final List<CodeGitFile> changedFileStats;
  final int additions;
  final int deletions;
  final VoidCallback onOpenGitReview;
  final List<CodeTodoItem> todos;
  final ValueChanged<String>? onOpenFileDiff;

  @override
  State<AiComposerDiffShelf> createState() => _AiComposerDiffShelfState();
}

class _AiComposerDiffShelfState extends State<AiComposerDiffShelf> {
  String? _expandedSection; // 'files' | 'todos'

  @override
  void didUpdateWidget(covariant AiComposerDiffShelf oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (_expandedSection == 'files' && widget.changedFiles.isEmpty) {
      _expandedSection = null;
    } else if (_expandedSection == 'todos' && widget.todos.isEmpty) {
      _expandedSection = null;
    }
  }

  void _toggleSection(String section) {
    setState(() {
      _expandedSection = _expandedSection == section ? null : section;
    });
  }

  @override
  Widget build(BuildContext context) {
    final hasFiles = widget.changedFiles.isNotEmpty;
    final hasTodos = widget.todos.isNotEmpty;
    if (!hasFiles && !hasTodos) return const SizedBox.shrink();

    final statsByPath = <String, CodeGitFile>{
      for (final item in widget.changedFileStats) item.path: item,
    };
    final completedTodos = widget.todos.where((t) => t.isCompleted).length;
    final hasInProgressTodo = widget.todos.any((t) => t.isInProgress);

    final isExpanded =
        (_expandedSection == 'files' && hasFiles) ||
        (_expandedSection == 'todos' && hasTodos);

    return Align(
      alignment: Alignment.centerLeft,
      child: Container(
        margin: const EdgeInsets.fromLTRB(16, 2, 16, 4),
        decoration: BoxDecoration(
          color: isExpanded
              ? AppTheme.surfaceMuted.withValues(alpha: 0.75)
              : Colors.transparent,
          borderRadius: BorderRadius.circular(isExpanded ? 12 : 999),
        ),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (_expandedSection == 'files' && hasFiles)
              ConstrainedBox(
                constraints: const BoxConstraints(maxHeight: 156),
                child: ListView.builder(
                  shrinkWrap: true,
                  padding: const EdgeInsets.fromLTRB(10, 6, 10, 2),
                  itemCount: widget.changedFiles.length,
                  itemBuilder: (context, idx) {
                    final path = widget.changedFiles[idx];
                    final stat = statsByPath[path];
                    final normalized = path.replaceAll(r'\', '/');
                    final slashIdx = normalized.lastIndexOf('/');
                    final name = slashIdx >= 0
                        ? normalized.substring(slashIdx + 1)
                        : normalized;
                    return Tooltip(
                      message: path,
                      child: InkWell(
                        borderRadius: BorderRadius.circular(6),
                        onTap: widget.onOpenFileDiff != null
                            ? () => widget.onOpenFileDiff!(path)
                            : widget.onOpenGitReview,
                        child: Padding(
                          padding: const EdgeInsets.symmetric(
                            horizontal: 2,
                            vertical: 4,
                          ),
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
                                    fontWeight: FontWeight.w600,
                                    fontFamily: 'monospace',
                                  ),
                                ),
                              ),
                              if (stat != null) ...[
                                const SizedBox(width: 6),
                                AiDiffStatBadges(
                                  additions: stat.additions,
                                  deletions: stat.deletions,
                                  fontSize: 10.5,
                                ),
                              ],
                              const SizedBox(width: 2),
                              Icon(
                                Icons.chevron_right_rounded,
                                size: 14,
                                color: AppTheme.textSecondary,
                              ),
                            ],
                          ),
                        ),
                      ),
                    );
                  },
                ),
              ),
            if (_expandedSection == 'todos' && hasTodos)
              ConstrainedBox(
                constraints: const BoxConstraints(maxHeight: 156),
                child: ListView.builder(
                  shrinkWrap: true,
                  padding: const EdgeInsets.fromLTRB(10, 6, 10, 2),
                  itemCount: widget.todos.length,
                  itemBuilder: (context, idx) {
                    final todo = widget.todos[idx];
                    final iconColor = todo.isCompleted
                        ? const Color(0xFF10B981)
                        : todo.isInProgress
                        ? AppTheme.primaryBlue
                        : AppTheme.textSecondary;
                    final iconData = todo.isCompleted
                        ? Icons.check_circle_outline_rounded
                        : todo.isInProgress
                        ? Icons.autorenew_rounded
                        : todo.isCancelled
                        ? Icons.remove_circle_outline_rounded
                        : Icons.radio_button_unchecked_rounded;
                    return Padding(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 2,
                        vertical: 4,
                      ),
                      child: Row(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Padding(
                            padding: const EdgeInsets.only(top: 1.5),
                            child: Icon(iconData, size: 13.5, color: iconColor),
                          ),
                          const SizedBox(width: 6),
                          Expanded(
                            child: Text(
                              todo.content,
                              style: TextStyle(
                                color: (todo.isCompleted || todo.isCancelled)
                                    ? AppTheme.textSecondary
                                    : AppTheme.textPrimary,
                                fontSize: 11.5,
                                fontWeight: todo.isInProgress
                                    ? FontWeight.w600
                                    : FontWeight.w400,
                                decoration:
                                    (todo.isCompleted || todo.isCancelled)
                                    ? TextDecoration.lineThrough
                                    : null,
                                height: 1.35,
                              ),
                            ),
                          ),
                        ],
                      ),
                    );
                  },
                ),
              ),
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  if (hasFiles)
                    Flexible(
                      child: InkWell(
                        borderRadius: BorderRadius.circular(999),
                        onTap: () => _toggleSection('files'),
                        child: Padding(
                          padding: const EdgeInsets.symmetric(vertical: 1),
                          child: Row(
                            mainAxisSize: MainAxisSize.min,
                            children: [
                              Icon(
                                Icons.difference_outlined,
                                size: 13.5,
                                color: AppTheme.primaryBlueText,
                              ),
                              const SizedBox(width: 4),
                              Flexible(
                                child: Text(
                                  '${widget.changedFiles.length} 个文件变更',
                                  maxLines: 1,
                                  overflow: TextOverflow.ellipsis,
                                  style: TextStyle(
                                    color: AppTheme.textPrimary,
                                    fontSize: 11.5,
                                    fontWeight: FontWeight.w600,
                                  ),
                                ),
                              ),
                              const SizedBox(width: 5),
                              AiDiffStatBadges(
                                additions: widget.additions,
                                deletions: widget.deletions,
                                fontSize: 11,
                              ),
                              const SizedBox(width: 2),
                              Icon(
                                _expandedSection == 'files'
                                    ? Icons.keyboard_arrow_down_rounded
                                    : Icons.keyboard_arrow_up_rounded,
                                size: 14,
                                color: AppTheme.textSecondary,
                              ),
                            ],
                          ),
                        ),
                      ),
                    ),
                  if (hasFiles && hasTodos) const SizedBox(width: 8),
                  if (hasTodos)
                    InkWell(
                      borderRadius: BorderRadius.circular(999),
                      onTap: () => _toggleSection('todos'),
                      child: Padding(
                        padding: const EdgeInsets.symmetric(vertical: 1),
                        child: Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Icon(
                              completedTodos == widget.todos.length
                                  ? Icons.check_circle_outline_rounded
                                  : hasInProgressTodo
                                  ? Icons.autorenew_rounded
                                  : Icons.checklist_rounded,
                              size: 13,
                              color: completedTodos == widget.todos.length
                                  ? const Color(0xFF10B981)
                                  : AppTheme.primaryBlueText,
                            ),
                            const SizedBox(width: 4),
                            Text(
                              hasFiles
                                  ? '$completedTodos/${widget.todos.length} 项任务'
                                  : '已完成 $completedTodos/${widget.todos.length} 项任务',
                              style: TextStyle(
                                color: AppTheme.textPrimary,
                                fontSize: 11.5,
                                fontWeight: FontWeight.w500,
                              ),
                            ),
                            const SizedBox(width: 2),
                            Icon(
                              _expandedSection == 'todos'
                                  ? Icons.keyboard_arrow_down_rounded
                                  : Icons.keyboard_arrow_up_rounded,
                              size: 13.5,
                              color: AppTheme.textSecondary,
                            ),
                          ],
                        ),
                      ),
                    ),
                  if (hasFiles) ...[
                    const SizedBox(width: 8),
                    InkWell(
                      borderRadius: BorderRadius.circular(999),
                      onTap: widget.onOpenGitReview,
                      child: Padding(
                        padding: const EdgeInsets.symmetric(vertical: 1),
                        child: Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Text(
                              '查看变更',
                              style: TextStyle(
                                color: AppTheme.primaryBlueText,
                                fontSize: 11.5,
                                fontWeight: FontWeight.w600,
                              ),
                            ),
                            Icon(
                              Icons.chevron_right_rounded,
                              size: 13.5,
                              color: AppTheme.primaryBlueText,
                            ),
                          ],
                        ),
                      ),
                    ),
                  ],
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

