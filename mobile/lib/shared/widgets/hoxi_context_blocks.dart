// Collapsible display widget for hoxi-context v1 envelopes inside mobile user
// chat bubbles: light quote bars for text excerpts and compact collapsible cards
// for selected entities, table records, and snapshots.
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../core/l10n/l10n.dart';
import '../../core/theme/app_theme.dart';
import '../models/hoxi_context_protocol.dart';

class HoxiContextBlocks extends StatelessWidget {
  const HoxiContextBlocks({
    super.key,
    required this.envelopes,
    this.onPrimaryBubble = true,
  });

  final List<HoxiContextEnvelope> envelopes;
  final bool onPrimaryBubble;

  @override
  Widget build(BuildContext context) {
    if (envelopes.isEmpty) return const SizedBox.shrink();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      mainAxisSize: MainAxisSize.min,
      children: [
        for (var i = 0; i < envelopes.length; i++) ...[
          if (i > 0) const SizedBox(height: 6),
          _ContextEnvelopeView(
            envelope: envelopes[i],
            onPrimaryBubble: onPrimaryBubble,
          ),
        ],
      ],
    );
  }
}

class _ContextPalette {
  const _ContextPalette({
    required this.surface,
    required this.innerSurface,
    required this.border,
    required this.accent,
    required this.primaryText,
    required this.secondaryText,
    required this.mutedText,
    required this.chipBg,
    required this.chipText,
  });

  final Color surface;
  final Color innerSurface;
  final Color border;
  final Color accent;
  final Color primaryText;
  final Color secondaryText;
  final Color mutedText;
  final Color chipBg;
  final Color chipText;

  factory _ContextPalette.resolve(bool onPrimaryBubble) {
    if (onPrimaryBubble) {
      return _ContextPalette(
        surface: Colors.white.withValues(alpha: 0.14),
        innerSurface: Colors.black.withValues(alpha: 0.16),
        border: Colors.white.withValues(alpha: 0.24),
        accent: Colors.white.withValues(alpha: 0.86),
        primaryText: Colors.white,
        secondaryText: Colors.white.withValues(alpha: 0.88),
        mutedText: Colors.white.withValues(alpha: 0.72),
        chipBg: Colors.white.withValues(alpha: 0.18),
        chipText: Colors.white,
      );
    }
    return _ContextPalette(
      surface: AppTheme.surfaceMuted,
      innerSurface: AppTheme.surface,
      border: AppTheme.border,
      accent: AppTheme.primaryBlue,
      primaryText: AppTheme.textPrimary,
      secondaryText: AppTheme.textSecondary,
      mutedText: AppTheme.textLight,
      chipBg: AppTheme.primaryBlueLight,
      chipText: AppTheme.primaryBlueText,
    );
  }
}

bool _isLongQuote(String text) {
  if (text.length > 110) return true;
  var nl = 0;
  for (var i = 0; i < text.length; i++) {
    if (text.codeUnitAt(i) == 10 && ++nl >= 2) return true;
  }
  return false;
}

IconData _iconForEnvelope(HoxiContextEnvelope envelope) {
  final first = envelope.items.isNotEmpty ? envelope.items.first : null;
  if (envelope.items.length > 1) {
    return first?.kind == HoxiContextKind.entity
        ? Icons.storage_rounded
        : Icons.layers_outlined;
  }
  return switch (first?.kind) {
    HoxiContextKind.quote => Icons.format_quote_rounded,
    HoxiContextKind.entity => Icons.storage_rounded,
    HoxiContextKind.snapshot => Icons.data_object_rounded,
    _ => Icons.layers_outlined,
  };
}

class _ContextEnvelopeView extends StatefulWidget {
  const _ContextEnvelopeView({
    required this.envelope,
    required this.onPrimaryBubble,
  });

  final HoxiContextEnvelope envelope;
  final bool onPrimaryBubble;

  @override
  State<_ContextEnvelopeView> createState() => _ContextEnvelopeViewState();
}

class _ContextEnvelopeViewState extends State<_ContextEnvelopeView> {
  bool _expanded = false;

