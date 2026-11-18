import 'dart:math' as math;
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/scheduler.dart';
import 'package:flutter/services.dart';

import '../../../../core/theme/app_theme.dart';
import '../../../../shared/widgets/chat_image_preview.dart';
import '../../../../shared/widgets/stream_text_pacer.dart';
import '../../models/chat_message.dart';
import '../../models/code_convoy_plan.dart';
import '../../models/code_git_review.dart';
import '../../models/code_tool_step_summary.dart';
import 'ai_chat_changed_files_card.dart';
import 'ai_chat_markdown.dart';
import 'ai_chat_state_widgets.dart';

class AiChatMessageItem extends StatefulWidget {
  const AiChatMessageItem({
    super.key,
    required this.message,
    this.changedFiles = const [],
    this.changedFileStats = const [],
    this.diffAdditions = 0,
    this.diffDeletions = 0,
    this.convoyPlans = const [],
    this.subagents = const [],
    this.workDir = '',
    this.isSearchHighlighted = false,
    this.onOpenGitReview,
    this.onOpenFileDiff,
    this.onOpenFile,
    this.onOpenSession,
    this.onStartConvoyPlan,
    this.onCancelConvoyPlan,
    this.onRetryConvoyPlan,
    this.onMergeConvoyPlan,
    this.onSummarizeConvoyPlan,
    this.onEditUserMessage,
    this.onRetry,
  });

  final ChatMessage message;
  final List<String> changedFiles;
  final List<CodeGitFile> changedFileStats;
  final int diffAdditions, diffDeletions;
  final List<CodeConvoyPlan> convoyPlans;
  final List<CodeSubagentItem> subagents;
  final String workDir;
  final bool isSearchHighlighted;
  final VoidCallback? onOpenGitReview, onRetry;
  final ValueChanged<String>? onOpenFileDiff, onOpenFile, onCancelConvoyPlan, onRetryConvoyPlan, onMergeConvoyPlan, onEditUserMessage;
  final ValueChanged<int>? onOpenSession;
  final ValueChanged<CodeConvoyPlan>? onStartConvoyPlan, onSummarizeConvoyPlan;

  @override
  State<AiChatMessageItem> createState() => _AiChatMessageItemState();
}

