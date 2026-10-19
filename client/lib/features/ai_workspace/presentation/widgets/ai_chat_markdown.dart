import 'package:flutter/material.dart';
import 'package:flutter_markdown_plus/flutter_markdown_plus.dart';
import 'package:url_launcher/url_launcher.dart';

/// AI 回复按 markdown 渲染：代码块、列表、表格、链接可读，整段可选中复制。
/// 此前是裸 Text，代码块和列表挤成一团，也没法选中复制。
class AiChatMarkdown extends StatelessWidget {
  const AiChatMarkdown({super.key, required this.text});

  final String text;

  @override
  Widget build(BuildContext context) {
    return MarkdownBody(
      data: text,
      selectable: true,
      // 命令输出、文件清单靠单个换行排版，不能按 markdown 规则并成一行
      softLineBreak: true,
      styleSheet: aiChatMarkdownStyle(Theme.of(context)),
      onTapLink: (_, href, _) => openAiChatLink(href),
      // 图片地址来自 AI 输出，自动加载等于让手机静默访问任意地址；只显示说明文字
      imageBuilder: (uri, _, alt) =>
          _OmittedImage(label: alt ?? uri.toString()),
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

MarkdownStyleSheet aiChatMarkdownStyle(ThemeData theme) {
  final body = TextStyle(
    color: Colors.white.withValues(alpha: 0.85),
    fontSize: 14,
    height: 1.5,
  );
  final heading = body.copyWith(
    color: Colors.white,
    fontWeight: FontWeight.w700,
  );
  return MarkdownStyleSheet.fromTheme(theme).copyWith(
    p: body,
    listBullet: body,
    tableHead: body.copyWith(fontWeight: FontWeight.w600),
    tableBody: body,
    blockquote: body.copyWith(color: Colors.white70),
    h1: heading.copyWith(fontSize: 18),
    h2: heading.copyWith(fontSize: 16),
    h3: heading.copyWith(fontSize: 15),
    h4: heading.copyWith(fontSize: 14),
    a: const TextStyle(
      color: Colors.lightBlueAccent,
      decoration: TextDecoration.underline,
    ),
    code: TextStyle(
      color: Colors.amber.shade100,
      fontFamily: 'monospace',
      fontSize: 13,
      backgroundColor: Colors.white.withValues(alpha: 0.08),
    ),
    codeblockPadding: const EdgeInsets.all(10),
    codeblockDecoration: BoxDecoration(
      color: Colors.black.withValues(alpha: 0.35),
      borderRadius: BorderRadius.circular(6),
    ),
    blockquoteDecoration: const BoxDecoration(
      border: Border(left: BorderSide(color: Colors.white24, width: 3)),
    ),
    tableBorder: TableBorder.all(color: Colors.white24),
    horizontalRuleDecoration: const BoxDecoration(
      border: Border(top: BorderSide(color: Colors.white24)),
    ),
  );
}

class _OmittedImage extends StatelessWidget {
  const _OmittedImage({required this.label});

  final String label;

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        const Icon(
          Icons.image_not_supported_outlined,
          size: 16,
          color: Colors.white54,
        ),
        const SizedBox(width: 4),
        Flexible(
          child: Text(
            label,
            style: const TextStyle(color: Colors.white54, fontSize: 13),
          ),
        ),
      ],
    );
  }
}