  @override
  Widget build(BuildContext context) {
    final env = widget.envelope;
    if (env.items.isEmpty) return const SizedBox.shrink();
    final palette = _ContextPalette.resolve(widget.onPrimaryBubble);
    final isSingleQuote =
        env.items.length == 1 && env.items.first.kind == HoxiContextKind.quote;
    if (isSingleQuote) {
      return _buildQuoteBar(context, env, env.items.first, palette);
    }
    return _buildStructuredCard(context, env, palette);
  }

  Widget _buildQuoteBar(
    BuildContext context,
    HoxiContextEnvelope env,
    HoxiContextItem item,
    _ContextPalette palette,
  ) {
    final l10n = context.l10n;
    final body = item.bodyText.isNotEmpty
        ? item.bodyText
        : (item.subtitle ?? item.title ?? '');
    final expandable = _isLongQuote(body);
    final sourceLabel = env.source?.displayLabel ?? '';
    final headerText = [
      if (sourceLabel.isNotEmpty) sourceLabel,
      if (item.title != null && item.title!.isNotEmpty) item.title!,
    ].join(' · ');

    return Material(
      color: Colors.transparent,
      child: InkWell(
        borderRadius: BorderRadius.circular(10),
        onTap: expandable
            ? () => setState(() => _expanded = !_expanded)
            : null,
        child: Container(
          padding: const EdgeInsets.fromLTRB(10, 7, 10, 7),
          decoration: BoxDecoration(
            color: palette.surface,
            borderRadius: BorderRadius.circular(10),
            border: Border(
              left: BorderSide(color: palette.accent, width: 3),
            ),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisSize: MainAxisSize.min,
            children: [
              Row(
                children: [
                  Icon(
                    Icons.format_quote_rounded,
                    size: 13,
                    color: palette.accent,
                  ),
                  const SizedBox(width: 4),
                  Expanded(
                    child: Text(
                      headerText.isNotEmpty ? headerText : l10n.contextQuoteLabel,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        color: palette.mutedText,
                        fontSize: 11,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  ),
                  if (expandable)
                    Icon(
                      _expanded
                          ? Icons.expand_less_rounded
                          : Icons.expand_more_rounded,
                      size: 15,
                      color: palette.mutedText,
                    ),
                ],
              ),
              if (body.isNotEmpty) ...[
                const SizedBox(height: 3),
                Text(
                  body,
                  maxLines: _expanded ? null : 2,
                  overflow: _expanded
                      ? TextOverflow.visible
                      : TextOverflow.ellipsis,
                  style: TextStyle(
                    color: palette.secondaryText,
                    fontSize: 12.5,
                    height: 1.4,
                  ),
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildStructuredCard(
    BuildContext context,
    HoxiContextEnvelope env,
    _ContextPalette palette,
  ) {
    final l10n = context.l10n;
    final first = env.items.first;
    final count = env.items.length;
    final sourceLabel = env.source?.displayLabel ?? '';
    final chars = estimateContextEnvelopeChars(env);
    final sizeLabel = chars > 0 ? formatContextSizeLabel(chars) : '';
    final headline = count == 1
        ? (first.title ??
            first.ref?.id ??
            deriveContextItemPreview(first, maxLen: 36))
        : l10n.contextAttachedCount(count);
    final collapsedPreview = count == 1
        ? (first.subtitle ??
            (first.title != null
                ? deriveContextItemPreview(first, maxLen: 64)
                : ''))
        : env.items
            .take(3)
            .map((it) => it.title ?? it.ref?.id ?? deriveContextItemPreview(it, maxLen: 18))
            .where((s) => s.isNotEmpty)
            .join(' · ');

    return Container(
      decoration: BoxDecoration(
        color: palette.surface,
        borderRadius: BorderRadius.circular(11),
        border: Border.all(color: palette.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        mainAxisSize: MainAxisSize.min,
        children: [
          Material(
            color: Colors.transparent,
            child: InkWell(
              borderRadius: BorderRadius.circular(10),
              onTap: () => setState(() => _expanded = !_expanded),
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Row(
                      children: [
                        Icon(_iconForEnvelope(env), size: 13.5, color: palette.accent),
                        const SizedBox(width: 5),
                        if (sourceLabel.isNotEmpty) ...[
                          Container(
                            padding: const EdgeInsets.symmetric(
                              horizontal: 5,
                              vertical: 1.5,
                            ),
                            decoration: BoxDecoration(
                              color: palette.chipBg,
                              borderRadius: BorderRadius.circular(4),
                            ),
                            child: Text(
                              sourceLabel,
                              style: TextStyle(
                                color: palette.chipText,
                                fontSize: 10,
                                fontWeight: FontWeight.w600,
                              ),
                            ),
                          ),
                          const SizedBox(width: 5),
                        ],
                        Expanded(
                          child: Text(
                            headline.isNotEmpty
                                ? headline
                                : l10n.contextReferencedSingle,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: TextStyle(
                              color: palette.primaryText,
                              fontSize: 12.5,
                              fontWeight: FontWeight.w600,
                            ),
                          ),
                        ),
                        if (sizeLabel.isNotEmpty) ...[
                          const SizedBox(width: 4),
                          Text(
                            sizeLabel,
                            style: TextStyle(
                              color: palette.mutedText,
                              fontSize: 10,
                              fontFamily: 'monospace',
                            ),
                          ),
                        ],
                        const SizedBox(width: 2),
                        Icon(
                          _expanded
                              ? Icons.expand_less_rounded
                              : Icons.expand_more_rounded,
                          size: 16,
                          color: palette.mutedText,
                        ),
                      ],
                    ),
                    if (!_expanded && collapsedPreview.isNotEmpty) ...[
                      const SizedBox(height: 3),
                      Text(
                        collapsedPreview,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: TextStyle(
                          color: palette.mutedText,
                          fontSize: 11.5,
                        ),
                      ),
                    ],
                  ],
                ),
              ),
            ),
          ),
          if (_expanded)
            Container(
              decoration: BoxDecoration(
                border: Border(top: BorderSide(color: palette.border)),
              ),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                mainAxisSize: MainAxisSize.min,
                children: [
                  for (var i = 0; i < env.items.length; i++) ...[
                    if (i > 0)
                      Divider(height: 1, thickness: 1, color: palette.border),
                    _ContextItemDetailRow(
                      item: env.items[i],
                      index: i,
                      total: count,
                      palette: palette,
                    ),
                  ],
                ],
              ),
            ),
        ],
      ),
    );
  }
}

class _ContextItemDetailRow extends StatefulWidget {
  const _ContextItemDetailRow({
    required this.item,
    required this.index,
    required this.total,
    required this.palette,
  });

  final HoxiContextItem item;
  final int index;
  final int total;
  final _ContextPalette palette;

  @override
  State<_ContextItemDetailRow> createState() => _ContextItemDetailRowState();
}

class _ContextItemDetailRowState extends State<_ContextItemDetailRow> {
  bool _copied = false;

  Future<void> _copyItem() async {
    final item = widget.item;
    final text = item.bodyText.isNotEmpty
        ? item.bodyText
        : (item.subtitle ?? item.title ?? '');
    if (text.isEmpty) return;
    await Clipboard.setData(ClipboardData(text: text));
    if (!mounted) return;
    setState(() => _copied = true);
    Future<void>.delayed(const Duration(milliseconds: 1400), () {
      if (!mounted) return;
      setState(() => _copied = false);
    });
  }

  @override
  Widget build(BuildContext context) {
    final item = widget.item;
    final palette = widget.palette;
    final l10n = context.l10n;
    final rawBody = item.bodyText;
    final formatted = rawBody.isNotEmpty ? prettyFormatContextBody(rawBody) : null;
    final resolveLabel = item.ref?.resolve?.displayLabel ?? '';

    return Padding(
      padding: const EdgeInsets.fromLTRB(10, 8, 10, 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              if (widget.total > 1) ...[
                Container(
                  padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
                  margin: const EdgeInsets.only(top: 1, right: 6),
                  decoration: BoxDecoration(
                    color: palette.chipBg,
                    borderRadius: BorderRadius.circular(4),
                  ),
                  child: Text(
                    '${widget.index + 1}',
                    style: TextStyle(
                      color: palette.chipText,
                      fontSize: 10,
                      fontFamily: 'monospace',
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
              ],
              Expanded(
                child: Wrap(
                  spacing: 5,
                  runSpacing: 4,
                  crossAxisAlignment: WrapCrossAlignment.center,
                  children: [
                    if (item.title != null && item.title!.isNotEmpty)
                      Text(
                        item.title!,
                        style: TextStyle(
                          color: palette.primaryText,
                          fontSize: 12,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    if (item.badge != null && item.badge!.isNotEmpty)
                      Container(
                        padding: const EdgeInsets.symmetric(
                          horizontal: 5,
                          vertical: 1,
                        ),
                        decoration: BoxDecoration(
                          color: palette.chipBg,
                          borderRadius: BorderRadius.circular(4),
                        ),
                        child: Text(
                          item.badge!,
                          style: TextStyle(
                            color: palette.chipText,
                            fontSize: 10,
                            fontWeight: FontWeight.w500,
                          ),
                        ),
                      ),
                    if (item.ref != null && item.ref!.id.isNotEmpty)
                      Container(
                        padding: const EdgeInsets.symmetric(
                          horizontal: 5,
                          vertical: 1,
                        ),
                        decoration: BoxDecoration(
                          color: palette.innerSurface,
                          borderRadius: BorderRadius.circular(4),
                        ),
                        child: Text(
                          '${item.entityType != null ? '${item.entityType}:' : '#'}${item.ref!.id}',
                          style: TextStyle(
                            color: palette.mutedText,
                            fontSize: 10,
                            fontFamily: 'monospace',
                          ),
                        ),
                      ),
                  ],
                ),
              ),
              if (rawBody.isNotEmpty || (item.subtitle?.isNotEmpty ?? false))
                InkWell(
                  borderRadius: BorderRadius.circular(6),
                  onTap: _copyItem,
                  child: Padding(
                    padding: const EdgeInsets.all(3),
                    child: Tooltip(
                      message: _copied ? l10n.commonCopied : l10n.contextCopyItem,
                      child: Icon(
                        _copied ? Icons.check_rounded : Icons.copy_rounded,
                        size: 13,
                        color: _copied ? AppTheme.success : palette.mutedText,
                      ),
                    ),
                  ),
                ),
            ],
          ),
          if (item.subtitle != null && item.subtitle!.isNotEmpty) ...[
            const SizedBox(height: 3),
            Text(
              item.subtitle!,
              style: TextStyle(
                color: palette.secondaryText,
                fontSize: 11.5,
                height: 1.35,
              ),
            ),
          ],
          if (resolveLabel.isNotEmpty) ...[
            const SizedBox(height: 3),
            Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(Icons.link_rounded, size: 11, color: palette.mutedText),
                const SizedBox(width: 4),
                Flexible(
                  child: Text(
                    '${l10n.contextResolveHint}: $resolveLabel',
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      color: palette.mutedText,
                      fontSize: 10.5,
                      fontFamily: 'monospace',
                    ),
                  ),
                ),
              ],
            ),
          ],
          if (formatted != null) ...[
            const SizedBox(height: 5),
            Container(
              width: double.infinity,
              constraints: const BoxConstraints(maxHeight: 160),
              padding: const EdgeInsets.all(7),
              decoration: BoxDecoration(
                color: palette.innerSurface,
                borderRadius: BorderRadius.circular(7),
              ),
              child: SingleChildScrollView(
                physics: const ClampingScrollPhysics(),
                child: SelectableText(
                  formatted.text,
                  scrollPhysics: const NeverScrollableScrollPhysics(),
                  style: TextStyle(
                    color: palette.secondaryText,
                    fontSize: formatted.isJson ? 10.5 : 11.5,
                    fontFamily: formatted.isJson ? 'monospace' : null,
                    height: 1.4,
                  ),
                ),
              ),
            ),
          ],
        ],
      ),
    );
  }
}
