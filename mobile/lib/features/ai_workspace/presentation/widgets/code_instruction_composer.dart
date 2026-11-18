import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../core/l10n/l10n.dart';
import '../../../../core/models/node_skill.dart';
import '../../../../core/models/voice_dictation_port.dart';
import '../../../../core/theme/app_theme.dart';
import '../../../../shared/widgets/chat_image_preview.dart';
import '../../../../shared/widgets/chat_keyboard_dismiss.dart';
import '../../../../shared/widgets/stream_text_pacer.dart';
import '../../models/code_instruction_options.dart';
import '../../models/code_workspace_file.dart';
import '../controllers/ai_workspace_controller.dart';

class CodeInstructionComposer extends ConsumerWidget {
  const CodeInstructionComposer({
    super.key,
    required this.controller,
    required this.focusNode,
    required this.enabled,
    required this.closed,
    required this.onSend,
    this.options = const CodeInstructionOptions(),
    this.onOptionsChanged,
    this.modelLabel = '',
    this.reasoningEffort = '',
    this.onSelectModel,
    this.pendingImageDataUrl,
    this.onClearPendingImage,
    this.attachedFiles = const [],
    this.onRemoveAttachedFile,
    this.selectedSkill,
    this.onClearSelectedSkill,
    this.onPickSkill,
    this.onTakePhoto,
    this.onPickFromGallery,
    this.onPickWorkspaceFile,
    this.isRunning = false,
    this.isStopping = false,
    this.onStop,
  });

  final TextEditingController controller;
  final FocusNode focusNode;
  final bool enabled;
  final VoidCallback onSend;
  final bool closed;
  final CodeInstructionOptions options;
  final ValueChanged<CodeInstructionOptions>? onOptionsChanged;
  final String modelLabel;
  final String reasoningEffort;
  final VoidCallback? onSelectModel;
  final String? pendingImageDataUrl;
  final VoidCallback? onClearPendingImage;
  final List<String> attachedFiles;
  final ValueChanged<String>? onRemoveAttachedFile;
  final NodeSkillSelection? selectedSkill;
  final VoidCallback? onClearSelectedSkill;
  final VoidCallback? onPickSkill;
  final VoidCallback? onTakePhoto;
  final VoidCallback? onPickFromGallery;
  final VoidCallback? onPickWorkspaceFile;
  final bool isRunning;
  final bool isStopping;
  final VoidCallback? onStop;

  Future<void> _toggleVoiceDictation(
    VoiceDictationPort dictationPort,
    VoiceDictationSnapshot dictation,
  ) async {
    if (!enabled) return;
    if (dictation.isRecording) {
      final transcript = await dictationPort.stopRecording();
      final text = transcript.trim();
      if (text.isEmpty) return;
      final current = controller.text.trim();
      controller.text = current.isEmpty ? text : '$current $text';
      controller.selection = TextSelection.collapsed(
        offset: controller.text.length,
      );
      focusNode.requestFocus();
    } else {
      dismissChatKeyboard();
      await dictationPort.startRecording();
    }
  }

