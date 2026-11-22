import 'dart:math' as math;
import 'package:flutter/material.dart';

import '../../core/l10n/l10n.dart';
import '../../core/theme/app_theme.dart';

/// Rate-matched streaming text pacer ported from PC Hoxi (`web/src/components/timeline/markdown/reveal.ts`
/// and `stream.ts`).
///
/// Estimates the SSE chunk arrival interval via EWMA and spreads buffered characters
/// evenly across 60fps frames until the next chunk is due, while accelerating smoothly
/// on large backlogs and snapping slice boundaries past atomic Markdown tokens.
class StreamTextPacer {
  StreamTextPacer({String initial = '', double nowMs = 0})
    : _target = initial,
      _shown = initial.length,
      _lastArrivalMs = nowMs;

  static const double defaultIntervalMs = 96.0;
  static const double minIntervalMs = 16.0;
  static const double maxIntervalMs = 600.0;
  static const double intervalEwmaAlpha = 0.3;
  static const double drainLead = 1.15;
  static const double minDrainRatio = 0.35;
  static const int catchupChars = 180;
  static const double catchupRatioPerFrame = 0.14;
  static const double minCharsPerSecond = 48.0;
  static const double frameMs = 1000.0 / 60.0;

  String _target;
  int _shown;
  double _carry = 0;
  double _lastArrivalMs;
  double _lastStepMs = 0;
  double _intervalMs = defaultIntervalMs;

  String get target => _target;
  int get shown => _shown;
  bool get pending => _shown < _target.length;
  String get displayedText =>
      _shown >= _target.length ? _target : _target.substring(0, _shown);

  /// Shows [content] in full immediately and clears any buffered backlog.
  void reset(String content, double atMs) {
    _target = content;
    _shown = content.length;
    _carry = 0;
    _lastArrivalMs = atMs;
    _lastStepMs = 0;
  }

  /// Records newly arrived content. Returns `false` when [next] is not an append
  /// of the current target (e.g. session switch or message replacement).
  bool push(String next, double atMs) {
    if (next == _target) return true;
    if (!next.startsWith(_target)) return false;
    final wasEmpty = _target.isEmpty && _shown == 0;
    final gap = atMs - _lastArrivalMs;
    if (gap >= minIntervalMs && gap <= maxIntervalMs) {
      _intervalMs =
          _intervalMs * (1 - intervalEwmaAlpha) + gap * intervalEwmaAlpha;
    }
    _lastArrivalMs = atMs;
    _target = next;
    if (wasEmpty && _target.isNotEmpty) {
      // Reveal the first visible character immediately on the arrival frame
      // so the waiting indicator transitions without an empty frame.
      _shown = initialRevealSliceLen(_target);
    }
    return true;
  }

  /// Advances one animation frame and returns the new revealed character length.
  int step(double atMs) {
    final buffered = _target.length - _shown;
    if (buffered <= 0) {
      _lastStepMs = 0;
      return _shown;
    }
    final dt = _lastStepMs > 0
        ? (atMs - _lastStepMs).clamp(4.0, 50.0)
        : frameMs;
    _lastStepMs = atMs;
    final budget = math.max(
      _intervalMs * minDrainRatio,
      _intervalMs * drainLead - (atMs - _lastArrivalMs),
    );
    var want = buffered * math.min(1.0, dt / budget);
    if (buffered > catchupChars) {
      want = math.max(want, buffered * catchupRatioPerFrame * (dt / frameMs));
    }
    _carry += math.max(want, (minCharsPerSecond * dt) / 1000.0);
    final whole = math.min(_carry.floor(), buffered);
    if (whole <= 0) return _shown;
    final next = advanceStreamingSlice(_target, _shown, _shown + whole);
    // Do not carry negative debt after snapping past an atomic Markdown token.
    _carry = math.max(0.0, _carry - (next - _shown));
    _shown = next;
    return _shown;
  }

