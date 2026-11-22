import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../models/code_workspace_file.dart';
import '../controllers/ai_workspace_controller.dart';
import '../controllers/code_workspace_files_controller.dart';
import '../widgets/code_workspace_editor.dart';
import '../widgets/workspace_binary_file_viewer.dart';
import '../../../../core/l10n/l10n.dart';
import '../../../../core/theme/app_theme.dart';
import '../../../../shared/utils/local_file_bridge.dart';

class CodeWorkspaceFilesScreen extends ConsumerStatefulWidget {
  const CodeWorkspaceFilesScreen({
    super.key,
    required this.sessionId,
    required this.sessionTitle,
    this.initialPath,
  });

  final int sessionId;
  final String sessionTitle;

  /// 直接打开指定文件（从会话里的读取步骤点入），为空时展示文件树根目录。
  final String? initialPath;

  @override
  ConsumerState<CodeWorkspaceFilesScreen> createState() =>
      _CodeWorkspaceFilesScreenState();
}

class _CodeWorkspaceFilesScreenState
    extends ConsumerState<CodeWorkspaceFilesScreen> {
  late final CodeWorkspaceFilesController _controller;

  @override
  void initState() {
    super.initState();
    _controller = CodeWorkspaceFilesController(
      repository: ref.read(aiWorkspaceRepositoryProvider),
      sessionId: widget.sessionId,
    );
    final initialPath = widget.initialPath?.trim() ?? '';
    if (initialPath.isNotEmpty) {
      _controller.openFilePath(initialPath);
    } else {
      _controller.loadDirectory();
    }
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  bool get _hasInitialFile => (widget.initialPath?.trim() ?? '').isNotEmpty;

  @override
  Widget build(BuildContext context) {
    return AnimatedBuilder(
      animation: _controller,
      builder: (context, _) {
        final state = _controller.state;
        final file = state.openFile;
        final initialPath = widget.initialPath?.trim() ?? '';
        final titleText = file != null
            ? file.path.split('/').last
            : (initialPath.isNotEmpty
                  ? initialPath.split('/').last
                  : context.l10n.codeWsFilesTitle);
        return PopScope<void>(
          canPop: _hasInitialFile
              ? (!state.isDirty && !state.isSaving)
              : file == null,
          onPopInvokedWithResult: (didPop, _) {
            if (!didPop) _handleBack();
          },
          child: Scaffold(
            backgroundColor: AppTheme.background,
            appBar: AppBar(
              backgroundColor: AppTheme.surface,
              foregroundColor: AppTheme.textPrimary,
              titleSpacing: 0,
              title: _ScreenTitle(
                title: titleText,
                subtitle: widget.sessionTitle,
              ),
              leading: IconButton(
                tooltip: context.l10n.codeWsActionBack,
                onPressed: _handleBack,
                icon: const Icon(Icons.arrow_back_rounded),
              ),
              actions: [
                if (file != null)
                  IconButton(
                    key: const ValueKey('workspace_file_appbar_export_btn'),
                    tooltip: context.l10n.codeWsFilesExportShare,
                    onPressed: state.isExporting
                        ? null
                        : () => _exportOpenFile(context, file),
                    icon: state.isExporting
                        ? const SizedBox.square(
                            dimension: 18,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : const Icon(Icons.ios_share_rounded, size: 20),
                  ),
                if (file != null &&
                    (!file.isBinary || file.extractedText.trim().isNotEmpty))
                  IconButton(
                    tooltip: context.l10n.codeWsFilesCopy,
                    onPressed: () => _copyFile(context, file),
                    icon: const Icon(Icons.content_copy_rounded),
                  ),
                IconButton(
                  tooltip: context.l10n.codeWsActionRefresh,
                  onPressed: state.isLoading || state.isSaving
                      ? null
                      : _refresh,
                  icon: const Icon(Icons.refresh_rounded),
                ),
              ],
            ),
            body: Column(
              children: [
                if (state.isLoading)
                  const LinearProgressIndicator(minHeight: 2),
                if (state.errorMessage != null)
                  _FilesErrorBanner(
                    message: state.errorMessage!,
                    onRetry: file != null && state.isDirty ? _save : _refresh,
                  ),
                Expanded(
                  child: file == null
                      ? (_hasInitialFile
                            ? _InitialFilePlaceholder(
                                path: initialPath,
                                isLoading: state.isLoading,
                              )
                            : _DirectoryView(
                                state: state,
                                onOpen: _controller.openEntry,
                                onExportEntry: (entry) =>
                                    _exportPath(context, entry.path),
                                onOpenParent: _controller.openParent,
                                onRefresh: _controller.refresh,
                              ))
                      : file.isBinary
                      ? WorkspaceBinaryFileViewer(
                          key: ValueKey('bin_${file.path}:${file.size}'),
                          file: file,
                          isExporting: state.isExporting,
                          onExport: () => _exportOpenFile(context, file),
                          onCopyExtractedText:
                              file.extractedText.trim().isNotEmpty
                              ? () => _copyFile(context, file)
                              : null,
                        )
                      : CodeWorkspaceEditor(
                          key: ValueKey('${file.path}:${file.version}'),
                          file: file,
                          content: state.draftContent,
                          isDirty: state.isDirty,
                          isSaving: state.isSaving,
                          onChanged: _controller.updateDraft,
                          onSave: _save,
                        ),
                ),
              ],
            ),
          ),
        );
      },
    );
  }

  Future<void> _copyFile(BuildContext context, CodeSessionFile file) async {
    final text = file.isBinary
        ? file.extractedText
        : _controller.state.draftContent;
    await Clipboard.setData(ClipboardData(text: text));
    if (!context.mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(context.l10n.codeWsFilesCopied)),
    );
  }

  Future<void> _exportOpenFile(
    BuildContext context,
    CodeSessionFile file,
  ) async {
    if (!file.isBinary) {
      final filename = file.path.contains('/')
          ? file.path.split('/').last
          : file.path;
      final bytes = Uint8List.fromList(
        utf8.encode(_controller.state.draftContent),
      );
      await _shareBytes(
        context,
        filename: filename,
        bytes: bytes,
        mime: file.mimeType,
      );
      return;
    }
    final inlineBytes = file.decodeBase64Bytes();
    if (inlineBytes != null &&
        inlineBytes.isNotEmpty &&
        (file.size <= 0 || inlineBytes.length == file.size)) {
      final filename = file.path.contains('/')
          ? file.path.split('/').last
          : file.path;
      await _shareBytes(
        context,
        filename: filename,
        bytes: inlineBytes,
        mime: file.mimeType,
      );
      return;
    }
    await _exportPath(context, file.path, mimeHint: file.mimeType);
  }

  Future<void> _exportPath(
    BuildContext context,
    String path, {
    String mimeHint = '',
  }) async {
    try {
      final downloaded = await _controller.downloadFileBytes(path);
      if (!context.mounted) return;
      await _shareBytes(
        context,
        filename: downloaded.filename,
        bytes: Uint8List.fromList(downloaded.bytes),
        mime: mimeHint,
      );
    } catch (error) {
      if (!context.mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(context.l10n.codeWsFilesExportFailed(error.toString())),
        ),
      );
    }
  }

  Future<void> _shareBytes(
    BuildContext context, {
    required String filename,
    required Uint8List bytes,
    String mime = '',
  }) async {
    try {
      await const LocalFileBridge().shareFile(
        filename: filename,
        bytes: bytes,
        mime: mime,
      );
    } on LocalFileBridgeException catch (e) {
      if (!context.mounted) return;
      final msg = e.failure == LocalFileBridgeFailure.unsupported
          ? context.l10n.codeWsFilesExportUnsupported
          : context.l10n.codeWsFilesExportFailed(
              e.message.isNotEmpty ? e.message : e.failure.name,
            );
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(msg)));
    }
  }

  Future<void> _save() async {
    final saved = await _controller.saveOpenFile();
    if (!mounted || !saved) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(context.l10n.codeWsFilesSaveSuccess),
      ),
    );
  }

  Future<void> _refresh() async {
    if (_controller.state.isDirty && !await _confirmDiscard()) return;
    if (_controller.state.openFile == null && _hasInitialFile) {
      await _controller.openFilePath(widget.initialPath!.trim());
      return;
    }
    await _controller.refresh();
  }

  Future<void> _handleBack() async {
    final state = _controller.state;
    if (state.isSaving) return;
    if (state.openFile == null || _hasInitialFile) {
      if (state.isDirty && !await _confirmDiscard()) return;
      if (mounted) Navigator.of(context).pop();
      return;
    }
    if (state.isDirty && !await _confirmDiscard()) return;
    _controller.closeFile();
  }

  Future<bool> _confirmDiscard() async {
    return await showDialog<bool>(
          context: context,
          builder: (dialogContext) => AlertDialog(
            title: Text(
              dialogContext.l10n.codeWsFilesUnsavedTitle,
            ),
            content: Text(
              dialogContext.l10n.codeWsFilesUnsavedHint,
            ),
            actions: [
              TextButton(
                onPressed: () => Navigator.of(dialogContext).pop(false),
                child: Text(
                  dialogContext.l10n.codeWsFilesKeepEditing,
                ),
              ),
              FilledButton(
                onPressed: () => Navigator.of(dialogContext).pop(true),
                child: Text(
                  dialogContext.l10n.codeWsFilesDiscard,
                ),
              ),
            ],
          ),
        ) ??
        false;
  }
}

