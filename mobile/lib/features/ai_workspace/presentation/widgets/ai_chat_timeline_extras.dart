import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../core/models/node_skill.dart';
import '../../../../core/storage/storage_service.dart';
import '../../../../core/theme/app_theme.dart';
import '../../../../shared/controllers/node_skill_controller.dart';
import '../../models/code_convoy_plan.dart';

/// 运行中待发送指令队列列表（对齐 PC 端 QueuedPromptsList.tsx）：
/// 支持「立即发送」、「编辑」与「删除」。
class AiQueuedPromptsList extends StatelessWidget {
  const AiQueuedPromptsList({
    super.key,
    required this.items,
    required this.onSendNow,
    required this.onEdit,
    required this.onDelete,
  });

  final List<CodeQueuedPromptItem> items;
  final ValueChanged<String> onSendNow;
  final ValueChanged<String> onEdit;
  final ValueChanged<String> onDelete;

  @override
  Widget build(BuildContext context) {
    if (items.isEmpty) return const SizedBox.shrink();
    return Padding(
      key: const ValueKey('ai_queued_prompts_list'),
      padding: const EdgeInsets.fromLTRB(16, 2, 16, 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [
          for (final item in items)
            Padding(
              key: ValueKey('queued_prompt_item_${item.id}'),
              padding: const EdgeInsets.only(bottom: 8),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.end,
                children: [
                  Container(
                    constraints: const BoxConstraints(maxWidth: 310),
                    padding: const EdgeInsets.symmetric(
                      horizontal: 13,
                      vertical: 9,
                    ),
                    decoration: BoxDecoration(
                      color: AppTheme.surfaceMuted.withValues(alpha: 0.85),
                      borderRadius: BorderRadius.circular(14),
                      border: Border.all(
                        color: AppTheme.primaryBlueBorder,
                      ),
                    ),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        if (item.skillSelection != null)
                          Padding(
                            padding: const EdgeInsets.only(bottom: 4),
                            child: Row(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                const Icon(
                                  Icons.auto_awesome_rounded,
                                  size: 12,
                                  color: AppTheme.primaryBlue,
                                ),
                                const SizedBox(width: 4),
                                Text(
                                  '技能: ${item.skillSelection!.name}',
                                  style: TextStyle(
                                    color: AppTheme.primaryBlueText,
                                    fontSize: 11,
                                    fontWeight: FontWeight.w600,
                                  ),
                                ),
                              ],
                            ),
                          ),
                        if (item.attachedFiles.isNotEmpty)
                          Padding(
                            padding: const EdgeInsets.only(bottom: 4),
                            child: Wrap(
                              spacing: 4,
                              runSpacing: 4,
                              children: [
                                for (final file in item.attachedFiles)
                                  Text(
                                    '@$file',
                                    style: TextStyle(
                                      color: AppTheme.primaryBlueText,
                                      fontSize: 11,
                                      fontFamily: 'monospace',
                                    ),
                                  ),
                              ],
                            ),
                          ),
                        if (item.text.isNotEmpty)
                          Text(
                            item.text,
                            style: TextStyle(
                              color: AppTheme.textPrimary,
                              fontSize: 13.5,
                              height: 1.45,
                            ),
                          ),
                      ],
                    ),
                  ),
                  const SizedBox(height: 4),
                  Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Icon(
                        Icons.schedule_rounded,
                        size: 12.5,
                        color: AppTheme.textSecondary,
                      ),
                      const SizedBox(width: 4),
                      Text(
                        '排队待发',
                        style: TextStyle(
                          color: AppTheme.textSecondary,
                          fontSize: 11,
                        ),
                      ),
                      const SizedBox(width: 8),
                      _QueueActionChip(
                        key: ValueKey('queued_prompt_send_now_${item.id}'),
                        icon: Icons.arrow_upward_rounded,
                        label: '立即发送',
                        color: AppTheme.primaryBlueText,
                        onTap: () => onSendNow(item.id),
                      ),
                      const SizedBox(width: 6),
                      _QueueActionChip(
                        key: ValueKey('queued_prompt_edit_${item.id}'),
                        icon: Icons.edit_outlined,
                        label: '编辑',
                        color: AppTheme.textSecondary,
                        onTap: () => onEdit(item.id),
                      ),
                      const SizedBox(width: 6),
                      _QueueActionChip(
                        key: ValueKey('queued_prompt_delete_${item.id}'),
                        icon: Icons.delete_outline_rounded,
                        label: '移除',
                        color: AppTheme.error,
                        onTap: () => onDelete(item.id),
                      ),
                    ],
                  ),
                ],
              ),
            ),
        ],
      ),
    );
  }
}

