import 'package:flutter/material.dart';

import '../../../../core/l10n/l10n.dart';
import '../../../../core/models/node_skill.dart';
import '../../../../core/theme/app_theme.dart';
import '../../../../shared/utils/local_file_bridge.dart';
import '../../../../shared/widgets/chat_image_preview.dart';
import '../../../../shared/widgets/chat_keyboard_dismiss.dart';

/// Shows the attachment source bottom sheet for [CodeInstructionComposer].
void showCodeComposerAttachmentMenu(
  BuildContext context, {
  VoidCallback? onPickDocument,
  VoidCallback? onPickWorkspaceFile,
  VoidCallback? onPickSkill,
  VoidCallback? onPickFromGallery,
  VoidCallback? onTakePhoto,
}) {
  dismissChatKeyboard(context);
  final l10n = context.l10n;
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
              if (onPickDocument != null)
                ListTile(
                  key: const ValueKey('code_composer_attach_document'),
                  leading: const Icon(
                    Icons.upload_file_rounded,
                    color: AppTheme.primaryBlue,
                  ),
                  title: Text(l10n.chatActionUploadDocument),
                  subtitle: Text(
                    l10n.chatActionUploadDocumentHint,
                    style: TextStyle(
                      color: AppTheme.textSecondary,
                      fontSize: 12,
                    ),
                  ),
                  onTap: () {
                    Navigator.of(sheetContext).pop();
                    onPickDocument();
                  },
                ),
              if (onPickWorkspaceFile != null)
                ListTile(
                  key: const ValueKey('code_composer_attach_file'),
                  leading: const Icon(
                    Icons.description_outlined,
                    color: AppTheme.primaryBlue,
                  ),
                  title: Text(l10n.codeComposerAttachWorkspaceFile),
                  subtitle: Text(
                    l10n.codeComposerAttachWorkspaceFileHint,
                    style: TextStyle(
                      color: AppTheme.textSecondary,
                      fontSize: 12,
                    ),
                  ),
                  onTap: () {
                    Navigator.of(sheetContext).pop();
                    onPickWorkspaceFile();
                  },
                ),
              if (onPickSkill != null)
                ListTile(
                  key: const ValueKey('code_composer_attach_skill'),
                  leading: const Icon(
                    Icons.auto_awesome_outlined,
                    color: AppTheme.primaryBlue,
                  ),
                  title: Text(l10n.codeComposerAttachSkill),
                  subtitle: Text(
                    l10n.codeComposerAttachSkillHint,
                    style: TextStyle(
                      color: AppTheme.textSecondary,
                      fontSize: 12,
                    ),
                  ),
                  onTap: () {
                    Navigator.of(sheetContext).pop();
                    onPickSkill();
                  },
                ),
              if (onPickFromGallery != null)
                ListTile(
                  key: const ValueKey('code_composer_attach_gallery'),
                  leading: const Icon(
                    Icons.photo_library_outlined,
                    color: AppTheme.primaryBlue,
                  ),
                  title: Text(l10n.chatActionPickGallery),
                  onTap: () {
                    Navigator.of(sheetContext).pop();
                    onPickFromGallery();
                  },
                ),
              if (onTakePhoto != null)
                ListTile(
                  key: const ValueKey('code_composer_attach_camera'),
                  leading: const Icon(
                    Icons.photo_camera_outlined,
                    color: AppTheme.primaryBlue,
                  ),
                  title: Text(l10n.chatTooltipCamera),
                  onTap: () {
                    Navigator.of(sheetContext).pop();
                    onTakePhoto();
                  },
                ),
            ],
          ),
        ),
      );
    },
  );
}

/// Renders pending attachment chips (selected skill, pending image, local
/// documents to upload, and referenced workspace `@files`) inside the composer.
class CodeComposerAttachmentsBar extends StatelessWidget {
  const CodeComposerAttachmentsBar({
    super.key,
    this.selectedSkill,
    this.onClearSelectedSkill,
    this.pendingImageDataUrl,
    this.onClearPendingImage,
    this.pendingDocuments = const [],
    this.onRemovePendingDocument,
    this.attachedFiles = const [],
    this.onRemoveAttachedFile,
  });

  final NodeSkillSelection? selectedSkill;
  final VoidCallback? onClearSelectedSkill;
  final String? pendingImageDataUrl;
  final VoidCallback? onClearPendingImage;
  final List<LocalPickedDocument> pendingDocuments;
  final ValueChanged<LocalPickedDocument>? onRemovePendingDocument;
  final List<String> attachedFiles;
  final ValueChanged<String>? onRemoveAttachedFile;

  @override
  Widget build(BuildContext context) {
    final hasPendingImage =
        pendingImageDataUrl != null && pendingImageDataUrl!.trim().isNotEmpty;
    return Padding(
      padding: const EdgeInsets.only(top: 2, bottom: 6),
      child: Wrap(
        spacing: 8,
        runSpacing: 6,
        crossAxisAlignment: WrapCrossAlignment.center,
        children: [
          if (selectedSkill != null)
            Container(
              key: const ValueKey('code_composer_selected_skill_chip'),
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
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
                    constraints: const BoxConstraints(maxWidth: 160),
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
                      key: const ValueKey('code_composer_clear_skill'),
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
                    border: Border.all(color: AppTheme.primaryBlueBorder),
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
                      key: const ValueKey('code_composer_clear_image'),
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
          for (final doc in pendingDocuments)
            Container(
              key: ValueKey('code_composer_document_chip_${doc.name}'),
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
              decoration: BoxDecoration(
                color: AppTheme.surface,
                borderRadius: BorderRadius.circular(6),
                border: Border.all(color: AppTheme.primaryBlueBorder),
              ),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Icon(
                    _iconForDocExtension(doc.extension),
                    size: 14,
                    color: AppTheme.primaryBlue,
                  ),
                  const SizedBox(width: 4),
                  ConstrainedBox(
                    constraints: const BoxConstraints(maxWidth: 165),
                    child: Text(
                      '${doc.name} · ${doc.formattedSize}',
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        color: AppTheme.textPrimary,
                        fontSize: 11.5,
                        fontWeight: FontWeight.w500,
                      ),
                    ),
                  ),
                  if (onRemovePendingDocument != null) ...[
                    const SizedBox(width: 4),
                    InkWell(
                      onTap: () => onRemovePendingDocument!(doc),
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
          for (final file in attachedFiles)
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
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
                    constraints: const BoxConstraints(maxWidth: 160),
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
    );
  }

  static IconData _iconForDocExtension(String ext) {
    return switch (ext) {
      'xlsx' || 'xls' || 'csv' || 'tsv' || 'ods' => Icons.table_chart_rounded,
      'pdf' => Icons.picture_as_pdf_rounded,
      'docx' || 'doc' || 'rtf' => Icons.article_rounded,
      'pptx' || 'ppt' => Icons.slideshow_rounded,
      'png' || 'jpg' || 'jpeg' || 'webp' || 'gif' => Icons.image_outlined,
      _ => Icons.attach_file_rounded,
    };
  }
}
