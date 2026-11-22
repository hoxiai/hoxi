import 'dart:typed_data';

import 'package:flutter/material.dart';

import '../../../../core/l10n/l10n.dart';
import '../../../../core/theme/app_theme.dart';
import '../../models/code_workspace_file.dart';

/// Displays binary workspace outputs (Excel spreadsheets, PDFs, Word docs,
/// images, archives) with native preview when available and a primary 1-tap
/// export/share button so users can save or forward results to WeChat/Files.
class WorkspaceBinaryFileViewer extends StatefulWidget {
  const WorkspaceBinaryFileViewer({
    super.key,
    required this.file,
    required this.isExporting,
    required this.onExport,
    this.onCopyExtractedText,
  });

  final CodeSessionFile file;
  final bool isExporting;
  final VoidCallback onExport;
  final VoidCallback? onCopyExtractedText;

  @override
  State<WorkspaceBinaryFileViewer> createState() =>
      _WorkspaceBinaryFileViewerState();
}

class _WorkspaceBinaryFileViewerState extends State<WorkspaceBinaryFileViewer> {
  int _selectedSheetIndex = 0;
  Uint8List? _cachedImageBytes;

  @override
  void initState() {
    super.initState();
    _refreshDecodedBytes();
  }

  @override
  void didUpdateWidget(covariant WorkspaceBinaryFileViewer oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.file.path != widget.file.path ||
        oldWidget.file.base64Content != widget.file.base64Content) {
      _selectedSheetIndex = 0;
      _refreshDecodedBytes();
    }
  }

  void _refreshDecodedBytes() {
    _cachedImageBytes = widget.file.isImage
        ? widget.file.decodeBase64Bytes()
        : null;
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final file = widget.file;
    final filename = file.path.contains('/')
        ? file.path.split('/').last
        : file.path;
    final visual = _resolveBinaryVisual(file.extension, file.mimeType);

    return Column(
      children: [
        Container(
          width: double.infinity,
          padding: const EdgeInsets.all(14),
          decoration: BoxDecoration(
            color: AppTheme.surface,
            border: Border(bottom: BorderSide(color: AppTheme.border)),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Container(
                    width: 40,
                    height: 40,
                    decoration: BoxDecoration(
                      color: visual.color.withValues(
                        alpha: AppTheme.isDark ? 0.22 : 0.12,
                      ),
                      borderRadius: BorderRadius.circular(10),
                    ),
                    child: Icon(visual.icon, color: visual.color, size: 22),
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          filename,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(
                            color: AppTheme.textPrimary,
                            fontSize: 14.5,
                            fontWeight: FontWeight.w700,
                          ),
                        ),
                        const SizedBox(height: 2),
                        Text(
                          [
                            if (file.extension.isNotEmpty)
                              file.extension.toUpperCase(),
                            if (file.size > 0) _formatSize(file.size),
                            if (file.mimeType.isNotEmpty) file.mimeType,
                          ].join(' · '),
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(
                            color: AppTheme.textSecondary,
                            fontSize: 11.5,
                          ),
                        ),
                      ],
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 12),
              Row(
                children: [
                  Expanded(
                    child: FilledButton.icon(
                      key: const ValueKey('workspace_binary_export_btn'),
                      onPressed: widget.isExporting ? null : widget.onExport,
                      icon: widget.isExporting
                          ? const SizedBox.square(
                              dimension: 16,
                              child: CircularProgressIndicator(strokeWidth: 2),
                            )
                          : const Icon(Icons.ios_share_rounded, size: 17),
                      label: Text(l10n.codeWsFilesExportShare),
                    ),
                  ),
                  if (file.extractedText.trim().isNotEmpty &&
                      widget.onCopyExtractedText != null) ...[
                    const SizedBox(width: 8),
                    OutlinedButton.icon(
                      key: const ValueKey('workspace_binary_copy_text_btn'),
                      onPressed: widget.onCopyExtractedText,
                      icon: const Icon(Icons.content_copy_rounded, size: 16),
                      label: Text(l10n.codeWsFilesCopy),
                    ),
                  ],
                ],
              ),
            ],
          ),
        ),
        Expanded(child: _buildPreviewArea(context)),
      ],
    );
  }

  Widget _buildPreviewArea(BuildContext context) {
    final file = widget.file;
    if (_cachedImageBytes != null && _cachedImageBytes!.isNotEmpty) {
      return InteractiveViewer(
        minScale: 0.5,
        maxScale: 4,
        child: Center(
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: Image.memory(
              _cachedImageBytes!,
              fit: BoxFit.contain,
              errorBuilder: (_, _, _) => _buildNoPreviewHint(context),
            ),
          ),
        ),
      );
    }

    if (file.sheets.isNotEmpty) {
      final safeIdx = _selectedSheetIndex.clamp(0, file.sheets.length - 1);
      final sheet = file.sheets[safeIdx];
      return Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (file.sheets.length > 1)
            SingleChildScrollView(
              scrollDirection: Axis.horizontal,
              padding: const EdgeInsets.fromLTRB(12, 10, 12, 6),
              child: Row(
                children: [
                  for (var i = 0; i < file.sheets.length; i++) ...[
                    ChoiceChip(
                      label: Text(file.sheets[i].name),
                      selected: i == safeIdx,
                      onSelected: (_) =>
                          setState(() => _selectedSheetIndex = i),
                    ),
                    const SizedBox(width: 8),
                  ],
                ],
              ),
            ),
          Expanded(child: _SpreadsheetPreviewTable(sheet: sheet)),
        ],
      );
    }

    if (file.extractedText.trim().isNotEmpty) {
      return SingleChildScrollView(
        padding: const EdgeInsets.all(16),
        child: SelectableText(
          file.extractedText,
          style: TextStyle(
            color: AppTheme.textPrimary,
            fontSize: 13.5,
            height: 1.55,
          ),
        ),
      );
    }

    return _buildNoPreviewHint(context);
  }

  Widget _buildNoPreviewHint(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(28),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              Icons.file_present_rounded,
              size: 46,
              color: AppTheme.textLight,
            ),
            const SizedBox(height: 12),
            Text(
              context.l10n.codeWsFilesBinaryPreviewHint,
              textAlign: TextAlign.center,
              style: TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 13.5,
                height: 1.45,
              ),
            ),
          ],
        ),
      ),
    );
  }

  static ({IconData icon, Color color}) _resolveBinaryVisual(
    String ext,
    String mime,
  ) {
    final lowerExt = ext.toLowerCase();
    final lowerMime = mime.toLowerCase();
    if (const {'xlsx', 'xls', 'ods', 'csv', 'tsv'}.contains(lowerExt) ||
        lowerMime.contains('spreadsheet') ||
        lowerMime.contains('excel')) {
      return (icon: Icons.table_chart_rounded, color: const Color(0xFF16A34A));
    }
    if (lowerExt == 'pdf' || lowerMime == 'application/pdf') {
      return (
        icon: Icons.picture_as_pdf_rounded,
        color: const Color(0xFFDC2626),
      );
    }
    if (const {'docx', 'doc', 'rtf'}.contains(lowerExt) ||
        lowerMime.contains('word')) {
      return (icon: Icons.article_rounded, color: const Color(0xFF2563EB));
    }
    if (const {'pptx', 'ppt'}.contains(lowerExt) ||
        lowerMime.contains('presentation')) {
      return (icon: Icons.slideshow_rounded, color: const Color(0xFFEA580C));
    }
    if (lowerMime.startsWith('image/') ||
        const {'png', 'jpg', 'jpeg', 'webp', 'gif'}.contains(lowerExt)) {
      return (icon: Icons.image_rounded, color: const Color(0xFF7C3AED));
    }
    return (icon: Icons.insert_drive_file_rounded, color: AppTheme.primaryBlue);
  }

  static String _formatSize(int bytes) {
    if (bytes < 1024) return '$bytes B';
    if (bytes < 1024 * 1024) return '${(bytes / 1024).toStringAsFixed(1)} KB';
    return '${(bytes / 1024 / 1024).toStringAsFixed(1)} MB';
  }
}

