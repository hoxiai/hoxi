import 'package:flutter/material.dart';

import '../../../../core/theme/app_theme.dart';
import '../../models/ai_dev_session.dart';

class AiTimelinePanel extends StatefulWidget {
  const AiTimelinePanel({
    super.key,
    required this.timelineEvents,
    required this.errorSummary,
    required this.changedFiles,
  });

  final List<AiTimelineEvent> timelineEvents;
  final String errorSummary;
  final List<String> changedFiles;

  @override
  State<AiTimelinePanel> createState() => _AiTimelinePanelState();
}

class _AiTimelinePanelState extends State<AiTimelinePanel> {
  bool? _userExpanded;

  bool get _isExpanded => _userExpanded ?? false;

  @override
  Widget build(BuildContext context) {
    final errorSummary = widget.errorSummary;
    final changedFiles = widget.changedFiles;
    final timelineEvents = widget.timelineEvents;
    final hasError = errorSummary.trim().isNotEmpty;
    final hasSummary = hasError || changedFiles.isNotEmpty;
    final hasTimeline = timelineEvents.isNotEmpty;
    if (!hasSummary && !hasTimeline) {
      return const SizedBox.shrink();
    }

    return Container(
      width: double.infinity,
      decoration: BoxDecoration(
        color: AppTheme.surfaceMuted.withValues(alpha: 0.3),
      ),
      child: InkWell(
        onTap: () => setState(() => _userExpanded = !_isExpanded),
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Icon(
                    hasError
                        ? Icons.error_outline_rounded
                        : Icons.timeline_rounded,
                    color: hasError ? AppTheme.error : AppTheme.textSecondary,
                    size: 15,
                  ),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text(
                      hasError && !_isExpanded
                          ? errorSummary.trim()
                          : '过程摘要',
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        color: hasError && !_isExpanded
                            ? AppTheme.error
                            : AppTheme.textSecondary,
                        fontSize: 12,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  ),
                  if (changedFiles.isNotEmpty) ...[
                    Text(
                      '${changedFiles.length} 个文件',
                      style: TextStyle(
                        color: AppTheme.primaryBlueText,
                        fontSize: 11.5,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    const SizedBox(width: 8),
                  ],
                  if (timelineEvents.isNotEmpty)
                    Text(
                      '${timelineEvents.length} 步',
                      style: TextStyle(
                        color: AppTheme.textSecondary,
                        fontSize: 11.5,
                        fontWeight: FontWeight.w500,
                      ),
                    ),
                  const SizedBox(width: 4),
                  Icon(
                    _isExpanded
                        ? Icons.expand_less_rounded
                        : Icons.expand_more_rounded,
                    size: 16,
                    color: AppTheme.textSecondary,
                  ),
                ],
              ),
              if (_isExpanded) ...[
                if (hasError) ...[
                  const SizedBox(height: 8),
                  Text(
                    errorSummary.trim(),
                    style: const TextStyle(
                      color: AppTheme.error,
                      fontSize: 12,
                      height: 1.5,
                    ),
                  ),
                ],
                if (changedFiles.isNotEmpty) ...[
                  const SizedBox(height: 8),
                  Wrap(
                    spacing: 6,
                    runSpacing: 6,
                    children: changedFiles
                        .take(6)
                        .map(
                          (file) => Container(
                            padding: const EdgeInsets.symmetric(
                              horizontal: 8,
                              vertical: 3,
                            ),
                            decoration: BoxDecoration(
                              color: AppTheme.primaryBlueLight,
                              borderRadius: BorderRadius.circular(6),
                            ),
                            child: Text(
                              file,
                              style: TextStyle(
                                color: AppTheme.primaryBlueText,
                                fontSize: 11,
                              ),
                            ),
                          ),
                        )
                        .toList(),
                  ),
                ],
                if (timelineEvents.isNotEmpty) ...[
                  const SizedBox(height: 10),
                  ...timelineEvents
                      .take(4)
                      .map(
                        (event) => Padding(
                          padding: const EdgeInsets.only(bottom: 6),
                          child: _TimelineItem(event: event),
                        ),
                      ),
                ],
              ],
            ],
          ),
        ),
      ),
    );
  }
}

class _TimelineItem extends StatelessWidget {
  const _TimelineItem({required this.event});

  final AiTimelineEvent event;

  @override
  Widget build(BuildContext context) {
    final style = _styleForStatus(event.status);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Container(
            width: 8,
            height: 8,
            margin: const EdgeInsets.only(top: 5),
            decoration: BoxDecoration(
              color: style.$1,
              borderRadius: BorderRadius.circular(999),
            ),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Expanded(
                      child: Text(
                        event.title.isEmpty ? '过程事件' : event.title,
                        style: TextStyle(
                          color: AppTheme.textPrimary,
                          fontSize: 12,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    ),
                    Text(
                      style.$2,
                      style: TextStyle(
                        color: style.$1,
                        fontSize: 11,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  ],
                ),
                if (event.content.trim().isNotEmpty) ...[
                  const SizedBox(height: 3),
                  Text(
                    event.content.trim(),
                    style: TextStyle(
                      color: AppTheme.textSecondary,
                      fontSize: 12,
                      height: 1.45,
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

  (Color, String) _styleForStatus(String value) {
    switch (value) {
      case 'success':
        return (AppTheme.success, '完成');
      case 'error':
        return (AppTheme.error, '异常');
      case 'running':
        return (AppTheme.warning, '进行中');
      default:
        return (AppTheme.primaryBlue, '记录');
    }
  }
}