class _ScreenTitle extends StatelessWidget {
  const _ScreenTitle({required this.title, required this.subtitle});

  final String title;
  final String subtitle;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(
          title,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: TextStyle(
            fontSize: 16,
            fontWeight: FontWeight.w700,
            color: AppTheme.textPrimary,
          ),
        ),
        Text(
          subtitle,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: TextStyle(fontSize: 11, color: AppTheme.textSecondary),
        ),
      ],
    );
  }
}

class _InitialFilePlaceholder extends StatelessWidget {
  const _InitialFilePlaceholder({
    required this.path,
    required this.isLoading,
  });

  final String path;
  final bool isLoading;

  @override
  Widget build(BuildContext context) {
    if (isLoading) {
      return const Center(child: CircularProgressIndicator());
    }
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              Icons.description_outlined,
              color: AppTheme.textLight,
              size: 44,
            ),
            const SizedBox(height: 12),
            Text(
              path,
              textAlign: TextAlign.center,
              style: TextStyle(
                color: AppTheme.textSecondary,
                fontFamily: 'monospace',
                fontSize: 12,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _DirectoryView extends StatelessWidget {
  const _DirectoryView({
    required this.state,
    required this.onOpen,
    required this.onExportEntry,
    required this.onOpenParent,
    required this.onRefresh,
  });

  final CodeWorkspaceFilesState state;
  final ValueChanged<CodeStructureEntry> onOpen;
  final ValueChanged<CodeStructureEntry> onExportEntry;
  final VoidCallback onOpenParent;
  final Future<void> Function() onRefresh;

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        _PathBar(path: state.currentPath),
        if (state.truncated)
          Container(
            width: double.infinity,
            color: AppTheme.warningSoft,
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
            child: Text(
              context.l10n.codeWsFilesTruncated,
              style: const TextStyle(color: AppTheme.warning, fontSize: 12),
            ),
          ),
        Expanded(
          child: RefreshIndicator(
            onRefresh: onRefresh,
            child: ListView.separated(
              physics: const AlwaysScrollableScrollPhysics(),
              padding: const EdgeInsets.fromLTRB(12, 8, 12, 24),
              itemCount: state.entries.isEmpty
                  ? (state.currentPath.isEmpty ? 1 : 2)
                  : state.entries.length + (state.currentPath.isEmpty ? 0 : 1),
              separatorBuilder: (_, _) =>
                  Divider(height: 1, color: AppTheme.border),
              itemBuilder: (context, index) {
                if (state.currentPath.isNotEmpty && index == 0) {
                  return _FileRow(
                    icon: Icons.drive_file_move_outline,
                    title: '..',
                    subtitle: context.l10n.codeWsActionParent,
                    onTap: onOpenParent,
                  );
                }
                if (state.entries.isEmpty) {
                  return _EmptyFilesHint(
                    text: context.l10n.codeWsFilesEmpty,
                  );
                }
                final offset = state.currentPath.isEmpty ? index : index - 1;
                final entry = state.entries[offset];
                return _FileRow(
                  icon: entry.isDirectory
                      ? Icons.folder_rounded
                      : _iconForExtension(entry.extension.toLowerCase()),
                  title: entry.name,
                  subtitle: entry.isDirectory
                      ? null
                      : entry.extension.toUpperCase(),
                  onTap: () => onOpen(entry),
                  onExport: entry.isDirectory || state.isExporting
                      ? null
                      : () => onExportEntry(entry),
                );
              },
            ),
          ),
        ),
      ],
    );
  }

