import 'package:flutter/material.dart';

import '../../../../core/theme/app_theme.dart';
import '../../models/ai_dev_session.dart';

class AiPreviewStrip extends StatelessWidget {
  const AiPreviewStrip({
    super.key,
    required this.previews,
    required this.onOpenPreview,
    required this.onViewAll,
  });

  final List<AiPreview> previews;
  final ValueChanged<AiPreview> onOpenPreview;
  final VoidCallback onViewAll;

  @override
  Widget build(BuildContext context) {
    if (previews.isEmpty) {
      return const SizedBox.shrink();
    }

    return Container(
      width: double.infinity,
      margin: const EdgeInsets.fromLTRB(16, 0, 16, 8),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(14),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(
                Icons.visibility_outlined,
                size: 16,
                color: AppTheme.primaryBlue,
              ),
              const SizedBox(width: 6),
              Text(
                '预览 (${previews.length})',
                style: TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 13,
                  fontWeight: FontWeight.w700,
                ),
              ),
              const Spacer(),
              TextButton(
                style: TextButton.styleFrom(
                  visualDensity: VisualDensity.compact,
                  padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                ),
                onPressed: onViewAll,
                child: const Text('查看全部', style: TextStyle(fontSize: 12)),
              ),
            ],
          ),
          const SizedBox(height: 4),
          ...previews
              .take(2)
              .map(
                (preview) => _PreviewItem(
                  preview: preview,
                  onTap: () => onOpenPreview(preview),
                ),
              ),
        ],
      ),
    );
  }
}

class _PreviewItem extends StatelessWidget {
  const _PreviewItem({required this.preview, required this.onTap});

  final AiPreview preview;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(8),
      child: Container(
        margin: const EdgeInsets.only(bottom: 4),
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 7),
        decoration: BoxDecoration(
          color: AppTheme.surfaceMuted,
          borderRadius: BorderRadius.circular(8),
        ),
        child: Row(
          children: [
            Container(
              width: 7,
              height: 7,
              decoration: BoxDecoration(
                color: _statusColor(preview.status),
                shape: BoxShape.circle,
              ),
            ),
            const SizedBox(width: 8),
            Flexible(
              child: Text(
                preview.title.isEmpty ? '开发预览' : preview.title,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 12.5,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                preview.url,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(
                  color: AppTheme.primaryBlueText,
                  fontSize: 11.5,
                ),
              ),
            ),
            const SizedBox(width: 6),
            Icon(
              Icons.open_in_new_rounded,
              size: 14,
              color: AppTheme.textSecondary,
            ),
          ],
        ),
      ),
    );
  }

  Color _statusColor(String status) {
    switch (status) {
      case 'ready':
      case 'available':
        return AppTheme.success;
      case 'local':
      case 'starting':
        return AppTheme.warning;
      default:
        return AppTheme.primaryBlue;
    }
  }
}