class _SpreadsheetPreviewTable extends StatelessWidget {
  const _SpreadsheetPreviewTable({required this.sheet});

  final CodeSpreadsheetSheet sheet;

  static const int _maxCols = 12;
  static const int _maxRows = 60;

  @override
  Widget build(BuildContext context) {
    final rawCols = sheet.headers.isNotEmpty
        ? sheet.headers
        : (sheet.rows.isNotEmpty
              ? List<String>.generate(
                  sheet.rows.first.length,
                  (i) => 'Col ${i + 1}',
                )
              : const <String>[]);
    if (rawCols.isEmpty) {
      return Center(
        child: Text(
          context.l10n.codeWsFilesEmpty,
          style: TextStyle(color: AppTheme.textSecondary),
        ),
      );
    }
    final cols = rawCols.take(_maxCols).toList(growable: false);
    final rows = sheet.rows.take(_maxRows).toList(growable: false);
    return SingleChildScrollView(
      padding: const EdgeInsets.all(12),
      child: SingleChildScrollView(
        scrollDirection: Axis.horizontal,
        child: DataTable(
          headingRowHeight: 38,
          dataRowMinHeight: 34,
          dataRowMaxHeight: 48,
          columnSpacing: 18,
          headingRowColor: WidgetStatePropertyAll(AppTheme.surfaceMuted),
          columns: [
            for (final col in cols)
              DataColumn(
                label: Text(
                  col.isEmpty ? '-' : col,
                  style: TextStyle(
                    color: AppTheme.textPrimary,
                    fontWeight: FontWeight.w700,
                    fontSize: 12,
                  ),
                ),
              ),
          ],
          rows: [
            for (final row in rows)
              DataRow(
                cells: [
                  for (var c = 0; c < cols.length; c++)
                    DataCell(
                      ConstrainedBox(
                        constraints: const BoxConstraints(maxWidth: 200),
                        child: Text(
                          c < row.length ? row[c] : '',
                          maxLines: 2,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(
                            color: AppTheme.textPrimary,
                            fontSize: 12,
                          ),
                        ),
                      ),
                    ),
                ],
              ),
          ],
        ),
      ),
    );
  }
}
