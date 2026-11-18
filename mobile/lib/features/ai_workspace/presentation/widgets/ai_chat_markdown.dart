import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_markdown_plus/flutter_markdown_plus.dart';
// ignore: depend_on_referenced_packages
import 'package:markdown/markdown.dart' as md;
import 'package:url_launcher/url_launcher.dart';

import '../../../../core/theme/app_theme.dart';
import '../../../../shared/widgets/stream_text_pacer.dart';
import '../../models/code_convoy_plan.dart';
import 'ai_convoy_widgets.dart';

/// AI 回复按 markdown 渲染：代码块带语言栏/一键复制/换行切换，同时识别内联编队卡片。
class AiChatMarkdown extends StatelessWidget {
  const AiChatMarkdown({
    super.key,
    required this.text,
    this.convoyPlans = const [],
    this.subagents = const [],
    this.onOpenSession,
    this.onStartConvoyPlan,
    this.onCancelConvoyPlan,
    this.onRetryConvoyPlan,
    this.onMergeConvoyPlan,
    this.onSummarizeConvoyPlan,
  });

  final String text;
  final List<CodeConvoyPlan> convoyPlans;
  final List<CodeSubagentItem> subagents;
  final ValueChanged<int>? onOpenSession;
  final ValueChanged<CodeConvoyPlan>? onStartConvoyPlan;
  final ValueChanged<String>? onCancelConvoyPlan;
  final ValueChanged<String>? onRetryConvoyPlan;
  final ValueChanged<String>? onMergeConvoyPlan;
  final ValueChanged<CodeConvoyPlan>? onSummarizeConvoyPlan;

  @override
  Widget build(BuildContext context) {
    final segments = splitMarkdownWithConvoyBlocks(text);
    final hasConvoyBlock = segments.any((s) => s.convoyPlan != null);
    if (!hasConvoyBlock) {
      return _buildMarkdownSegment(context, text);
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        for (final segment in segments)
          if (segment.convoyPlan != null)
            AiInlineConvoyPlanCard(
              plan: segment.convoyPlan!,
              livePlans: convoyPlans,
              subagents: subagents,
              onOpenSession: onOpenSession,
              onStartConvoyPlan: onStartConvoyPlan,
              onCancelConvoyPlan: onCancelConvoyPlan,
              onRetryConvoyPlan: onRetryConvoyPlan,
              onMergeConvoyPlan: onMergeConvoyPlan,
              onSummarizeConvoyPlan: onSummarizeConvoyPlan,
            )
          else if (segment.markdown != null &&
              segment.markdown!.trim().isNotEmpty)
            _buildMarkdownSegment(context, segment.markdown!),
      ],
    );
  }

  Widget _buildMarkdownSegment(BuildContext context, String data) {
    return ScrollConfiguration(
      behavior: ScrollConfiguration.of(context).copyWith(
        physics: const NeverScrollableScrollPhysics(),
      ),
      child: MarkdownBody(
        data: data,
        selectable: true,
        contextMenuBuilder: buildConversationContextMenu,
        softLineBreak: true,
        styleSheet: aiChatMarkdownStyle(Theme.of(context)),
        builders: <String, MarkdownElementBuilder>{
          'pre': _AiCodeBlockBuilder(),
        },
        onTapLink: (_, href, _) => openAiChatLink(href),
        imageBuilder: (uri, _, alt) =>
            _OmittedImage(label: alt ?? uri.toString()),
      ),
    );
  }
}

class _AiCodeBlockBuilder extends MarkdownElementBuilder {
  @override
  bool isBlockElement() => true;

  @override
  Widget? visitElementAfterWithContext(
    BuildContext context,
    md.Element element,
    TextStyle? preferredStyle,
    TextStyle? parentStyle,
  ) {
    var lang = '';
    final children = element.children;
    if (children != null && children.isNotEmpty) {
      final first = children.first;
      if (first is md.Element && first.tag == 'code') {
        final cls = first.attributes['class'] ?? '';
        if (cls.startsWith('language-')) {
          lang = cls.substring('language-'.length).trim();
        }
      }
    }
    final code = element.textContent.replaceFirst(RegExp(r'\n$'), '');
    return _AiCodeBlockCard(lang: lang, code: code);
  }
}

class _AiCodeBlockCard extends StatefulWidget {
  const _AiCodeBlockCard({required this.lang, required this.code});

  final String lang;
  final String code;

  @override
  State<_AiCodeBlockCard> createState() => _AiCodeBlockCardState();
}

class _AiCodeBlockCardState extends State<_AiCodeBlockCard> {
  bool _copied = false;
  bool _wrapLines = true;