  static final RegExp _nonWhitespaceRe = RegExp(r'\S');
  static final RegExp _tableRowLineRe = RegExp(r'^\s*\|');
  static final RegExp _tableSeparatorLineRe = RegExp(
    r'^\s*\|?\s*:?-+:?\s*(?:\|\s*:?-+:?\s*)*\|?\s*$',
  );
  static final RegExp _codeFenceRe = RegExp(r'^\s*(?:`{3,}|~{3,})');
  static final RegExp _linePrefixRe = RegExp(
    r'^(\s*(?:#{1,6}[ \t]+\S?|(?:`{3,}|~{3,})[^\n]*|(?:[-*+]|\d+[.)])[ \t]+(?:\[[ xX]\][ \t]+)?\S?|>\s*(?:\[![A-Za-z]+\]\s*)?|(?:[-*_]\s*){3,}$))',
  );
  static final RegExp _atomicInlineRe = RegExp(
    r'!?\[(?:`[^`\n]+`|[^[\]`\n])+\]\((?:[^()\s]|\([^()\s]*\))+\)|`[^`\n]+`|\*\*(?:`[^`\n]+`|[^*`\n]|\*(?!\*)){1,48}\*\*|__(?:`[^`\n]+`|[^_`\n]|_(?!_)){1,48}__|~~[^~\n]{1,48}~~|\*[^*\n]{1,48}\*',
  );
  static final RegExp _delimiterRunCharRe = RegExp(r'[*_~`#\-!\[\]()]');

  static int initialRevealSliceLen(String content) {
    final match = _nonWhitespaceRe.firstMatch(content);
    if (match == null) return content.length;
    return advanceStreamingSlice(content, 0, match.start + 1);
  }

  /// Aligns streaming slice boundaries so complete inline tokens (`[label](url)`,
  /// `` `code` ``, `**bold**`), surrogate pairs, and line-start markers (`### `, `- `, ```` ``` ````)
  /// already present in [target] are never cut into half-closed intermediate states.
  static int advanceStreamingSlice(
    String target,
    int currentLen,
    int rawNextLen,
  ) {
    if (target.isEmpty || currentLen >= target.length) return target.length;
    var nextLen = math.min(target.length, math.max(currentLen + 1, rawNextLen));

    // Never split a UTF-16 surrogate pair
    final code = target.codeUnitAt(nextLen - 1);
    if (code >= 0xD800 && code <= 0xDBFF && nextLen < target.length) {
      nextLen++;
    }

    final prevNewline = target.lastIndexOf('\n', nextLen - 1);
    final lineStart = prevNewline + 1;
    final lineEndIdx = target.indexOf('\n', lineStart);
    final lineEnd = lineEndIdx == -1 ? target.length : lineEndIdx;
    final line = target.substring(lineStart, lineEnd);
    var relPos = nextLen - lineStart;

    // Reveal table rows (header + separator + first row, or subsequent body rows) a whole line at a time
    // so tables never flip between paragraph and table or reflow columns mid-row.
    if (_tableRowLineRe.hasMatch(line)) {
      if (lineEndIdx == -1) return lineEnd;
      final sepEnd = target.indexOf('\n', lineEnd + 1);
      if (sepEnd == -1 ||
          !_tableSeparatorLineRe.hasMatch(
            target.substring(lineEnd + 1, sepEnd),
          )) {
        return lineEnd + 1;
      }
      final rowEnd = target.indexOf('\n', sepEnd + 1);
      final row = target.substring(
        sepEnd + 1,
        rowEnd == -1 ? target.length : rowEnd,
      );
      return rowEnd != -1 && _tableRowLineRe.hasMatch(row)
          ? rowEnd + 1
          : sepEnd + 1;
    }

    // Snap past line-start block markers (`### `, `- `, `1. `, code fence opener)
    final prefixMatch = _linePrefixRe.firstMatch(line);
    if (prefixMatch != null) {
      final prefix = prefixMatch.group(1)!;
      if (relPos <= prefix.length) {
        if (_codeFenceRe.hasMatch(prefix)) {
          return lineEndIdx == -1 ? lineEnd : lineEnd + 1;
        }
        relPos = math.max(relPos, prefix.length);
      }
    }

    // Snap past complete inline tokens (links, code spans, short bold/italic)
    for (final match in _atomicInlineRe.allMatches(line)) {
      if (match.start < relPos && relPos < match.end) {
        relPos = match.end;
        break;
      }
      if (match.start >= relPos) break;
    }

    nextLen = math.min(target.length, lineStart + relPos);

    // Do not stop mid-delimiter run (`**`, `__`, `~~`, `![`, `](`)
    while (nextLen < lineEnd &&
        _delimiterRunCharRe.hasMatch(target[nextLen - 1]) &&
        _delimiterRunCharRe.hasMatch(target[nextLen])) {
      nextLen++;
    }

    // Re-verify surrogate pair after adjustments
    if (nextLen < target.length) {
      final c = target.codeUnitAt(nextLen - 1);
      if (c >= 0xD800 && c <= 0xDBFF) nextLen++;
    }

    return nextLen;
  }
}