  void _showAttachmentMenu(BuildContext context) {
    if (!enabled) return;
    dismissChatKeyboard(context);
    showModalBottomSheet<void>(
      context: context,
      backgroundColor: AppTheme.surface,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(18)),
      ),
      builder: (sheetContext) {
        return SafeArea(
          child: Padding(
            padding: const EdgeInsets.fromLTRB(12, 8, 12, 12),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Container(
                  width: 36,
                  height: 4,
                  margin: const EdgeInsets.only(bottom: 8),
                  decoration: BoxDecoration(
                    color: AppTheme.border,
                    borderRadius: BorderRadius.circular(99),
                  ),
                ),
                if (onPickWorkspaceFile != null)
                  ListTile(
                    key: const ValueKey('code_composer_attach_file'),
                    leading: const Icon(
                      Icons.description_outlined,
                      color: AppTheme.primaryBlue,
                    ),
                    title: const Text('引用工作区文件 (@文件)'),
                    subtitle: Text(
                      '从当前项目选择代码文件作为上下文',
                      style: TextStyle(
                        color: AppTheme.textSecondary,
                        fontSize: 12,
                      ),
                    ),
                    onTap: () {
                      Navigator.of(sheetContext).pop();
                      onPickWorkspaceFile?.call();
                    },
                  ),
                if (onPickSkill != null)
                  ListTile(
                    key: const ValueKey('code_composer_attach_skill'),
                    leading: const Icon(
                      Icons.auto_awesome_outlined,
                      color: AppTheme.primaryBlue,
                    ),
                    title: const Text('挂载工作区技能 (Skill)'),
                    subtitle: Text(
                      '在当前会话指令中应用指定 Skill 规范',
                      style: TextStyle(
                        color: AppTheme.textSecondary,
                        fontSize: 12,
                      ),
                    ),
                    onTap: () {
                      Navigator.of(sheetContext).pop();
                      onPickSkill?.call();
                    },
                  ),
                if (onPickFromGallery != null)
                  ListTile(
                    key: const ValueKey('code_composer_attach_gallery'),
                    leading: const Icon(
                      Icons.photo_library_outlined,
                      color: AppTheme.primaryBlue,
                    ),
                    title: Text(context.l10n.chatActionPickGallery),
                    onTap: () {
                      Navigator.of(sheetContext).pop();
                      onPickFromGallery?.call();
                    },
                  ),
                if (onTakePhoto != null)
                  ListTile(
                    key: const ValueKey('code_composer_attach_camera'),
                    leading: const Icon(
                      Icons.photo_camera_outlined,
                      color: AppTheme.primaryBlue,
                    ),
                    title: Text(context.l10n.chatTooltipCamera),
                    onTap: () {
                      Navigator.of(sheetContext).pop();
                      onTakePhoto?.call();
                    },
                  ),
              ],
            ),
          ),
        );
      },
    );
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final dictationPort = ref.watch(voiceDictationPortProvider);
    final dictation = ref.watch(voiceDictationSnapshotProvider);
    final isRecording = dictation.isRecording;

    final hasPendingImage =
        pendingImageDataUrl != null && pendingImageDataUrl!.trim().isNotEmpty;
    final hasAttachments =
        hasPendingImage || attachedFiles.isNotEmpty || selectedSkill != null;
    final displayModel = modelLabel.trim().isEmpty ? '默认模型' : modelLabel.trim();
    final effortText = reasoningEffort.trim();

    return Padding(
      padding: EdgeInsets.fromLTRB(
        12,
        2,
        12,
        MediaQuery.paddingOf(context).bottom + 6,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          const ChatHideKeyboardButton(
            buttonKey: ValueKey('code_composer_hide_keyboard_button'),
            compact: true,
          ),
          TextFieldTapRegion(
            child: Container(
              padding: const EdgeInsets.fromLTRB(12, 6, 8, 6),
              decoration: BoxDecoration(
                color: AppTheme.surfaceMuted,
                borderRadius: BorderRadius.circular(20),
                border: isRecording
                    ? Border.all(color: AppTheme.primaryBlue)
                    : null,
              ),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  if (hasAttachments)
                    Padding(
                      padding: const EdgeInsets.only(top: 2, bottom: 6),
                      child: Wrap(
                        spacing: 8,
                        runSpacing: 6,
                        crossAxisAlignment: WrapCrossAlignment.center,
                        children: [
                          if (selectedSkill != null)
                            Container(
                              key: const ValueKey(
                                'code_composer_selected_skill_chip',
                              ),
                              padding: const EdgeInsets.symmetric(
                                horizontal: 8,
                                vertical: 4,
                              ),
                              decoration: BoxDecoration(
                                color: AppTheme.primaryBlueLight,
                                borderRadius: BorderRadius.circular(6),
                              ),
                              child: Row(
                                mainAxisSize: MainAxisSize.min,
                                children: [
                                  const Icon(
                                    Icons.auto_awesome_rounded,
                                    size: 13,
                                    color: AppTheme.primaryBlue,
                                  ),
                                  const SizedBox(width: 4),
                                  ConstrainedBox(
                                    constraints: const BoxConstraints(
                                      maxWidth: 160,
                                    ),
                                    child: Text(
                                      '技能: ${selectedSkill!.name}',
                                      maxLines: 1,
                                      overflow: TextOverflow.ellipsis,
                                      style: const TextStyle(
                                        color: AppTheme.primaryBlue,
                                        fontSize: 11.5,
                                        fontWeight: FontWeight.w600,
                                        fontFamily: 'monospace',
                                      ),
                                    ),
                                  ),
                                  if (onClearSelectedSkill != null) ...[
                                    const SizedBox(width: 4),
                                    InkWell(
                                      key: const ValueKey(
                                        'code_composer_clear_skill',
                                      ),
                                      onTap: onClearSelectedSkill,
                                      child: const Icon(
                                        Icons.close_rounded,
                                        size: 13,
                                        color: AppTheme.primaryBlue,
                                      ),
                                    ),
                                  ],
                                ],
                              ),
                            ),
                          if (hasPendingImage)
                            Stack(
                              clipBehavior: Clip.none,
                              children: [
                                Container(
                                  width: 44,
                                  height: 44,
                                  decoration: BoxDecoration(
                                    borderRadius: BorderRadius.circular(8),
                                    border: Border.all(
                                      color: AppTheme.primaryBlueBorder,
                                    ),
                                  ),
                                  clipBehavior: Clip.antiAlias,
                                  child: ChatTappableImage(
                                    imageDataUrl: pendingImageDataUrl!,
                                    width: 44,
                                    height: 44,
                                    borderRadius: BorderRadius.zero,
                                    errorBuilder: (_, _, _) => const Icon(
                                      Icons.image_outlined,
                                      size: 20,
                                      color: AppTheme.primaryBlue,
                                    ),
                                  ),
                                ),
                                if (onClearPendingImage != null)
                                  Positioned(
                                    top: -5,
                                    right: -5,
                                    child: InkWell(
                                      key: const ValueKey(
                                        'code_composer_clear_image',
                                      ),
                                      onTap: onClearPendingImage,
                                      borderRadius: BorderRadius.circular(99),
                                      child: Container(
                                        padding: const EdgeInsets.all(2),
                                        decoration: BoxDecoration(
                                          color: AppTheme.textPrimary,
                                          shape: BoxShape.circle,
                                        ),
                                        child: Icon(
                                          Icons.close_rounded,
                                          size: 11,
                                          color: AppTheme.surface,
                                        ),
                                      ),
                                    ),
                                  ),
                              ],
                            ),
                          for (final file in attachedFiles)
                            Container(
                              padding: const EdgeInsets.symmetric(
                                horizontal: 8,
                                vertical: 4,
                              ),
                              decoration: BoxDecoration(
                                color: AppTheme.surface,
                                borderRadius: BorderRadius.circular(6),
                              ),
                              child: Row(
                                mainAxisSize: MainAxisSize.min,
                                children: [
                                  Icon(
                                    Icons.description_outlined,
                                    size: 13,
                                    color: AppTheme.primaryBlueText,
                                  ),
                                  const SizedBox(width: 4),
                                  ConstrainedBox(
                                    constraints: const BoxConstraints(
                                      maxWidth: 160,
                                    ),
                                    child: Text(
                                      '@$file',
                                      maxLines: 1,
                                      overflow: TextOverflow.ellipsis,
                                      style: TextStyle(
                                        color: AppTheme.textPrimary,
                                        fontSize: 11.5,
                                        fontFamily: 'monospace',
                                      ),
                                    ),
                                  ),
                                  if (onRemoveAttachedFile != null) ...[
                                    const SizedBox(width: 4),
                                    InkWell(
                                      onTap: () => onRemoveAttachedFile!(file),
                                      child: Icon(
                                        Icons.close_rounded,
                                        size: 13,
                                        color: AppTheme.textSecondary,
                                      ),
                                    ),
                                  ],
                                ],
                              ),
                            ),
                        ],
                      ),
                    ),
                  TextField(
                    controller: controller,
                    focusNode: focusNode,
                    enabled: enabled,
                    maxLines: 5,
                    minLines: 1,
                    textInputAction: TextInputAction.send,
                    onTapOutside: (_) => dismissChatKeyboard(context),
                    onSubmitted: (_) => onSend(),
                    contextMenuBuilder: buildConversationContextMenu,
                    style: TextStyle(color: AppTheme.textPrimary, fontSize: 15),
                    decoration: InputDecoration(
                      isDense: true,
                      filled: false,
                      contentPadding: const EdgeInsets.symmetric(
                        horizontal: 2,
                        vertical: 6,
                      ),
                      hintText: isRecording
                          ? (dictation.liveTranscript.isEmpty
                                ? context.l10n.chatListeningShort
                                : dictation.liveTranscript)
                          : closed
                          ? context.l10n.codeWsInstructionClosedHint
                          : !enabled
                          ? context.l10n.codeWsInstructionSelectSessionHint
                          : isRunning
                          ? '输入后续指令（当前执行完后自动发送）…'
                          : context.l10n.codeWsInstructionInputHint,
                      hintStyle: TextStyle(
                        color: isRecording
                            ? AppTheme.primaryBlue
                            : AppTheme.textLight,
                        fontSize: 14.5,
                      ),
                      border: InputBorder.none,
                      enabledBorder: InputBorder.none,
                      focusedBorder: InputBorder.none,
                    ),
                  ),
                  const SizedBox(height: 2),
                  Row(
                    children: [
                      InkWell(
                        key: const ValueKey('code_composer_attachment_button'),
                        borderRadius: BorderRadius.circular(99),
                        onTap: enabled
                            ? () => _showAttachmentMenu(context)
                            : null,
                        child: Padding(
                          padding: const EdgeInsets.symmetric(
                            horizontal: 6,
                            vertical: 3,
                          ),
                          child: Row(
                            mainAxisSize: MainAxisSize.min,
                            children: [
                              Icon(
                                Icons.add_rounded,
                                size: 15,
                                color: !enabled
                                    ? AppTheme.textLight
                                    : hasAttachments
                                    ? AppTheme.primaryBlueText
                                    : AppTheme.textSecondary,
                              ),
                              const SizedBox(width: 2),
                              Text(
                                '附件',
                                style: TextStyle(
                                  color: !enabled
                                      ? AppTheme.textLight
                                      : hasAttachments
                                      ? AppTheme.primaryBlueText
                                      : AppTheme.textSecondary,
                                  fontSize: 11.5,
                                  fontWeight: FontWeight.w600,
                                ),
                              ),
                            ],
                          ),
                        ),
                      ),
                      const SizedBox(width: 6),
                      Expanded(
                        child: Align(
                          alignment: Alignment.centerLeft,
                          child: InkWell(
                            key: const ValueKey('overview_model_chip'),
                            borderRadius: BorderRadius.circular(99),
                            onTap: enabled ? onSelectModel : null,
                            child: Padding(
                              padding: const EdgeInsets.symmetric(
                                horizontal: 6,
                                vertical: 3,
                              ),
                              child: Row(
                                mainAxisSize: MainAxisSize.min,
                                children: [
                                  Icon(
                                    Icons.memory_rounded,
                                    size: 13.5,
                                    color: enabled
                                        ? AppTheme.primaryBlueText
                                        : AppTheme.textLight,
                                  ),
                                  const SizedBox(width: 4),
                                  Flexible(
                                    child: Text(
                                      effortText.isEmpty
                                          ? displayModel
                                          : '$displayModel · $effortText',
                                      maxLines: 1,
                                      overflow: TextOverflow.ellipsis,
                                      style: TextStyle(
                                        color: enabled
                                            ? AppTheme.textPrimary
                                            : AppTheme.textLight,
                                        fontSize: 11.5,
                                        fontWeight: FontWeight.w600,
                                      ),
                                    ),
                                  ),
                                  const SizedBox(width: 2),
                                  Icon(
                                    Icons.keyboard_arrow_down_rounded,
                                    size: 15,
                                    color: enabled
                                        ? AppTheme.textSecondary
                                        : AppTheme.textLight,
                                  ),
                                ],
                              ),
                            ),
                          ),
                        ),
                      ),
                      if (dictationPort != null && dictation.available) ...[
                        const SizedBox(width: 4),
                        IconButton(
                          key: const ValueKey(
                            'code_composer_voice_dictation_button',
                          ),
                          visualDensity: VisualDensity.compact,
                          constraints: const BoxConstraints.tightFor(
                            width: 32,
                            height: 32,
                          ),
                          padding: EdgeInsets.zero,
                          tooltip: context.l10n.chatTooltipHoldToTalk,
                          onPressed: enabled
                              ? () => _toggleVoiceDictation(
                                  dictationPort,
                                  dictation,
                                )
                              : null,
                          icon: Icon(
                            isRecording
                                ? Icons.mic_rounded
                                : Icons.mic_none_rounded,
                            size: 19,
                          ),
                          color: isRecording
                              ? AppTheme.primaryBlue
                              : AppTheme.textSecondary,
                        ),
                      ],
                      if (isRunning && onStop != null)
                        IconButton(
                          visualDensity: VisualDensity.compact,
                          constraints: const BoxConstraints.tightFor(
                            width: 32,
                            height: 32,
                          ),
                          padding: EdgeInsets.zero,
                          tooltip: context.l10n.codeWsActionStop,
                          onPressed: isStopping ? null : onStop,
                          icon: isStopping
                              ? const SizedBox.square(
                                  dimension: 16,
                                  child: CircularProgressIndicator(
                                    strokeWidth: 2,
                                    color: AppTheme.error,
                                  ),
                                )
                              : const Icon(Icons.stop_circle_rounded, size: 22),
                          color: AppTheme.error,
                        ),
                      IconButton(
                        key: const ValueKey('code_composer_send_button'),
                        visualDensity: VisualDensity.compact,
                        constraints: const BoxConstraints.tightFor(
                          width: 32,
                          height: 32,
                        ),
                        padding: EdgeInsets.zero,
                        tooltip: isRunning
                            ? '加入待发送队列'
                            : context.l10n.codeWsInstructionSend,
                        onPressed: enabled ? onSend : null,
                        icon: Icon(
                          isRunning
                              ? Icons.schedule_send_rounded
                              : Icons.send_rounded,
                          size: 19,
                        ),
                        color: AppTheme.primaryBlue,
                      ),
                    ],
                  ),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class CodeWorkspaceFilePickerSheet extends ConsumerStatefulWidget {
  const CodeWorkspaceFilePickerSheet({super.key, required this.sessionId});

  final int sessionId;

  static Future<String?> show(BuildContext context, {required int sessionId}) {
    return showModalBottomSheet<String>(
      context: context,
      isScrollControlled: true,
      backgroundColor: AppTheme.surface,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(18)),
      ),
      builder: (_) => CodeWorkspaceFilePickerSheet(sessionId: sessionId),
    );
  }

  @override
  ConsumerState<CodeWorkspaceFilePickerSheet> createState() =>
      _CodeWorkspaceFilePickerSheetState();
}

class _CodeWorkspaceFilePickerSheetState
    extends ConsumerState<CodeWorkspaceFilePickerSheet> {
  String _currentPath = '';
  bool _loading = true;
  String? _error;
  List<CodeStructureEntry> _entries = const [];

  @override
  void initState() {
    super.initState();
    _load('');
  }

  Future<void> _load(String path) async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final res = await ref
          .read(aiWorkspaceRepositoryProvider)
          .getSessionStructure(widget.sessionId, path: path);
      if (!mounted) return;
      setState(() {
        _currentPath = res.path;
        _entries = res.entries;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _loading = false;
      });
    }
  }

  void _openParent() {
    if (_currentPath.isEmpty) return;
    final slash = _currentPath.lastIndexOf('/');
    _load(slash > 0 ? _currentPath.substring(0, slash) : '');
  }

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      child: ConstrainedBox(
        constraints: BoxConstraints(
          maxHeight: MediaQuery.sizeOf(context).height * 0.7,
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
              padding: const EdgeInsets.fromLTRB(16, 12, 16, 8),
              child: Row(
                children: [
                  if (_currentPath.isNotEmpty)
                    IconButton(
                      visualDensity: VisualDensity.compact,
                      onPressed: _openParent,
                      icon: const Icon(Icons.arrow_back_rounded, size: 18),
                    ),
                  Expanded(
                    child: Text(
                      _currentPath.isEmpty ? '选择要引用的文件' : _currentPath,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        color: AppTheme.textPrimary,
                        fontSize: 15,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                  ),
                ],
              ),
            ),
            Divider(height: 1, color: AppTheme.border),
            if (_loading)
              const Padding(
                padding: EdgeInsets.all(24),
                child: Center(child: CircularProgressIndicator()),
              )
            else if (_error != null)
              Padding(
                padding: const EdgeInsets.all(16),
                child: Text(
                  _error!,
                  style: const TextStyle(color: AppTheme.error, fontSize: 13),
                ),
              )
            else
              Flexible(
                child: ListView.separated(
                  shrinkWrap: true,
                  itemCount: _entries.length,
                  separatorBuilder: (_, _) =>
                      Divider(height: 1, color: AppTheme.border),
                  itemBuilder: (context, index) {
                    final entry = _entries[index];
                    return ListTile(
                      dense: true,
                      leading: Icon(
                        entry.isDirectory
                            ? Icons.folder_outlined
                            : Icons.description_outlined,
                        size: 19,
                        color: entry.isDirectory
                            ? AppTheme.primaryBlue
                            : AppTheme.textSecondary,
                      ),
                      title: Text(
                        entry.name,
                        style: TextStyle(
                          color: AppTheme.textPrimary,
                          fontSize: 13.5,
                          fontFamily: entry.isDirectory ? null : 'monospace',
                        ),
                      ),
                      trailing: entry.isDirectory
                          ? Icon(
                              Icons.chevron_right_rounded,
                              size: 18,
                              color: AppTheme.textSecondary,
                            )
                          : null,
                      onTap: () {
                        if (entry.isDirectory) {
                          _load(entry.path);
                        } else {
                          Navigator.of(context).pop(entry.path);
                        }
                      },
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