class _AiChatMessageItemState extends State<AiChatMessageItem>
    with SingleTickerProviderStateMixin {
  Widget? _cachedChild;
  bool? _userReasoningExpanded;
  bool _copied = false;

  late final StreamTextPacer _textPacer;
  late final StreamTextPacer _reasoningPacer;
  late final Ticker _ticker;
  final Stopwatch _wallClock = Stopwatch()..start();
  double _virtualNowMs = 0;
  int _lastWallElapsedMs = 0;
  Duration? _lastTickElapsed;
  int _measuredDurationMs = 0;

  double _advanceClock([Duration? tickElapsed]) {
    final wallMs = _wallClock.elapsedMilliseconds;
    final wallDelta = math.max(0, wallMs - _lastWallElapsedMs);
    _lastWallElapsedMs = wallMs;

    var tickDelta = 0.0;
    if (tickElapsed != null) {
      if (_lastTickElapsed != null) {
        tickDelta = (tickElapsed - _lastTickElapsed!).inMicroseconds / 1000.0;
      } else {
        tickDelta = StreamTextPacer.frameMs;
      }
      _lastTickElapsed = tickElapsed;
    }
    _virtualNowMs += math.max(wallDelta.toDouble(), tickDelta);
    return _virtualNowMs;
  }

  bool get _effectiveReasoningExpanded {
    if (_userReasoningExpanded != null) return _userReasoningExpanded!;
    return widget.message.isStreaming &&
        widget.message.text.trim().isEmpty &&
        widget.message.toolSteps.isEmpty;
  }

  @override
  void initState() {
    super.initState();
    _textPacer = StreamTextPacer(initial: widget.message.text, nowMs: 0);
    _reasoningPacer = StreamTextPacer(initial: widget.message.reasoning, nowMs: 0);
    _ticker = createTicker(_onTick);
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _cachedChild = null;
  }

  @override
  void didUpdateWidget(covariant AiChatMessageItem oldWidget) {
    super.didUpdateWidget(oldWidget);
    final now = _advanceClock();
    final msg = widget.message;
    final oldId = oldWidget.message.id;
    final isSameTurnTransition =
        !oldWidget.message.isUser &&
        !msg.isUser &&
        (oldId.startsWith('stream-') || msg.id.startsWith('stream-'));

    if ((oldId != msg.id && !isSameTurnTransition) ||
        oldWidget.message.isUser != msg.isUser ||
        msg.isUser) {
      _stopTicker();
      _measuredDurationMs = 0;
      _userReasoningExpanded = null;
      _textPacer.reset(msg.text, now);
      _reasoningPacer.reset(msg.reasoning, now);
      _cachedChild = null;
      return;
    }

    if (oldWidget.message.isStreaming && !msg.isStreaming) {
      _measuredDurationMs = math.max(_measuredDurationMs, now.round());
    }

    final disableAnimations = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
    final shouldPaceText = !disableAnimations && (msg.isStreaming || _textPacer.pending);
    if (!shouldPaceText || !_textPacer.push(msg.text, now)) {
      _textPacer.reset(msg.text, now);
    }

    final shouldPaceReasoning = !disableAnimations && (msg.isStreaming || _reasoningPacer.pending);
    if (!shouldPaceReasoning || !_reasoningPacer.push(msg.reasoning, now)) {
      _reasoningPacer.reset(msg.reasoning, now);
    }

    if (_textPacer.pending || _reasoningPacer.pending) {
      if (!_ticker.isActive) {
        _lastTickElapsed = null;
        _ticker.start();
      }
    } else {
      _stopTicker();
    }

    if (oldWidget.message != msg ||
        oldWidget.isSearchHighlighted != widget.isSearchHighlighted ||
        !listEquals(oldWidget.changedFiles, widget.changedFiles) ||
        !listEquals(oldWidget.convoyPlans, widget.convoyPlans) ||
        !listEquals(oldWidget.subagents, widget.subagents) ||
        oldWidget.changedFileStats.length != widget.changedFileStats.length ||
        oldWidget.diffAdditions != widget.diffAdditions ||
        oldWidget.diffDeletions != widget.diffDeletions ||
        (oldWidget.onRetry == null) != (widget.onRetry == null)) {
      _cachedChild = null;
    }
  }

  void _stopTicker() {
    if (_ticker.isActive) _ticker.stop();
    _lastTickElapsed = null;
  }

  void _onTick(Duration elapsed) {
    final now = _advanceClock(elapsed);
    final prevText = _textPacer.shown;
    final prevReasoning = _reasoningPacer.shown;
    final nextText = _textPacer.step(now);
    final nextReasoning = _reasoningPacer.step(now);

    if (!_textPacer.pending && !_reasoningPacer.pending) _stopTicker();
    if (nextText != prevText || nextReasoning != prevReasoning) {
      setState(() => _cachedChild = null);
    }
  }

  @override
  void dispose() {
    _ticker.dispose();
    super.dispose();
  }

  Future<void> _copyText() async {
    final text = widget.message.text.trim();
    final content = text.isNotEmpty ? text : widget.message.reasoning.trim();
    if (content.isEmpty) return;
    await Clipboard.setData(ClipboardData(text: content));
    if (!mounted) return;
    setState(() {
      _copied = true;
      _cachedChild = null;
    });
    Future<void>.delayed(const Duration(seconds: 2), () {
      if (!mounted) return;
      setState(() {
        _copied = false;
        _cachedChild = null;
      });
    });
  }

  void _toggleReasoning() {
    setState(() {
      _userReasoningExpanded = !_effectiveReasoningExpanded;
      _cachedChild = null;
    });
  }

  RegExp get _diffStatPattern => RegExp(r'^\+(\d+)\s+-(\d+)$');

  int? _resolveSubagentSessionId(String rawTarget) {
    if (widget.subagents.isEmpty) return null;
    final target = rawTarget.trim();
    if (target.isEmpty) {
      return widget.subagents.length == 1 ? widget.subagents.single.sessionId : null;
    }
    for (final sub in widget.subagents) {
      if (sub.sessionId <= 0) continue;
      if (sub.fullTitle == target ||
          sub.title == target ||
          sub.hoxiSessionId == target ||
          sub.convoyTaskId == target ||
          target.contains(sub.title) ||
          sub.fullTitle.contains(target)) {
        return sub.sessionId;
      }
    }
    return widget.subagents.length == 1 ? widget.subagents.single.sessionId : null;
  }

  ({
    IconData icon,
    String label,
    String detail,
    String path,
    int? subagentSessionId,
    bool isEdit,
    String readMeta,
    int additions,
    int deletions,
  }) _describeToolStep(String step, List<CodeGitFile> fileStats) {
    final parts = step.split(' · ').map((s) => s.trim()).toList(growable: false);
    final rawTool = parts.isNotEmpty ? parts.first : step.trim();
    final lower = rawTool.toLowerCase();
    final isRead = CodeToolStepSummary.isReadTool(lower);
    final isEdit = CodeToolStepSummary.isEditTool(lower);
    final isTaskTool = isTaskLikeToolName(lower) || lower == 'delegate_task' || lower == 'spawn_agent';
    final (icon, label) = switch (lower) {
      'read' || 'view' || 'read_file' || 'view_file' => (Icons.description_outlined, '读取'),
      'edit' || 'write' || 'patch' || 'multiedit' => (Icons.edit_note_rounded, '修改'),
      'edit_file' || 'write_file' || 'apply_patch' || 'replace_file_content' ||
      'multi_replace_file_content' || 'create_file' || 'write_to_file' ||
      'str_replace_based_edit_tool' || 'file_edit' => (Icons.edit_note_rounded, '修改'),
      'bash' || 'command' || 'sh' => (Icons.terminal_rounded, '命令'),
      'grep' || 'search' => (Icons.search_rounded, '搜索'),
      'glob' || 'list' || 'ls' => (Icons.folder_open_outlined, '查找'),
      'todowrite' => (Icons.checklist_rounded, '任务'),
      'task' || 'agent' || 'subagent' || 'delegate_task' || 'spawn_agent' => (Icons.account_tree_rounded, '子任务'),
      'skill' => (Icons.auto_awesome_outlined, '技能'),
      _ => (Icons.handyman_outlined, rawTool),
    };

    var rawTarget = parts.length > 1 ? parts[1] : '';
    var readMeta = '';
    var additions = 0, deletions = 0;

    if (isRead && parts.length > 2) {
      readMeta = parts.sublist(2).join(' · ');
    } else if (isEdit && parts.length > 2) {
      final m = _diffStatPattern.firstMatch(parts.last);
      if (m != null) {
        additions = int.tryParse(m.group(1)!) ?? 0;
        deletions = int.tryParse(m.group(2)!) ?? 0;
        if (parts.length > 3) rawTarget = parts.sublist(1, parts.length - 1).join(' · ');
      } else {
        rawTarget = parts.sublist(1).join(' · ');
      }
    } else if (parts.length > 2) {
      rawTarget = parts.sublist(1).join(' · ');
    }

    final fullPath = _compactStepDetail(rawTarget);
    if (isEdit && additions == 0 && deletions == 0 && rawTarget.isNotEmpty && fileStats.isNotEmpty) {
      final stat = fileStats
          .where((item) => item.path == rawTarget || item.path == fullPath || rawTarget.endsWith('/${item.path}') || item.path.endsWith('/$fullPath'))
          .firstOrNull;
      if (stat != null) {
        additions = stat.additions;
        deletions = stat.deletions;
      }
    }

    final openPath = _normalizeOpenPath(rawTarget);
    final filePath = (isRead || isEdit) && _looksLikeFilePath(openPath) ? openPath : '';
    final subagentSid = isTaskTool ? _resolveSubagentSessionId(rawTarget) : null;
    if (subagentSid != null && additions == 0 && deletions == 0) {
      final matchedSub = widget.subagents.where((s) => s.sessionId == subagentSid).firstOrNull;
      if (matchedSub != null) {
        additions = matchedSub.additions;
        deletions = matchedSub.deletions;
      }
    }
    return (
      icon: icon, label: label,
      detail: filePath.isEmpty ? fullPath : _fileName(filePath),
      path: filePath, subagentSessionId: subagentSid, isEdit: isEdit,
      readMeta: readMeta, additions: additions, deletions: deletions,
    );
  }

  String _normalizeOpenPath(String raw) {
    var path = raw.replaceAll(r'\', '/').trim();
    final root = widget.workDir.replaceAll(r'\', '/').replaceAll(RegExp(r'/+$'), '');
    if (root.isNotEmpty && path.startsWith('$root/')) return path.substring(root.length + 1);
    const worktreeMarker = '/.hoxi/worktrees/';
    final worktreeIdx = path.indexOf(worktreeMarker);
    if (worktreeIdx >= 0) {
      final after = path.substring(worktreeIdx + worktreeMarker.length);
      final slash = after.indexOf('/');
      if (slash >= 0) path = after.substring(slash + 1);
    }
    for (final marker in const [
      '/lib/', '/src/', '/internal/', '/web/', '/packages/',
      '/app/', '/test/', '/docs/', '/scripts/', '/cmd/', '/pkg/',
    ]) {
      final idx = path.indexOf(marker);
      if (idx > 0) return path.substring(idx + 1);
    }
    return path;
  }

  bool _looksLikeFilePath(String raw) {
    if (raw.isEmpty || raw.contains(' ')) return false;
    return raw.contains('/') || raw.contains('.');
  }

  String _fileName(String raw) {
    if (raw.isEmpty) return '';
    final slashIdx = raw.lastIndexOf('/');
    return slashIdx >= 0 ? raw.substring(slashIdx + 1) : raw;
  }

  String _compactStepDetail(String raw) {
    if (raw.isEmpty) return '';
    if (raw.contains('/') && !raw.contains(' ')) {
      final libIdx = raw.indexOf('/lib/');
      final normalized = libIdx >= 0 ? raw.substring(libIdx + 1) : raw;
      final parts = normalized.split('/').where((s) => s.isNotEmpty).toList(growable: false);
      if (parts.length > 3 && normalized.length > 42) {
        return '${parts.first}/…/${parts[parts.length - 2]}/${parts.last}';
      }
      return normalized;
    }
    return raw;
  }

  @override
  Widget build(BuildContext context) {
    return _cachedChild ??= RepaintBoundary(child: _buildBubble());
  }

  Widget _buildBubble() {
    final message = widget.message;
    if (message.isUser) {
      final hasImage = message.hasImage;
      final hasUserText = message.text.trim().isNotEmpty;
      return Align(
        alignment: Alignment.centerRight,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.end,
          mainAxisSize: MainAxisSize.min,
          children: [
            Container(
              constraints: const BoxConstraints(maxWidth: 310),
              padding: const EdgeInsets.symmetric(horizontal: 15, vertical: 10),
              decoration: BoxDecoration(
                color: AppTheme.primaryBlue,
                borderRadius: const BorderRadius.only(
                  topLeft: Radius.circular(18),
                  topRight: Radius.circular(18),
                  bottomLeft: Radius.circular(18),
                  bottomRight: Radius.circular(4),
                ),
                border: widget.isSearchHighlighted
                    ? Border.all(color: AppTheme.warning, width: 2)
                    : null,
              ),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.end,
                mainAxisSize: MainAxisSize.min,
                children: [
                  if (hasImage)
                    ChatTappableImage(imageDataUrl: message.imageDataUrl!, width: 220),
                  if (hasImage && hasUserText) const SizedBox(height: 8),
                  if (hasUserText)
                    SelectableText(
                      message.text,
                      scrollPhysics: const NeverScrollableScrollPhysics(),
                      contextMenuBuilder: buildConversationContextMenu,
                      style: const TextStyle(color: Colors.white, fontSize: 14.5, height: 1.5),
                    ),
                ],
              ),
            ),
            if (hasUserText)
              Padding(
                padding: const EdgeInsets.only(top: 3, right: 2),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    InkWell(
                      borderRadius: BorderRadius.circular(6),
                      onTap: _copyText,
                      child: Padding(
                        padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 2),
                        child: Icon(
                          _copied ? Icons.check_rounded : Icons.copy_rounded,
                          size: 13,
                          color: _copied ? AppTheme.success : AppTheme.textSecondary,
                        ),
                      ),
                    ),
                    if (widget.onEditUserMessage != null)
                      InkWell(
                        key: ValueKey('user_msg_edit_${message.id}'),
                        borderRadius: BorderRadius.circular(6),
                        onTap: () => widget.onEditUserMessage!(message.text),
                        child: Padding(
                          padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 2),
                          child: Icon(Icons.edit_outlined, size: 13.5, color: AppTheme.textSecondary),
                        ),
                      ),
                  ],
                ),
              ),
          ],
        ),
      );
    }

    final displayedReasoning = _reasoningPacer.displayedText;
    final displayedText = _textPacer.displayedText;
    final isActivelyPacing =
        message.isStreaming || _textPacer.pending || _reasoningPacer.pending;
    final hasReasoning = displayedReasoning.trim().isNotEmpty;
    final hasText = displayedText.trim().isNotEmpty;
    final toolSteps = message.toolSteps;
    final hasToolSteps = toolSteps.isNotEmpty;
    final statusLabel = message.activity.trim().isNotEmpty ? message.activity.trim() : '正在思考...';
    final agentLabel = message.agentLabel.trim();
    final reasoningTicker = hasReasoning
        ? extractLiveReasoningTicker(displayedReasoning, isStreaming: isActivelyPacing)
        : '';
    final isThinkingPhase = isActivelyPacing && !hasText && !hasToolSteps;
    final lastToolIdx = toolSteps.lastIndexWhere((s) => !s.startsWith('note · '));

    return Align(
      alignment: Alignment.centerLeft,
      child: _SearchHighlightFrame(
        highlighted: widget.isSearchHighlighted,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (agentLabel.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(bottom: 5),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    const Icon(Icons.auto_awesome_rounded, size: 13, color: AppTheme.primaryBlue),
                    const SizedBox(width: 5),
                    Flexible(
                      child: Text(
                        agentLabel,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: TextStyle(color: AppTheme.textSecondary, fontSize: 11.5, fontWeight: FontWeight.w600),
                      ),
                    ),
                  ],
                ),
              ),
            if (hasReasoning) ...[
              InkWell(
                borderRadius: BorderRadius.circular(6),
                onTap: _toggleReasoning,
                child: Padding(
                  padding: const EdgeInsets.symmetric(vertical: 2),
                  child: Row(
                    children: [
                      if (isThinkingPhase)
                        const SizedBox.square(
                          dimension: 12,
                          child: CircularProgressIndicator(strokeWidth: 1.8, color: AppTheme.primaryBlue),
                        )
                      else
                        Icon(Icons.psychology_outlined, size: 14, color: AppTheme.textSecondary),
                      const SizedBox(width: 6),
                      Text(
                        isThinkingPhase ? '正在深度思考...' : '思考过程',
                        style: TextStyle(
                          color: isThinkingPhase ? AppTheme.primaryBlue : AppTheme.textSecondary,
                          fontSize: 12,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                      if (reasoningTicker.isNotEmpty && !_effectiveReasoningExpanded) ...[
                        const SizedBox(width: 4),
                        Flexible(
                          child: Text(
                            '· $reasoningTicker',
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: TextStyle(color: AppTheme.textLight, fontSize: 12),
                          ),
                        ),
                      ],
                      const SizedBox(width: 2),
                      Icon(
                        _effectiveReasoningExpanded ? Icons.expand_less_rounded : Icons.expand_more_rounded,
                        size: 15,
                        color: AppTheme.textSecondary,
                      ),
                    ],
                  ),
                ),
              ),
              if (_effectiveReasoningExpanded)
                Container(
                  width: double.infinity,
                  constraints: const BoxConstraints(maxHeight: 220),
                  margin: const EdgeInsets.only(top: 4, left: 2, bottom: 4),
                  padding: const EdgeInsets.only(left: 10, top: 2, bottom: 2),
                  decoration: BoxDecoration(border: Border(left: BorderSide(color: AppTheme.border, width: 2))),
                  child: SingleChildScrollView(
                    reverse: isActivelyPacing,
                    physics: const ClampingScrollPhysics(),
                    child: SelectableText(
                      displayedReasoning.trim(),
                      scrollPhysics: const NeverScrollableScrollPhysics(),
                      contextMenuBuilder: buildConversationContextMenu,
                      style: TextStyle(color: AppTheme.textSecondary, fontSize: 12.5, height: 1.5),
                    ),
                  ),
                ),
            ],
            if (hasToolSteps) ...[
              if (hasReasoning) const SizedBox(height: 4),
              for (var i = 0; i < toolSteps.length; i++)
                if (toolSteps[i].startsWith('note · '))
                  Padding(
                    padding: EdgeInsets.only(top: i == 0 ? 0 : 6, bottom: 2),
                    child: AiChatMarkdown(
                      text: toolSteps[i].substring(7).trim(),
                      convoyPlans: widget.convoyPlans,
                      subagents: widget.subagents,
                      onOpenSession: widget.onOpenSession,
                      onStartConvoyPlan: widget.onStartConvoyPlan,
                      onCancelConvoyPlan: widget.onCancelConvoyPlan,
                      onRetryConvoyPlan: widget.onRetryConvoyPlan,
                      onMergeConvoyPlan: widget.onMergeConvoyPlan,
                      onSummarizeConvoyPlan: widget.onSummarizeConvoyPlan,
                    ),
                  )
                else
                  Builder(
                    builder: (context) {
                      final step = toolSteps[i];
                      final parsed = _describeToolStep(step, widget.changedFileStats);
                      final isStepRunning = isActivelyPacing && !hasText && i == lastToolIdx;
                      final filePath = parsed.path;
                      final subSid = parsed.subagentSessionId;
                      final canOpenFile = filePath.isNotEmpty;
                      final canOpenSubagent = subSid != null && subSid > 0 && widget.onOpenSession != null;
                      final canOpen = canOpenFile || canOpenSubagent;
                      final onTap = canOpenSubagent
                          ? () => widget.onOpenSession!(subSid)
                          : (canOpenFile
                                ? (parsed.isEdit && widget.onOpenFileDiff != null
                                      ? () => widget.onOpenFileDiff!(filePath)
                                      : (widget.onOpenFile != null
                                            ? () => widget.onOpenFile!(filePath)
                                            : null))
                                : null);
                      final row = Padding(
                        padding: const EdgeInsets.symmetric(vertical: 2),
                        child: Row(
                          children: [
                            if (isStepRunning)
                              const SizedBox.square(
                                dimension: 12,
                                child: CircularProgressIndicator(strokeWidth: 1.8, color: AppTheme.primaryBlue),
                              )
                            else
                              Icon(
                                parsed.icon,
                                size: 13.5,
                                color: canOpen ? AppTheme.primaryBlueText : AppTheme.textSecondary,
                              ),
                            const SizedBox(width: 6),
                            Text(
                              parsed.label,
                              style: TextStyle(
                                color: isStepRunning ? AppTheme.primaryBlue : AppTheme.textSecondary,
                                fontSize: 12,
                                fontWeight: FontWeight.w500,
                              ),
                            ),
                            if (parsed.detail.isNotEmpty) ...[
                              const SizedBox(width: 6),
                              Flexible(
                                child: Text(
                                  parsed.detail,
                                  maxLines: 1,
                                  overflow: TextOverflow.ellipsis,
                                  style: TextStyle(
                                    color: canOpen ? AppTheme.textPrimary : AppTheme.textLight,
                                    fontSize: 12,
                                    fontFamily: 'monospace',
                                    decoration: canOpen ? TextDecoration.underline : null,
                                    decorationColor: AppTheme.textLight,
                                  ),
                                ),
                              ),
                              if (parsed.readMeta.isNotEmpty) ...[
                                const SizedBox(width: 6),
                                Text(
                                  parsed.readMeta,
                                  style: TextStyle(color: AppTheme.textSecondary, fontSize: 11, fontFamily: 'monospace'),
                                ),
                              ],
                            ],
                            if (parsed.additions > 0 || parsed.deletions > 0) ...[
                              const SizedBox(width: 6),
                              AiDiffStatBadges(additions: parsed.additions, deletions: parsed.deletions, fontSize: 11),
                            ],
                            if (canOpenSubagent) ...[
                              const SizedBox(width: 4),
                              Icon(Icons.open_in_new_rounded, size: 12, color: AppTheme.primaryBlueText),
                            ],
                          ],
                        ),
                      );
                      if (onTap == null) return row;
                      return Tooltip(
                        message: canOpenSubagent ? '点击进入子会话' : filePath,
                        child: InkWell(borderRadius: BorderRadius.circular(6), onTap: onTap, child: row),
                      );
                    },
                  ),
            ],
            if (hasText) ...[
              if (hasReasoning || hasToolSteps) const SizedBox(height: 8),
              AiChatMarkdown(
                text: displayedText,
                convoyPlans: widget.convoyPlans,
                subagents: widget.subagents,
                onOpenSession: widget.onOpenSession,
                onStartConvoyPlan: widget.onStartConvoyPlan,
                onCancelConvoyPlan: widget.onCancelConvoyPlan,
                onRetryConvoyPlan: widget.onRetryConvoyPlan,
                onMergeConvoyPlan: widget.onMergeConvoyPlan,
                onSummarizeConvoyPlan: widget.onSummarizeConvoyPlan,
              ),
            ] else if (isActivelyPacing && !hasReasoning && !hasToolSteps) ...[
              Padding(
                padding: const EdgeInsets.symmetric(vertical: 4),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    const SizedBox.square(
                      dimension: 12,
                      child: CircularProgressIndicator(strokeWidth: 1.8, color: AppTheme.primaryBlue),
                    ),
                    const SizedBox(width: 8),
                    Flexible(
                      child: Text(statusLabel, style: TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
                    ),
                  ],
                ),
              ),
            ],
            if (!isActivelyPacing && widget.changedFiles.isNotEmpty) ...[
              const SizedBox(height: 10),
              AiChatChangedFilesCard(
                changedFiles: widget.changedFiles,
                changedFileStats: widget.changedFileStats,
                additions: widget.diffAdditions,
                deletions: widget.diffDeletions,
                onOpenGitReview: widget.onOpenGitReview,
                onOpenFileDiff: widget.onOpenFileDiff,
              ),
            ],
            if (!isActivelyPacing &&
                (hasText || hasReasoning || hasToolSteps || widget.onRetry != null))
              AssistantTurnFooter(
                copied: _copied,
                onCopy: _copyText,
                onRetry: widget.onRetry,
                durationMs: message.durationMs > 0 ? message.durationMs : _measuredDurationMs,
                totalTokens: message.totalTokens,
                timestamp: message.timestamp,
              ),
          ],
        ),
      ),
    );
  }
}

class _SearchHighlightFrame extends StatelessWidget {
  final bool highlighted;
  final Widget child;

  const _SearchHighlightFrame({
    required this.highlighted,
    required this.child,
  });

  @override
  Widget build(BuildContext context) {
    if (!highlighted) {
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 2),
        child: child,
      );
    }
    return Container(
      padding: const EdgeInsets.symmetric(vertical: 2, horizontal: 6),
      decoration: BoxDecoration(
        color: AppTheme.warningSoft.withValues(alpha: 0.35),
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: AppTheme.warning),
      ),
      child: child,
    );
  }
}