String _sliceUtf16Prefix(String text, int maxUnits) {
  if (maxUnits <= 0 || text.isEmpty) return '';
  if (text.length <= maxUnits) return text;
  var end = maxUnits;
  final prev = text.codeUnitAt(end - 1);
  if (prev >= 0xD800 && prev <= 0xDBFF) {
    end -= 1;
  }
  return end <= 0 ? '' : text.substring(0, end);
}

String _sliceUtf16Suffix(String text, int maxUnits) {
  if (maxUnits <= 0 || text.isEmpty) return '';
  if (text.length <= maxUnits) return text;
  var start = text.length - maxUnits;
  final first = text.codeUnitAt(start);
  if (first >= 0xDC00 && first <= 0xDFFF && start < text.length) {
    start += 1;
  }
  return start >= text.length ? '' : text.substring(start);
}

/// Extracts a concise single-line live ticker from streaming or completed reasoning text,
/// ported from PC Hoxi.
String extractLiveReasoningTicker(String rawText, {bool isStreaming = false}) {
  if (rawText.isEmpty) return '';
  final windowText = isStreaming && rawText.length > 600
      ? _sliceUtf16Suffix(rawText, 600)
      : _sliceUtf16Prefix(rawText, 400);
  final cleaned = windowText
      .replaceAll(RegExp(r'<\/?think(?:ing)?>', caseSensitive: false), '')
      .replaceAll('\r\n', '\n')
      .trim();
  if (cleaned.isEmpty) return '';

  final lines = cleaned
      .split('\n')
      .map((line) => line.trim())
      .where((line) => line.isNotEmpty && !line.startsWith('```'))
      .toList(growable: false);
  if (lines.isEmpty) return '';

  String cleanLine(String s) {
    final stripped = s
        .replaceAll(RegExp(r'^#{1,6}\s+'), '')
        .replaceAll(RegExp(r'^[*-]\s+'), '')
        .replaceAll(RegExp(r'[`*_~]+'), '')
        .replaceAll(RegExp(r'\s+'), ' ')
        .trim();
    if (stripped.length <= 44) return stripped;
    return '${_sliceUtf16Prefix(stripped, 44).trimRight()}…';
  }

  if (isStreaming) {
    for (var i = lines.length - 1; i >= 0; i--) {
      final line = lines[i];
      if (line.startsWith('#') ||
          line.startsWith('**') ||
          line.startsWith('__')) {
        final title = cleanLine(line);
        if (title.isNotEmpty) return title;
      }
    }
    return cleanLine(lines.last);
  }

  final first = lines.first;
  if (first.startsWith('#') ||
      (first.startsWith('**') && first.endsWith('**')) ||
      (first.startsWith('__') && first.endsWith('__'))) {
    return cleanLine(first);
  }
  return '';
}

/// Formats elapsed milliseconds in the same compact form as PC Hoxi's `formatElapsedSeconds`
/// (`web/src/components/timeline/duration-label.ts`).
String formatElapsedDurationMs(int durationMs) {
  if (durationMs < 50) return '';
  final sec = durationMs / 1000.0;
  if (sec < 60) {
    return '${sec.toStringAsFixed(1)}s';
  }
  final total = sec.round();
  final minutes = total ~/ 60;
  final seconds = total % 60;
  if (minutes < 60) {
    return '${minutes}m ${seconds}s';
  }
  final hours = minutes ~/ 60;
  return '${hours}h ${minutes % 60}m';
}

/// Formats a message timestamp as `HH:mm` matching PC Hoxi's `formatMessageTime`.
String formatTurnClockTime(DateTime timestamp) {
  if (timestamp.millisecondsSinceEpoch <= 0 || timestamp.year < 2000) {
    return '';
  }
  final local = timestamp.toLocal();
  final hh = local.hour.toString().padLeft(2, '0');
  final mm = local.minute.toString().padLeft(2, '0');
  return '$hh:$mm';
}