  IconData _iconForExtension(String extension) {
    return switch (extension) {
      'xlsx' || 'xls' || 'csv' || 'tsv' || 'ods' => Icons.table_chart_rounded,
      'pdf' => Icons.picture_as_pdf_rounded,
      'docx' || 'doc' || 'rtf' || 'md' => Icons.article_outlined,
      'pptx' || 'ppt' => Icons.slideshow_rounded,
      'json' || 'yaml' || 'yml' => Icons.data_object_rounded,
      'png' || 'jpg' || 'jpeg' || 'gif' || 'webp' => Icons.image_outlined,
      'mp4' || 'mov' || 'webm' => Icons.movie_outlined,
      'zip' || 'tar' || 'gz' => Icons.folder_zip_outlined,
      _ => Icons.description_outlined,
    };
  }
}

class _PathBar extends StatelessWidget {
  const _PathBar({required this.path});

  final String path;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      decoration: BoxDecoration(
        color: AppTheme.surfaceMuted,
        border: Border(bottom: BorderSide(color: AppTheme.border)),
      ),
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
      child: Row(
        children: [
          const Icon(
            Icons.account_tree_outlined,
            size: 18,
            color: AppTheme.primaryBlue,
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              path.isEmpty ? context.l10n.codeWsFilesRoot : path,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(
                color: AppTheme.textSecondary,
                fontFamily: 'monospace',
                fontSize: 12,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _FileRow extends StatelessWidget {
  const _FileRow({
    required this.icon,
    required this.title,
    required this.onTap,
    this.subtitle,
    this.onExport,
  });

  final IconData icon;
  final String title;
  final String? subtitle;
  final VoidCallback onTap;
  final VoidCallback? onExport;

  @override
  Widget build(BuildContext context) {
    return ListTile(
      contentPadding: const EdgeInsets.symmetric(horizontal: 8),
      leading: Icon(icon, color: AppTheme.primaryBlue),
      title: Text(title, style: TextStyle(color: AppTheme.textPrimary)),
      subtitle: subtitle == null
          ? null
          : Text(
              subtitle!,
              style: TextStyle(color: AppTheme.textSecondary, fontSize: 12),
            ),
      trailing: onExport != null
          ? IconButton(
              key: ValueKey('workspace_file_row_export_$title'),
              tooltip: context.l10n.codeWsFilesExportShare,
              onPressed: onExport,
              icon: Icon(
                Icons.ios_share_rounded,
                size: 18,
                color: AppTheme.textSecondary,
              ),
            )
          : Icon(Icons.chevron_right_rounded, color: AppTheme.textLight),
      onTap: onTap,
    );
  }
}

class _EmptyFilesHint extends StatelessWidget {
  const _EmptyFilesHint({required this.text});

  final String text;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 64),
      child: Column(
        children: [
          Icon(
            Icons.folder_open_rounded,
            color: AppTheme.textLight,
            size: 44,
          ),
          const SizedBox(height: 12),
          Text(text, style: TextStyle(color: AppTheme.textSecondary)),
        ],
      ),
    );
  }
}

class _FilesErrorBanner extends StatelessWidget {
  const _FilesErrorBanner({required this.message, required this.onRetry});

  final String message;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    return MaterialBanner(
      backgroundColor: AppTheme.errorSoft,
      content: Text(
        '${context.l10n.codeWsFilesOperationFailed}：$message',
        style: const TextStyle(color: AppTheme.error),
      ),
      actions: [
        TextButton(
          onPressed: onRetry,
          child: Text(context.l10n.codeWsActionRetry),
        ),
      ],
    );
  }
}