class _QueueActionChip extends StatelessWidget {
  const _QueueActionChip({
    super.key,
    required this.icon,
    required this.label,
    required this.color,
    required this.onTap,
  });

  final IconData icon;
  final String label;
  final Color color;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(6),
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 2),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 12.5, color: color),
            const SizedBox(width: 2),
            Text(
              label,
              style: TextStyle(
                color: color,
                fontSize: 11,
                fontWeight: FontWeight.w600,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// 会话内关键词搜索栏（对齐 PC 端 TranscriptSearchBar.tsx）：
/// 支持实时检索当前会话消息并按上/下一条跳转定位。
class AiTranscriptSearchBar extends StatelessWidget {
  const AiTranscriptSearchBar({
    super.key,
    required this.controller,
    required this.matchCount,
    required this.activeMatchIndex,
    required this.onChanged,
    required this.onPrev,
    required this.onNext,
    required this.onClose,
  });

  final TextEditingController controller;
  final int matchCount;
  final int activeMatchIndex;
  final ValueChanged<String> onChanged;
  final VoidCallback onPrev;
  final VoidCallback onNext;
  final VoidCallback onClose;

  @override
  Widget build(BuildContext context) {
    final hasQuery = controller.text.trim().isNotEmpty;
    final countText = !hasQuery
        ? ''
        : (matchCount == 0 ? '0/0' : '${activeMatchIndex + 1}/$matchCount');

    return Container(
      key: const ValueKey('ai_transcript_search_bar'),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
      decoration: BoxDecoration(
        color: AppTheme.surfaceMuted,
        border: Border(bottom: BorderSide(color: AppTheme.border)),
      ),
      child: Row(
        children: [
          Icon(Icons.search_rounded, size: 17, color: AppTheme.textSecondary),
          const SizedBox(width: 8),
          Expanded(
            child: TextField(
              key: const ValueKey('ai_transcript_search_input'),
              controller: controller,
              autofocus: true,
              onChanged: onChanged,
              style: TextStyle(color: AppTheme.textPrimary, fontSize: 13.5),
              decoration: InputDecoration(
                isDense: true,
                filled: false,
                hintText: '搜索当前会话内容…',
                hintStyle: TextStyle(
                  color: AppTheme.textSecondary,
                  fontSize: 13,
                ),
                contentPadding: const EdgeInsets.symmetric(vertical: 4),
                border: InputBorder.none,
                enabledBorder: InputBorder.none,
                focusedBorder: InputBorder.none,
              ),
            ),
          ),
          if (countText.isNotEmpty) ...[
            Text(
              countText,
              style: TextStyle(
                color: matchCount == 0
                    ? AppTheme.error
                    : AppTheme.textSecondary,
                fontSize: 11.5,
                fontFamily: 'monospace',
              ),
            ),
            const SizedBox(width: 4),
          ],
          IconButton(
            key: const ValueKey('ai_transcript_search_prev'),
            visualDensity: VisualDensity.compact,
            constraints: const BoxConstraints.tightFor(width: 28, height: 28),
            padding: EdgeInsets.zero,
            tooltip: '上一处匹配',
            onPressed: matchCount > 0 ? onPrev : null,
            icon: const Icon(Icons.keyboard_arrow_up_rounded, size: 18),
          ),
          IconButton(
            key: const ValueKey('ai_transcript_search_next'),
            visualDensity: VisualDensity.compact,
            constraints: const BoxConstraints.tightFor(width: 28, height: 28),
            padding: EdgeInsets.zero,
            tooltip: '下一处匹配',
            onPressed: matchCount > 0 ? onNext : null,
            icon: const Icon(Icons.keyboard_arrow_down_rounded, size: 18),
          ),
          IconButton(
            key: const ValueKey('ai_transcript_search_close'),
            visualDensity: VisualDensity.compact,
            constraints: const BoxConstraints.tightFor(width: 28, height: 28),
            padding: EdgeInsets.zero,
            tooltip: '关闭搜索',
            onPressed: onClose,
            icon: const Icon(Icons.close_rounded, size: 17),
          ),
        ],
      ),
    );
  }
}

/// 远离底部时的悬浮「回到底部」胶囊按钮（对齐 PC 端 MessageStream.tsx scrollToBottom 按钮）。
class AiScrollToBottomFab extends StatelessWidget {
  const AiScrollToBottomFab({
    super.key,
    required this.isStreaming,
    required this.onTap,
  });

  final bool isStreaming;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Material(
      key: const ValueKey('ai_scroll_to_bottom_fab'),
      color: AppTheme.surface,
      elevation: 3,
      shadowColor: Colors.black26,
      shape: StadiumBorder(side: BorderSide(color: AppTheme.border)),
      child: InkWell(
        customBorder: const StadiumBorder(),
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 7),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              if (isStreaming) ...[
                const SizedBox.square(
                  dimension: 11,
                  child: CircularProgressIndicator(
                    strokeWidth: 1.8,
                    color: AppTheme.primaryBlue,
                  ),
                ),
                const SizedBox(width: 6),
                Text(
                  '正在输出 · 回到底部',
                  style: TextStyle(
                    color: AppTheme.primaryBlueText,
                    fontSize: 11.5,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ] else ...[
                Icon(
                  Icons.arrow_downward_rounded,
                  size: 14,
                  color: AppTheme.textPrimary,
                ),
                const SizedBox(width: 4),
                Text(
                  '回到底部',
                  style: TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 11.5,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }
}

/// 工作区节点 Skill 选择器弹窗（对齐 PC 端 Composer Skill 选择挂载）。
class CodeSkillPickerSheet extends ConsumerStatefulWidget {
  const CodeSkillPickerSheet({super.key, required this.directory});

  final String directory;

  static Future<NodeSkillSelection?> show(
    BuildContext context, {
    required String directory,
  }) {
    return showModalBottomSheet<NodeSkillSelection>(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppTheme.surface,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(18)),
      ),
      builder: (_) => CodeSkillPickerSheet(directory: directory),
    );
  }

  @override
  ConsumerState<CodeSkillPickerSheet> createState() =>
      _CodeSkillPickerSheetState();
}

class _CodeSkillPickerSheetState extends ConsumerState<CodeSkillPickerSheet> {
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted || widget.directory.trim().isEmpty) return;
      ref.read(nodeSkillControllerProvider.notifier).load(
        directory: widget.directory,
        isHoxi: StorageService.isActiveHoxi,
      );
    });
  }

  @override
  Widget build(BuildContext context) {
    final skillState = ref.watch(nodeSkillControllerProvider);
    final skills = skillState.catalog.skills;

    return SafeArea(
      child: ConstrainedBox(
        constraints: BoxConstraints(
          maxHeight: MediaQuery.sizeOf(context).height * 0.65,
        ),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const SizedBox(height: 10),
            Center(
              child: Container(
                width: 36,
                height: 4,
                decoration: BoxDecoration(
                  color: AppTheme.border,
                  borderRadius: BorderRadius.circular(99),
                ),
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 12, 16, 10),
              child: Row(
                children: [
                  const Icon(
                    Icons.auto_awesome_rounded,
                    size: 18,
                    color: AppTheme.primaryBlue,
                  ),
                  const SizedBox(width: 8),
                  Text(
                    '挂载工作区技能 (Skill)',
                    style: TextStyle(
                      color: AppTheme.textPrimary,
                      fontSize: 15,
                      fontWeight: FontWeight.w700,
                    ),
                  ),
                ],
              ),
            ),
            Divider(height: 1, color: AppTheme.border),
            if (skillState.isLoading)
              const Padding(
                padding: EdgeInsets.all(24),
                child: Center(child: CircularProgressIndicator()),
              )
            else if (skills.isEmpty)
              Padding(
                padding: const EdgeInsets.all(20),
                child: Text(
                  skillState.catalog.notice.isNotEmpty
                      ? skillState.catalog.notice
                      : '当前工作区暂无可用 Skill',
                  style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
                ),
              )
            else
              Flexible(
                child: ListView.separated(
                  shrinkWrap: true,
                  itemCount: skills.length,
                  separatorBuilder: (_, _) =>
                      Divider(height: 1, color: AppTheme.border),
                  itemBuilder: (context, index) {
                    final skill = skills[index];
                    return ListTile(
                      dense: true,
                      leading: const Icon(
                        Icons.auto_awesome_outlined,
                        size: 18,
                        color: AppTheme.primaryBlue,
                      ),
                      title: Text(
                        skill.name,
                        style: TextStyle(
                          color: AppTheme.textPrimary,
                          fontSize: 13.5,
                          fontWeight: FontWeight.w600,
                          fontFamily: 'monospace',
                        ),
                      ),
                      subtitle: skill.description.isNotEmpty
                          ? Text(
                              skill.description,
                              maxLines: 2,
                              overflow: TextOverflow.ellipsis,
                              style: TextStyle(
                                color: AppTheme.textSecondary,
                                fontSize: 12,
                              ),
                            )
                          : null,
                      onTap: () => Navigator.of(context).pop(
                        NodeSkillSelection(key: skill.key, name: skill.name),
                      ),
                    );
                  },
                ),
              ),
          ],
        ),
      ),
    );
  }
}