/// Formats token counts compactly (`850` -> `850`, `1420` -> `1.4k`).
String formatCompactTokens(int tokens) {
  if (tokens <= 0) return '';
  if (tokens < 1000) return '$tokens';
  if (tokens < 10000) return '${(tokens / 1000).toStringAsFixed(1)}k';
  if (tokens < 1000000) return '${(tokens / 1000).round()}k';
  return '${(tokens / 1000000).toStringAsFixed(1)}M';
}

/// Bottom action and telemetry row for completed assistant turns, aligned with PC Hoxi's
/// `AssistantTurnFooter` (`web/src/components/timeline/message-stream-parts.tsx`).
class AssistantTurnFooter extends StatelessWidget {
  const AssistantTurnFooter({
    super.key,
    required this.copied,
    required this.onCopy,
    this.onRetry,
    this.onReport,
    this.reported = false,
    this.durationMs = 0,
    this.totalTokens = 0,
    this.timestamp,
  });

  final bool copied;
  final VoidCallback onCopy;
  final VoidCallback? onRetry;
  final VoidCallback? onReport;
  final bool reported;
  final int durationMs;
  final int totalTokens;
  final DateTime? timestamp;

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final durationLabel = formatElapsedDurationMs(durationMs);
    final tokensLabel = formatCompactTokens(totalTokens);
    final clockLabel = timestamp != null ? formatTurnClockTime(timestamp!) : '';
    final mutedStyle = TextStyle(
      color: AppTheme.textLight,
      fontSize: 11.5,
      fontFeatures: const [FontFeature.tabularFigures()],
    );

    return Padding(
      padding: const EdgeInsets.only(top: 8),
      child: Wrap(
        spacing: 8,
        runSpacing: 4,
        crossAxisAlignment: WrapCrossAlignment.center,
        children: [
          InkWell(
            borderRadius: BorderRadius.circular(6),
            onTap: onCopy,
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 2),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Icon(
                    copied ? Icons.check_rounded : Icons.copy_rounded,
                    size: 13,
                    color: copied ? AppTheme.success : AppTheme.textLight,
                  ),
                  const SizedBox(width: 4),
                  Text(
                    copied ? l10n.commonCopied : l10n.commonCopy,
                    style: TextStyle(
                      color: copied ? AppTheme.success : AppTheme.textLight,
                      fontSize: 11.5,
                      fontWeight: copied ? FontWeight.w600 : FontWeight.w500,
                    ),
                  ),
                ],
              ),
            ),
          ),
          if (onRetry != null)
            InkWell(
              borderRadius: BorderRadius.circular(6),
              onTap: onRetry,
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 2),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    const Icon(
                      Icons.refresh_rounded,
                      size: 13,
                      color: AppTheme.primaryBlue,
                    ),
                    const SizedBox(width: 3),
                    Text(
                      l10n.commonRetry,
                      style: const TextStyle(
                        color: AppTheme.primaryBlue,
                        fontSize: 11.5,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  ],
                ),
              ),
            ),
          if (onReport != null)
            InkWell(
              key: const ValueKey('assistant_turn_report_button'),
              borderRadius: BorderRadius.circular(6),
              onTap: onReport,
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 2),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Icon(
                      reported ? Icons.flag_rounded : Icons.flag_outlined,
                      size: 13,
                      color: reported
                          ? AppTheme.textSecondary
                          : AppTheme.textLight,
                    ),
                    const SizedBox(width: 3),
                    Text(
                      reported ? l10n.chatReportedBadge : l10n.chatReportAction,
                      style: TextStyle(
                        color: reported
                            ? AppTheme.textSecondary
                            : AppTheme.textLight,
                        fontSize: 11.5,
                        fontWeight: reported
                            ? FontWeight.w600
                            : FontWeight.w500,
                      ),
                    ),
                  ],
                ),
              ),
            ),
          if (durationLabel.isNotEmpty)
            Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(
                  Icons.timer_outlined,
                  size: 12,
                  color: AppTheme.textLight,
                ),
                const SizedBox(width: 3),
                Text(durationLabel, style: mutedStyle),
              ],
            ),
          if (tokensLabel.isNotEmpty)
            Text('$tokensLabel tok', style: mutedStyle),
          if (clockLabel.isNotEmpty) Text(clockLabel, style: mutedStyle),
        ],
      ),
    );
  }
}