  Future<void> _copyCode() async {
    if (widget.code.isEmpty) return;
    await Clipboard.setData(ClipboardData(text: widget.code));
    if (!mounted) return;
    setState(() => _copied = true);
    Future<void>.delayed(const Duration(milliseconds: 1800), () {
      if (mounted) setState(() => _copied = false);
    });
  }

  @override
  Widget build(BuildContext context) {
    final isDark =
        Theme.of(context).brightness == Brightness.dark || AppTheme.isDark;
    final borderColor = isDark
        ? const Color(0xFF243044)
        : const Color(0xFFE2E8F0);
    final headerBg = isDark
        ? const Color(0xFF151E2E)
        : const Color(0xFFF1F5F9);
    final codeBg = isDark ? const Color(0xFF1B2436) : const Color(0xFFF8FAFC);
    final codeColor = isDark
        ? const Color(0xFFE2E8F0)
        : const Color(0xFF0F172A);
    final langLabel = widget.lang.trim().isNotEmpty
        ? widget.lang.trim()
        : 'code';

    return Container(
      margin: const EdgeInsets.symmetric(vertical: 4),
      decoration: BoxDecoration(
        color: codeBg,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: borderColor),
      ),
      clipBehavior: Clip.antiAlias,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
            decoration: BoxDecoration(
              color: headerBg,
              border: Border(bottom: BorderSide(color: borderColor)),
            ),
            child: Row(
              children: [
                Icon(
                  Icons.code_rounded,
                  size: 13.5,
                  color: AppTheme.textSecondary,
                ),
                const SizedBox(width: 5),
                Text(
                  langLabel,
                  style: TextStyle(
                    color: AppTheme.textSecondary,
                    fontSize: 11,
                    fontWeight: FontWeight.w600,
                    fontFamily: 'monospace',
                  ),
                ),
                const Spacer(),
                InkWell(
                  borderRadius: BorderRadius.circular(6),
                  onTap: () => setState(() => _wrapLines = !_wrapLines),
                  child: Padding(
                    padding: const EdgeInsets.symmetric(
                      horizontal: 6,
                      vertical: 2,
                    ),
                    child: Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Icon(
                          Icons.wrap_text_rounded,
                          size: 13,
                          color: _wrapLines
                              ? AppTheme.primaryBlueText
                              : AppTheme.textSecondary,
                        ),
                        const SizedBox(width: 3),
                        Text(
                          _wrapLines ? '自动换行' : '横向滚动',
                          style: TextStyle(
                            color: _wrapLines
                                ? AppTheme.primaryBlueText
                                : AppTheme.textSecondary,
                            fontSize: 10.5,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                      ],
                    ),
                  ),
                ),
                const SizedBox(width: 6),
                InkWell(
                  key: ValueKey('ai_code_block_copy_$langLabel'),
                  borderRadius: BorderRadius.circular(6),
                  onTap: _copyCode,
                  child: Padding(
                    padding: const EdgeInsets.symmetric(
                      horizontal: 6,
                      vertical: 2,
                    ),
                    child: Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Icon(
                          _copied ? Icons.check_rounded : Icons.copy_rounded,
                          size: 13,
                          color: _copied
                              ? AppTheme.success
                              : AppTheme.textSecondary,
                        ),
                        const SizedBox(width: 3),
                        Text(
                          _copied ? '已复制' : '复制',
                          style: TextStyle(
                            color: _copied
                                ? AppTheme.success
                                : AppTheme.textSecondary,
                            fontSize: 10.5,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                      ],
                    ),
                  ),
                ),
              ],
            ),
          ),
          Padding(
            padding: const EdgeInsets.all(12),
            child: _wrapLines
                ? SelectableText(
                    widget.code,
                    scrollPhysics: const NeverScrollableScrollPhysics(),
                    contextMenuBuilder: buildConversationContextMenu,
                    style: TextStyle(
                      color: codeColor,
                      fontFamily: 'monospace',
                      fontSize: 12.5,
                      height: 1.5,
                    ),
                  )
                : SingleChildScrollView(
                    scrollDirection: Axis.horizontal,
                    child: SelectableText(
                      widget.code,
                      scrollPhysics: const NeverScrollableScrollPhysics(),
                      contextMenuBuilder: buildConversationContextMenu,
                      style: TextStyle(
                        color: codeColor,
                        fontFamily: 'monospace',
                        fontSize: 12.5,
                        height: 1.5,
                      ),
                    ),
                  ),
          ),
        ],
      ),
    );
  }
}

/// 只打开 http/https 链接，交给外部浏览器；其他 scheme（file:、intent: 等）一律忽略。
Future<void> openAiChatLink(String? href) async {
  final uri = aiChatLinkUri(href);
  if (uri == null) return;
  await launchUrl(uri, mode: LaunchMode.externalApplication);
}