const int kInitialVisibleMessages = 12;
const int kMessageBatchSize = 12;
const int kMaxAutoExpandedMessages = 32;

/// Computes the start index of the visible message window, stepping back one slot
/// if the window boundary would orphan a leading assistant reply from its user prompt
/// (matching PC Hoxi's `computeTurnWindowSlice` in `turn-window.tsx`).
int computeMessageWindowStart<T>(
  List<T> messages,
  int limit,
  bool Function(T item) isUser,
) {
  if (messages.isEmpty || limit >= messages.length) return 0;
  var start = math.max(0, messages.length - limit);
  if (start > 0 && !isUser(messages[start]) && isUser(messages[start - 1])) {
    start--;
  }
  return start;
}

/// Top fold banner for windowed conversations, aligned with PC Hoxi's
/// `EarlierTurnsBanner` (`web/src/components/timeline/turn-window.tsx`).
class EarlierMessagesBanner extends StatelessWidget {
  const EarlierMessagesBanner({
    super.key,
    required this.hiddenCount,
    required this.totalCount,
    required this.onLoadBatch,
    required this.onLoadAll,
  });

  final int hiddenCount;
  final int totalCount;
  final VoidCallback onLoadBatch;
  final VoidCallback onLoadAll;

  @override
  Widget build(BuildContext context) {
    if (hiddenCount <= 0) return const SizedBox.shrink();
    final batchCount = math.min(hiddenCount, kMessageBatchSize);
    final isZh =
        Localizations.maybeLocaleOf(context)?.languageCode.startsWith('zh') ??
        true;

    return Container(
      width: double.infinity,
      margin: const EdgeInsets.only(bottom: 12),
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
      decoration: BoxDecoration(
        color: AppTheme.surfaceMuted.withValues(alpha: 0.6),
        borderRadius: BorderRadius.circular(12),
      ),
      child: Column(
        children: [
          Text(
            isZh
                ? '已折叠前 $hiddenCount 条历史消息（上拉或点击加载）'
                : '$hiddenCount earlier messages folded',
            style: TextStyle(color: AppTheme.textLight, fontSize: 11.5),
          ),
          const SizedBox(height: 6),
          Wrap(
            spacing: 8,
            runSpacing: 6,
            alignment: WrapAlignment.center,
            crossAxisAlignment: WrapCrossAlignment.center,
            children: [
              InkWell(
                borderRadius: BorderRadius.circular(8),
                onTap: onLoadBatch,
                child: Container(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 10,
                    vertical: 5,
                  ),
                  decoration: BoxDecoration(
                    color: AppTheme.surface,
                    borderRadius: BorderRadius.circular(8),
                  ),
                  child: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      const Icon(
                        Icons.arrow_upward_rounded,
                        size: 13,
                        color: AppTheme.primaryBlue,
                      ),
                      const SizedBox(width: 4),
                      Text(
                        isZh ? '加载更早 $batchCount 条' : 'Load $batchCount earlier',
                        style: TextStyle(
                          color: AppTheme.textPrimary,
                          fontSize: 12,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    ],
                  ),
                ),
              ),
              if (hiddenCount > batchCount)
                InkWell(
                  borderRadius: BorderRadius.circular(8),
                  onTap: onLoadAll,
                  child: Padding(
                    padding: const EdgeInsets.symmetric(
                      horizontal: 8,
                      vertical: 5,
                    ),
                    child: Text(
                      isZh ? '展开全部 ($totalCount)' : 'Show all ($totalCount)',
                      style: TextStyle(
                        color: AppTheme.textSecondary,
                        fontSize: 12,
                        fontWeight: FontWeight.w500,
                      ),
                    ),
                  ),
                ),
            ],
          ),
        ],
      ),
    );
  }
}