Uri? aiChatLinkUri(String? href) {
  final uri = Uri.tryParse(href?.trim() ?? '');
  if (uri == null || (uri.scheme != 'http' && uri.scheme != 'https')) {
    return null;
  }
  return uri;
}

MarkdownStyleSheet? _cachedLightMarkdownStyle;
MarkdownStyleSheet? _cachedDarkMarkdownStyle;

MarkdownStyleSheet aiChatMarkdownStyle(ThemeData theme) {
  final isDark = theme.brightness == Brightness.dark || AppTheme.isDark;
  final cached = isDark ? _cachedDarkMarkdownStyle : _cachedLightMarkdownStyle;
  if (cached != null) return cached;

  final textColor = isDark ? const Color(0xFFE2E8F0) : const Color(0xFF0F172A);
  final mutedColor = isDark ? const Color(0xFF94A3B8) : const Color(0xFF64748B);
  final borderColor = isDark ? const Color(0xFF243044) : const Color(0xFFE2E8F0);
  final accentText = isDark ? const Color(0xFF93C5FD) : const Color(0xFF1D4ED8);
  final accentSoft = isDark ? const Color(0xFF17233A) : const Color(0xFFEFF6FF);
  final surfaceMuted = isDark
      ? const Color(0xFF1B2436)
      : const Color(0xFFF8FAFC);
  final accentColor = isDark ? const Color(0xFF3B82F6) : AppTheme.primaryBlue;
  final body = TextStyle(
    color: textColor,
    fontSize: 14,
    height: 1.55,
  );
  final heading = body.copyWith(
    color: textColor,
    fontWeight: FontWeight.w700,
  );
  final normalizedTheme = theme.textTheme.bodyMedium?.fontSize != null
      ? theme
      : theme.copyWith(
          textTheme: theme.textTheme.copyWith(
            bodyMedium: (theme.textTheme.bodyMedium ?? const TextStyle())
                .copyWith(fontSize: 14),
          ),
        );
  final built = MarkdownStyleSheet.fromTheme(normalizedTheme).copyWith(
    p: body,
    pPadding: const EdgeInsets.only(bottom: 8),
    listBullet: body,
    listIndent: 18,
    tableHead: body.copyWith(fontWeight: FontWeight.w600),
    tableBody: body,
    tableCellsPadding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
    blockquote: body.copyWith(color: mutedColor),
    blockquotePadding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
    h1: heading.copyWith(fontSize: 17, height: 1.4),
    h1Padding: const EdgeInsets.only(top: 6, bottom: 6),
    h2: heading.copyWith(fontSize: 15.5, height: 1.4),
    h2Padding: const EdgeInsets.only(top: 6, bottom: 6),
    h3: heading.copyWith(fontSize: 14.5, height: 1.4),
    h3Padding: const EdgeInsets.only(top: 4, bottom: 4),
    h4: heading.copyWith(fontSize: 14),
    a: TextStyle(
      color: accentText,
      decoration: TextDecoration.underline,
    ),
    code: TextStyle(
      color: accentText,
      fontFamily: 'monospace',
      fontSize: 12.5,
      backgroundColor: accentSoft,
    ),
    codeblockPadding: const EdgeInsets.all(12),
    codeblockDecoration: BoxDecoration(
      color: surfaceMuted,
      borderRadius: BorderRadius.circular(12),
      border: Border.all(color: borderColor),
    ),
    blockquoteDecoration: BoxDecoration(
      color: surfaceMuted.withValues(alpha: 0.6),
      borderRadius: const BorderRadius.only(
        topRight: Radius.circular(8),
        bottomRight: Radius.circular(8),
      ),
      border: Border(
        left: BorderSide(
          color: accentColor.withValues(alpha: 0.6),
          width: 3,
        ),
      ),
    ),
    tableBorder: TableBorder.all(color: borderColor, borderRadius: BorderRadius.circular(8)),
    horizontalRuleDecoration: BoxDecoration(
      border: Border(top: BorderSide(color: borderColor)),
    ),
  );
  if (isDark) {
    _cachedDarkMarkdownStyle = built;
  } else {
    _cachedLightMarkdownStyle = built;
  }
  return built;
}

class _OmittedImage extends StatelessWidget {
  const _OmittedImage({required this.label});

  final String label;

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(
          Icons.image_not_supported_outlined,
          size: 16,
          color: AppTheme.textSecondary,
        ),
        const SizedBox(width: 4),
        Flexible(
          child: Text(
            label,
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ),
      ],
    );
  }
}