/// Builds a text selection context menu for conversation messages and input
/// composers so that:
/// 1. "全选" (Select All) is available whenever there is selectable text that
///    is not yet fully selected.
/// 2. Tapping "全选" keeps the context menu open and immediately refreshes its
///    actions so "复制" (Copy) is displayed and tappable.
/// 3. Whenever any non-empty text range is selected, the "复制" (Copy) button
///    is present in the menu.
Widget buildConversationContextMenu(
  BuildContext context,
  EditableTextState editableTextState,
) {
  final value = editableTextState.textEditingValue;
  final selection = value.selection;
  final text = value.text;
  final obscure = editableTextState.widget.obscureText;
  final interactive = editableTextState.widget.enableInteractiveSelection;
  final hasText = text.isNotEmpty;
  final hasSelection =
      selection.isValid &&
      !selection.isCollapsed &&
      selection.start != selection.end;
  final isAllSelected =
      hasText &&
      selection.isValid &&
      math.min(selection.start, selection.end) <= 0 &&
      math.max(selection.start, selection.end) >= text.length;

  final buttonItems = List<ContextMenuButtonItem>.of(
    editableTextState.contextMenuButtonItems,
  );

  if (hasSelection && !obscure) {
    final hasCopy = buttonItems.any(
      (item) => item.type == ContextMenuButtonType.copy,
    );
    if (!hasCopy) {
      final cutIndex = buttonItems.indexWhere(
        (item) => item.type == ContextMenuButtonType.cut,
      );
      final insertIndex = cutIndex >= 0 ? cutIndex + 1 : 0;
      buttonItems.insert(
        insertIndex,
        ContextMenuButtonItem(
          type: ContextMenuButtonType.copy,
          onPressed: () =>
              editableTextState.copySelection(SelectionChangedCause.toolbar),
        ),
      );
    }
  }

  void handleSelectAll() {
    final currentValue = editableTextState.textEditingValue;
    if (currentValue.text.isEmpty) return;
    final fullSelection = TextSelection(
      baseOffset: 0,
      extentOffset: currentValue.text.length,
    );
    editableTextState.userUpdateTextEditingValue(
      currentValue.copyWith(selection: fullSelection),
      SelectionChangedCause.toolbar,
    );
    editableTextState.hideToolbar(false);
    editableTextState.showToolbar();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!editableTextState.mounted) return;
      final sel = editableTextState.textEditingValue.selection;
      if (sel.isValid && !sel.isCollapsed) {
        editableTextState.hideToolbar(false);
        editableTextState.showToolbar();
      }
    });
  }

  final selectAllIndex = buttonItems.indexWhere(
    (item) => item.type == ContextMenuButtonType.selectAll,
  );
  if (selectAllIndex >= 0) {
    if (isAllSelected) {
      buttonItems.removeAt(selectAllIndex);
    } else {
      buttonItems[selectAllIndex] = buttonItems[selectAllIndex].copyWith(
        onPressed: handleSelectAll,
      );
    }
  } else if (hasText && !isAllSelected && !obscure && interactive) {
    buttonItems.add(
      ContextMenuButtonItem(
        type: ContextMenuButtonType.selectAll,
        onPressed: handleSelectAll,
      ),
    );
  }

  return AdaptiveTextSelectionToolbar.buttonItems(
    anchors: _clampAnchorsToViewport(
      context,
      editableTextState.contextMenuAnchors,
    ),
    buttonItems: buttonItems,
  );
}

TextSelectionToolbarAnchors _clampAnchorsToViewport(
  BuildContext context,
  TextSelectionToolbarAnchors rawAnchors,
) {
  final mediaQuery = MediaQuery.maybeOf(context);
  if (mediaQuery == null) return rawAnchors;
  final screenSize = mediaQuery.size;
  if (screenSize.width <= 0 || screenSize.height <= 0) return rawAnchors;
  final minX = mediaQuery.padding.left + 16.0;
  final maxX = math.max(
    minX,
    screenSize.width - mediaQuery.padding.right - 16.0,
  );
  final minY = mediaQuery.padding.top + 56.0;
  final maxY = math.max(
    minY,
    screenSize.height -
        mediaQuery.viewInsets.bottom -
        mediaQuery.padding.bottom -
        56.0,
  );
  Offset clampOffset(Offset offset) => Offset(
    offset.dx.clamp(minX, maxX).toDouble(),
    offset.dy.clamp(minY, maxY).toDouble(),
  );
  return TextSelectionToolbarAnchors(
    primaryAnchor: clampOffset(rawAnchors.primaryAnchor),
    secondaryAnchor: rawAnchors.secondaryAnchor == null
        ? null
        : clampOffset(rawAnchors.secondaryAnchor!),
  );
}




